package sync

import (
	"context"
	"fmt"
	"log/slog"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

// differenceLocked applies Telegram's channel difference since pts: new
// messages project exactly like a history page, deleted backing messages
// tombstone their files, and channels.pts advances with every page. It
// reports handled=false when Telegram refuses the difference (TooLong); the
// caller then falls back to the history scan. replayOverlap covers the pages
// that were applied either way. Caller holds the channel lock.
func (e *Engine) differenceLocked(ctx context.Context, channelID int64, peer tgclient.InputPeer, pts, watermark int64, parseOpts ParseOptions) (handled bool, replayOverlap bool, err error) {
	for {
		if err := ctx.Err(); err != nil {
			return false, replayOverlap, err
		}
		diff, err := e.getDifference(ctx, channelID, peer, pts)
		if err != nil {
			return false, replayOverlap, fmt.Errorf("sync: get channel difference: %w", err)
		}
		if diff.TooLong {
			slog.Info("sync: channel difference too long, falling back to history scan", "channel_id", channelID, "pts", pts)
			return false, replayOverlap, nil
		}
		overlap, err := e.applyDifference(channelID, diff, &watermark, parseOpts)
		if err != nil {
			return false, replayOverlap, err
		}
		replayOverlap = replayOverlap || overlap
		e.tombstoneDeleted(channelID, diff.DeletedIDs)
		pts = diff.Pts
		if diff.Final {
			return true, replayOverlap, nil
		}
	}
}

// applyDifference projects one difference page in a single transaction and
// advances the watermark and pts with it. Messages at or below the watermark
// were already applied by the scan that bootstrapped pts, so they are skipped
// rather than reported as replay overlap.
func (e *Engine) applyDifference(channelID int64, diff tgclient.ChannelDifference, watermark *int64, parseOpts ParseOptions) (bool, error) {
	filtered := make([]tgclient.HistoryMessage, 0, len(diff.NewMessages))
	pageWatermark := *watermark
	for _, m := range diff.NewMessages {
		if m.MsgID <= *watermark {
			continue
		}
		filtered = append(filtered, m)
		if m.MsgID > pageWatermark {
			pageWatermark = m.MsgID
		}
	}
	parsed := ParseHistoryPageWithOptions(filtered, parseOpts)
	SortAscending(parsed)
	slog.Debug("sync: applying channel difference", "channel_id", channelID, "ops", len(parsed), "deleted", len(diff.DeletedIDs), "pts", diff.Pts)

	tx, err := e.db.Begin()
	if err != nil {
		return false, fmt.Errorf("sync: begin projection: %w", err)
	}
	overlap := false
	for _, p := range parsed {
		alreadySeen, err := projection.ProjectFromOpTx(tx, channelID, p.MsgID, p.Op, p.FromID, p.RawHeader)
		if err != nil {
			_ = tx.Rollback()
			return false, fmt.Errorf("sync: project msg=%d: %w", p.MsgID, err)
		}
		overlap = overlap || alreadySeen
	}
	if overlap {
		if err := markProjectionRebuildRequiredTx(tx, channelID); err != nil {
			_ = tx.Rollback()
			return false, err
		}
	}
	if pageWatermark > *watermark {
		if err := writeWatermarkTx(tx, channelID, pageWatermark); err != nil {
			_ = tx.Rollback()
			return false, err
		}
	}
	if err := projection.SetChannelPtsTx(tx, channelID, diff.Pts); err != nil {
		_ = tx.Rollback()
		return false, fmt.Errorf("sync: store channel pts: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("sync: commit projection: %w", err)
	}
	*watermark = pageWatermark
	return overlap, nil
}

// tombstoneDeleted maps message ids Telegram reported deleted to the live
// files they back and tombstones those files, the same way ReconcileDeletions
// does after an existence check.
func (e *Engine) tombstoneDeleted(channelID int64, deleted []int64) {
	if e.EmitTomb == nil || len(deleted) == 0 {
		return
	}
	refs, err := projection.LiveFileMessageIDs(e.db, channelID)
	if err != nil {
		slog.Error("sync: list live files for difference", "channel_id", channelID, "error", err)
		return
	}
	missing := make(map[int64]struct{}, len(deleted))
	for _, id := range deleted {
		missing[id] = struct{}{}
	}
	e.tombstoneMissing(channelID, refs, missing)
}

// getDifference wraps tg.GetChannelDifference with the same bounded
// FLOOD_WAIT retries as history reads.
func (e *Engine) getDifference(ctx context.Context, channelID int64, peer tgclient.InputPeer, pts int64) (tgclient.ChannelDifference, error) {
	var diff tgclient.ChannelDifference
	err := e.retryFloodWait(ctx, channelID, "channel difference", func() error {
		var err error
		diff, err = e.tg.GetChannelDifference(ctx, peer, pts, defaultPageSize)
		return err
	})
	return diff, err
}

// currentPts asks Telegram for the channel's pts before a history scan. 0
// (with a warning) leaves differences disabled until the next scan, so a
// failure here never blocks the scan itself.
func (e *Engine) currentPts(ctx context.Context, channelID int64, peer tgclient.InputPeer) int64 {
	pts, err := e.tg.GetChannelPts(ctx, peer)
	if err != nil {
		slog.Warn("sync: channel pts unavailable, differences disabled until next scan", "channel_id", channelID, "error", err)
		return 0
	}
	return pts
}

func (e *Engine) storePts(channelID, pts int64) error {
	if pts <= 0 {
		return nil
	}
	if err := projection.SetChannelPts(e.db, channelID, pts); err != nil {
		return fmt.Errorf("sync: store channel pts: %w", err)
	}
	return nil
}

// setDeletionsCurrent records whether the last incremental pass for the
// channel already applied deletions from a difference, which makes the
// per-message existence check in ReconcileDeletions redundant.
func (e *Engine) setDeletionsCurrent(channelID int64, current bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.deletionsCurrent == nil {
		e.deletionsCurrent = make(map[int64]bool)
	}
	e.deletionsCurrent[channelID] = current
}

func (e *Engine) deletionsCurrentFor(channelID int64) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.deletionsCurrent[channelID]
}
