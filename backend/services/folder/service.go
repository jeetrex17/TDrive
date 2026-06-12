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
type ActorIDFunc func(ctx context.Context) (int64, error)
type RequireEncryptionKeyFunc func(encrypted bool) ([]byte, error)
type WarnFunc func(format string, args ...any)

type Service struct {
	DB                   *sql.DB
	TG                   tgclient.Client
	Peers                PeerResolver
	EmitOp               EmitOpFunc
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
	if err := s.requireEncryptedDeleteKey(files); err != nil {
		return err
	}

	// Delete the whole subtree, but don't abort on the first per-item failure.
	// Each tombstone op is idempotent, so we make maximal progress and report
	// what's left; a retry finishes the rest rather than stranding the tree in a
	// worse half-state with a bare "delete failed".
	var failed []string
	deleted := make([]projection.FileSlim, 0, len(files))
	for _, file := range files {
		op := projection.Op{
			Type: projection.OpTomb,
			Obj:  fmt.Sprintf("%s%d", projection.FileIDPrefix, file.MsgID),
		}
		if err := s.emit(channelID, op); err != nil {
			failed = append(failed, fmt.Sprintf("file %q: %v", file.Name, err))
			continue
		}
		deleted = append(deleted, file)
	}
	for _, folder := range folders {
		if err := s.emit(channelID, projection.Op{Type: projection.OpRmdir, Obj: folder.ID}); err != nil {
			failed = append(failed, fmt.Sprintf("folder %q: %v", folder.Name, err))
		}
	}

	// Only drop bodies for files whose tombstone actually landed, so a file's
	// content is never deleted while its metadata still shows it as present.
	s.deleteBodiesBestEffort(ctx, channelID, deleted)

	if len(failed) > 0 {
		return fmt.Errorf("folder partially deleted, try again to finish (%d item(s) failed: %s)", len(failed), strings.Join(failed, "; "))
	}
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

func (s *Service) requireEncryptedDeleteKey(files []projection.FileSlim) error {
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

func (s *Service) deleteBodiesBestEffort(ctx context.Context, channelID int64, files []projection.FileSlim) {
	if len(files) == 0 || s.TG == nil || s.Peers == nil {
		return
	}
	ids := make([]int64, 0, len(files))
	for _, file := range files {
		ids = append(ids, file.MsgID)
	}
	peer, err := s.Peers.ResolvePeer(ctx, channelID)
	if err != nil {
		s.warnf("warn: folder tomb succeeded but peer resolve failed for %d file bodies: %v\n", len(ids), err)
		return
	}
	if err := s.TG.DeleteMessages(ctx, peer, ids); err != nil {
		s.warnf("warn: folder tomb succeeded but body delete failed for %d file bodies: %v\n", len(ids), err)
	}
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
