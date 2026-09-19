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
	// messages is how many messages the counting pass observed. It is the
	// denominator the apply pass counts towards.
	messages int
}

// ProgressPhase names the stage a history scan is in.
type ProgressPhase string

const (
	// ProgressCounting is the backwards pagination that discovers how much
	// history exists. Totals are unknown until it finishes.
	ProgressCounting ProgressPhase = "counting"
	// ProgressApplying is the forwards pass that projects each page.
	ProgressApplying ProgressPhase = "applying"
	// ProgressWaiting is a Telegram-imposed pause. Nothing is read during it,
	// and it is the one stage long enough to look like a hang.
	ProgressWaiting ProgressPhase = "waiting"
)

// Progress reports how far a history scan has got. Totals are 0 while still
// unknown, so a UI shows an indeterminate indicator rather than "x of 0".
type Progress struct {
	ChannelID     int64
	Phase         ProgressPhase
	PagesDone     int
	PagesTotal    int
	MessagesDone  int
	MessagesTotal int
	// Wait is how long Telegram asked us to pause. Set only for
	// ProgressWaiting; the counters keep their last known values.
	Wait time.Duration
}

type Engine struct {
	db    *sql.DB
	tg    tgclient.Client
	peers PeerResolver

	// OnProgress, if set, is invoked as a history scan advances, including
	// when a read-side FLOOD_WAIT forces a pause. Optional UI hook; nil is
	// fine. It runs on the scanning goroutine, so it must not block.
	OnProgress func(Progress)

	// EmitTomb persists a tomb op for a file whose backing message(s) were
	// found deleted directly on Telegram, bypassing TDrive's own delete
	// path. Required only for ReconcileDeletions; nil makes it a no-op.
	EmitTomb func(channelID int64, fileMsgID int64) error

	mu    stdsync.Mutex
	locks map[int64]*stdsync.Mutex
	// deletionsCurrent marks channels whose last incremental pass applied
	// deletions from a Telegram difference, so ReconcileDeletions can skip
	// its per-message existence check. Guarded by mu.
	deletionsCurrent map[int64]bool
}

// getHistory wraps tg.GetHistory with bounded FLOOD_WAIT retries. Telegram
// rate-limits history reads on large channels; without this a single
// FLOOD_WAIT would abort the whole sync pass.
func (e *Engine) getHistory(ctx context.Context, channelID int64, peer tgclient.InputPeer, minID, offsetID int64, limit int) ([]tgclient.HistoryMessage, error) {
	var page []tgclient.HistoryMessage
	err := e.retryFloodWait(ctx, channelID, "history read", func() error {
		var err error
		page, err = e.tg.GetHistory(ctx, peer, minID, offsetID, limit)
		return err
	})
	return page, err
}

