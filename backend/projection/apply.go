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
//  4. mkdir into a missing or tombstoned parent is recorded; recovery/debug
//     reads can still detect broken parent chains without rejecting history.
//  5. move/rename targeting a tombstoned or missing object: ignored, logged.
//  6. A move that would create a cycle is rejected deterministically by walking
//     ancestors before applying. Projection is not mutated on rejection.
//  7. tomb / rmdir is idempotent. Re-applying does nothing.
//  8. Virtual buckets are SELECT-time concepts. Never written as folder rows.
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
	"strconv"
	"strings"
)

var (
	ErrCycleRejected = errors.New("projection: move would create cycle")
	ErrBadOp         = errors.New("projection: malformed op")
)

func ApplyOp(tx *sql.Tx, channelID int64, msgID int64, op Op, actorID int64) error {
	if isVersionedWritableOp(op.Type) {
		if err := validateVersionedWritableOp(op); err != nil {
			return err
		}
		seen, err := projectionOperationExistsTx(tx, channelID, op.OpID)
		if err != nil {
			return err
		}
		if seen {
			return nil
		}
		var applyErr error
		switch op.Type {
		case OpFileCommit:
			applyErr = applyFileCommit(tx, channelID, msgID, op, actorID)
		case OpFileReplace:
			applyErr = applyFileReplace(tx, channelID, msgID, op, actorID)
		case OpFolderCommit:
			applyErr = applyFolderCommit(tx, channelID, op)
		case OpRelocate:
			applyErr = applyRelocate(tx, channelID, op)
		case OpTrashTree:
			applyErr = applyTrashTree(tx, channelID, op)
		}
		if applyErr != nil {
			return applyErr
		}
		return recordProjectionOperationTx(tx, channelID, msgID, op, OperationApplied, nil)
	}
	switch op.Type {
	case OpMkdir:
		return applyMkdir(tx, channelID, op)
	case OpFileUpload, OpMeta:
		return applyFileMeta(tx, channelID, msgID, op, actorID)
	case OpFilePart:
		return applyFilePart(tx, channelID, msgID, op)
	case OpFileManifest:
		return applyManifest(tx, channelID, msgID, op, actorID)
	case OpRename:
		return applyRename(tx, channelID, op)
	case OpMove:
		return applyMove(tx, channelID, op)
	case OpRmdir:
		return applyRmdir(tx, channelID, op)
	case OpTomb:
		return applyTomb(tx, channelID, op)
	case OpEncConfig:
		return applyEncConfig(tx, channelID, op)
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
	if err != nil {
		return err
	}
	return syncLegacyFolderDirent(tx, channelID, op.Obj)
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

	// Preserve the original uploader: once a real (>0) uploader is recorded
	// (typically by the initial f op), later meta ops never overwrite it.
	// Only fills it in when the row's existing uploader is 0, which is the
	// legacy/backfill path for rows migrated before Step 3.
	encryptedFlag := 0
	if op.Encrypted {
		encryptedFlag = 1
	}
	encryptionVersion := op.EncryptionVersion
	if op.Encrypted && encryptionVersion == 0 {
		encryptionVersion = 1
	}
	_, err := tx.Exec(`
		INSERT INTO files (channel_id, msg_id, name, size, parent_id, upload_time, uploader_user_id, tombstoned, encrypted, plaintext_size, encryption_version, content_msg_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?)
		ON CONFLICT(channel_id, msg_id) DO UPDATE SET
			name = excluded.name,
			parent_id = excluded.parent_id,
			size = CASE WHEN excluded.size > 0 THEN excluded.size ELSE files.size END,
			upload_time = CASE WHEN excluded.upload_time > 0 THEN excluded.upload_time ELSE files.upload_time END,
			uploader_user_id = CASE WHEN files.uploader_user_id > 0 THEN files.uploader_user_id ELSE excluded.uploader_user_id END,
			encrypted = CASE WHEN excluded.encrypted = 1 THEN 1 ELSE files.encrypted END,
			plaintext_size = CASE WHEN excluded.plaintext_size > 0 THEN excluded.plaintext_size ELSE files.plaintext_size END,
			encryption_version = CASE WHEN excluded.encryption_version > 0 THEN excluded.encryption_version ELSE files.encryption_version END
	`, channelID, fileMsgID, op.Name, op.FileSize, op.Parent, op.FileUploadTime, actorID,
		encryptedFlag, op.PlaintextSize, encryptionVersion, fileMsgID)
	if err != nil {
		return err
	}
	if err := syncLegacyFileDirent(tx, channelID, fileMsgID); err != nil {
		return err
	}
	return ensureLegacyFileRevision(tx, channelID, fileMsgID, actorID)
}

// applyFilePart records one part of a multipart file in file_parts. Parts never
// enter the files table, so they never surface as files or as orphans. The
// manifest op (applied last, with a higher msg_id) creates the single logical
// file row that references these parts by upload_uuid.
func applyFilePart(tx *sql.Tx, channelID int64, msgID int64, op Op) error {
	if strings.TrimSpace(op.UploadUUID) == "" {
		return fmt.Errorf("%w: part requires upload uuid", ErrBadOp)
	}
	if op.PartIndex < 0 {
		return fmt.Errorf("%w: part index must be >= 0", ErrBadOp)
	}
	if msgID <= 0 {
		return fmt.Errorf("%w: part requires msg id", ErrBadOp)
	}
	// First-write-wins, like applyMkdir: a duplicate part op (same uuid+index)
	// must not repoint to a different message, which would strand the original
	// body (delete would clean up the replacement and miss the original).
	_, err := tx.Exec(`
		INSERT INTO file_parts (channel_id, upload_uuid, part_index, msg_id, size)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(channel_id, upload_uuid, part_index) DO NOTHING
	`, channelID, op.UploadUUID, op.PartIndex, msgID, op.FileSize)
	return err
}

// applyManifest commits a multipart file: it creates the single logical file
// row whose identity is the manifest's own msg_id (f:<msgID>), mirroring how a
// normal file row keys off its upload message. The part rows already exist
// (their ops carry lower msg_ids), linked by upload_uuid. size is the stored
// (ciphertext) total; plaintext_size is the display size.
func applyManifest(tx *sql.Tx, channelID int64, msgID int64, op Op, actorID int64) error {
	if op.Parent != RootParent && !IsFolderID(op.Parent) {
		return fmt.Errorf("%w: manifest parent must be root or d:", ErrBadOp)
	}
	if strings.TrimSpace(op.Name) == "" {
		return fmt.Errorf("%w: manifest requires name", ErrBadOp)
	}
	if strings.TrimSpace(op.UploadUUID) == "" {
		return fmt.Errorf("%w: manifest requires upload uuid", ErrBadOp)
	}
	if op.PartCount <= 0 {
		return fmt.Errorf("%w: manifest requires part count", ErrBadOp)
	}
	if msgID <= 0 {
		return fmt.Errorf("%w: manifest requires msg id", ErrBadOp)
	}

	encryptedFlag := 0
	if op.Encrypted {
		encryptedFlag = 1
	}
	encryptionVersion := op.EncryptionVersion
	if op.Encrypted && encryptionVersion == 0 {
		encryptionVersion = 1
	}
	_, err := tx.Exec(`
		INSERT INTO files (channel_id, msg_id, name, size, parent_id, upload_time, uploader_user_id, tombstoned, encrypted, plaintext_size, encryption_version, upload_uuid, part_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?)
		ON CONFLICT(channel_id, msg_id) DO UPDATE SET
			name = excluded.name,
			parent_id = excluded.parent_id,
			size = CASE WHEN excluded.size > 0 THEN excluded.size ELSE files.size END,
			upload_time = CASE WHEN excluded.upload_time > 0 THEN excluded.upload_time ELSE files.upload_time END,
			uploader_user_id = CASE WHEN files.uploader_user_id > 0 THEN files.uploader_user_id ELSE excluded.uploader_user_id END,
			encrypted = CASE WHEN excluded.encrypted = 1 THEN 1 ELSE files.encrypted END,
			plaintext_size = CASE WHEN excluded.plaintext_size > 0 THEN excluded.plaintext_size ELSE files.plaintext_size END,
			encryption_version = CASE WHEN excluded.encryption_version > 0 THEN excluded.encryption_version ELSE files.encryption_version END,
			upload_uuid = excluded.upload_uuid,
			part_count = excluded.part_count
	`, channelID, msgID, op.Name, op.FileSize, op.Parent, op.FileUploadTime, actorID,
		encryptedFlag, op.PlaintextSize, encryptionVersion, op.UploadUUID, op.PartCount)
	if err != nil {
		return err
	}
	if err := syncLegacyFileDirent(tx, channelID, msgID); err != nil {
		return err
	}
	return ensureLegacyFileRevision(tx, channelID, msgID, actorID)
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
		return applyLegacyFileRename(tx, channelID, fileMsgID, op.Name)
	case IsFolderID(op.Obj):
		_, err := tx.Exec(`
			UPDATE folders SET name = ?, revision = revision + 1
			WHERE id = ? AND channel_id = ? AND tombstoned = 0
		`, op.Name, op.Obj, channelID)
		if err != nil {
			return err
		}
		return syncLegacyFolderDirent(tx, channelID, op.Obj)
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
		return applyLegacyFileMove(tx, channelID, fileMsgID, op.Parent)
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
			UPDATE folders SET parent_id = ?, revision = revision + 1
			WHERE id = ? AND channel_id = ? AND tombstoned = 0
		`, op.Parent, op.Obj, channelID)
		if err != nil {
			return err
		}
		return syncLegacyFolderDirent(tx, channelID, op.Obj)
	default:
		return fmt.Errorf("%w: move obj must be f: or d:", ErrBadOp)
	}
}

func applyRmdir(tx *sql.Tx, channelID int64, op Op) error {
	if !IsFolderID(op.Obj) {
		return fmt.Errorf("%w: rmdir requires d: obj", ErrBadOp)
	}
	_, err := tx.Exec(`
		UPDATE folders SET tombstoned = 1, revision = revision + 1
		WHERE id = ? AND channel_id = ? AND tombstoned = 0
	`, op.Obj, channelID)
	if err != nil {
		return err
	}
	return syncLegacyFolderDirent(tx, channelID, op.Obj)
}

func applyTomb(tx *sql.Tx, channelID int64, op Op) error {
	if !IsFileID(op.Obj) {
		return fmt.Errorf("%w: tomb requires f: obj", ErrBadOp)
	}
	fileMsgID, err := parseFileMsgID(op.Obj)
	if err != nil {
		return err
	}
	return applyLegacyFileTomb(tx, channelID, fileMsgID)
}

func applyEncConfig(tx *sql.Tx, channelID int64, op Op) error {
	version := op.ConfigVersion
	if version == 0 {
		version = 1
	}
	err := PutEncryptionConfigTx(tx, EncryptionConfig{
		ChannelID:        channelID,
		Enabled:          true,
		KDFSalt:          op.KDFSalt,
		KDFParamsJSON:    op.KDFParamsJSON,
		WrappedMasterKey: op.WrappedMasterKey,
		KeyCheck:         op.KeyCheck,
		Hint:             strings.TrimSpace(op.Hint),
		Version:          version,
	})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrBadOp, err)
	}
	return nil
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
	if raw == "" {
		return 0, fmt.Errorf("%w: file id must be positive", ErrBadOp)
	}
	for _, ch := range raw {
		if ch < '0' || ch > '9' {
			return 0, fmt.Errorf("%w: file id has non-digit %q", ErrBadOp, obj)
		}
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: file id out of range %q", ErrBadOp, obj)
	}
	if n <= 0 {
		return 0, fmt.Errorf("%w: file id must be positive", ErrBadOp)
	}
	return n, nil
}
