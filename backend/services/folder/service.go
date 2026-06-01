package folder

import (
	"database/sql"
	"fmt"
	"strings"

	"TDrive/backend/projection"

	"github.com/google/uuid"
)

type EmitOpFunc func(channelID int64, op projection.Op) error

type Service struct {
	DB     *sql.DB
	EmitOp EmitOpFunc
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

func (s *Service) Delete(channelID int64, folderID string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if channelID == 0 {
		return fmt.Errorf("No active channel")
	}
	if !projection.IsFolderID(folderID) || !projection.FolderExists(s.DB, channelID, folderID) {
		return fmt.Errorf("Folder not found")
	}

	op := projection.Op{Type: projection.OpRmdir, Obj: folderID}
	return s.emit(channelID, op)
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
