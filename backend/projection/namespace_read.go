package projection

import (
	"database/sql"
	"errors"
	"fmt"
)

// DirentByID reads unified namespace metadata for a logical file or folder.
// Tombstoned entries are returned so recovery and cleanup can inspect them.
func DirentByID(db *sql.DB, channelID int64, objectID string) (Dirent, bool, error) {
	if db == nil {
		return Dirent{}, false, fmt.Errorf("projection: dirent by id: db is nil")
	}
	if channelID == 0 || (!IsFileID(objectID) && !IsFolderID(objectID)) {
		return Dirent{}, false, fmt.Errorf("projection: dirent by id: invalid identity")
	}
	return scanDirent(db.QueryRow(`
		SELECT channel_id, object_id, object_kind, parent_id, display_name,
		       name_key, revision, tombstoned
		FROM dirents WHERE channel_id=? AND object_id=?
	`, channelID, objectID))
}

// LiveDirentByName resolves one portable sibling name using the exact same
// NFC/case-fold key used during projection replay.
func LiveDirentByName(db *sql.DB, channelID int64, parentID, name string) (Dirent, bool, error) {
	if db == nil {
		return Dirent{}, false, fmt.Errorf("projection: live dirent by name: db is nil")
	}
	if channelID == 0 || (parentID != RootParent && !IsFolderID(parentID)) {
		return Dirent{}, false, fmt.Errorf("projection: live dirent by name: invalid parent")
	}
	key, err := CanonicalNameKey(name)
	if err != nil {
		return Dirent{}, false, err
	}
	return scanDirent(db.QueryRow(`
		SELECT channel_id, object_id, object_kind, parent_id, display_name,
		       name_key, revision, tombstoned
		FROM dirents
		WHERE channel_id=? AND parent_id=? AND name_key=? AND tombstoned=0
	`, channelID, parentID, key))
}

func scanDirent(scanner sqlScanner) (Dirent, bool, error) {
	var entry Dirent
	var tombstoned int
	err := scanner.Scan(
		&entry.ChannelID, &entry.ObjectID, &entry.ObjectKind, &entry.ParentID,
		&entry.DisplayName, &entry.NameKey, &entry.Revision, &tombstoned,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Dirent{}, false, nil
	}
	if err != nil {
		return Dirent{}, false, fmt.Errorf("projection: scan dirent: %w", err)
	}
	entry.Tombstoned = tombstoned != 0
	return entry, true, nil
}
