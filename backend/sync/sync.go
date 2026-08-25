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
	"log/slog"
	"time"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

const (
	defaultPageSize     = 100
	maxFloodWaitRetries = 5
	maxFloodWaitSleep   = 60 * time.Second
)

var errHistoryPaginationNoProgress = errors.New("sync: history pagination made no progress")

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

	// EmitTomb persists a tomb op for a file whose backing message(s) were
	// found deleted directly on Telegram, bypassing TDrive's own delete
	// path. Required only for ReconcileDeletions; nil makes it a no-op.
	EmitTomb func(channelID int64, fileMsgID int64) error

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
		slog.Warn("sync: FLOOD_WAIT on history read, retrying", "channel_id", channelID, "attempt", attempt+1, "wait", wait)
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
	start := time.Now()
	slog.Debug("sync: incremental sync starting", "channel_id", channelID)
	err := e.incrementalLocked(ctx, channelID)
	if err != nil {
		slog.Error("sync: incremental sync failed", "channel_id", channelID, "elapsed", time.Since(start), "error", err)
		return err
	}
	slog.Info("sync: incremental sync completed", "channel_id", channelID, "elapsed", time.Since(start))
	return nil
}

func (e *Engine) incrementalLocked(ctx context.Context, channelID int64) error {
	peer, err := e.peers.ResolvePeer(ctx, channelID)
	if err != nil {
		return fmt.Errorf("sync: resolve peer: %w", err)
	}

	watermark, err := readWatermark(e.db, channelID)
	if err != nil {
		return err
	}
	parseOpts, err := parseOptionsForChannel(e.db, channelID)
	if err != nil {
		return err
	}

	plan, err := e.planHistory(ctx, channelID, peer, watermark)
	if err != nil {
		return err
	}
	if len(plan.upperBounds) == 0 {
		return e.adoptRecentCaptionlessMedia(ctx, channelID, peer, parseOpts)
	}
	return e.applyHistoryPlan(ctx, channelID, peer, watermark, plan, parseOpts)
}

// ReconcileDeletions checks every locally-live file's backing Telegram
// message(s) in channelID and tombstones any file for which at least one
// backing message (its own, or for a multipart upload, any part) has been
// deleted directly on Telegram, bypassing TDrive's own delete path. Missing
// even one part makes a multipart file's content unrecoverable, so any one
// missing backing message tombstones the whole file. Returns the number of
// files tombstoned this pass. A nil EmitTomb makes this a no-op.
func (e *Engine) ReconcileDeletions(ctx context.Context, channelID int64) (int, error) {
	if e.EmitTomb == nil {
		return 0, nil
	}
	lk := e.lockFor(channelID)
	lk.Lock()
	defer lk.Unlock()

	refs, err := projection.LiveFileMessageIDs(e.db, channelID)
	if err != nil {
		return 0, fmt.Errorf("sync: list live files: %w", err)
	}
	if len(refs) == 0 {
		return 0, nil
	}

	var allIDs []int64
	for _, ref := range refs {
		allIDs = append(allIDs, ref.BackingMsgIDs...)
	}

	peer, err := e.peers.ResolvePeer(ctx, channelID)
	if err != nil {
		return 0, fmt.Errorf("sync: resolve peer: %w", err)
	}
	missing, err := e.tg.MissingMessages(ctx, peer, allIDs)
	if err != nil {
		return 0, fmt.Errorf("sync: check message existence: %w", err)
	}
	if len(missing) == 0 {
		return 0, nil
	}
	missingSet := make(map[int64]struct{}, len(missing))
	for _, id := range missing {
		missingSet[id] = struct{}{}
	}

	tombstoned := 0
	for _, ref := range refs {
		gone := false
		for _, id := range ref.BackingMsgIDs {
			if _, ok := missingSet[id]; ok {
				gone = true
				break
			}
		}
		if !gone {
			continue
		}
		slog.Warn("sync: file's backing message deleted directly on Telegram, tombstoning", "channel_id", channelID, "file_msg_id", ref.FileMsgID)
		if err := e.EmitTomb(channelID, ref.FileMsgID); err != nil {
			slog.Error("sync: reconcile tombstone failed", "channel_id", channelID, "file_msg_id", ref.FileMsgID, "error", err)
			continue
		}
		tombstoned++
	}
	return tombstoned, nil
}

