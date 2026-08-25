package projection

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	ErrNameConflict            = errors.New("projection: portable sibling name conflict")
	ErrRevisionConflict        = errors.New("projection: object revision conflict")
	ErrObjectNotFound          = errors.New("projection: object not found")
	ErrObjectExists            = errors.New("projection: object already exists")
	ErrDestinationMismatch     = errors.New("projection: overwrite destination mismatch")
	ErrContentIncomplete       = errors.New("projection: committed content is incomplete")
	ErrContentAlreadyCommitted = errors.New("projection: content reference is already committed")
)

func isVersionedWritableOp(opType OpType) bool {
	switch opType {
	case OpFileCommit, OpFileReplace, OpFolderCommit, OpRelocate, OpTrashTree:
		return true
	default:
		return false
	}
}

func applyFolderCommit(tx *sql.Tx, channelID int64, op Op) error {
	if !IsFolderID(op.Obj) {
		return fmt.Errorf("%w: folder commit requires d: object", ErrBadOp)
	}
	if err := validateParentAndName(tx, channelID, op.Parent, op.Name); err != nil {
		return err
	}
	var exists int
	err := tx.QueryRow(`
		SELECT 1 FROM folders WHERE channel_id=? AND id=?
	`, channelID, op.Obj).Scan(&exists)
	if err == nil {
		return ErrObjectExists
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("projection: inspect folder commit identity: %w", err)
	}
	if err := insertStrictDirent(tx, channelID, op.Obj, ObjectKindFolder, op.Parent, op.Name, 1); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT INTO folders
		  (channel_id, id, name, parent_id, tombstoned, revision)
		VALUES (?, ?, ?, ?, 0, 1)
	`, channelID, op.Obj, op.Name, op.Parent); err != nil {
		return fmt.Errorf("projection: commit folder: %w", err)
	}
	return nil
}

func validateVersionedWritableOp(op Op) error {
	if op.ProtocolVersion != 1 {
		return fmt.Errorf("%w: writable op version must be 1", ErrBadOp)
	}
	if !utf8.ValidString(op.OpID) || strings.TrimSpace(op.OpID) == "" || len([]byte(op.OpID)) > 128 {
		return fmt.Errorf("%w: writable op id is invalid", ErrBadOp)
	}
	for _, r := range op.OpID {
		if unicode.IsControl(r) || r == '|' {
			return fmt.Errorf("%w: writable op id is invalid", ErrBadOp)
		}
	}
	return nil
}

func projectionOperationExistsTx(tx *sql.Tx, channelID int64, opID string) (bool, error) {
	var one int
	err := tx.QueryRow(`
		SELECT 1 FROM projection_operations
		WHERE channel_id=? AND op_id=?
	`, channelID, opID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("projection: find operation: %w", err)
	}
	return true, nil
}

func recordProjectionOperationTx(tx *sql.Tx, channelID, msgID int64, op Op, outcome string, applyErr error) error {
	if strings.TrimSpace(op.OpID) == "" {
		return nil
	}
	errorText := ""
	if applyErr != nil {
		errorText = applyErr.Error()
	}
	_, err := tx.Exec(`
		INSERT INTO projection_operations
		  (channel_id, op_id, msg_id, op_type, outcome, error)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(channel_id, op_id) DO NOTHING
	`, channelID, op.OpID, msgID, string(op.Type), outcome, errorText)
	if err != nil {
		return fmt.Errorf("projection: record operation: %w", err)
	}
	return nil
}

// ProjectionOperationByID returns the durable replay outcome for an operation.
func ProjectionOperationByID(db *sql.DB, channelID int64, opID string) (ProjectionOperation, bool, error) {
	var operation ProjectionOperation
	err := db.QueryRow(`
		SELECT channel_id, op_id, msg_id, op_type, outcome, error
		FROM projection_operations
		WHERE channel_id=? AND op_id=?
	`, channelID, opID).Scan(
		&operation.ChannelID, &operation.OpID, &operation.MsgID,
		&operation.OpType, &operation.Outcome, &operation.Error,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectionOperation{}, false, nil
	}
	if err != nil {
		return ProjectionOperation{}, false, fmt.Errorf("projection: lookup operation: %w", err)
	}
	return operation, true, nil
}

func applyFileCommit(tx *sql.Tx, channelID, msgID int64, op Op, actorID int64) error {
	if msgID <= 0 {
		return fmt.Errorf("%w: file commit requires message id", ErrBadOp)
	}
	if op.Obj != "" && op.Obj != FileIDPrefix+fmt.Sprint(msgID) {
		return fmt.Errorf("%w: file commit object does not match message id", ErrBadOp)
	}
	if err := validateParentAndName(tx, channelID, op.Parent, op.Name); err != nil {
		return err
	}
	if err := validateContentReference(op); err != nil {
		return err
	}
	if err := validateCommittedContent(tx, channelID, op); err != nil {
		return err
	}
	if err := validateUncommittedContent(tx, channelID, op); err != nil {
		return err
	}
	var exists int
	err := tx.QueryRow(`SELECT 1 FROM files WHERE channel_id=? AND msg_id=?`, channelID, msgID).Scan(&exists)
	if err == nil {
		return ErrObjectExists
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("projection: inspect file commit identity: %w", err)
	}
	objectID := FileIDPrefix + fmt.Sprint(msgID)
	if err := insertStrictDirent(tx, channelID, objectID, "file", op.Parent, op.Name, 1); err != nil {
		return err
	}
	encrypted, encryptionVersion := encryptionValues(op)
	_, err = tx.Exec(`
		INSERT INTO files
		  (channel_id, msg_id, name, size, parent_id, upload_time,
		   uploader_user_id, tombstoned, encrypted, plaintext_size,
		   encryption_version, upload_uuid, part_count, content_msg_id,
		   content_hash, revision)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?, 1)
	`, channelID, msgID, op.Name, op.FileSize, op.Parent, op.FileUploadTime,
		actorID, encrypted, op.PlaintextSize, encryptionVersion,
		op.UploadUUID, op.PartCount, op.ContentMsgID, op.ContentHash)
	if err != nil {
		return fmt.Errorf("projection: commit file: %w", err)
	}
	return insertFileRevision(tx, channelID, msgID, 1, msgID, op, actorID)
}

func applyFileReplace(tx *sql.Tx, channelID, msgID int64, op Op, actorID int64) error {
	fileMsgID, err := parseFileMsgID(op.Obj)
	if err != nil {
		return err
	}
	if op.ExpectedRevision <= 0 {
		return fmt.Errorf("%w: replacement requires expected revision", ErrBadOp)
	}
	if op.RetainedUntil <= 0 {
		return fmt.Errorf("%w: replacement requires retention deadline", ErrBadOp)
	}
	if err := validateContentReference(op); err != nil {
		return err
	}
	if err := validateCommittedContent(tx, channelID, op); err != nil {
		return err
	}
	if err := validateUncommittedContent(tx, channelID, op); err != nil {
		return err
	}
	var currentRevision int64
	var tombstoned int
	err = tx.QueryRow(`
		SELECT revision, tombstoned FROM files
		WHERE channel_id=? AND msg_id=?
	`, channelID, fileMsgID).Scan(&currentRevision, &tombstoned)
	if errors.Is(err, sql.ErrNoRows) || tombstoned != 0 {
		return ErrObjectNotFound
	}
	if err != nil {
		return fmt.Errorf("projection: read replacement target: %w", err)
	}
	if currentRevision != op.ExpectedRevision {
		return ErrRevisionConflict
	}
	if _, err := tx.Exec(`
		UPDATE file_revisions SET retained_until=?
		WHERE channel_id=? AND file_msg_id=? AND revision=(
			SELECT MAX(revision) FROM file_revisions
			WHERE channel_id=? AND file_msg_id=?
		)
	`, op.RetainedUntil, channelID, fileMsgID, channelID, fileMsgID); err != nil {
		return fmt.Errorf("projection: retain superseded revision: %w", err)
	}
	newRevision := currentRevision + 1
	encrypted, encryptionVersion := encryptionValues(op)
	_, err = tx.Exec(`
		UPDATE files SET
			size=?,
			upload_time=CASE WHEN ? > 0 THEN ? ELSE upload_time END,
			encrypted=?, plaintext_size=?, encryption_version=?,
			upload_uuid=?, part_count=?, content_msg_id=?, content_hash=?,
			revision=?
		WHERE channel_id=? AND msg_id=? AND tombstoned=0 AND revision=?
	`, op.FileSize, op.FileUploadTime, op.FileUploadTime,
		encrypted, op.PlaintextSize, encryptionVersion,
		op.UploadUUID, op.PartCount, op.ContentMsgID, op.ContentHash,
		newRevision, channelID, fileMsgID, currentRevision)
	if err != nil {
		return fmt.Errorf("projection: replace file: %w", err)
	}
	if _, err := tx.Exec(`
		UPDATE dirents SET revision=?
		WHERE channel_id=? AND object_id=? AND tombstoned=0
	`, newRevision, channelID, op.Obj); err != nil {
		return fmt.Errorf("projection: revise file dirent: %w", err)
	}
	return insertFileRevision(tx, channelID, fileMsgID, newRevision, msgID, op, actorID)
}

func validateContentReference(op Op) error {
	single := op.ContentMsgID > 0 && op.UploadUUID == "" && op.PartCount == 0
	multipart := op.ContentMsgID == 0 && strings.TrimSpace(op.UploadUUID) != "" && op.PartCount > 0
	if !single && !multipart {
		return fmt.Errorf("%w: file operation requires exactly one content reference", ErrBadOp)
	}
	if op.FileSize < 0 || op.PlaintextSize < 0 {
		return fmt.Errorf("%w: file size cannot be negative", ErrBadOp)
	}
	if multipart {
		if strings.TrimSpace(op.UploadUUID) != op.UploadUUID || len([]byte(op.UploadUUID)) > 128 || op.PartCount > 32 {
			return fmt.Errorf("%w: invalid multipart content reference", ErrBadOp)
		}
		for _, r := range op.UploadUUID {
			if unicode.IsControl(r) || r == '|' {
				return fmt.Errorf("%w: invalid multipart content reference", ErrBadOp)
			}
		}
	}
	if len([]byte(op.ContentHash)) > 128 {
		return fmt.Errorf("%w: content hash is too long", ErrBadOp)
	}
	for _, r := range op.ContentHash {
		if unicode.IsControl(r) || r == '|' {
			return fmt.Errorf("%w: invalid content hash", ErrBadOp)
		}
	}
	return nil
}

func validateCommittedContent(tx *sql.Tx, channelID int64, op Op) error {
	if op.ContentMsgID > 0 {
		return nil
	}
	var count, firstIndex, lastIndex int
	var storedSize int64
	err := tx.QueryRow(`
		SELECT COUNT(*), COALESCE(MIN(part_index), -1),
		       COALESCE(MAX(part_index), -1), COALESCE(SUM(size), 0)
		FROM file_parts WHERE channel_id=? AND upload_uuid=?
	`, channelID, op.UploadUUID).Scan(&count, &firstIndex, &lastIndex, &storedSize)
	if err != nil {
		return fmt.Errorf("projection: inspect committed parts: %w", err)
	}
	if count != op.PartCount || firstIndex != 0 || lastIndex != op.PartCount-1 {
		return ErrContentIncomplete
	}
	if op.FileSize > 0 && storedSize != op.FileSize {
		return ErrContentIncomplete
	}
	return nil
}

func validateUncommittedContent(tx *sql.Tx, channelID int64, op Op) error {
	query := `
		SELECT 1 FROM file_revisions
		WHERE channel_id=? AND content_msg_id=? AND content_msg_id>0
		LIMIT 1
	`
	value := any(op.ContentMsgID)
	if op.ContentMsgID == 0 {
		query = `
			SELECT 1 FROM file_revisions
			WHERE channel_id=? AND upload_uuid=? AND upload_uuid!=''
			LIMIT 1
		`
		value = op.UploadUUID
	}
	var one int
	err := tx.QueryRow(query, channelID, value).Scan(&one)
	if err == nil {
		return ErrContentAlreadyCommitted
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return fmt.Errorf("projection: inspect content ownership: %w", err)
}

func encryptionValues(op Op) (encrypted int, version int) {
	if !op.Encrypted {
		return 0, 0
	}
	version = op.EncryptionVersion
	if version == 0 {
		version = 1
	}
	return 1, version
}

func insertFileRevision(tx *sql.Tx, channelID, fileMsgID, revision, committedMsgID int64, op Op, actorID int64) error {
	encrypted, encryptionVersion := encryptionValues(op)
	_, err := tx.Exec(`
		INSERT INTO file_revisions
		  (channel_id, file_msg_id, revision, content_msg_id, upload_uuid,
		   part_count, size, plaintext_size, content_hash, encrypted,
		   encryption_version, committed_msg_id, actor_user_id, op_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(channel_id, file_msg_id, revision) DO NOTHING
	`, channelID, fileMsgID, revision, op.ContentMsgID, op.UploadUUID,
		op.PartCount, op.FileSize, op.PlaintextSize, op.ContentHash, encrypted,
		encryptionVersion, committedMsgID, actorID, op.OpID)
	if err != nil {
		return fmt.Errorf("projection: insert file revision: %w", err)
	}
	return nil
}

func validateParentAndName(tx *sql.Tx, channelID int64, parentID, name string) error {
	if parentID != RootParent && !IsFolderID(parentID) {
		return fmt.Errorf("%w: parent must be root or d:", ErrBadOp)
	}
	if _, err := CanonicalNameKey(name); err != nil {
		return fmt.Errorf("%w: %v", ErrBadOp, err)
	}
	if parentID == RootParent {
		return nil
	}
	var one int
	err := tx.QueryRow(`
		SELECT 1 FROM folders
		WHERE channel_id=? AND id=? AND tombstoned=0
	`, channelID, parentID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrObjectNotFound
	}
	if err != nil {
		return fmt.Errorf("projection: validate parent: %w", err)
	}
	return nil
}

func insertStrictDirent(tx *sql.Tx, channelID int64, objectID, kind, parentID, name string, revision int64) error {
	key, err := CanonicalNameKey(name)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBadOp, err)
	}
	var existing string
	err = tx.QueryRow(`
		SELECT object_id FROM dirents
		WHERE channel_id=? AND parent_id=? AND name_key=? AND tombstoned=0
	`, channelID, parentID, key).Scan(&existing)
	if err == nil && existing != objectID {
		return ErrNameConflict
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("projection: inspect sibling name: %w", err)
	}
	_, err = tx.Exec(`
		INSERT INTO dirents
		  (channel_id, object_id, object_kind, parent_id, display_name,
		   name_key, revision, tombstoned)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0)
	`, channelID, objectID, kind, parentID, name, key, revision)
	if err != nil {
		return fmt.Errorf("projection: insert dirent: %w", err)
	}
	return nil
}

func applyRelocate(tx *sql.Tx, channelID int64, op Op) error {
	if op.ExpectedRevision <= 0 {
		return fmt.Errorf("%w: relocate requires expected revision", ErrBadOp)
	}
	if err := validateParentAndName(tx, channelID, op.Parent, op.Name); err != nil {
		return err
	}
	kind, err := objectKind(op.Obj)
	if err != nil {
		return err
	}
	var currentParent, currentName string
	var currentRevision int64
	err = tx.QueryRow(`
		SELECT parent_id, display_name, revision FROM dirents
		WHERE channel_id=? AND object_id=? AND tombstoned=0
	`, channelID, op.Obj).Scan(&currentParent, &currentName, &currentRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrObjectNotFound
	}
	if err != nil {
		return fmt.Errorf("projection: read relocate source: %w", err)
	}
	if currentRevision != op.ExpectedRevision {
		return ErrRevisionConflict
	}
	if kind == "folder" && op.Parent != RootParent {
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

	key, err := CanonicalNameKey(op.Name)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBadOp, err)
	}
	var destinationID, destinationKind string
	var destinationRevision int64
	err = tx.QueryRow(`
		SELECT object_id, object_kind, revision FROM dirents
		WHERE channel_id=? AND parent_id=? AND name_key=? AND tombstoned=0
	`, channelID, op.Parent, key).Scan(&destinationID, &destinationKind, &destinationRevision)
	switch {
	case err == nil && destinationID == op.Obj:
		if op.DestinationObj != "" && op.DestinationObj != op.Obj {
			return ErrDestinationMismatch
		}
	case err == nil:
		if !op.Overwrite {
			return ErrNameConflict
		}
		if op.DestinationObj == "" || op.DestinationObj != destinationID {
			return ErrDestinationMismatch
		}
		if op.ExpectedDestinationRevision <= 0 || op.ExpectedDestinationRevision != destinationRevision {
			return ErrRevisionConflict
		}
		if kind != "file" || destinationKind != "file" {
			return fmt.Errorf("%w: directory overwrite is not supported", ErrBadOp)
		}
		if op.DeletedAt <= 0 {
			return fmt.Errorf("%w: overwrite requires deletion time", ErrBadOp)
		}
		if op.PurgeAfter <= op.DeletedAt {
			return fmt.Errorf("%w: overwrite requires later purge deadline", ErrBadOp)
		}
		slog.Info("projection: relocate overwriting existing destination, trashing victim",
			"channel_id", channelID, "destination_object_id", destinationID)
		if err := trashExactFile(tx, channelID, destinationID, op.OpID, op.DeletedAt, op.PurgeAfter); err != nil {
			return err
		}
	case errors.Is(err, sql.ErrNoRows):
		if op.DestinationObj != "" || op.ExpectedDestinationRevision != 0 {
			return ErrDestinationMismatch
		}
	default:
		return fmt.Errorf("projection: inspect relocate destination: %w", err)
	}

	newRevision := currentRevision + 1
	switch kind {
	case "file":
		fileMsgID, err := parseFileMsgID(op.Obj)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`
			UPDATE files SET name=?, parent_id=?, revision=?
			WHERE channel_id=? AND msg_id=? AND tombstoned=0 AND revision=?
		`, op.Name, op.Parent, newRevision, channelID, fileMsgID, currentRevision); err != nil {
			return fmt.Errorf("projection: relocate file: %w", err)
		}
		if err := advanceActiveFileRevision(tx, channelID, fileMsgID, currentRevision, newRevision); err != nil {
			return err
		}
	case "folder":
		if _, err := tx.Exec(`
			UPDATE folders SET name=?, parent_id=?, revision=?
			WHERE channel_id=? AND id=? AND tombstoned=0 AND revision=?
		`, op.Name, op.Parent, newRevision, channelID, op.Obj, currentRevision); err != nil {
			return fmt.Errorf("projection: relocate folder: %w", err)
		}
	}
	_, err = tx.Exec(`
		UPDATE dirents SET parent_id=?, display_name=?, name_key=?, revision=?
		WHERE channel_id=? AND object_id=? AND tombstoned=0 AND revision=?
	`, op.Parent, op.Name, key, newRevision, channelID, op.Obj, currentRevision)
	if err != nil {
		return fmt.Errorf("projection: relocate dirent: %w", err)
	}
	return nil
}

func trashExactFile(tx *sql.Tx, channelID int64, objectID, opID string, deletedAt, purgeAfter int64) error {
	fileMsgID, err := parseFileMsgID(objectID)
	if err != nil {
		return err
	}
	var parentID, name string
	var revision int64
	err = tx.QueryRow(`
		SELECT parent_id, name, revision FROM files
		WHERE channel_id=? AND msg_id=? AND tombstoned=0
	`, channelID, fileMsgID).Scan(&parentID, &name, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrObjectNotFound
	}
	if err != nil {
		return fmt.Errorf("projection: read overwrite victim: %w", err)
	}
	if err := upsertTrashEntry(tx, channelID, objectID, "file", parentID, name, revision, deletedAt, purgeAfter, opID); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		UPDATE files SET tombstoned=1, revision=revision+1
		WHERE channel_id=? AND msg_id=? AND tombstoned=0
	`, channelID, fileMsgID); err != nil {
		return fmt.Errorf("projection: trash overwrite victim: %w", err)
	}
	if err := advanceActiveFileRevision(tx, channelID, fileMsgID, revision, revision+1); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		UPDATE dirents SET tombstoned=1, revision=revision+1
		WHERE channel_id=? AND object_id=? AND tombstoned=0
	`, channelID, objectID); err != nil {
		return fmt.Errorf("projection: trash overwrite dirent: %w", err)
	}
	return nil
}

func applyTrashTree(tx *sql.Tx, channelID int64, op Op) error {
	if op.ExpectedRevision <= 0 || op.DeletedAt <= 0 || op.PurgeAfter <= op.DeletedAt {
		return fmt.Errorf("%w: trash requires revision, deletion time, and later purge deadline", ErrBadOp)
	}
	kind, err := objectKind(op.Obj)
	if err != nil {
		return err
	}
	var parentID, name string
	var revision int64
	err = tx.QueryRow(`
		SELECT parent_id, display_name, revision FROM dirents
		WHERE channel_id=? AND object_id=? AND tombstoned=0
	`, channelID, op.Obj).Scan(&parentID, &name, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrObjectNotFound
	}
	if err != nil {
		return fmt.Errorf("projection: read trash root: %w", err)
	}
	if revision != op.ExpectedRevision {
		return ErrRevisionConflict
	}
	if err := upsertTrashEntry(tx, channelID, op.Obj, kind, parentID, name, revision, op.DeletedAt, op.PurgeAfter, op.OpID); err != nil {
		return err
	}
	if kind == "file" {
		return trashFileTreeRoot(tx, channelID, op.Obj, revision)
	}
	return trashFolderTree(tx, channelID, op.Obj)
}

func upsertTrashEntry(tx *sql.Tx, channelID int64, objectID, kind, parentID, name string, revision, deletedAt, purgeAfter int64, opID string) error {
	_, err := tx.Exec(`
		INSERT INTO trash_entries
		  (channel_id, object_id, object_kind, original_parent_id,
		   original_name, original_revision, deleted_at, purge_after, op_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(channel_id, object_id) DO UPDATE SET
			object_kind=excluded.object_kind,
			original_parent_id=excluded.original_parent_id,
			original_name=excluded.original_name,
			original_revision=excluded.original_revision,
			deleted_at=excluded.deleted_at,
			purge_after=excluded.purge_after,
			op_id=excluded.op_id
	`, channelID, objectID, kind, parentID, name, revision, deletedAt, purgeAfter, opID)
	if err != nil {
		return fmt.Errorf("projection: record trash entry: %w", err)
	}
	return nil
}

func trashFileTreeRoot(tx *sql.Tx, channelID int64, objectID string, currentRevision int64) error {
	fileMsgID, err := parseFileMsgID(objectID)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`
		UPDATE files SET tombstoned=1, revision=revision+1
		WHERE channel_id=? AND msg_id=? AND tombstoned=0
	`, channelID, fileMsgID); err != nil {
		return fmt.Errorf("projection: trash file: %w", err)
	}
	if err := advanceActiveFileRevision(tx, channelID, fileMsgID, currentRevision, currentRevision+1); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		UPDATE dirents SET tombstoned=1, revision=revision+1
		WHERE channel_id=? AND object_id=? AND tombstoned=0
	`, channelID, objectID); err != nil {
		return fmt.Errorf("projection: trash file dirent: %w", err)
	}
	return nil
}

