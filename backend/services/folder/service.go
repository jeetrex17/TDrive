// Package folder is the only writer of directory-structure ops: mkdir, rename,
// move, and the trash operation that deletes a subtree. It validates and emits;
// the injected emitters send to Telegram first and project locally second, so
// Telegram is the log of record and a local failure converges on the next sync.
//
// Deleting a subtree is one operation on the folder itself, not a tombstone per
// member: the projection tombstones the tree and records a single restorable
// trash entry for the root. No Telegram body is deleted here at all. The bytes
// outlive the delete and are destroyed only by an explicit purge or by the
// retention sweep once the entry's purge_after has passed.
//
// A move is checked against the projection for cycles before it is emitted, so
// a folder can never be moved inside its own descendant.
//
// The encryption key is requested purely as a gate and cleared immediately
// without being used: renaming, moving or deleting a subtree containing any
// encrypted file requires an unlocked vault, so the UI prompts rather than
// silently succeeding. On a shared drive a delete additionally requires the
// actor to own every file in the subtree, and an unknown uploader fails closed.
package folder

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"

	"TDrive/backend/projection"
	"TDrive/backend/services/servicecontext"

	"github.com/google/uuid"
)

type EmitOpFunc func(channelID int64, op projection.Op) error
type EmitOpContextFunc func(ctx context.Context, channelID int64, op projection.Op) error
type ActorIDFunc func(ctx context.Context) (int64, error)

// RequireEncryptionKeyFunc returns a caller-owned key copy. Service clears a
// non-nil key whether the provider succeeds or returns an error.
type RequireEncryptionKeyFunc func(encrypted bool) ([]byte, error)

// TrashObjectFunc publishes the trash operation for one object id. It is
// injected rather than reimplemented so a folder delete and a file delete are
// literally the same operation, differing only in which object they name.
type TrashObjectFunc func(ctx context.Context, channelID int64, objectID string) error

type Service struct {
	DB                   *sql.DB
	EmitOp               EmitOpFunc
	EmitOpContext        EmitOpContextFunc
	ActorID              ActorIDFunc
	RequireEncryptionKey RequireEncryptionKeyFunc
	TrashObject          TrashObjectFunc
}

type Folder struct {
	ID       string
	Name     string
	ParentID string
}

func (s *Service) Create(channelID int64, name string, parentID string) (Folder, error) {
	return s.CreateContext(context.Background(), channelID, name, parentID)
}

func (s *Service) CreateContext(ctx context.Context, channelID int64, name string, parentID string) (Folder, error) {
	if err := servicecontext.Check(ctx, "folder: create"); err != nil {
		return Folder{}, err
	}
	if err := s.ready(); err != nil {
		return Folder{}, err
	}
	if channelID == 0 {
		return Folder{}, fmt.Errorf("no active channel")
	}

	name = strings.TrimSpace(name)
	if name == "" {
		return Folder{}, fmt.Errorf("folder name can't be empty")
	}
	parent := normalizeParent(parentID)
	if parent != projection.RootParent {
		if !projection.IsFolderID(parent) {
			return Folder{}, fmt.Errorf("invalid parent folder id")
		}
		if !projection.FolderExists(s.DB, channelID, parent) {
			return Folder{}, fmt.Errorf("parent folder not found")
		}
	}
	taken, err := projection.FolderSiblingHasName(s.DB, channelID, parent, name)
	if err != nil {
		return Folder{}, err
	}
	if taken {
		return Folder{}, fmt.Errorf("folder '%s' already exists here", name)
	}

	folderID := projection.FolderIDPrefix + uuid.NewString()
	op := projection.Op{
		Type:   projection.OpMkdir,
		Obj:    folderID,
		Parent: parent,
		Name:   name,
	}
	if err := s.emit(ctx, channelID, op); err != nil {
		slog.Error("folder: create failed", "channel_id", channelID, "name", name, "parent_id", parent, "error", err)
		return Folder{}, fmt.Errorf("create folder failed: %w", err)
	}
	slog.Debug("folder: created", "channel_id", channelID, "folder_id", folderID, "name", name, "parent_id", parent)

	return Folder{
		ID:       folderID,
		Name:     name,
		ParentID: parent,
	}, nil
}

// Delete moves a folder and its whole subtree into the trash with one
// operation. The subtree is walked here only to gate the delete -- permission
// and encryption still apply to every file in it -- and no Telegram body is
// touched: the bytes stay until an explicit purge or the retention sweep.
func (s *Service) Delete(ctx context.Context, channelID int64, folderID string) error {
	if err := servicecontext.Check(ctx, "folder: delete"); err != nil {
		return err
	}
	if err := s.ready(); err != nil {
		return err
	}
	if channelID == 0 {
		return fmt.Errorf("No active channel")
	}
	if !projection.IsFolderID(folderID) || !projection.FolderExists(s.DB, channelID, folderID) {
		return fmt.Errorf("Folder not found")
	}
	files, err := projection.FolderSubtreeFiles(s.DB, channelID, folderID)
	if err != nil {
		return err
	}
	if err := s.requireDeletePermission(ctx, channelID, files); err != nil {
		return err
	}
	if err := s.requireEncryptedKey(files); err != nil {
		return err
	}
	if s.TrashObject == nil {
		return fmt.Errorf("folder trash not ready")
	}
	if err := s.TrashObject(ctx, channelID, folderID); err != nil {
		slog.Error("folder: delete failed", "channel_id", channelID, "folder_id", folderID, "error", err)
		return fmt.Errorf("delete folder failed: %w", err)
	}
	slog.Debug("folder: moved to trash", "channel_id", channelID, "folder_id", folderID, "files", len(files))
	return nil
}