// EnsureAuthoritative guarantees that the local projection has observed the
// channel's complete Telegram history before returning successfully. It is
// used for security decisions whose absence is meaningful, such as deciding
// that a personal drive has no encryption policy.
//
// A previously completed full scan only needs an incremental refresh. Older
// databases without the persisted initial-sync marker are reconciled from
// message zero through the same idempotent replay-log path, even when their
// projection is already non-empty. The marker and all newly discovered ops
// commit atomically, so cancellation or a partial history failure never turns
// an unknown policy into an authoritative plaintext policy.
func (e *Engine) EnsureAuthoritative(ctx context.Context, channelID int64) error {
	lk := e.lockFor(channelID)
	lk.Lock()
	defer lk.Unlock()
	start := time.Now()

	channel, err := projection.GetChannel(e.db, channelID)
	if err != nil {
		return fmt.Errorf("sync: read channel authority: %w", err)
	}
	if channel.InitialSyncDone {
		slog.Debug("sync: channel already authoritative, running incremental refresh", "channel_id", channelID)
		return e.incrementalLocked(ctx, channelID)
	}
	slog.Info("sync: establishing authoritative full history scan", "channel_id", channelID)

	parseOpts, err := parseOptionsForChannel(e.db, channelID)
	if err != nil {
		return err
	}
	peer, err := e.peers.ResolvePeer(ctx, channelID)
	if err != nil {
		return fmt.Errorf("sync: resolve peer: %w", err)
	}
	plan, err := e.planHistory(ctx, channelID, peer, 0)
	if err != nil {
		return err
	}
	if len(plan.upperBounds) == 0 {
		slog.Info("sync: authoritative scan found no history", "channel_id", channelID, "elapsed", time.Since(start))
		return markInitialSyncDone(e.db, channelID, 0)
	}
	err = e.applyInitialHistoryPlan(ctx, channelID, peer, 0, plan, parseOpts)
	if err != nil {
		slog.Error("sync: authoritative scan failed", "channel_id", channelID, "elapsed", time.Since(start), "error", err)
		return err
	}
	slog.Info("sync: authoritative scan completed", "channel_id", channelID, "elapsed", time.Since(start), "pages", len(plan.upperBounds), "highest_msg_id", plan.highestSeen)
	return nil
}