func trashFolderTree(tx *sql.Tx, channelID int64, rootID string) error {
	fileCount, err := countTrashDescendantFiles(tx, channelID, rootID)
	if err != nil {
		return err
	}
	advanced, err := advanceTrashDescendantFileRevisions(tx, channelID, rootID)
	if err != nil {
		return err
	}
	if advanced != fileCount {
		return fmt.Errorf("projection: advanced %d descendant file revisions, want %d", advanced, fileCount)
	}
	if _, err := tx.Exec(`
		WITH RECURSIVE subtree(id) AS (
			SELECT ?
			UNION
			SELECT child.id FROM folders child
			JOIN subtree parent ON child.parent_id=parent.id
			WHERE child.channel_id=? AND child.tombstoned=0
		)
		UPDATE files SET tombstoned=1, revision=revision+1
		WHERE channel_id=? AND tombstoned=0 AND parent_id IN (SELECT id FROM subtree)
	`, rootID, channelID, channelID); err != nil {
		return fmt.Errorf("projection: trash descendant files: %w", err)
	}
	if _, err := tx.Exec(`
		WITH RECURSIVE subtree(id) AS (
			SELECT ?
			UNION
			SELECT child.id FROM folders child
			JOIN subtree parent ON child.parent_id=parent.id
			WHERE child.channel_id=? AND child.tombstoned=0
		)
		UPDATE dirents SET tombstoned=1, revision=revision+1
		WHERE channel_id=? AND tombstoned=0 AND (
			object_id IN (SELECT id FROM subtree)
			OR object_id IN (
				SELECT 'f:' || CAST(msg_id AS TEXT) FROM files
				WHERE channel_id=? AND parent_id IN (SELECT id FROM subtree)
			)
		)
	`, rootID, channelID, channelID, channelID); err != nil {
		return fmt.Errorf("projection: trash descendant dirents: %w", err)
	}
	if _, err := tx.Exec(`
		WITH RECURSIVE subtree(id) AS (
			SELECT ?
			UNION
			SELECT child.id FROM folders child
			JOIN subtree parent ON child.parent_id=parent.id
			WHERE child.channel_id=? AND child.tombstoned=0
		)
		UPDATE folders SET tombstoned=1, revision=revision+1
		WHERE channel_id=? AND tombstoned=0 AND id IN (SELECT id FROM subtree)
	`, rootID, channelID, channelID); err != nil {
		return fmt.Errorf("projection: trash descendant folders: %w", err)
	}
	slog.Info("projection: trashed folder tree", "channel_id", channelID, "root_object_id", rootID, "file_count", fileCount)
	return nil
}

