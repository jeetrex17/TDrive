// Package projection owns the writable side of the local file/folder cache.
//
// Invariants enforced here:
//
//  1. Root parent is the empty string "". Never NULL, never "root", never "/".
//  2. Ops apply in strictly ascending (channel_id, msg_id) order. Out-of-order
//     application is undefined; sync must sort before calling ApplyOp.
//  3. Object IDs are namespaced: files use "f:<msg_id>", folders use "d:<uuid>".
//     Parent refs always carry the prefix. "" is the only unprefixed value
//     and means root.
//  4. mkdir into a missing or tombstoned parent is recorded — orphan handling
//     happens at read time, never by rejecting writes.
//  5. move/rename targeting a tombstoned or missing object: ignored, logged.
//  6. A move that would create a cycle is rejected deterministically by walking
//     ancestors before applying. Projection is not mutated on rejection.
//  7. tomb / rmdir is idempotent. Re-applying does nothing.
//  8. Virtual buckets (Unmanaged, Orphaned) are SELECT-time concepts. Never
//     written as folder rows.
//  9. ApplyOp is the only writer to files and folders. The rest of the app
//     reads via read.go and mutates only by calling Local* helpers in writes.go,
//     which themselves call ApplyOp.
//  10. ApplyOp is deterministic: same (channel_id, msg_id, op) against the same
//     prior projection always produces the same result. No clocks, randomness,
//     or external lookups.
package projection

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrCycleRejected = errors.New("projection: move would create cycle")
	ErrBadOp         = errors.New("projection: malformed op")
)

func ApplyOp(tx *sql.Tx, channelID int64, msgID int64, op Op, actorID int64) error {
	switch op.Type {
	case OpMkdir:
		return applyMkdir(tx, channelID, op)
	case OpFileUpload, OpMeta:
		return applyFileMeta(tx, channelID, msgID, op, actorID)
	case OpRename:
		return applyRename(tx, channelID, op)
	case OpMove:
		return applyMove(tx, channelID, op)
	case OpRmdir:
		return applyRmdir(tx, channelID, op)
	case OpTomb:
		return applyTomb(tx, channelID, op)
	default:
		return fmt.Errorf("%w: unknown type %q", ErrBadOp, op.Type)
	}
}

func applyMkdir(tx *sql.Tx, channelID int64, op Op) error {
	if !IsFolderID(op.Obj) {
		return fmt.Errorf("%w: mkdir requires d: obj", ErrBadOp)
	}
	if op.Parent != RootParent && !IsFolderID(op.Parent) {
		return fmt.Errorf("%w: mkdir parent must be root or d:", ErrBadOp)
	}
	if strings.TrimSpace(op.Name) == "" {
		return fmt.Errorf("%w: mkdir requires name", ErrBadOp)
	}
	// First-mkdir-wins: subsequent mkdirs for the same (channel_id, id) are
	// no-ops. Renames/moves go through their own ops so identity state can't
	// be silently rewritten by a duplicate mkdir.
	_, err := tx.Exec(`
		INSERT INTO folders (channel_id, id, name, parent_id, tombstoned)
		VALUES (?, ?, ?, ?, 0)
		ON CONFLICT(channel_id, id) DO NOTHING
	`, channelID, op.Obj, op.Name, op.Parent)
	return err
}

func applyFileMeta(tx *sql.Tx, channelID int64, msgID int64, op Op, actorID int64) error {
	if op.Parent != RootParent && !IsFolderID(op.Parent) {
		return fmt.Errorf("%w: file parent must be root or d:", ErrBadOp)
	}
	if strings.TrimSpace(op.Name) == "" {
		return fmt.Errorf("%w: file op requires name", ErrBadOp)
	}

	fileMsgID := msgID
	if op.Type == OpMeta {
		if !IsFileID(op.Obj) {
			return fmt.Errorf("%w: meta requires f: obj", ErrBadOp)
		}
		parsed, err := parseFileMsgID(op.Obj)
		if err != nil {
			return err
		}
		fileMsgID = parsed
	}
	if fileMsgID <= 0 {
		return fmt.Errorf("%w: file op requires msg id", ErrBadOp)
	}

	_, err := tx.Exec(`
		INSERT INTO files (channel_id, msg_id, name, size, parent_id, upload_time, uploader_user_id, tombstoned)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0)
		ON CONFLICT(channel_id, msg_id) DO UPDATE SET
			name = excluded.name,
			parent_id = excluded.parent_id,
			size = CASE WHEN excluded.size > 0 THEN excluded.size ELSE files.size END,
			upload_time = CASE WHEN excluded.upload_time > 0 THEN excluded.upload_time ELSE files.upload_time END,
			uploader_user_id = CASE WHEN excluded.uploader_user_id > 0 THEN excluded.uploader_user_id ELSE files.uploader_user_id END
	`, channelID, fileMsgID, op.Name, op.FileSize, op.Parent, op.FileUploadTime, actorID)
	return err
}

