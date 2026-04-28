// writes.go provides the only legal way for the rest of the application to
// mutate files/folders. Each Local* helper builds an Op in memory and calls
// ApplyOp inside a transaction.
//
// Step 1: these helpers do not write to replay_log. They project locally only,
// because step 1 does not yet emit TDX1 control messages onto Telegram.
// Step 3 will replace this with: emit TDX1 -> insert replay_log -> ApplyOp from
// the replay row. ApplyOp itself is unchanged; only the call sites move.
package projection

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrFolderNotFound = errors.New("projection: folder not found")
	ErrFileNotFound   = errors.New("projection: file not found")
	ErrParentMissing  = errors.New("projection: parent folder not found")
	ErrNameTaken      = errors.New("projection: a folder with that name already exists here")
	ErrInvalidName    = errors.New("projection: invalid name")
	ErrInvalidParent  = errors.New("projection: invalid parent")
	ErrSamePlace      = errors.New("projection: already in this location")
)

func LocalCreateFolder(db *sql.DB, channelID int64, parentID, name string) (FolderSlim, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return FolderSlim{}, ErrInvalidName
	}

	parentID = strings.TrimSpace(parentID)
	if parentID != RootParent {
		if !IsFolderID(parentID) {
			return FolderSlim{}, ErrInvalidParent
		}
		if !FolderExists(db, channelID, parentID) {
			return FolderSlim{}, ErrParentMissing
		}
	}

	taken, err := FolderSiblingHasName(db, channelID, parentID, name)
	if err != nil {
		return FolderSlim{}, err
	}
	if taken {
		return FolderSlim{}, ErrNameTaken
	}

	folderID := FolderIDPrefix + newUUID()

	op := Op{
		Type:   OpMkdir,
		Obj:    folderID,
		Parent: parentID,
		Name:   name,
	}
	if err := runApply(db, channelID, 0, op, 0); err != nil {
		return FolderSlim{}, err
	}

	return FolderSlim{ID: folderID, Name: name, ParentID: parentID}, nil
}

func LocalRegisterFile(db *sql.DB, channelID int64, msgID int64, name string, size int64, parentID string, uploadTime int64, uploaderID int64) error {
	if msgID <= 0 {
		return fmt.Errorf("%w: msg id required", ErrBadOp)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Untitled"
	}
	parentID = strings.TrimSpace(parentID)
	if parentID != RootParent {
		if !IsFolderID(parentID) {
			return ErrInvalidParent
		}
		if !FolderExists(db, channelID, parentID) {
			return ErrParentMissing
		}
	}

	op := Op{
		Type:           OpFileUpload,
		Parent:         parentID,
		Name:           name,
		FileSize:       size,
		FileUploadTime: uploadTime,
	}
	return runApply(db, channelID, msgID, op, uploaderID)
}

