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

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

const defaultPageSize = 100

type Engine struct {
	db    *sql.DB
	tg    tgclient.Client
	peers PeerResolver

	mu    stdsync.Mutex
	locks map[int64]*stdsync.Mutex
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

	var allParsed []ParsedMessage
	highestSeen := watermark
	offsetID := int64(0)
	for {
		page, err := e.tg.GetHistory(ctx, peer, watermark, offsetID, defaultPageSize)
		if err != nil {
			return fmt.Errorf("sync: get history: %w", err)
		}
		if len(page) == 0 {
			break
		}
		lowestInPage := page[0].MsgID
		for _, m := range page {
			if m.MsgID < lowestInPage {
				lowestInPage = m.MsgID
			}
			if m.MsgID > highestSeen {
				highestSeen = m.MsgID
			}
		}
		allParsed = append(allParsed, ParseHistoryPage(page)...)
		if len(page) < defaultPageSize {
			break
		}
		if offsetID == lowestInPage {
			break
		}
		offsetID = lowestInPage
	}

	SortAscending(allParsed)
	for _, p := range allParsed {
		if _, err := projection.ProjectFromOp(e.db, channelID, p.MsgID, p.Op, p.FromID, p.RawHeader); err != nil {
			return fmt.Errorf("sync: project msg=%d: %w", p.MsgID, err)
		}
	}
	if highestSeen > watermark {
		if err := writeWatermark(e.db, channelID, highestSeen); err != nil {
			return err
		}
	}
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

	empty, err := projection.ChannelIsEmpty(e.db, channelID)
	if err != nil {
		return err
	}
	if !empty {
		return projection.ErrChannelNotEmpty
	}

	peer, err := e.peers.ResolvePeer(ctx, channelID)
	if err != nil {
		return fmt.Errorf("sync: resolve peer: %w", err)
	}

	var allParsed []ParsedMessage
	var highestSeen int64
	offsetID := int64(0)
	for {
		page, err := e.tg.GetHistory(ctx, peer, 0, offsetID, defaultPageSize)
		if err != nil {
			return fmt.Errorf("sync: get history: %w", err)
		}
		if len(page) == 0 {
			break
		}
		// Backward paging: lowest msg_id is at the end of the page.
		var lowestInPage int64 = page[0].MsgID
		for _, m := range page {
			if m.MsgID < lowestInPage {
				lowestInPage = m.MsgID
			}
			if m.MsgID > highestSeen {
				highestSeen = m.MsgID
			}
		}
		allParsed = append(allParsed, ParseHistoryPage(page)...)
		if len(page) < defaultPageSize {
			break
		}
		offsetID = lowestInPage
	}

	SortAscending(allParsed)

	for _, p := range allParsed {
		if _, err := projection.ProjectFromOp(e.db, channelID, p.MsgID, p.Op, p.FromID, p.RawHeader); err != nil {
			return fmt.Errorf("sync: project msg=%d: %w", p.MsgID, err)
		}
	}

	if err := markInitialSyncDone(e.db, channelID, highestSeen); err != nil {
		return err
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

func markInitialSyncDone(db *sql.DB, channelID int64, watermark int64) error {
	_, err := db.Exec(`
		UPDATE channels
		SET last_synced_msg = ?, initial_sync_done = 1
		WHERE channel_id = ?
	`, watermark, channelID)
	return err
}
