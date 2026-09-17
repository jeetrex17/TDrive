// Package galleryprepare runs explicitly requested, bounded legacy preview jobs.
// It owns lifecycle and accounting; storage and Telegram details stay in Source.
package galleryprepare

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

const PageSize = 64

var (
	ErrSkipped = errors.New("preview source is unsupported by this decoder")
	ErrRunning = errors.New("preview preparation is already running")
	ErrBudget  = errors.New("preview preparation reached its estimated download budget; refresh and resume")
)

type Item struct {
	MsgID int64
	Size  int64
}
type Estimate struct {
	Total      int
	BytesTotal int64
}

type State struct {
	Running    bool   `json:"running"`
	ChannelID  int64  `json:"channel_id"`
	Completed  int    `json:"completed"`
	Skipped    int    `json:"skipped"`
	Total      int    `json:"total"`
	BytesTotal int64  `json:"bytes_total"`
	BytesDone  int64  `json:"bytes_done"`
	Error      string `json:"error"`
}

type Source interface {
	Estimate(context.Context, int64) (Estimate, error)
	Page(context.Context, int64, int64, int) ([]Item, error)
	Prepare(context.Context, int64, int64, int64) (int64, error)
}

type Runner struct {
	mu sync.Mutex
	// eventMu preserves transition/event ordering across Start and worker exit.
	// It is separate from mu so observers may safely read Snapshot or call Stop.
	eventMu    sync.Mutex
	state      State
	cancel     context.CancelFunc
	onProgress func(State)
}

// New accepts a fast, nonblocking progress observer. Observers may inspect or
// stop the runner; starting another job belongs to the caller's UI action.
func New(onProgress func(State)) *Runner { return &Runner{onProgress: onProgress} }

func (r *Runner) Snapshot() State { r.mu.Lock(); defer r.mu.Unlock(); return r.state }

// Start reserves the single worker before estimating. Stop can therefore abort
// startup too. Restart re-queries missing renditions, reusing durable completed
// remote work without retaining a collection-sized list or an in-memory cursor.
func (r *Runner) Start(ctx context.Context, channelID int64, source Source) error {
	if ctx == nil || channelID == 0 || source == nil {
		return fmt.Errorf("invalid preview preparation request")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.eventMu.Lock()
	defer r.eventMu.Unlock()
	r.mu.Lock()
	if r.cancel != nil {
		r.mu.Unlock()
		return ErrRunning
	}
	workerCtx, cancel := context.WithCancel(ctx)
	state := State{Running: true, ChannelID: channelID}
	r.state = state
	r.cancel = cancel
	r.mu.Unlock()
	r.emit(state)
	go r.run(workerCtx, channelID, source)
	return nil
}

// Stop is idempotent and keeps the worker slot reserved until cancellation has
// unwound the active transfer. A rapid stop/start cannot overlap source decodes.
func (r *Runner) Stop() {
	r.mu.Lock()
	cancel := r.cancel
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (r *Runner) run(ctx context.Context, channelID int64, source Source) {
	err := r.prepare(ctx, channelID, source)
	r.transition(func(state State) State {
		state.Running = false
		if err != nil && !errors.Is(err, context.Canceled) {
			state.Error = err.Error()
		}
		return state
	}, true)
}

func (r *Runner) prepare(ctx context.Context, channelID int64, source Source) error {
	estimate, err := source.Estimate(ctx, channelID)
	if err != nil {
		return err
	}
	if estimate.Total < 0 || estimate.BytesTotal < 0 {
		return fmt.Errorf("invalid preview preparation estimate")
	}
	r.transition(func(state State) State {
		state.Total = estimate.Total
		state.BytesTotal = estimate.BytesTotal
		return state
	}, false)
	var after int64
	for r.processed() < estimate.Total {
		if err := ctx.Err(); err != nil {
			return err
		}
		items, err := source.Page(ctx, channelID, after, PageSize)
		if err != nil {
			return err
		}
		if len(items) > PageSize {
			return fmt.Errorf("preview preparation page exceeds limit")
		}
		if len(items) == 0 {
			return nil
		}
		for _, item := range items {
			if item.MsgID <= after || item.Size <= 0 {
				return fmt.Errorf("invalid preview preparation page")
			}
			if err := r.prepareItem(ctx, source, channelID, item); err != nil {
				return err
			}
			after = item.MsgID
			if r.processed() >= estimate.Total {
				return nil
			}
		}
	}
	return ctx.Err()
}

func (r *Runner) prepareItem(ctx context.Context, source Source, channelID int64, item Item) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	state := r.Snapshot()
	if item.Size > state.BytesTotal-state.BytesDone {
		return ErrBudget
	}
	written, err := source.Prepare(ctx, channelID, item.MsgID, item.Size)
	if written < 0 {
		return fmt.Errorf("invalid preview download byte count")
	}
	r.transition(func(state State) State {
		state.BytesDone += written
		if errors.Is(err, ErrSkipped) {
			state.Skipped++
		} else if err == nil {
			state.Completed++
		}
		return state
	}, false)
	if err != nil && !errors.Is(err, ErrSkipped) {
		return err
	}
	if r.Snapshot().BytesDone > state.BytesTotal {
		return ErrBudget
	}
	return nil
}

func (r *Runner) transition(update func(State) State, finished bool) {
	r.eventMu.Lock()
	defer r.eventMu.Unlock()
	r.mu.Lock()
	state := update(r.state)
	r.state = state
	if finished && r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	r.mu.Unlock()
	r.emit(state)
}

func (r *Runner) emit(state State) {
	if r.onProgress != nil {
		r.onProgress(state)
	}
}

func (r *Runner) processed() int { state := r.Snapshot(); return state.Completed + state.Skipped }
