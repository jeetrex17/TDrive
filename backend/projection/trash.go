// trash.go is the read and local-claim side of the trash model. The writes
// themselves are ops: OpTrashTree puts an object in the trash, OpRestoreTree
// takes it back out, and OpHardDeleteTree purges it. Nothing here mutates the
// namespace.
//
// Purge timing lives here rather than in the appliers on purpose. ApplyOp must
// stay deterministic (package invariant 10), so it can never read a clock and
// can never decide whether a retention window has elapsed. The clock belongs at
// the only place that matters: an installation physically deletes Telegram
// bodies only for a marker it registered an intent for, so refusing to register
// that intent before purge_after is what makes early destruction impossible.
package projection

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// DefaultTrashRetention is how long a deleted object stays restorable. It
// matches the writable mount's window so an object deleted from the GUI and one
// deleted through the mount age out together.
const DefaultTrashRetention = 30 * 24 * time.Hour

// maxTrashListPageSize bounds one trash listing. The trash is a small working
// set by construction -- expired entries leave it -- and an unbounded read would
// be the one place a huge deletion could stall the UI thread.
const maxTrashListPageSize = 1000

var ErrTrashEntryNotFound = errors.New("projection: trash entry not found")

// ErrTrashEntryNotExpired reports a purge attempted before purge_after. It is
// the retention guarantee: an automatic sweep can never destroy bytes early.
var ErrTrashEntryNotExpired = errors.New("projection: trash entry retention has not elapsed")

// TrashEntry is one restorable object. Times are unix seconds, matching the
// wire fields they were projected from.
type TrashEntry struct {
	ObjectID         string
	ObjectKind       string
	OriginalParentID string
	OriginalName     string
	OriginalRevision int64
	DeletedAt        int64
	PurgeAfter       int64
	OpID             string
	// Size is the logical size of a trashed file, plaintext where the file is
	// encrypted, and zero for a folder.
	Size int64
	// What the trash view needs to draw a file the way the drive drew it: the
	// revision that addresses its thumbnail, and whether it wears a lock. Both
	// are zero for a folder, which has neither.
	Revision  int64
	Encrypted bool
}

// DeterministicOpID derives a stable operation id from a purpose label and the
// exact state a mutation was planned against. Retrying an interrupted trash,
// restore or purge re-derives the same id, so the projection deduplicates the
// retry instead of stacking a second operation on the same object.
func DeterministicOpID(purpose, objectID string, revision int64) string {
	digest := sha256.Sum256(fmt.Appendf(nil, "tdrive.trash.v1\x00%s\x00%s\x00%d", purpose, objectID, revision))
	return purpose + "-" + hex.EncodeToString(digest[:16])
}

// ListTrashEntries returns the channel's trash, most recently deleted first.
func ListTrashEntries(db *sql.DB, channelID int64) ([]TrashEntry, error) {
	return queryTrashEntries(db, channelID, `
		ORDER BY entry.deleted_at DESC, entry.object_id
		LIMIT ?
	`, maxTrashListPageSize)
}

// ExpiredTrashEntries returns entries whose retention window closed at or
// before now (unix seconds), oldest first, so a sweep purges in the order the
// user deleted them.
func ExpiredTrashEntries(db *sql.DB, channelID int64, now int64, limit int) ([]TrashEntry, error) {
	if now <= 0 {
		return nil, fmt.Errorf("projection: expired trash entries: current time required")
	}
	if limit <= 0 || limit > maxTrashListPageSize {
		return nil, fmt.Errorf("projection: expired trash entries: limit must be 1..%d", maxTrashListPageSize)
	}
	return queryTrashEntries(db, channelID, `
		AND entry.purge_after <= ?
		ORDER BY entry.purge_after, entry.object_id
		LIMIT ?
	`, now, limit)
}

// TrashEntryByID reads one entry, reporting ErrTrashEntryNotFound when the
// object is not in the trash.
func TrashEntryByID(db *sql.DB, channelID int64, objectID string) (TrashEntry, error) {
	entries, err := queryTrashEntries(db, channelID, `AND entry.object_id=?`, objectID)
	if err != nil {
		return TrashEntry{}, err
	}
	if len(entries) == 0 {
		return TrashEntry{}, ErrTrashEntryNotFound
	}
	return entries[0], nil
}

