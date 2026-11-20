package app

import (
	"context"
	"errors"
	"strings"

	"TDrive/backend"
	"TDrive/backend/core"
	"TDrive/backend/projection"
	fileservice "TDrive/backend/services/file"
)

// SourceCopyFolderPage belongs to the configured My Drive, regardless of
// which drive or Telegram source is currently open in the dashboard.
type SourceCopyFolderPage struct {
	DriveID int64            `json:"drive_id"`
	Folders []backend.Folder `json:"folders"`
}

func (s *DriveService) sourceCopyDrive(ctx context.Context, expectedAccountID, expectedDriveID int64) (*core.Engine, int64, error) {
	if expectedAccountID <= 0 {
		return nil, 0, errors.New("sign in before saving to My Drive")
	}
	if s.host == nil {
		return nil, 0, errBackendUnavailable
	}
	engine := s.engine()
	if engine == nil || backend.DB == nil {
		return nil, 0, errBackendUnavailable
	}
	accountID, err := engine.ActorID(ctx)
	if err != nil {
		return nil, 0, err
	}
	if accountID != expectedAccountID {
		return nil, 0, errors.New("the Telegram account changed")
	}
	driveID := core.PersonalChannelID()
	if driveID <= 0 || (expectedDriveID != 0 && expectedDriveID != driveID) {
		return nil, 0, errors.New("My Drive is unavailable or changed")
	}
	drive, err := projection.GetChannel(backend.DB, driveID)
	if err != nil || drive.Kind != projection.KindPersonal {
		return nil, 0, errors.New("My Drive is unavailable")
	}
	return engine, driveID, nil
}

func (s *DriveService) ListSourceCopyFolders(expectedAccountID int64, parentID string) (SourceCopyFolderPage, error) {
	if s.host == nil {
		return SourceCopyFolderPage{}, errBackendUnavailable
	}
	ctx, finish, err := s.sourceCopies.begin(s.host.appContext(), 0)
	if err != nil {
		return SourceCopyFolderPage{}, err
	}
	defer finish()
	_, driveID, err := s.sourceCopyDrive(ctx, expectedAccountID, 0)
	if err != nil {
		return SourceCopyFolderPage{}, err
	}
	if parentID != projection.RootParent && !projection.FolderExists(backend.DB, driveID, parentID) {
		return SourceCopyFolderPage{}, errors.New("this folder is no longer available in My Drive")
	}
	folders, err := projection.ListChildFoldersContext(ctx, backend.DB, driveID, parentID)
	if err != nil {
		return SourceCopyFolderPage{}, err
	}
	result := SourceCopyFolderPage{DriveID: driveID, Folders: make([]backend.Folder, 0, len(folders))}
	for _, folder := range folders {
		result.Folders = append(result.Folders, backend.Folder{ID: folder.ID, ParentID: folder.ParentID, Name: folder.Name})
	}
	return result, nil
}

func (s *DriveService) CreateSourceCopyFolder(expectedAccountID, expectedDriveID int64, parentID, name string) (backend.Folder, error) {
	if expectedDriveID <= 0 {
		return backend.Folder{}, errors.New("choose a My Drive destination")
	}
	if s.host == nil {
		return backend.Folder{}, errBackendUnavailable
	}
	name = strings.TrimSpace(name)
	if _, err := projection.CanonicalNameKey(name); err != nil {
		return backend.Folder{}, errors.New("choose a folder name that works on every device")
	}
	ctx, finish, err := s.sourceCopies.begin(s.host.appContext(), 0)
	if err != nil {
		return backend.Folder{}, err
	}
	defer finish()
	engine, driveID, err := s.sourceCopyDrive(ctx, expectedAccountID, expectedDriveID)
	if err != nil {
		return backend.Folder{}, err
	}
	folder, err := engine.FolderService().CreateContext(ctx, driveID, name, parentID)
	if err != nil {
		return backend.Folder{}, err
	}
	return backend.Folder{ID: folder.ID, ParentID: folder.ParentID, Name: folder.Name}, nil
}

func (s *DriveService) PreviewSourceCopyNames(names []string) ([]string, error) {
	if len(names) > fileservice.SourceCopyBatchLimit {
		return nil, errors.New("too many files selected")
	}
	normalized := make([]string, 0, len(names))
	for _, name := range names {
		normalized = append(normalized, fileservice.NormalizeSourceCopyName(name))
	}
	return normalized, nil
}

func (s *DriveService) SaveSourceCopies(ctx context.Context, expectedAccountID, expectedDriveID int64, parentID string, items []fileservice.SourceCopyInput, allowPlain bool) ([]fileservice.SourceCopyResult, error) {
	if !allowPlain {
		return nil, errors.New("confirm saving files without vault encryption")
	}
	if expectedAccountID <= 0 || expectedDriveID <= 0 {
		return nil, errors.New("choose a My Drive destination")
	}
	ctx, finish, err := s.sourceCopies.begin(ctx, expectedAccountID)
	if err != nil {
		return nil, err
	}
	defer finish()
	engine, driveID, err := s.sourceCopyDrive(ctx, expectedAccountID, expectedDriveID)
	if err != nil {
		return nil, err
	}
	return engine.FileService().StartSourceCopies(ctx, driveID, expectedAccountID, parentID, items, sourceCopyDependencies(engine))
}

func (s *DriveService) CancelSourceCopies(expectedAccountID int64) (bool, error) {
	if expectedAccountID <= 0 {
		return false, errors.New("invalid Telegram account")
	}
	return s.sourceCopies.cancelBatchFor(expectedAccountID), nil
}

func (s *DriveService) ListSourceCopies(expectedAccountID int64) ([]fileservice.SourceCopyResult, error) {
	if s.host == nil {
		return nil, errBackendUnavailable
	}
	ctx, finish, err := s.sourceCopies.begin(s.host.appContext(), 0)
	if err != nil {
		return nil, err
	}
	defer finish()
	engine, driveID, err := s.sourceCopyDrive(ctx, expectedAccountID, 0)
	if err != nil {
		return nil, err
	}
	return engine.FileService().ListSourceCopies(ctx, driveID, expectedAccountID)
}

func (s *DriveService) RetrySourceCopy(ctx context.Context, expectedAccountID int64, id string) (fileservice.SourceCopyResult, error) {
	ctx, finish, err := s.sourceCopies.begin(ctx, 0)
	if err != nil {
		return fileservice.SourceCopyResult{}, err
	}
	defer finish()
	engine, driveID, err := s.sourceCopyDrive(ctx, expectedAccountID, 0)
	if err != nil {
		return fileservice.SourceCopyResult{}, err
	}
	return engine.FileService().RetrySourceCopy(ctx, driveID, expectedAccountID, id, sourceCopyDependencies(engine))
}

func sourceCopyDependencies(engine *core.Engine) fileservice.SourceCopyDependencies {
	return fileservice.SourceCopyDependencies{
		Resolve:    engine.ChannelSourceService().ResolveCopyMedia,
		Sync:       engine.SyncSourceCopyDrive,
		SyncShared: engine.SyncSourceCopyDrive,
	}
}