func LocalRegisterFiles(db *sql.DB, channelID int64, files []FileSlim, uploaderID int64) error {
	for _, f := range files {
		if f.MsgID <= 0 {
			return fmt.Errorf("%w: msg id required", ErrBadOp)
		}
		parent := normalizeParent(f.ParentID)
		if parent != RootParent {
			if !IsFolderID(parent) {
				return ErrInvalidParent
			}
			if !FolderExists(db, channelID, parent) {
				return ErrParentMissing
			}
		}
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for _, f := range files {
		op := Op{
			Type:           OpFileUpload,
			Parent:         normalizeParent(f.ParentID),
			Name:           strings.TrimSpace(f.Name),
			FileSize:       f.Size,
			FileUploadTime: f.UploadTime,
		}
		if op.Name == "" {
			op.Name = "Untitled"
		}
		if err := ApplyOp(tx, channelID, f.MsgID, op, uploaderID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func LocalRenameFile(db *sql.DB, channelID int64, msgID int64, newName string) error {
	newName = strings.TrimSpace(newName)
	if newName == "" {
		return ErrInvalidName
	}
	if !FileExists(db, channelID, msgID) {
		return ErrFileNotFound
	}
	op := Op{
		Type: OpRename,
		Obj:  fmt.Sprintf("%s%d", FileIDPrefix, msgID),
		Name: newName,
	}
	return runApply(db, channelID, 0, op, 0)
}

func LocalRenameFolder(db *sql.DB, channelID int64, folderID, newName string) error {
	newName = strings.TrimSpace(newName)
	if newName == "" {
		return ErrInvalidName
	}
	if !IsFolderID(folderID) {
		return ErrInvalidParent
	}
	if !FolderExists(db, channelID, folderID) {
		return ErrFolderNotFound
	}
	op := Op{
		Type: OpRename,
		Obj:  folderID,
		Name: newName,
	}
	return runApply(db, channelID, 0, op, 0)
}

func LocalMoveFile(db *sql.DB, channelID int64, msgID int64, newParentID string) error {
	newParentID = normalizeParent(newParentID)
	if newParentID != RootParent {
		if !IsFolderID(newParentID) {
			return ErrInvalidParent
		}
		if !FolderExists(db, channelID, newParentID) {
			return ErrParentMissing
		}
	}

	cur, err := FileParent(db, channelID, msgID)
	if err != nil {
		return ErrFileNotFound
	}
	if cur == newParentID {
		return ErrSamePlace
	}

	op := Op{
		Type:   OpMove,
		Obj:    fmt.Sprintf("%s%d", FileIDPrefix, msgID),
		Parent: newParentID,
	}
	return runApply(db, channelID, 0, op, 0)
}

func LocalMoveFolder(db *sql.DB, channelID int64, folderID, newParentID string) error {
	newParentID = normalizeParent(newParentID)
	if !IsFolderID(folderID) {
		return ErrInvalidParent
	}
	if folderID == newParentID {
		return ErrCycleRejected
	}

	cur, err := FolderParent(db, channelID, folderID)
	if err != nil {
		return ErrFolderNotFound
	}
	if cur == newParentID {
		return ErrSamePlace
	}

	if newParentID != RootParent {
		if !IsFolderID(newParentID) {
			return ErrInvalidParent
		}
		if !FolderExists(db, channelID, newParentID) {
			return ErrParentMissing
		}
		isAnc, err := IsAncestor(db, channelID, folderID, newParentID)
		if err != nil {
			return err
		}
		if isAnc {
			return ErrCycleRejected
		}
	}

	op := Op{
		Type:   OpMove,
		Obj:    folderID,
		Parent: newParentID,
	}
	return runApply(db, channelID, 0, op, 0)
}

func LocalDeleteFile(db *sql.DB, channelID int64, msgID int64) error {
	if !FileExists(db, channelID, msgID) {
		return ErrFileNotFound
	}
	op := Op{
		Type: OpTomb,
		Obj:  fmt.Sprintf("%s%d", FileIDPrefix, msgID),
	}
	return runApplyHardDelete(db, channelID, msgID, op)
}

func LocalDeleteFolderTree(db *sql.DB, channelID int64, folderIDs []string, msgIDs []int64) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for _, msgID := range msgIDs {
		if _, err := tx.Exec(`DELETE FROM files WHERE channel_id = ? AND msg_id = ?`, channelID, msgID); err != nil {
			return err
		}
	}
	for _, id := range folderIDs {
		if _, err := tx.Exec(`DELETE FROM folders WHERE channel_id = ? AND id = ?`, channelID, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func runApply(db *sql.DB, channelID int64, msgID int64, op Op, actorID int64) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err := ApplyOp(tx, channelID, msgID, op, actorID); err != nil {
		return err
	}
	return tx.Commit()
}

// runApplyHardDelete is the step-1 fallback that physically removes a row
// instead of tombstoning it. Step 3 will replace this with a real OpTomb apply
// that flips tombstoned=1; today the rest of the app expects deleted files to
// disappear entirely from local SQL queries, so we keep the legacy semantics.
func runApplyHardDelete(db *sql.DB, channelID int64, msgID int64, _ Op) error {
	_, err := db.Exec(`DELETE FROM files WHERE channel_id = ? AND msg_id = ?`, channelID, msgID)
	return err
}

func normalizeParent(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return RootParent
	}
	return p
}
