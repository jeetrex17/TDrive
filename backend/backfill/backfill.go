// Package backfill publishes a user's existing local folders/files into
// their personal Telegram channel as TDX1 mkdir/meta ops, so that wipe-
// and-resync produces an identical projection.
//
// Runs at most once per channel (gated by channels.personal_backfill_done).
// Idempotent and resumable: the snapshot tables capture work to do, the
// backfill_progress cursor records the latest checkpoint, and re-running
// after a crash continues without duplicating ops.
package backfill

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

// OpsPerSec throttles backfill sends. Set just below where Telegram tends
// to hand out FLOOD_WAIT for normal accounts.
const OpsPerSec = 5

type Runner struct {
	db    *sql.DB
	tg    tgclient.Client
	peers PeerResolver
}

type PeerResolver interface {
	ResolvePeer(ctx context.Context, channelID int64) (tgclient.InputPeer, error)
}

type ProgressEvent struct {
	ChannelID int64
	Done      int
	Total     int
	Phase     string // "folders" | "files" | "done"
}

func NewRunner(db *sql.DB, tg tgclient.Client, peers PeerResolver) *Runner {
	return &Runner{db: db, tg: tg, peers: peers}
}

// RunPersonal publishes every folder and file already projected for the
// channel as TDX1 ops, then marks personal_backfill_done = 1.
//
// Skips ops whose obj_id is already represented in replay_log (covers
// re-runs after a partial prior backfill).
//
// onProgress may be nil.
func (r *Runner) RunPersonal(ctx context.Context, channelID int64, onProgress func(ProgressEvent)) error {
	done, err := r.alreadyDone(channelID)
	if err != nil {
		return err
	}
	if done {
		return nil
	}

	peer, err := r.peers.ResolvePeer(ctx, channelID)
	if err != nil {
		return fmt.Errorf("backfill: resolve peer: %w", err)
	}
	actorID, err := r.tg.SelfID(ctx)
	if err != nil {
		return fmt.Errorf("backfill: self user id: %w", err)
	}

	// Pre-scan: any obj_ids already in replay_log skip this run. Captures
	// resumed runs and channels that already had TDX1 ops from someone
	// uploading mid-Step-3 before backfill landed.
	seen, err := r.loadAlreadyPublished(channelID)
	if err != nil {
		return err
	}

	folders, err := r.snapshotFolders(channelID)
	if err != nil {
		return err
	}
	files, err := r.snapshotFiles(channelID)
	if err != nil {
		return err
	}
	folders = orderParentsFirst(folders)

	totalFolders := len(folders)
	totalFiles := len(files)
	slog.Info("backfill: personal channel backfill starting", "channel_id", channelID,
		"folders", totalFolders, "files", totalFiles, "already_published", len(seen))

	throttle := time.NewTicker(time.Second / time.Duration(OpsPerSec))
	defer throttle.Stop()

	doneCount := 0
	for _, f := range folders {
		if _, ok := seen["folder:"+f.ID]; ok {
			doneCount++
			continue
		}
		<-throttle.C
		op := projection.Op{
			Type:   projection.OpMkdir,
			Obj:    f.ID,
			Parent: f.ParentID,
			Name:   f.Name,
		}
		if err := r.publishOne(ctx, peer, channelID, actorID, op, f.ID, "folder"); err != nil {
			return err
		}
		doneCount++
		if onProgress != nil {
			onProgress(ProgressEvent{ChannelID: channelID, Done: doneCount, Total: totalFolders + totalFiles, Phase: "folders"})
		}
	}

	for _, fi := range files {
		objID := fmt.Sprintf("%s%d", projection.FileIDPrefix, fi.MsgID)
		if _, ok := seen["file:"+objID]; ok {
			doneCount++
			continue
		}
		<-throttle.C
		op := projection.Op{
			Type:           projection.OpMeta,
			Obj:            objID,
			Parent:         fi.ParentID,
			Name:           fi.Name,
			FileSize:       fi.Size,
			FileUploadTime: fi.UploadTime,
		}
		if err := r.publishOne(ctx, peer, channelID, actorID, op, objID, "file"); err != nil {
			return err
		}
		doneCount++
		if onProgress != nil {
			onProgress(ProgressEvent{ChannelID: channelID, Done: doneCount, Total: totalFolders + totalFiles, Phase: "files"})
		}
	}

	if err := r.markDone(channelID); err != nil {
		return err
	}
	slog.Info("backfill: personal channel backfill complete", "channel_id", channelID, "published", doneCount)
	if onProgress != nil {
		onProgress(ProgressEvent{ChannelID: channelID, Done: doneCount, Total: totalFolders + totalFiles, Phase: "done"})
	}
	return nil
}

