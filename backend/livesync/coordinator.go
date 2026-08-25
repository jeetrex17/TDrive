package livesync

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

const (
	EventStarted   = "live_sync_started"
	EventCompleted = "live_sync_completed"
	EventFailed    = "live_sync_failed"
)

const (
	ReasonSignal   = "signal"
	ReasonBackstop = "backstop"
)

type Syncer interface {
	SyncChannel(ctx context.Context, channelID int64) error
}

type EventSink interface {
	Emit(name string, args ...any)
}

type WarnFunc func(format string, args ...any)

type Config struct {
	Activity Activity
	Syncer   Syncer
	Events   EventSink
	Warnf    WarnFunc

	ListChannels func(ctx context.Context) ([]int64, error)

	Debounce         time.Duration
	BackstopInterval time.Duration
	SyncTimeout      time.Duration
}

type Coordinator struct {
	activity Activity
	syncer   Syncer
	events   EventSink
	warnf    WarnFunc

	listChannels func(ctx context.Context) ([]int64, error)

	debounce         time.Duration
	backstopInterval time.Duration
	syncTimeout      time.Duration

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

func NewCoordinator(cfg Config) *Coordinator {
	if cfg.Debounce <= 0 {
		cfg.Debounce = 750 * time.Millisecond
	}
	if cfg.BackstopInterval < 0 {
		cfg.BackstopInterval = 0
	}
	if cfg.BackstopInterval == 0 {
		cfg.BackstopInterval = 5 * time.Minute
	}
	if cfg.SyncTimeout <= 0 {
		cfg.SyncTimeout = 2 * time.Minute
	}
	if cfg.Warnf == nil {
		cfg.Warnf = func(string, ...any) {}
	}
	return &Coordinator{
		activity:         cfg.Activity,
		syncer:           cfg.Syncer,
		events:           cfg.Events,
		warnf:            cfg.Warnf,
		listChannels:     cfg.ListChannels,
		debounce:         cfg.Debounce,
		backstopInterval: cfg.BackstopInterval,
		syncTimeout:      cfg.SyncTimeout,
	}
}

func (c *Coordinator) Start(ctx context.Context) {
	if c == nil || c.activity == nil || c.syncer == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cancel != nil {
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	c.done = make(chan struct{})
	slog.Info("livesync: coordinator starting", "debounce", c.debounce, "backstop_interval", c.backstopInterval)
	go c.loop(runCtx, c.done)
}

func (c *Coordinator) Stop() {
	if c == nil {
		return
	}
	c.mu.Lock()
	cancel := c.cancel
	done := c.done
	c.cancel = nil
	c.done = nil
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
		slog.Info("livesync: coordinator stopped")
	}
}

func (c *Coordinator) loop(ctx context.Context, done chan<- struct{}) {
	defer close(done)

	signals := c.activity.Signals()
	known, _ := c.loadKnown(ctx)
	pending := make(map[int64]string)

	var debounceTimer *time.Timer
	var debounceC <-chan time.Time
	stopDebounce := func() {
		if debounceTimer == nil {
			return
		}
		if !debounceTimer.Stop() {
			select {
			case <-debounceTimer.C:
			default:
			}
		}
		debounceTimer = nil
		debounceC = nil
	}
	resetDebounce := func(delay time.Duration) {
		stopDebounce()
		debounceTimer = time.NewTimer(delay)
		debounceC = debounceTimer.C
	}
	defer stopDebounce()

	backstop := time.NewTicker(c.backstopInterval)
	defer backstop.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case channelID, ok := <-signals:
			if !ok {
				return
			}
			if channelID <= 0 {
				continue
			}
			if !known[channelID] {
				known, _ = c.loadKnown(ctx)
				if !known[channelID] {
					continue
				}
			}
			pending[channelID] = ReasonSignal
			resetDebounce(c.debounce)
		case <-debounceC:
			c.flush(ctx, pending)
			pending = make(map[int64]string)
			debounceTimer = nil
			debounceC = nil
		case <-backstop.C:
			known, _ = c.loadKnown(ctx)
			for channelID := range known {
				pending[channelID] = ReasonBackstop
			}
			if len(pending) > 0 {
				resetDebounce(0)
			}
		}
	}
}

func (c *Coordinator) flush(ctx context.Context, pending map[int64]string) {
	for channelID, reason := range pending {
		if ctx.Err() != nil {
			return
		}
		c.syncOne(ctx, channelID, reason)
	}
}

func (c *Coordinator) syncOne(ctx context.Context, channelID int64, reason string) {
	payload := map[string]any{"channel_id": channelID, "reason": reason}
	slog.Debug("livesync: sync starting", "channel_id", channelID, "reason", reason)
	c.emit(EventStarted, payload)

	syncCtx, cancel := context.WithTimeout(ctx, c.syncTimeout)
	defer cancel()
	err := c.syncer.SyncChannel(syncCtx, channelID)
	if err != nil {
		slog.Warn("livesync: sync failed", "channel_id", channelID, "reason", reason, "error", err)
		c.warnf("live sync: channel=%d reason=%s: %v\n", channelID, reason, err)
		c.emit(EventFailed, map[string]any{
			"channel_id": channelID,
			"reason":     reason,
			"error":      err.Error(),
		})
		return
	}
	slog.Debug("livesync: sync complete", "channel_id", channelID, "reason", reason)
	c.emit(EventCompleted, payload)
}

func (c *Coordinator) loadKnown(ctx context.Context) (map[int64]bool, error) {
	out := make(map[int64]bool)
	if c.listChannels == nil {
		return out, nil
	}
	ids, err := c.listChannels(ctx)
	if err != nil {
		c.warnf("live sync: list channels: %v\n", err)
		return out, fmt.Errorf("list channels: %w", err)
	}
	for _, id := range ids {
		if id > 0 {
			out[id] = true
		}
	}
	return out, nil
}

func (c *Coordinator) emit(name string, args ...any) {
	if c.events != nil {
		c.events.Emit(name, args...)
	}
}