func applyRename(tx *sql.Tx, channelID int64, op Op) error {
	if strings.TrimSpace(op.Name) == "" {
		return fmt.Errorf("%w: rename requires name", ErrBadOp)
	}
	switch {
	case IsFileID(op.Obj):
		fileMsgID, err := parseFileMsgID(op.Obj)
		if err != nil {
			return err
		}
		_, err = tx.Exec(`
			UPDATE files SET name = ?
			WHERE channel_id = ? AND msg_id = ? AND tombstoned = 0
		`, op.Name, channelID, fileMsgID)
		return err
	case IsFolderID(op.Obj):
		_, err := tx.Exec(`
			UPDATE folders SET name = ?
			WHERE id = ? AND channel_id = ? AND tombstoned = 0
		`, op.Name, op.Obj, channelID)
		return err
	default:
		return fmt.Errorf("%w: rename obj must be f: or d:", ErrBadOp)
	}
}

func applyMove(tx *sql.Tx, channelID int64, op Op) error {
	if op.Parent != RootParent && !IsFolderID(op.Parent) {
		return fmt.Errorf("%w: move parent must be root or d:", ErrBadOp)
	}
	switch {
	case IsFileID(op.Obj):
		fileMsgID, err := parseFileMsgID(op.Obj)
		if err != nil {
			return err
		}
		_, err = tx.Exec(`
			UPDATE files SET parent_id = ?
			WHERE channel_id = ? AND msg_id = ? AND tombstoned = 0
		`, op.Parent, channelID, fileMsgID)
		return err
	case IsFolderID(op.Obj):
		if op.Parent != RootParent {
			if op.Parent == op.Obj {
				return ErrCycleRejected
			}
			cycle, err := wouldCreateCycle(tx, channelID, op.Obj, op.Parent)
			if err != nil {
				return err
			}
			if cycle {
				return ErrCycleRejected
			}
		}
		_, err := tx.Exec(`
			UPDATE folders SET parent_id = ?
			WHERE id = ? AND channel_id = ? AND tombstoned = 0
		`, op.Parent, op.Obj, channelID)
		return err
	default:
		return fmt.Errorf("%w: move obj must be f: or d:", ErrBadOp)
	}
}

func applyRmdir(tx *sql.Tx, channelID int64, op Op) error {
	if !IsFolderID(op.Obj) {
		return fmt.Errorf("%w: rmdir requires d: obj", ErrBadOp)
	}
	_, err := tx.Exec(`
		UPDATE folders SET tombstoned = 1
		WHERE id = ? AND channel_id = ?
	`, op.Obj, channelID)
	return err
}

func applyTomb(tx *sql.Tx, channelID int64, op Op) error {
	if !IsFileID(op.Obj) {
		return fmt.Errorf("%w: tomb requires f: obj", ErrBadOp)
	}
	fileMsgID, err := parseFileMsgID(op.Obj)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
		UPDATE files SET tombstoned = 1
		WHERE channel_id = ? AND msg_id = ?
	`, channelID, fileMsgID)
	return err
}

func wouldCreateCycle(tx *sql.Tx, channelID int64, movingFolderID, newParentID string) (bool, error) {
	cur := newParentID
	visited := make(map[string]bool)
	for cur != RootParent {
		if cur == movingFolderID {
			return true, nil
		}
		if visited[cur] {
			return false, nil
		}
		visited[cur] = true

		var next string
		err := tx.QueryRow(`
			SELECT parent_id FROM folders
			WHERE id = ? AND channel_id = ?
		`, cur, channelID).Scan(&next)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		cur = next
	}
	return false, nil
}

func parseFileMsgID(obj string) (int64, error) {
	if !IsFileID(obj) {
		return 0, fmt.Errorf("%w: not a file id %q", ErrBadOp, obj)
	}
	raw := obj[len(FileIDPrefix):]
	var n int64
	for _, ch := range raw {
		if ch < '0' || ch > '9' {
			return 0, fmt.Errorf("%w: file id has non-digit %q", ErrBadOp, obj)
		}
		n = n*10 + int64(ch-'0')
	}
	if n <= 0 {
		return 0, fmt.Errorf("%w: file id must be positive", ErrBadOp)
	}
	return n, nil
}