func countTrashDescendantFiles(tx *sql.Tx, channelID int64, rootID string) (int64, error) {
	var count int64
	if err := tx.QueryRow(`
		WITH RECURSIVE subtree(id) AS (
			SELECT ?
			UNION
			SELECT child.id FROM folders child
			JOIN subtree parent ON child.parent_id=parent.id
			WHERE child.channel_id=? AND child.tombstoned=0
		)
		SELECT COUNT(*) FROM files
		WHERE channel_id=? AND tombstoned=0 AND parent_id IN (SELECT id FROM subtree)
	`, rootID, channelID, channelID).Scan(&count); err != nil {
		return 0, fmt.Errorf("projection: count descendant files for trash: %w", err)
	}
	return count, nil
}

func advanceTrashDescendantFileRevisions(tx *sql.Tx, channelID int64, rootID string) (int64, error) {
	result, err := tx.Exec(`
		WITH RECURSIVE subtree(id) AS (
			SELECT ?
			UNION
			SELECT child.id FROM folders child
			JOIN subtree parent ON child.parent_id=parent.id
			WHERE child.channel_id=? AND child.tombstoned=0
		),
		affected(file_msg_id, revision) AS (
			SELECT msg_id, revision FROM files
			WHERE channel_id=? AND tombstoned=0 AND parent_id IN (SELECT id FROM subtree)
		)
		UPDATE file_revisions
		SET revision=revision+1
		WHERE channel_id=? AND retained_until=0
		  AND EXISTS (
			SELECT 1 FROM affected
			WHERE affected.file_msg_id=file_revisions.file_msg_id
			  AND affected.revision=file_revisions.revision
		  )
	`, rootID, channelID, channelID, channelID)
	if err != nil {
		return 0, fmt.Errorf("projection: advance descendant file revisions for trash: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("projection: count advanced descendant file revisions: %w", err)
	}
	return affected, nil
}
