// Package sync owns the channel <-> projection sync engine.
//
// The Engine is shared across all channels (personal + shared, when Step 4
// lands). Per-channel mutexes serialize Initial vs Incremental and prevent
// two concurrent syncs of the same channel.
//
// All projection writes go through projection.ProjectFromOp — the single
// apply path — so tamper detection and idempotency are inherited.
package sync

import (
	stdsync "sync"

	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

const (
	defaultPageSize     = 100
	maxFloodWaitRetries = 5
	maxFloodWaitSleep   = 60 * time.Second
)

type historyPlan struct {
	upperBounds []int64
	highestSeen int64
}

type Engine struct {
	db    *sql.DB
	tg    tgclient.Client
	peers PeerResolver

	// OnFloodWait, if set, is invoked before sleeping out a read-side
	// FLOOD_WAIT. Optional progress/UI hook; nil is fine.
	OnFloodWait func(channelID int64, wait time.Duration)

	mu    stdsync.Mutex
	locks map[int64]*stdsync.Mutex
}

// getHistory wraps tg.GetHistory with bounded FLOOD_WAIT retries. Telegram
// rate-limits history reads on large channels; without this a single
// FLOOD_WAIT would abort the whole sync pass.
func (e *Engine) getHistory(ctx context.Context, channelID int64, peer tgclient.InputPeer, minID, offsetID int64, limit int) ([]tgclient.HistoryMessage, error) {
	for attempt := 0; ; attempt++ {
		page, err := e.tg.GetHistory(ctx, peer, minID, offsetID, limit)
		if err == nil {
			return page, nil
		}
		wait, ok := tgclient.FloodWaitDuration(err)
		if !ok || attempt >= maxFloodWaitRetries {
			return nil, err
		}
		if wait > maxFloodWaitSleep {
			wait = maxFloodWaitSleep
		}
		if e.OnFloodWait != nil {
			e.OnFloodWait(channelID, wait)
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// PeerResolver returns the InputPeer for a given channel id. Channels know
// their access_hash from the channels table once they've been joined.
type PeerResolver interface {
	ResolvePeer(ctx context.Context, channelID int64) (tgclient.InputPeer, error)
}

func NewEngine(db *sql.DB, tg tgclient.Client, peers PeerResolver) *Engine {
	return &Engine{
		db:    db,
		tg:    tg,
		peers: peers,
		locks: make(map[int64]*stdsync.Mutex),
	}
}

func (e *Engine) lockFor(channelID int64) *stdsync.Mutex {
	e.mu.Lock()
	defer e.mu.Unlock()
	if m, ok := e.locks[channelID]; ok {
		return m
	}
	m := &stdsync.Mutex{}
	e.locks[channelID] = m
	return m
}

// Incremental fetches messages newer than channels.last_synced_msg, applies
// them ascending, and bumps the watermark. Idempotent — re-running with no
// new messages is a no-op.
func (e *Engine) Incremental(ctx context.Context, channelID int64) error {
	lk := e.lockFor(channelID)
	lk.Lock()
	defer lk.Unlock()

	peer, err := e.peers.ResolvePeer(ctx, channelID)
	if err != nil {
		return fmt.Errorf("sync: resolve peer: %w", err)
	}

	watermark, err := readWatermark(e.db, channelID)
	if err != nil {
		return err
	}

	plan, err := e.planHistory(ctx, channelID, peer, watermark)
	if err != nil {
		return err
	}
	return e.applyHistoryPlan(ctx, channelID, peer, watermark, plan)
}

// InitialSyncEmptyChannel paginates the full history of a channel that has
// no local state yet. Refuses to run if the channel already has projection
// or replay_log rows (use Incremental + RebuildProjection in that case to
// preserve tamper-detection hashes).
func (e *Engine) InitialSyncEmptyChannel(ctx context.Context, channelID int64) error {
	lk := e.lockFor(channelID)
	lk.Lock()
	defer lk.Unlock()

	empty, err := projection.ChannelIsEmpty(e.db, channelID)
	if err != nil {
		return err
	}
	if !empty {
		return projection.ErrChannelNotEmpty
	}
	watermark := int64(0)

	peer, err := e.peers.ResolvePeer(ctx, channelID)
	if err != nil {
		return fmt.Errorf("sync: resolve peer: %w", err)
	}

	plan, err := e.planHistory(ctx, channelID, peer, watermark)
	if err != nil {
		return err
	}
	if len(plan.upperBounds) == 0 {
		return markInitialSyncDone(e.db, channelID, watermark)
	}
	return e.applyInitialHistoryPlan(ctx, channelID, peer, watermark, plan)
}

func (e *Engine) planHistory(ctx context.Context, channelID int64, peer tgclient.InputPeer, minID int64) (historyPlan, error) {
	var lowestPerPage []int64
	highestSeen := minID
	offsetID := int64(0)
	for {
		page, err := e.getHistory(ctx, channelID, peer, minID, offsetID, defaultPageSize)
		if err != nil {
			return historyPlan{}, fmt.Errorf("sync: get history: %w", err)
		}
		if len(page) == 0 {
			break
		}
		var lowestInPage int64 = page[0].MsgID
		for _, m := range page {
			if m.MsgID < lowestInPage {
				lowestInPage = m.MsgID
			}
			if m.MsgID > highestSeen {
				highestSeen = m.MsgID
			}
		}
		lowestPerPage = append(lowestPerPage, lowestInPage)
		if len(page) < defaultPageSize {
			break
		}
		if offsetID == lowestInPage {
			break
		}
		offsetID = lowestInPage
	}

	if len(lowestPerPage) == 0 {
		return historyPlan{highestSeen: highestSeen}, nil
	}
	upperBounds := make([]int64, len(lowestPerPage))
	upperBounds[0] = highestSeen + 1
	for i := 1; i < len(lowestPerPage); i++ {
		upperBounds[i] = lowestPerPage[i-1]
	}
	return historyPlan{upperBounds: upperBounds, highestSeen: highestSeen}, nil
}

func (e *Engine) applyHistoryPlan(ctx context.Context, channelID int64, peer tgclient.InputPeer, minID int64, plan historyPlan) error {
	for i := len(plan.upperBounds) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return err
		}
		page, err := e.getHistory(ctx, channelID, peer, minID, plan.upperBounds[i], defaultPageSize)
		if err != nil {
			return fmt.Errorf("sync: get history: %w", err)
		}
		pageWatermark := minID
		filtered := page[:0]
		for _, m := range page {
			if m.MsgID <= minID || m.MsgID > plan.highestSeen {
				continue
			}
			filtered = append(filtered, m)
			if m.MsgID > pageWatermark {
				pageWatermark = m.MsgID
			}
		}
		parsed := ParseHistoryPage(filtered)
		SortAscending(parsed)

		tx, err := e.db.Begin()
		if err != nil {
			return fmt.Errorf("sync: begin projection: %w", err)
		}
		for _, p := range parsed {
			if _, err := projection.ProjectFromOpTx(tx, channelID, p.MsgID, p.Op, p.FromID, p.RawHeader); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("sync: project msg=%d: %w", p.MsgID, err)
			}
		}
		if pageWatermark > minID {
			if err := writeWatermarkTx(tx, channelID, pageWatermark); err != nil {
				_ = tx.Rollback()
				return err
			}
			minID = pageWatermark
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("sync: commit projection: %w", err)
		}
	}
	return nil
}

func (e *Engine) applyInitialHistoryPlan(ctx context.Context, channelID int64, peer tgclient.InputPeer, minID int64, plan historyPlan) error {
	tx, err := e.db.Begin()
	if err != nil {
		return fmt.Errorf("sync: begin initial projection: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for i := len(plan.upperBounds) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return err
		}
		page, err := e.getHistory(ctx, channelID, peer, minID, plan.upperBounds[i], defaultPageSize)
		if err != nil {
			return fmt.Errorf("sync: get history: %w", err)
		}
		filtered := page[:0]
		for _, m := range page {
			if m.MsgID <= minID || m.MsgID > plan.highestSeen {
				continue
			}
			filtered = append(filtered, m)
		}
		parsed := ParseHistoryPage(filtered)
		SortAscending(parsed)
		for _, p := range parsed {
			if _, err := projection.ProjectFromOpTx(tx, channelID, p.MsgID, p.Op, p.FromID, p.RawHeader); err != nil {
				return fmt.Errorf("sync: project msg=%d: %w", p.MsgID, err)
			}
		}
	}

	if err := markInitialSyncDoneTx(tx, channelID, plan.highestSeen); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sync: commit initial projection: %w", err)
	}
	return nil
}

