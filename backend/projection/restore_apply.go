package projection

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
)

// applyRestoreTree is the exact inverse of applyTrashTree.
//
// Two rules make it deterministic, which is what replay convergence needs:
//
//  1. The destination is carried on the wire, not inferred. The original parent
//     may have been deleted or renamed since, so the emitting client picks a
//     live parent and a free name once and every replica applies that same
//     choice instead of each guessing a fallback.
//  2. Only the objects this trash entry took down come back. trashFolderTree
//     tombstoned exactly the subtree members that were live at the time, so the
//     inverse walk skips anything carrying a trash entry of its own: a file or
//     folder trashed separately *before* its parent stays trashed, with its own
//     entry still restorable.
func applyRestoreTree(tx *sql.Tx, channelID int64, op Op) error {
	if op.ExpectedRevision <= 0 {
		return fmt.Errorf("%w: restore requires expected revision", ErrBadOp)
	}
	scope, err := loadRestoreScope(tx, channelID, op)
	if err != nil {
		return err
	}
	if err := validateParentAndName(tx, channelID, op.Parent, op.Name); err != nil {
		return err
	}
	if scope.kind == ObjectKindFolder && op.Parent != RootParent {
		// A live destination can never sit inside a trashed subtree, but the op
		// arrives off the wire and a malformed one must not be able to detach a
		// branch of the namespace into a cycle.
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
	nameKey, err := CanonicalNameKey(op.Name)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBadOp, err)
	}
	var occupant string
	err = tx.QueryRow(`
		SELECT object_id FROM dirents
		WHERE channel_id=? AND parent_id=? AND name_key=? AND tombstoned=0
	`, channelID, op.Parent, nameKey).Scan(&occupant)
	if err == nil {
		return ErrNameConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("projection: inspect restore destination: %w", err)
	}

	if scope.kind == ObjectKindFolder {
		if err := restoreSubtree(tx, scope); err != nil {
			return err
		}
	}
	if err := restoreRoot(tx, scope, op.Parent, op.Name, nameKey); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		DELETE FROM trash_entries WHERE channel_id=? AND object_id=?
	`, channelID, scope.objectID); err != nil {
		return fmt.Errorf("projection: clear restored trash entry: %w", err)
	}
	return nil
}

// restoreScope is the identity a restore was planned against: the trashed root
// and the CAS revision its tombstone left behind.
type restoreScope struct {
	channelID int64
	objectID  string
	kind      string
	fileMsgID int64
	revision  int64
}

func loadRestoreScope(tx *sql.Tx, channelID int64, op Op) (restoreScope, error) {
	kind, err := objectKind(op.Obj)
	if err != nil {
		return restoreScope{}, err
	}
	// The trash entry is the authority on what may be restored: it is written
	// only by a trash operation and removed by a restore or a hard delete, so
	// its presence is also what stops a restore resurrecting a purged object.
	var entryKind string
	err = tx.QueryRow(`
		SELECT object_kind FROM trash_entries
		WHERE channel_id=? AND object_id=?
	`, channelID, op.Obj).Scan(&entryKind)
	if errors.Is(err, sql.ErrNoRows) {
		return restoreScope{}, ErrObjectNotFound
	}
	if err != nil {
		return restoreScope{}, fmt.Errorf("projection: read trash entry: %w", err)
	}
	if entryKind != kind {
		return restoreScope{}, fmt.Errorf("%w: trash entry kind %q does not match object", ErrBadOp, entryKind)
	}

	var revision int64
	err = tx.QueryRow(`
		SELECT revision FROM dirents
		WHERE channel_id=? AND object_id=? AND tombstoned=1
	`, channelID, op.Obj).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		return restoreScope{}, ErrObjectNotFound
	}
	if err != nil {
		return restoreScope{}, fmt.Errorf("projection: read restore root: %w", err)
	}
	if revision != op.ExpectedRevision {
		return restoreScope{}, ErrRevisionConflict
	}

	scope := restoreScope{channelID: channelID, objectID: op.Obj, kind: kind, revision: revision}
	if kind == ObjectKindFile {
		scope.fileMsgID, err = parseFileMsgID(op.Obj)
		if err != nil {
			return restoreScope{}, err
		}
	}
	return scope, nil
}

// withRestorable returns a statement prefix defining subtree (the trashed
// folders this restore reopens, root included) and restorable (their trashed
// contents, root excluded), plus the arguments that prefix consumes. Both
// exclude objects holding a trash entry of their own, which is what keeps an
// independently trashed branch trashed and terminates the walk there.
func (s restoreScope) withRestorable(body string) (string, []any) {
	return `WITH RECURSIVE subtree(id) AS (
			SELECT ?
			UNION
			SELECT child.id FROM folders child
			JOIN subtree parent ON child.parent_id=parent.id
			WHERE child.channel_id=? AND child.tombstoned=1
			  AND child.id NOT IN (
				SELECT object_id FROM trash_entries WHERE channel_id=?
			  )
		),
		restorable(object_id) AS (
			SELECT object_id FROM (
				SELECT id AS object_id FROM subtree WHERE id<>?
				UNION
				SELECT 'f:' || CAST(msg_id AS TEXT) FROM files
				WHERE channel_id=? AND parent_id IN (SELECT id FROM subtree)
			)
			WHERE object_id NOT IN (
				SELECT object_id FROM trash_entries WHERE channel_id=?
			)
		)
		` + body, []any{
			s.objectID, s.channelID, s.channelID,
			s.objectID, s.channelID, s.channelID,
		}
}

func restoreSubtree(tx *sql.Tx, scope restoreScope) error {
	if err := validateRestorableNames(tx, scope); err != nil {
		return err
	}
	fileCount, err := countRestorableFiles(tx, scope)
	if err != nil {
		return err
	}
	advanced, err := advanceRestorableFileRevisions(tx, scope)
	if err != nil {
		return err
	}
	if advanced != fileCount {
		return fmt.Errorf("projection: advanced %d restored file revisions, want %d", advanced, fileCount)
	}
	// Order mirrors trashFolderTree: files, then dirents, then folders. Each
	// statement's own tombstone filter is the guard, and the folder rows stay
	// tombstoned until last so the recursive walk still sees the subtree.
	for _, step := range []struct {
		label string
		sql   string
	}{
		{"restore descendant files", `
			UPDATE files SET tombstoned=0, revision=revision+1
			WHERE channel_id=? AND tombstoned=1
			  AND 'f:' || CAST(msg_id AS TEXT) IN (SELECT object_id FROM restorable)
		`},
		{"restore descendant dirents", `
			UPDATE dirents SET tombstoned=0, revision=revision+1
			WHERE channel_id=? AND tombstoned=1
			  AND object_id IN (SELECT object_id FROM restorable)
		`},
		{"restore descendant folders", `
			UPDATE folders SET tombstoned=0, revision=revision+1
			WHERE channel_id=? AND tombstoned=1
			  AND id IN (SELECT object_id FROM restorable)
		`},
	} {
		query, args := scope.withRestorable(step.sql)
		args = append(args, scope.channelID)
		if _, err := tx.Exec(query, args...); err != nil {
			return fmt.Errorf("projection: %s: %w", step.label, err)
		}
	}
	slog.Info("projection: restored folder tree", "channel_id", scope.channelID,
		"root_object_id", scope.objectID, "file_count", fileCount)
	return nil
}

// validateRestorableNames rejects a restore that would put two live siblings
// under one name. dirents enforces that with a unique index, and a constraint
// violation is not a skippable apply error -- it would abort a whole rebuild --
// so the conflict is detected first and reported as one.
func validateRestorableNames(tx *sql.Tx, scope restoreScope) error {
	query, args := scope.withRestorable(`
		SELECT 1
		FROM dirents moving
		JOIN dirents live
		  ON live.channel_id=moving.channel_id
		 AND live.parent_id=moving.parent_id
		 AND live.name_key=moving.name_key
		 AND live.tombstoned=0
		WHERE moving.channel_id=?
		  AND moving.object_id IN (SELECT object_id FROM restorable)
		LIMIT 1
	`)
	args = append(args, scope.channelID)
	var one int
	err := tx.QueryRow(query, args...).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("projection: inspect restored sibling names: %w", err)
	}
	return ErrNameConflict
}

func countRestorableFiles(tx *sql.Tx, scope restoreScope) (int64, error) {
	query, args := scope.withRestorable(`
		SELECT COUNT(*) FROM files
		WHERE channel_id=? AND tombstoned=1
		  AND 'f:' || CAST(msg_id AS TEXT) IN (SELECT object_id FROM restorable)
	`)
	args = append(args, scope.channelID)
	var count int64
	if err := tx.QueryRow(query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("projection: count restorable files: %w", err)
	}
	return count, nil
}

func advanceRestorableFileRevisions(tx *sql.Tx, scope restoreScope) (int64, error) {
	query, args := scope.withRestorable(`
		, affected(file_msg_id, revision) AS (
			SELECT msg_id, revision FROM files
			WHERE channel_id=? AND tombstoned=1
			  AND 'f:' || CAST(msg_id AS TEXT) IN (SELECT object_id FROM restorable)
		)
		UPDATE file_revisions
		SET revision=revision+1
		WHERE channel_id=? AND retained_until=0
		  AND EXISTS (
			SELECT 1 FROM affected
			WHERE affected.file_msg_id=file_revisions.file_msg_id
			  AND affected.revision=file_revisions.revision
		  )
	`)
	args = append(args, scope.channelID, scope.channelID)
	result, err := tx.Exec(query, args...)
	if err != nil {
		return 0, fmt.Errorf("projection: advance restored file revisions: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("projection: count advanced restored file revisions: %w", err)
	}
	return affected, nil
}

// restoreRoot reopens the trashed root itself under its new parent and name.
// Both the object row and its dirent advance to one revision past the tombstone
// so CAS stays monotonic across a delete/restore cycle.
func restoreRoot(tx *sql.Tx, scope restoreScope, parentID, name, nameKey string) error {
	nextRevision := scope.revision + 1
	switch scope.kind {
	case ObjectKindFile:
		result, err := tx.Exec(`
			UPDATE files SET tombstoned=0, name=?, parent_id=?, revision=?
			WHERE channel_id=? AND msg_id=? AND tombstoned=1 AND revision=?
		`, name, parentID, nextRevision, scope.channelID, scope.fileMsgID, scope.revision)
		if err != nil {
			return fmt.Errorf("projection: restore file: %w", err)
		}
		if err := requireOneUpdatedRow(result, "restore file"); err != nil {
			return err
		}
		if err := advanceActiveFileRevision(tx, scope.channelID, scope.fileMsgID, scope.revision, nextRevision); err != nil {
			return err
		}
	case ObjectKindFolder:
		result, err := tx.Exec(`
			UPDATE folders SET tombstoned=0, name=?, parent_id=?, revision=?
			WHERE channel_id=? AND id=? AND tombstoned=1 AND revision=?
		`, name, parentID, nextRevision, scope.channelID, scope.objectID, scope.revision)
		if err != nil {
			return fmt.Errorf("projection: restore folder: %w", err)
		}
		if err := requireOneUpdatedRow(result, "restore folder"); err != nil {
			return err
		}
	}
	result, err := tx.Exec(`
		UPDATE dirents
		SET tombstoned=0, parent_id=?, display_name=?, name_key=?, revision=?
		WHERE channel_id=? AND object_id=? AND tombstoned=1 AND revision=?
	`, parentID, name, nameKey, nextRevision, scope.channelID, scope.objectID, scope.revision)
	if err != nil {
		return fmt.Errorf("projection: restore dirent: %w", err)
	}
	return requireOneUpdatedRow(result, "restore dirent")
}