// InitialSyncEmptyChannel paginates the full history of a channel that has
// no local state yet. Refuses to run if the channel already has projection
// or replay_log rows (use Incremental + RebuildProjection in that case to
// preserve tamper-detection hashes).
func (e *Engine) InitialSyncEmptyChannel(ctx context.Context, channelID int64) error {
	lk := e.lockFor(channelID)
	lk.Lock()
	defer lk.Unlock()
	start := time.Now()
	slog.Info("sync: initial sync of empty channel starting", "channel_id", channelID)

	empty, err := projection.ChannelIsEmpty(e.db, channelID)
	if err != nil {
		return err
	}
	if !empty {
		return projection.ErrChannelNotEmpty
	}
	watermark := int64(0)
	parseOpts, err := parseOptionsForChannel(e.db, channelID)
	if err != nil {
		return err
	}

	peer, err := e.peers.ResolvePeer(ctx, channelID)
	if err != nil {
		return fmt.Errorf("sync: resolve peer: %w", err)
	}

	plan, err := e.planHistory(ctx, channelID, peer, watermark)
	if err != nil {
		return err
	}
	if len(plan.upperBounds) == 0 {
		slog.Info("sync: initial sync found no history", "channel_id", channelID, "elapsed", time.Since(start))
		return markInitialSyncDone(e.db, channelID, watermark)
	}
	err = e.applyInitialHistoryPlan(ctx, channelID, peer, watermark, plan, parseOpts)
	if err != nil {
		slog.Error("sync: initial sync failed", "channel_id", channelID, "elapsed", time.Since(start), "error", err)
		return err
	}
	slog.Info("sync: initial sync completed", "channel_id", channelID, "elapsed", time.Since(start), "pages", len(plan.upperBounds), "highest_msg_id", plan.highestSeen)
	return nil
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
		if offsetID != 0 && lowestInPage >= offsetID {
			return historyPlan{}, errHistoryPaginationNoProgress
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

func (e *Engine) applyHistoryPlan(ctx context.Context, channelID int64, peer tgclient.InputPeer, minID int64, plan historyPlan, parseOpts ParseOptions) error {
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
		parsed := ParseHistoryPageWithOptions(filtered, parseOpts)
		SortAscending(parsed)
		slog.Debug("sync: applying history page", "channel_id", channelID, "ops", len(parsed))

		tx, err := e.db.Begin()
		if err != nil {
			return fmt.Errorf("sync: begin projection: %w", err)
		}
		for _, p := range parsed {
			if _, err := projection.ProjectFromOpTx(tx, channelID, p.MsgID, p.Op, p.FromID, p.RawHeader); err != nil {
				_ = tx.Rollback()
				slog.Error("sync: applying op failed, page rolled back", "channel_id", channelID, "msg_id", p.MsgID, "op_type", p.Op.Type, "error", err)
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

func (e *Engine) applyInitialHistoryPlan(ctx context.Context, channelID int64, peer tgclient.InputPeer, minID int64, plan historyPlan, parseOpts ParseOptions) error {
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
		parsed := ParseHistoryPageWithOptions(filtered, parseOpts)
		SortAscending(parsed)
		slog.Debug("sync: applying initial-scan page", "channel_id", channelID, "ops", len(parsed))
		for _, p := range parsed {
			if _, err := projection.ProjectFromOpTx(tx, channelID, p.MsgID, p.Op, p.FromID, p.RawHeader); err != nil {
				slog.Error("sync: applying op failed during initial scan", "channel_id", channelID, "msg_id", p.MsgID, "op_type", p.Op.Type, "error", err)
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

func (e *Engine) adoptRecentCaptionlessMedia(ctx context.Context, channelID int64, peer tgclient.InputPeer, parseOpts ParseOptions) error {
	if !parseOpts.AdoptCaptionlessMedia {
		return nil
	}
	page, err := e.getHistory(ctx, channelID, peer, 0, 0, defaultPageSize)
	if err != nil {
		return fmt.Errorf("sync: get recent history: %w", err)
	}
	parsed := ParseHistoryPageWithOptions(page, parseOpts)
	SortAscending(parsed)
	adopted := parsed[:0]
	for _, p := range parsed {
		if p.AdoptedCaptionless {
			adopted = append(adopted, p)
		}
	}
	if len(adopted) == 0 {
		return nil
	}
	slog.Debug("sync: adopting captionless media as root files", "channel_id", channelID, "count", len(adopted))

	tx, err := e.db.Begin()
	if err != nil {
		return fmt.Errorf("sync: begin captionless adoption: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, p := range adopted {
		if _, err := projection.ProjectFromOpTx(tx, channelID, p.MsgID, p.Op, p.FromID, p.RawHeader); err != nil {
			return fmt.Errorf("sync: adopt captionless media msg=%d: %w", p.MsgID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sync: commit captionless adoption: %w", err)
	}
	return nil
}

func parseOptionsForChannel(db *sql.DB, channelID int64) (ParseOptions, error) {
	ch, err := projection.GetChannel(db, channelID)
	if err != nil {
		return ParseOptions{}, err
	}
	return ParseOptions{AdoptCaptionlessMedia: ch.Kind == projection.KindPersonal}, nil
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