// queryTrashEntries runs the shared projection with a caller-supplied tail. The
// size join is left outer because a folder entry has no files row; SQLite's
// two-argument max picks plaintext size over stored size for encrypted files,
// which is the size the user recognises.
func queryTrashEntries(db *sql.DB, channelID int64, tail string, args ...any) ([]TrashEntry, error) {
	if db == nil {
		return nil, fmt.Errorf("projection: list trash: db is nil")
	}
	if channelID == 0 {
		return nil, fmt.Errorf("projection: list trash: channel id required")
	}
	rows, err := db.Query(`
		SELECT entry.object_id, entry.object_kind, entry.original_parent_id,
		       entry.original_name, entry.original_revision, entry.deleted_at,
		       entry.purge_after, entry.op_id,
		       COALESCE(MAX(file.plaintext_size, file.size), 0),
		       COALESCE(file.revision, 0), COALESCE(file.encrypted, 0)
		FROM trash_entries entry
		LEFT JOIN files file
		  ON file.channel_id=entry.channel_id
		 AND entry.object_kind='file'
		 AND file.msg_id=CAST(SUBSTR(entry.object_id, 3) AS INTEGER)
		WHERE entry.channel_id=?
	`+tail, append([]any{channelID}, args...)...)
	if err != nil {
		return nil, fmt.Errorf("projection: list trash: %w", err)
	}
	defer rows.Close()

	var entries []TrashEntry
	for rows.Next() {
		var entry TrashEntry
		var encrypted int
		if err := rows.Scan(
			&entry.ObjectID, &entry.ObjectKind, &entry.OriginalParentID,
			&entry.OriginalName, &entry.OriginalRevision, &entry.DeletedAt,
			&entry.PurgeAfter, &entry.OpID, &entry.Size,
			&entry.Revision, &encrypted,
		); err != nil {
			return nil, fmt.Errorf("projection: scan trash entry: %w", err)
		}
		entry.Encrypted = encrypted != 0
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("projection: iterate trash entries: %w", err)
	}
	return entries, nil
}

// FolderPathNames returns the folder names from the drive root down to and
// including folderID, empty for the root itself. Tombstoned folders are
// included: a trashed object's original parent is very often trashed too, and
// the caller still wants to show where it came from. The visited set stops a
// corrupted parent chain from looping forever.
func FolderPathNames(db *sql.DB, channelID int64, folderID string) ([]string, error) {
	if db == nil {
		return nil, fmt.Errorf("projection: folder path: db is nil")
	}
	names := make([]string, 0, 8)
	visited := make(map[string]struct{}, 8)
	for current := strings.TrimSpace(folderID); current != RootParent; {
		if _, seen := visited[current]; seen {
			break
		}
		visited[current] = struct{}{}
		var name, parent string
		err := db.QueryRow(`
			SELECT name, parent_id FROM folders WHERE channel_id=? AND id=?
		`, channelID, current).Scan(&name, &parent)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("projection: folder path: %w", err)
		}
		names = append(names, name)
		current = strings.TrimSpace(parent)
	}
	for i, j := 0, len(names)-1; i < j; i, j = i+1, j-1 {
		names[i], names[j] = names[j], names[i]
	}
	return names, nil
}

// NextFreeSiblingName returns name when no live sibling under parentID already
// holds its canonical key, and otherwise the first free "name (k)". A restore
// needs it because the original name may have been taken while the object sat
// in the trash, and the alternative -- rejecting the restore -- would leave the
// user with no way to get the object back at all. For a file the counter goes
// before the extension, so "photo.jpg" becomes "photo (2).jpg".
func NextFreeSiblingName(db *sql.DB, channelID int64, parentID, name, kind string) (string, error) {
	if db == nil {
		return "", fmt.Errorf("projection: next free name: db is nil")
	}
	stem, extension := name, ""
	if kind == ObjectKindFile {
		stem, extension = splitLegacyFileExtension(name)
	}
	for attempt := 1; attempt <= maxTrashListPageSize; attempt++ {
		candidate := name
		if attempt > 1 {
			candidate = fmt.Sprintf("%s (%d)%s", stem, attempt, extension)
		}
		key, err := CanonicalNameKey(candidate)
		if err != nil {
			return "", err
		}
		var one int
		err = db.QueryRow(`
			SELECT 1 FROM dirents
			WHERE channel_id=? AND parent_id=? AND name_key=? AND tombstoned=0
		`, channelID, parentID, key).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return candidate, nil
		}
		if err != nil {
			return "", fmt.Errorf("projection: inspect sibling name: %w", err)
		}
	}
	return "", fmt.Errorf("projection: no free name for %q under %q", name, parentID)
}

// RegisterTrashPurgeIntent claims local ownership of the physical deletion of
// one trashed object and returns the revision its hard-delete marker must
// carry. It is the user-initiated form: the user has explicitly asked for this
// object to be destroyed now, so the retention window is not consulted.
func RegisterTrashPurgeIntent(ctx context.Context, db *sql.DB, channelID int64, opID, objectID string) (int64, error) {
	return registerTrashPurgeIntent(ctx, db, channelID, opID, objectID, 0)
}

// RegisterExpiredTrashPurgeIntent is the retention-sweep form. now is the
// current time in unix seconds; an entry whose purge_after has not been reached
// is refused with ErrTrashEntryNotExpired, so an automatic purge can never
// destroy bytes the user could still have restored.
func RegisterExpiredTrashPurgeIntent(ctx context.Context, db *sql.DB, channelID int64, opID, objectID string, now int64) (int64, error) {
	if now <= 0 {
		return 0, fmt.Errorf("projection: expired trash purge intent: current time required")
	}
	return registerTrashPurgeIntent(ctx, db, channelID, opID, objectID, now)
}