// publishOne sends a single TDX op and projects it inside one tx (so the
// replay_log insert and the backfill_progress checkpoint commit atomically).
// On flood-wait it sleeps and retries.
func (r *Runner) publishOne(ctx context.Context, peer tgclient.InputPeer, channelID int64, actorID int64, op projection.Op, cursor, kind string) error {
	header := projection.Format(op)

	const maxRetries = 5
	var msgID int64
	for attempt := 0; attempt < maxRetries; attempt++ {
		id, err := r.tg.SendControl(ctx, peer, header, true)
		if err == nil {
			msgID = id
			break
		}
		if wait, ok := tgclient.FloodWaitDuration(err); ok {
			if wait <= 0 {
				wait = time.Second
			}
			slog.Warn("backfill: FLOOD_WAIT, retrying", "channel_id", channelID, "op_type", op.Type,
				"attempt", attempt+1, "wait", wait)
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
			continue
		}
		return fmt.Errorf("backfill: send %s: %w", op.Type, err)
	}
	if msgID == 0 {
		return fmt.Errorf("backfill: send failed after retries")
	}

	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("backfill: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := projection.ProjectFromOpTx(tx, channelID, msgID, op, actorID, header); err != nil {
		return fmt.Errorf("backfill: project: %w", err)
	}
	now := time.Now().Unix()
	if _, err := tx.Exec(`
		INSERT INTO backfill_progress (channel_id, cursor_obj_id, cursor_kind, started_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(channel_id) DO UPDATE SET
			cursor_obj_id = excluded.cursor_obj_id,
			cursor_kind = excluded.cursor_kind,
			updated_at = excluded.updated_at
	`, channelID, cursor, kind, now, now); err != nil {
		return fmt.Errorf("backfill: checkpoint: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("backfill: commit: %w", err)
	}
	return nil
}

func (r *Runner) alreadyDone(channelID int64) (bool, error) {
	var done int
	err := r.db.QueryRow(`SELECT personal_backfill_done FROM channels WHERE channel_id = ?`, channelID).Scan(&done)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return done == 1, nil
}

func (r *Runner) markDone(channelID int64) error {
	_, err := r.db.Exec(`UPDATE channels SET personal_backfill_done = 1 WHERE channel_id = ?`, channelID)
	return err
}

// loadAlreadyPublished returns a set of "folder:<id>" / "file:<f:msg_id>"
// keys that are already in replay_log for this channel. We use this rather
// than scanning recent Telegram history because replay_log is the canonical
// local record of what we (or sync) have already projected.
func (r *Runner) loadAlreadyPublished(channelID int64) (map[string]struct{}, error) {
	out := map[string]struct{}{}

	rows, err := r.db.Query(`SELECT op_type, op_payload_json FROM replay_log WHERE channel_id = ?`, channelID)
	if err != nil {
		return nil, fmt.Errorf("backfill: scan replay_log: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var opType, payload string
		if err := rows.Scan(&opType, &payload); err != nil {
			return nil, err
		}
		// Cheap manual parse — we only need obj_id from the payload.
		objID := extractObjFromPayload(payload)
		switch projection.OpType(opType) {
		case projection.OpMkdir:
			if objID != "" {
				out["folder:"+objID] = struct{}{}
			}
		case projection.OpMeta, projection.OpFileUpload:
			if objID != "" {
				out["file:"+objID] = struct{}{}
			}
		}
	}
	return out, rows.Err()
}

func extractObjFromPayload(json string) string {
	const key = `"Obj":"`
	idx := strings.Index(json, key)
	if idx < 0 {
		return ""
	}
	rest := json[idx+len(key):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return ""
	}
	return rest[:end]
}

type folderRow struct {
	ID, Name, ParentID string
}

type fileRow struct {
	MsgID      int64
	Name       string
	Size       int64
	ParentID   string
	UploadTime int64
}

func (r *Runner) snapshotFolders(channelID int64) ([]folderRow, error) {
	rows, err := r.db.Query(`
		SELECT id, name, parent_id FROM folders
		WHERE channel_id = ? AND tombstoned = 0
	`, channelID)
	if err != nil {
		return nil, fmt.Errorf("backfill: read folders: %w", err)
	}
	defer rows.Close()
	var out []folderRow
	for rows.Next() {
		var f folderRow
		if err := rows.Scan(&f.ID, &f.Name, &f.ParentID); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (r *Runner) snapshotFiles(channelID int64) ([]fileRow, error) {
	rows, err := r.db.Query(`
		SELECT msg_id, name, size, parent_id, upload_time FROM files
		WHERE channel_id = ? AND tombstoned = 0
	`, channelID)
	if err != nil {
		return nil, fmt.Errorf("backfill: read files: %w", err)
	}
	defer rows.Close()
	var out []fileRow
	for rows.Next() {
		var f fileRow
		if err := rows.Scan(&f.MsgID, &f.Name, &f.Size, &f.ParentID, &f.UploadTime); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// orderParentsFirst sorts folders so each row appears after its parent.
// BFS-by-depth from root using parent_id graph.
func orderParentsFirst(rows []folderRow) []folderRow {
	byID := make(map[string]folderRow, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
	}
	depthMemo := make(map[string]int, len(rows))
	var depth func(id string, seen map[string]bool) int
	depth = func(id string, seen map[string]bool) int {
		if id == projection.RootParent || id == "" || seen[id] {
			return 0
		}
		if d, ok := depthMemo[id]; ok {
			return d
		}
		seen[id] = true
		f, ok := byID[id]
		if !ok {
			return 0
		}
		d := 1 + depth(f.ParentID, seen)
		depthMemo[id] = d
		return d
	}
	out := make([]folderRow, len(rows))
	copy(out, rows)
	depths := make(map[string]int, len(out))
	for _, r := range out {
		depths[r.ID] = depth(r.ID, make(map[string]bool))
	}
	sort.SliceStable(out, func(i, j int) bool {
		return depths[out[i].ID] < depths[out[j].ID]
	})
	return out
}