// retryFloodWait runs call, sleeping through bounded FLOOD_WAITs for as long
// as Telegram asks (capped), so one rate limit does not abort a sync pass.
func (e *Engine) retryFloodWait(ctx context.Context, channelID int64, what string, call func() error) error {
	for attempt := 0; ; attempt++ {
		err := call()
		if err == nil {
			return nil
		}
		wait, ok := tgclient.FloodWaitDuration(err)
		if !ok || attempt >= maxFloodWaitRetries {
			return err
		}
		if wait > maxFloodWaitSleep {
			wait = maxFloodWaitSleep
		}
		slog.Warn("sync: FLOOD_WAIT on "+what+", retrying", "channel_id", channelID, "attempt", attempt+1, "wait", wait)
		e.report(Progress{ChannelID: channelID, Phase: ProgressWaiting, Wait: wait})
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
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

func (e *Engine) report(p Progress) {
	if e.OnProgress != nil {
		e.OnProgress(p)
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

// PrepareHardDeleteProjection synchronizes the channel while holding its
// per-channel lock. Writable hard deletes use this stronger boundary because
// local commits can be projected ahead of the contiguous sync watermark. The
// incremental pass fills the missing range first, then rebuilds from the now
// complete local replay log only when it encounters overlap. This avoids an
// unnecessary full Telegram history scan for ordinary local writes.
func (e *Engine) PrepareHardDeleteProjection(ctx context.Context, channelID int64) error {
	lk := e.lockFor(channelID)
	lk.Lock()
	defer lk.Unlock()
	return e.incrementalLocked(ctx, channelID)
}

func (e *Engine) incrementalLocked(ctx context.Context, channelID int64) error {
	replayOverlap, err := e.incrementalLockedWithReplayStatus(ctx, channelID)
	if err != nil || !replayOverlap {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// applyHistoryPlan durably marks an overlap before returning. Rebuild while
	// the caller still owns the channel lock so no successful Incremental call
	// exposes an out-of-order derived projection or leaves repair to a later run.
	return projection.RebuildProjection(e.db, channelID)
}

// incrementalLockedWithReplayStatus reports whether a message fetched above
// the contiguous watermark was already present in replay_log. That can happen
// when a local writable commit was projected ahead of sync, in which case the
// caller must replay the completed log before reporting successful sync.
func (e *Engine) incrementalLockedWithReplayStatus(ctx context.Context, channelID int64) (bool, error) {
	channel, err := projection.GetChannel(e.db, channelID)
	if err != nil {
		return false, fmt.Errorf("sync: read channel authority: %w", err)
	}
	peer, err := e.peers.ResolvePeer(ctx, channelID)
	if err != nil {
		return false, fmt.Errorf("sync: resolve peer: %w", err)
	}

	// A drive flagged for rebuild cannot be moved forward from its watermark:
	// the ops below it were never read, and the ones above were applied against
	// objects that did not exist. It needs the full scan, which owns the repair.
	if channel.NeedsProjectionRebuild {
		slog.Info("sync: rebuild pending, escalating to a full history scan", "channel_id", channelID)
		pts := e.currentPts(ctx, channelID, peer)
		e.setDeletionsCurrent(channelID, false)
		if err := e.authoritativeLocked(ctx, channelID); err != nil {
			return false, err
		}
		return false, e.storePts(channelID, pts)
	}

	watermark, err := readWatermark(e.db, channelID)
	if err != nil {
		return false, err
	}
	parseOpts, err := parseOptionsForChannel(e.db, channelID)
	if err != nil {
		return false, err
	}

	// With a stored pts, Telegram tells us exactly what changed: new messages
	// and deletions in one call, no history scan and no existence check. A
	// zero watermark means the channel was reset and needs the scan regardless.
	replayOverlap := false
	if channel.Pts > 0 && watermark > 0 {
		handled, overlap, err := e.differenceLocked(ctx, channelID, peer, channel.Pts, watermark, parseOpts)
		if err != nil {
			return false, err
		}
		if handled {
			e.setDeletionsCurrent(channelID, true)
			return overlap, nil
		}
		replayOverlap = overlap
		if watermark, err = readWatermark(e.db, channelID); err != nil {
			return false, err
		}
	}

	// No usable pts: scan history from the watermark. Read the pts before the
	// scan so anything landing mid-scan is still covered by the next
	// difference; the watermark filter drops what the scan already applied.
	pts := e.currentPts(ctx, channelID, peer)
	e.setDeletionsCurrent(channelID, false)

	plan, err := e.planHistory(ctx, channelID, peer, watermark)
	if err != nil {
		return false, err
	}
	if len(plan.upperBounds) == 0 {
		err = e.adoptRecentCaptionlessMedia(ctx, channelID, peer, parseOpts)
	} else {
		var overlap bool
		overlap, err = e.applyHistoryPlan(ctx, channelID, peer, watermark, plan, parseOpts)
		replayOverlap = replayOverlap || overlap
	}
	if err != nil {
		return false, err
	}
	return replayOverlap, e.storePts(channelID, pts)
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

	if e.deletionsCurrentFor(channelID) {
		slog.Debug("sync: deletions already applied from channel difference, skipping existence check", "channel_id", channelID)
		return 0, nil
	}

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
	return e.tombstoneMissing(channelID, refs, missingSet), nil
}

// tombstoneMissing tombstones every file with at least one backing message
// in missing and returns how many it tombstoned. Missing even one part makes
// a multipart file's content unrecoverable, so any one missing backing
// message tombstones the whole file.
func (e *Engine) tombstoneMissing(channelID int64, refs []projection.FileMessageRefs, missingSet map[int64]struct{}) int {
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
	return tombstoned
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

	channel, err := projection.GetChannel(e.db, channelID)
	if err != nil {
		return fmt.Errorf("sync: read channel authority: %w", err)
	}
	// A pending rebuild means the log is known to be missing history, so the
	// incremental path would refresh from a watermark that was never earned.
	if channel.InitialSyncDone && !channel.NeedsProjectionRebuild {
		slog.Debug("sync: channel already authoritative, running incremental refresh", "channel_id", channelID)
		return e.incrementalLocked(ctx, channelID)
	}
	return e.authoritativeLocked(ctx, channelID)
}

// authoritativeLocked runs the full-history scan. The caller must already hold
// the channel lock.
func (e *Engine) authoritativeLocked(ctx context.Context, channelID int64) error {
	start := time.Now()
	slog.Info("sync: establishing authoritative full history scan", "channel_id", channelID)

	channel, err := projection.GetChannel(e.db, channelID)
	if err != nil {
		return fmt.Errorf("sync: read channel authority: %w", err)
	}
	parseOpts, err := parseOptionsForChannel(e.db, channelID)
	if err != nil {
		return err
	}
	// Adopting caption-less media is only safe on an empty projection. On a
	// populated one the adopted upload re-applies below the meta/move ops
	// that already placed the file (those are in replay_log and skipped), so
	// the file would land back at root. TDX1 ops replay idempotently either
	// way, which is all a full scan of a populated channel needs.
	if parseOpts.AdoptCaptionlessMedia {
		empty, err := projection.ChannelIsEmpty(e.db, channelID)
		if err != nil {
			return fmt.Errorf("sync: inspect projection: %w", err)
		}
		if !empty {
			slog.Info("sync: projection already populated, skipping caption-less adoption during full scan", "channel_id", channelID)
			parseOpts.AdoptCaptionlessMedia = false
		}
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
	err = e.applyInitialHistoryPlan(ctx, channelID, peer, 0, plan, parseOpts, channel.NeedsProjectionRebuild)
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
	err = e.applyInitialHistoryPlan(ctx, channelID, peer, watermark, plan, parseOpts, false)
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
	messages := 0
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
		messages += len(page)
		e.report(Progress{
			ChannelID:    channelID,
			Phase:        ProgressCounting,
			PagesDone:    len(lowestPerPage),
			MessagesDone: messages,
		})
		// Only an empty page proves the channel is exhausted. A short page
		// does not: Telegram returns fewer than the limit whenever the window
		// it scanned is thinned by deletions or service events. Stopping on
		// one silently truncated the scan, and because the caller then marks
		// the channel authoritative at the highest id seen, every later pass
		// starts above the history that was skipped and can never reach it.
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
	return historyPlan{upperBounds: upperBounds, highestSeen: highestSeen, messages: messages}, nil
}

// applyProgress reports one projected page. Pages are applied oldest-first
// from the end of the plan, so the index maps to a count directly.
func (e *Engine) applyProgress(channelID int64, plan historyPlan, index, messagesDone int) {
	e.report(Progress{
		ChannelID:     channelID,
		Phase:         ProgressApplying,
		PagesDone:     len(plan.upperBounds) - index,
		PagesTotal:    len(plan.upperBounds),
		MessagesDone:  messagesDone,
		MessagesTotal: plan.messages,
	})
}

func (e *Engine) applyHistoryPlan(ctx context.Context, channelID int64, peer tgclient.InputPeer, minID int64, plan historyPlan, parseOpts ParseOptions) (bool, error) {
	messagesDone := 0
	replayOverlap := false
	for i := len(plan.upperBounds) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		page, err := e.getHistory(ctx, channelID, peer, minID, plan.upperBounds[i], defaultPageSize)
		if err != nil {
			return false, fmt.Errorf("sync: get history: %w", err)
		}
		filtered, pageWatermark := filterHistoryPage(page, minID, plan.highestSeen)
		parsed := ParseHistoryPageWithOptions(filtered, parseOpts)
		SortAscending(parsed)
		slog.Debug("sync: applying history page", "channel_id", channelID, "ops", len(parsed))

		tx, err := e.db.Begin()
		if err != nil {
			return false, fmt.Errorf("sync: begin projection: %w", err)
		}
		pageReplayOverlap := false
		for _, p := range parsed {
			alreadySeen, err := projection.ProjectFromOpTx(tx, channelID, p.MsgID, p.Op, p.FromID, p.RawHeader)
			if err != nil {
				_ = tx.Rollback()
				slog.Error("sync: applying op failed, page rolled back", "channel_id", channelID, "msg_id", p.MsgID, "op_type", p.Op.Type, "error", err)
				return false, fmt.Errorf("sync: project msg=%d: %w", p.MsgID, err)
			}
			pageReplayOverlap = pageReplayOverlap || alreadySeen
		}
		if pageReplayOverlap {
			if err := markProjectionRebuildRequiredTx(tx, channelID); err != nil {
				_ = tx.Rollback()
				return false, err
			}
			replayOverlap = true
		}
		if pageWatermark > minID {
			if err := writeWatermarkTx(tx, channelID, pageWatermark); err != nil {
				_ = tx.Rollback()
				return false, err
			}
			minID = pageWatermark
		}
		if err := tx.Commit(); err != nil {
			return false, fmt.Errorf("sync: commit projection: %w", err)
		}
		messagesDone += len(page)
		e.applyProgress(channelID, plan, i, messagesDone)
	}
	return replayOverlap, nil
}

// filterHistoryPage drops the messages a scan must ignore — ids the watermark
// already covers, and ids that arrived after the counting pass fixed the
// scan's upper bound — and reports the highest id it kept. Filtering is done
// in place, so the result aliases page's backing array.
func filterHistoryPage(page []tgclient.HistoryMessage, minID, highestSeen int64) ([]tgclient.HistoryMessage, int64) {
	kept := page[:0]
	watermark := minID
	for _, m := range page {
		if m.MsgID <= minID || m.MsgID > highestSeen {
			continue
		}
		kept = append(kept, m)
		if m.MsgID > watermark {
			watermark = m.MsgID
		}
	}
	return kept, watermark
}

// applyInitialHistoryPlan lands a full-history scan as one atomic unit: either
// the whole history, the optional repair rebuild and the initial-sync marker
// commit together, or nothing does. Three guarantees rest on that. An
// authoritative "this drive has no encryption policy" answer is only sound
// while the marker cannot outlive a partial scan; InitialSyncEmptyChannel's
// refusal to run on a non-empty channel, and the caption-less adoption that
// authoritativeLocked enables only on an empty projection, are only meaningful
// while an abandoned scan leaves no trace behind.
//
// The scan is therefore run as two passes over a spool (see
// initial_scan_spool.go): the network pass parks each fetched page with no
// transaction open, then one transaction replays the spool. backend.InitDB
// caps the pool at one connection, so an open transaction is the whole
// application's database access; e.getHistory sleeps through FLOOD_WAITs for
// minutes, and holding the transaction across those sleeps stalled every
// unrelated read for the length of the scan.
//
// The spool rather than a slice because a million-message drive is a target of
// this codebase and its parsed ops measure ~570 bytes each — over half a
// gigabyte held at once, which the mobile builds do not survive.
func (e *Engine) applyInitialHistoryPlan(ctx context.Context, channelID int64, peer tgclient.InputPeer, minID int64, plan historyPlan, parseOpts ParseOptions, rebuild bool) error {
	if err := ensureInitialScanSpool(e.db); err != nil {
		return err
	}
	// Anything already spooled belongs to a scan that was killed before it
	// could commit. This one re-reads the same range from message zero, so
	// those rows are stale scratch, not progress to resume from.
	if err := clearInitialScanSpool(e.db, channelID); err != nil {
		return err
	}
	if err := e.spoolHistoryPlan(ctx, channelID, peer, minID, plan); err != nil {
		return err
	}
	// Past this point the scan is local work only, and a cancelled context
	// should abandon it rather than leave a half-applied commit's worth of
	// work to roll back.
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := e.commitInitialHistoryPlan(channelID, plan, parseOpts, rebuild); err != nil {
		return err
	}
	// Best effort: the spool is scratch that the next scan clears anyway, and
	// failing here would report a scan that actually landed as failed.
	if err := clearInitialScanSpool(e.db, channelID); err != nil {
		slog.Warn("sync: could not clear the initial-scan spool", "channel_id", channelID, "error", err)
	}
	return nil
}

// spoolHistoryPlan reads every page of plan with no transaction open and parks
// it. Pages are walked oldest-first and each lands in its own short
// transaction, so the connection is free between round trips and a FLOOD_WAIT
// blocks nothing but this scan.
func (e *Engine) spoolHistoryPlan(ctx context.Context, channelID int64, peer tgclient.InputPeer, minID int64, plan historyPlan) error {
	messagesDone := 0
	for i := len(plan.upperBounds) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return err
		}
		page, err := e.getHistory(ctx, channelID, peer, minID, plan.upperBounds[i], defaultPageSize)
		if err != nil {
			return fmt.Errorf("sync: get history: %w", err)
		}
		filtered, _ := filterHistoryPage(page, minID, plan.highestSeen)
		if err := spoolHistoryPage(e.db, channelID, filtered); err != nil {
			return err
		}
		messagesDone += len(page)
		// The network dominates a scan, so page reads are what progress means
		// to a user watching it; the local commit that follows is not worth
		// reporting separately.
		e.applyProgress(channelID, plan, i, messagesDone)
	}
	return nil
}

// commitInitialHistoryPlan projects the spooled scan and marks the channel
// authoritative. Everything it does is local, so the pool's single connection
// is held only for as long as the writes themselves take. Parsing happens here
// rather than at spool time so the projection is fed by the same function over
// the same data as before, with no encode/decode step able to alter an op.
func (e *Engine) commitInitialHistoryPlan(channelID int64, plan historyPlan, parseOpts ParseOptions, rebuild bool) error {
	tx, err := e.db.Begin()
	if err != nil {
		return fmt.Errorf("sync: begin initial projection: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	projected := 0
	err = eachSpooledBatch(tx, channelID, func(batch []tgclient.HistoryMessage) error {
		parsed := ParseHistoryPageWithOptions(batch, parseOpts)
		SortAscending(parsed)
		for _, p := range parsed {
			if _, err := projection.ProjectFromOpTx(tx, channelID, p.MsgID, p.Op, p.FromID, p.RawHeader); err != nil {
				slog.Error("sync: applying op failed during initial scan", "channel_id", channelID, "msg_id", p.MsgID, "op_type", p.Op.Type, "error", err)
				return fmt.Errorf("sync: project msg=%d: %w", p.MsgID, err)
			}
			projected++
		}
		return nil
	})
	if err != nil {
		return err
	}
	slog.Debug("sync: applied initial-scan ops", "channel_id", channelID, "ops", projected)

	// The scan has now read everything the log was missing, but the ops that
	// depended on those objects were banked as applied the first time round and
	// were skipped again just now. Replaying the completed log from scratch is
	// what actually lands them, and doing it here keeps repair and scan atomic.
	if rebuild {
		applied, rejected, err := projection.RebuildProjectionTx(tx, channelID)
		if err != nil {
			return fmt.Errorf("sync: rebuild projection after full scan: %w", err)
		}
		slog.Info("sync: rebuilt projection from the completed log", "channel_id", channelID,
			"applied", applied, "rejected", rejected)
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

func markProjectionRebuildRequiredTx(tx *sql.Tx, channelID int64) error {
	result, err := tx.Exec(`
		UPDATE channels SET needs_projection_rebuild = 1 WHERE channel_id = ?
	`, channelID)
	if err != nil {
		return fmt.Errorf("sync: mark projection rebuild required: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("sync: inspect projection rebuild marker: %w", err)
	}
	if updated != 1 {
		return fmt.Errorf("sync: channel %d not registered", channelID)
	}
	return nil
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