func (s *Service) Rename(channelID int64, folderID string, newName string) error {
	return s.RenameContext(context.Background(), channelID, folderID, newName)
}

func (s *Service) RenameContext(ctx context.Context, channelID int64, folderID string, newName string) error {
	if err := servicecontext.Check(ctx, "folder: rename"); err != nil {
		return err
	}
	if err := s.ready(); err != nil {
		return err
	}
	if channelID == 0 {
		return fmt.Errorf("No active channel")
	}
	newName = strings.TrimSpace(newName)
	if newName == "" {
		return fmt.Errorf("Invalid name")
	}
	if !projection.IsFolderID(folderID) || !projection.FolderExists(s.DB, channelID, folderID) {
		return fmt.Errorf("Folder not found")
	}
	if err := s.requireSubtreeEncryptionKey(channelID, folderID); err != nil {
		return err
	}
	op := projection.Op{
		Type: projection.OpRename,
		Obj:  folderID,
		Name: newName,
	}
	if err := s.emit(ctx, channelID, op); err != nil {
		slog.Error("folder: rename failed", "channel_id", channelID, "folder_id", folderID, "error", err)
		return err
	}
	slog.Debug("folder: renamed", "channel_id", channelID, "folder_id", folderID, "new_name", newName)
	return nil
}

func (s *Service) Move(channelID int64, folderID string, newParentID string) error {
	return s.MoveContext(context.Background(), channelID, folderID, newParentID)
}

func (s *Service) MoveContext(ctx context.Context, channelID int64, folderID string, newParentID string) error {
	if err := servicecontext.Check(ctx, "folder: move"); err != nil {
		return err
	}
	if err := s.ready(); err != nil {
		return err
	}
	if channelID == 0 {
		return fmt.Errorf("No active channel")
	}
	if !projection.IsFolderID(folderID) {
		return fmt.Errorf("Invalid folder id")
	}
	parent := normalizeParent(newParentID)
	if folderID == parent {
		return fmt.Errorf("Cannot move folder into its own subfolder")
	}
	if !projection.FolderExists(s.DB, channelID, folderID) {
		return fmt.Errorf("Folder not found")
	}
	cur, err := projection.FolderParent(s.DB, channelID, folderID)
	if err != nil {
		return fmt.Errorf("Folder not found")
	}
	if cur == parent {
		return fmt.Errorf("Folder is already here")
	}
	if parent != projection.RootParent {
		if !projection.IsFolderID(parent) {
			return fmt.Errorf("Invalid target folder id")
		}
		if !projection.FolderExists(s.DB, channelID, parent) {
			return fmt.Errorf("Target folder not found")
		}
		isAnc, err := projection.IsAncestor(s.DB, channelID, folderID, parent)
		if err != nil {
			return err
		}
		if isAnc {
			return fmt.Errorf("Cannot move folder into its own subfolder")
		}
	}
	if err := s.requireSubtreeEncryptionKey(channelID, folderID); err != nil {
		return err
	}
	op := projection.Op{
		Type:   projection.OpMove,
		Obj:    folderID,
		Parent: parent,
	}
	if err := s.emit(ctx, channelID, op); err != nil {
		slog.Error("folder: move failed", "channel_id", channelID, "folder_id", folderID, "new_parent_id", parent, "error", err)
		return err
	}
	slog.Debug("folder: moved", "channel_id", channelID, "folder_id", folderID, "new_parent_id", parent)
	return nil
}

func (s *Service) emit(ctx context.Context, channelID int64, op projection.Op) error {
	if err := servicecontext.Check(ctx, "folder: emit operation"); err != nil {
		return err
	}
	if s.EmitOpContext != nil {
		return s.EmitOpContext(ctx, channelID, op)
	}
	if s.EmitOp == nil {
		return fmt.Errorf("folder emitter not ready")
	}
	return s.EmitOp(channelID, op)
}

func (s *Service) requireDeletePermission(ctx context.Context, channelID int64, files []projection.FileSlim) error {
	if len(files) == 0 {
		return nil
	}
	ch, err := projection.GetChannel(s.DB, channelID)
	if err != nil {
		return err
	}
	if ch.Kind != projection.KindShared {
		return nil
	}
	if s.ActorID == nil {
		return fmt.Errorf("actor resolver not ready")
	}
	actorID, err := s.ActorID(ctx)
	if err != nil {
		return err
	}
	for _, file := range files {
		if file.UploaderID == 0 || file.UploaderID != actorID {
			return fmt.Errorf("Only the uploader can delete every file in this folder in a shared drive")
		}
	}
	return nil
}

// requireEncryptedKey demands the encryption key when any of the files is
// encrypted, before a destructive or moving operation. A locked vault returns
// the "encryption password required" error the frontend prompts on.
func (s *Service) requireEncryptedKey(files []projection.FileSlim) error {
	encrypted := false
	for _, file := range files {
		if file.Encrypted {
			encrypted = true
			break
		}
	}
	if !encrypted || s.RequireEncryptionKey == nil {
		return nil
	}
	key, err := s.RequireEncryptionKey(true)
	clear(key)
	return err
}

// requireSubtreeEncryptionKey demands the key when the folder's subtree contains
// any encrypted file, before renaming/moving/deleting the folder.
func (s *Service) requireSubtreeEncryptionKey(channelID int64, folderID string) error {
	files, err := projection.FolderSubtreeFiles(s.DB, channelID, folderID)
	if err != nil {
		return err
	}
	return s.requireEncryptedKey(files)
}

func (s *Service) ready() error {
	if s.DB == nil {
		return fmt.Errorf("db not ready")
	}
	return nil
}

func normalizeParent(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return projection.RootParent
	}
	return p
}
