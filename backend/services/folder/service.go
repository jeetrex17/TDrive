package folder

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"

	"github.com/google/uuid"
)

type PeerResolver interface {
	ResolvePeer(ctx context.Context, channelID int64) (tgclient.InputPeer, error)
}

type EmitOpFunc func(channelID int64, op projection.Op) error
type EmitOpsFunc func(channelID int64, ops []projection.Op) error
type ActorIDFunc func(ctx context.Context) (int64, error)
type RequireEncryptionKeyFunc func(encrypted bool) ([]byte, error)
type WarnFunc func(format string, args ...any)

type Service struct {
	DB                   *sql.DB
	TG                   tgclient.Client
	Peers                PeerResolver
	EmitOp               EmitOpFunc
	EmitOps              EmitOpsFunc
	ActorID              ActorIDFunc
	RequireEncryptionKey RequireEncryptionKeyFunc
	Warnf                WarnFunc
}

type Folder struct {
	ID       string
	Name     string
	ParentID string
}

func (s *Service) Create(channelID int64, name string, parentID string) (Folder, error) {
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
	if err := s.emit(channelID, op); err != nil {
		return Folder{}, fmt.Errorf("create folder failed: %w", err)
	}

	return Folder{
		ID:       folderID,
		Name:     name,
		ParentID: parent,
	}, nil
}

func (s *Service) Delete(ctx context.Context, channelID int64, folderID string) error {
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
	folders, err := projection.FolderSubtreeFolders(s.DB, channelID, folderID)
	if err != nil {
		return err
	}

	if err := s.requireDeletePermission(ctx, channelID, files); err != nil {
		return err
	}
	if err := s.requireEncryptedKey(files); err != nil {
		return err
	}

	ops := make([]projection.Op, 0, len(files)+len(folders))
	for _, file := range files {
		ops = append(ops, projection.Op{
			Type: projection.OpTomb,
			Obj:  fmt.Sprintf("%s%d", projection.FileIDPrefix, file.MsgID),
		})
	}
	for _, folder := range folders {
		ops = append(ops, projection.Op{Type: projection.OpRmdir, Obj: folder.ID})
	}
	if err := s.emitMany(channelID, ops); err != nil {
		return fmt.Errorf("delete folder failed: %w", err)
	}

	// Body deletion is best effort and runs only after the whole subtree's
	// metadata is locally tombstoned. If Telegram body cleanup fails, replayed
	// tombstone metadata stays canonical and the orphan sweep can retry parts.
	s.deleteBodiesBestEffort(ctx, channelID, files)
	return nil
}

func (s *Service) Rename(channelID int64, folderID string, newName string) error {
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
	return s.emit(channelID, op)
}

func (s *Service) Move(channelID int64, folderID string, newParentID string) error {
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
	return s.emit(channelID, op)
}

func (s *Service) emit(channelID int64, op projection.Op) error {
	if s.EmitOp == nil {
		return fmt.Errorf("folder emitter not ready")
	}
	return s.EmitOp(channelID, op)
}

func (s *Service) emitMany(channelID int64, ops []projection.Op) error {
	if len(ops) == 0 {
		return nil
	}
	if s.EmitOps == nil {
		return fmt.Errorf("folder batch emitter not ready")
	}
	return s.EmitOps(channelID, ops)
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
	_, err := s.RequireEncryptionKey(true)
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

func (s *Service) deleteBodiesBestEffort(ctx context.Context, channelID int64, files []projection.FileSlim) {
	if len(files) == 0 || s.TG == nil || s.Peers == nil {
		return
	}
	// Each file's body is its own message; a multipart file also has N part
	// document bodies that must be deleted too (and their file_parts rows).
	ids := make([]int64, 0, len(files))
	for _, file := range files {
		ids = append(ids, file.MsgID)
	}
	partIDs, err := projection.MultipartPartMsgIDsForFiles(s.DB, channelID, ids)
	if err != nil {
		s.warnf("warn: folder tomb succeeded but reading multipart part bodies failed: %v\n", err)
		return
	}
	ids = append(ids, partIDs...)
	peer, err := s.Peers.ResolvePeer(ctx, channelID)
	if err != nil {
		s.warnf("warn: folder tomb succeeded but peer resolve failed for %d file bodies: %v\n", len(ids), err)
		return
	}
	if err := s.deleteMessagesChunked(ctx, peer, ids); err != nil {
		// Keep the file_parts rows so the orphan sweep can retry the part bodies.
		s.warnf("warn: folder tomb succeeded but body delete failed for %d bodies: %v\n", len(ids), err)
		return
	}
	if len(partIDs) > 0 {
		if err := projection.DeleteFilePartsByMsgIDs(s.DB, channelID, partIDs); err != nil {
			s.warnf("warn: folder tomb succeeded but dropping file_parts rows failed: %v\n", err)
		}
	}
}

// deleteMessagesChunked deletes Telegram messages in batches of 100 (a folder
// subtree plus multipart parts can exceed the deleteMessages limit), returning
// the first error encountered, nil if every chunk succeeded.
func (s *Service) deleteMessagesChunked(ctx context.Context, peer tgclient.InputPeer, msgIDs []int64) error {
	if s.TG == nil || len(msgIDs) == 0 {
		return nil
	}
	const chunk = 100
	var firstErr error
	for start := 0; start < len(msgIDs); start += chunk {
		end := min(start+chunk, len(msgIDs))
		if err := s.TG.DeleteMessages(ctx, peer, msgIDs[start:end]); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *Service) warnf(format string, args ...any) {
	if s.Warnf != nil {
		s.Warnf(format, args...)
	}
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