// registerTrashPurgeIntent holds one transaction over the whole claim so the
// retention check, the trash entry and the intent row cannot be read apart. A
// zero notBefore skips the retention check; a positive one requires the entry's
// purge_after to have been reached by it.
func registerTrashPurgeIntent(ctx context.Context, db *sql.DB, channelID int64, opID, objectID string, notBefore int64) (revision int64, err error) {
	if err := validateHardDeletePlanRequest(ctx, db, channelID, opID); err != nil {
		return 0, err
	}
	if _, err := objectKind(objectID); err != nil {
		return 0, err
	}
	if err := validateVersionedWritableOp(Op{ProtocolVersion: 1, OpID: opID}); err != nil {
		return 0, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("projection: begin trash purge intent: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := validateHardDeleteChannel(tx, channelID); err != nil {
		return 0, err
	}
	// A purge this installation already claimed resumes on the revision it was
	// claimed at, without re-checking retention: an interrupted purge has
	// already destroyed bodies and must finish, not restart.
	storedObjectID, storedRevision, claimed, err := loadHardDeleteIntentTx(tx, channelID, opID)
	if err != nil {
		return 0, err
	}
	if claimed {
		if storedObjectID != objectID {
			return 0, fmt.Errorf("%w: purge operation id targets another object", ErrBadOp)
		}
		if err := tx.Commit(); err != nil {
			return 0, fmt.Errorf("projection: commit trash purge intent: %w", err)
		}
		return storedRevision, nil
	}

	var purgeAfter int64
	err = tx.QueryRowContext(ctx, `
		SELECT purge_after FROM trash_entries WHERE channel_id=? AND object_id=?
	`, channelID, objectID).Scan(&purgeAfter)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrTrashEntryNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("projection: read trash entry for purge: %w", err)
	}
	if notBefore > 0 && purgeAfter > notBefore {
		return 0, ErrTrashEntryNotExpired
	}
	err = tx.QueryRowContext(ctx, `
		SELECT revision FROM dirents
		WHERE channel_id=? AND object_id=? AND tombstoned=1
	`, channelID, objectID).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrObjectNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("projection: read trashed purge target: %w", err)
	}
	if err := recordHardDeleteIntentTx(tx, channelID, opID, objectID, revision); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("projection: commit trash purge intent: %w", err)
	}
	return revision, nil
}

// ErrOperationRejected reports that a published operation reached the
// projection and was refused there, typically because the object moved under
// the caller between planning and applying.
var ErrOperationRejected = errors.New("projection: operation rejected")

// TrashOp builds the operation that moves one live object, and everything under
// it, into the trash. entry must be the object's current live dirent: its
// revision is the compare-and-swap anchor, so an object that changed since it
// was read is refused rather than deleted from under a concurrent edit.
func TrashOp(entry Dirent, now time.Time, retention time.Duration) Op {
	return Op{
		Type:             OpTrashTree,
		ProtocolVersion:  1,
		OpID:             DeterministicOpID("trash", entry.ObjectID, entry.Revision),
		Obj:              entry.ObjectID,
		ExpectedRevision: entry.Revision,
		DeletedAt:        now.Unix(),
		PurgeAfter:       now.Add(retention).Unix(),
	}
}

// RestoreOp builds the operation that takes one trashed object back out, into
// the caller-chosen live parent and name. currentRevision is the revision the
// tombstone left on the object; parentID and name are resolved by the caller
// precisely once so that every replica applies the same destination.
func RestoreOp(objectID string, currentRevision int64, parentID, name string) Op {
	return Op{
		Type:             OpRestoreTree,
		ProtocolVersion:  1,
		OpID:             DeterministicOpID("restore", objectID, currentRevision),
		Obj:              objectID,
		Parent:           parentID,
		Name:             name,
		ExpectedRevision: currentRevision,
	}
}

// HardDeleteOp builds the marker that permanently destroys one object. The
// operation id must be the one a purge intent was registered under, because
// only a marker matching a local intent captures the immutable body plan that
// physical deletion is validated against.
func HardDeleteOp(opID, objectID string, currentRevision int64) Op {
	return Op{
		Type:             OpHardDeleteTree,
		ProtocolVersion:  1,
		OpID:             opID,
		Obj:              objectID,
		ExpectedRevision: currentRevision,
	}
}

// ConfirmWritableOperation reports whether an operation this client just
// published actually applied. ProjectFromOp records a compare-and-swap refusal
// as a skippable replay event rather than an error -- replay must not stop at
// one -- so an emitter has to read the durable outcome back before telling its
// caller the mutation happened.
func ConfirmWritableOperation(db *sql.DB, channelID int64, opID string) error {
	operation, found, err := ProjectionOperationByID(db, channelID, opID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: %s left no outcome", ErrOperationRejected, opID)
	}
	if operation.Outcome != OperationApplied {
		return fmt.Errorf("%w: %s: %s", ErrOperationRejected, operation.OpType, operation.Error)
	}
	return nil
}