func readWatermark(db *sql.DB, channelID int64) (int64, error) {
	var v int64
	err := db.QueryRow(`SELECT last_synced_msg FROM channels WHERE channel_id = ?`, channelID).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("sync: channel %d not registered", channelID)
	}
	if err != nil {
		return 0, err
	}
	return v, nil
}

func writeWatermark(db *sql.DB, channelID int64, msgID int64) error {
	_, err := db.Exec(`UPDATE channels SET last_synced_msg = ? WHERE channel_id = ?`, msgID, channelID)
	return err
}

func writeWatermarkTx(tx *sql.Tx, channelID int64, msgID int64) error {
	_, err := tx.Exec(`UPDATE channels SET last_synced_msg = ? WHERE channel_id = ?`, msgID, channelID)
	return err
}

func markInitialSyncDone(db *sql.DB, channelID int64, watermark int64) error {
	_, err := db.Exec(`
		UPDATE channels
		SET last_synced_msg = ?, initial_sync_done = 1
		WHERE channel_id = ?
	`, watermark, channelID)
	return err
}

func markInitialSyncDoneTx(tx *sql.Tx, channelID int64, watermark int64) error {
	_, err := tx.Exec(`
		UPDATE channels
		SET last_synced_msg = ?, initial_sync_done = 1
		WHERE channel_id = ?
	`, watermark, channelID)
	return err
}
