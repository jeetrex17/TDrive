package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"TDrive/backend/media"
	"TDrive/backend/mountcontent"
	"TDrive/backend/mountdav"
	"TDrive/backend/mountfs"
	readservice "TDrive/backend/services/read"
	"TDrive/backend/tgclient"
)

func (s *Server) startMount(ctx context.Context, selector string, windowsDrive string) (MountResponse, error) {
	s.mountMu.Lock()
	defer s.mountMu.Unlock()

	requestedWindowsDrive, err := normalizeRequestedWindowsDrive(windowsDrive)
	if err != nil {
		return MountResponse{}, err
	}
	mount := s.mountServerLocked()
	existing := mount.Status()
	if !existing.Running {
		// A failed Serve loop may make the protocol server non-running without a
		// daemon stop command. Do not carry its range cache into a new mount.
		s.closeMountContentLocked()
	}
	if err := ensureCompatibleWindowsDrive(existing, requestedWindowsDrive); err != nil {
		return MountResponse{}, err
	}

	selector = strings.TrimSpace(selector)
	if existing.Running && selector == "" {
		return mountResponse(existing, s.driveFromMountStatus(existing)), nil
	}

	var drive Drive
	if selector != "" {
		drive, err = s.resolveDrive(selector)
	} else {
		drive, err = s.activeDrive()
	}
	if err != nil {
		return MountResponse{}, err
	}
	if existing.Running {
		if existing.DriveID != drive.ID {
			return MountResponse{}, mountAlreadyRunningError(existing)
		}
		return mountResponse(existing, s.driveFromMountStatus(existing)), nil
	}

	opener, err := s.newMountContentOpener()
	if err != nil {
		return MountResponse{}, err
	}
	filesystem, err := mountfs.New(
		drive.ID,
		mountDirectorySource{reads: s.engine.ReadService()},
		mountContentAdapter{opener: opener},
	)
	if err != nil {
		opener.Close()
		return MountResponse{}, fmt.Errorf("mount: create filesystem: %w", err)
	}

	status, err := mount.Start(ctx, mountdav.StartConfig{
		FS:           filesystem,
		DriveID:      drive.ID,
		DriveTitle:   drive.Title,
		WindowsDrive: requestedWindowsDrive,
	})
	if err != nil {
		opener.Close()
		return MountResponse{}, err
	}
	if status.DriveID != drive.ID {
		opener.Close()
		return MountResponse{}, mountAlreadyRunningError(status)
	}
	s.mountContent = opener
	return mountResponse(status, s.driveFromMountStatus(status)), nil
}

func (s *Server) mountStatus() MountResponse {
	s.mountMu.Lock()
	defer s.mountMu.Unlock()

	status := s.mountServerLocked().Status()
	if !status.Running {
		s.closeMountContentLocked()
	}
	return mountResponse(status, s.driveFromMountStatus(status))
}

func (s *Server) stopMount(ctx context.Context) (MountResponse, error) {
	if err := s.stopMountServer(ctx); err != nil {
		return MountResponse{}, err
	}
	return MountResponse{Running: false}, nil
}

func (s *Server) stopMountServer(ctx context.Context) error {
	s.mountMu.Lock()
	defer s.mountMu.Unlock()

	err := s.mountServerLocked().Stop(ctx)
	s.closeMountContentLocked()
	return err
}

func (s *Server) closeMountContentLocked() {
	content := s.mountContent
	s.mountContent = nil
	if content != nil {
		content.Close()
	}
}

func (s *Server) mountServerLocked() *mountdav.Server {
	if s.mount == nil {
		s.mount = mountdav.NewServer()
	}
	return s.mount
}

func (s *Server) newMountContentOpener() (*mountcontent.Opener, error) {
	if s.engine == nil || s.engine.ReadService() == nil || s.engine.ReadService().DB == nil {
		return nil, fmt.Errorf("mount: database is not ready")
	}
	ranges, ok := s.engine.Telegram().(tgclient.RangeClient)
	if !ok || ranges == nil {
		return nil, fmt.Errorf("mount: Telegram range reads are not available")
	}
	opener, err := mountcontent.New(mountcontent.Config{
		DB:     s.engine.ReadService().DB,
		Peers:  s.engine,
		Ranges: ranges,
	})
	if err != nil {
		return nil, fmt.Errorf("mount: initialize content reader: %w", err)
	}
	return opener, nil
}

func (s *Server) driveFromMountStatus(status mountdav.Status) Drive {
	if !status.Running {
		return Drive{}
	}
	active := int64(0)
	if s.engine != nil {
		active = s.engine.ActiveChannelID()
	}
	return Drive{
		ID:     status.DriveID,
		Title:  status.DriveTitle,
		Active: status.DriveID == active,
	}
}

func mountAlreadyRunningError(status mountdav.Status) error {
	return fmt.Errorf(
		"mount already running for %q (%d); stop it before mounting another drive",
		status.DriveTitle,
		status.DriveID,
	)
}

func normalizeRequestedWindowsDrive(value string) (string, error) {
	value = strings.ToUpper(strings.TrimSpace(value))
	if value == "" {
		return "", nil
	}
	if len(value) == 1 && value[0] >= 'A' && value[0] <= 'Z' {
		return value + ":", nil
	}
	if len(value) == 2 && value[0] >= 'A' && value[0] <= 'Z' && value[1] == ':' {
		return value, nil
	}
	return "", fmt.Errorf("invalid Windows drive %q: use a letter such as T:", value)
}

func ensureCompatibleWindowsDrive(status mountdav.Status, requested string) error {
	if !status.Running || requested == "" || requested == status.WindowsDrive {
		return nil
	}
	return fmt.Errorf(
		"mount already uses Windows drive %s; stop it before changing to %s",
		status.WindowsDrive,
		requested,
	)
}

type mountDirectorySource struct {
	reads *readservice.Service
}

func (source mountDirectorySource) ListDirectory(ctx context.Context, channelID int64, parentID string) ([]mountfs.SourceEntry, error) {
	if ctx == nil {
		return nil, mountDirectoryUnavailable()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if source.reads == nil {
		return nil, mountDirectoryUnavailable()
	}
	if channelID <= 0 {
		return nil, mountDirectoryUnavailable()
	}

	projectionParentID, err := projectionFolderID(parentID)
	if err != nil {
		return nil, mountDirectoryUnavailable()
	}
	contents, err := source.reads.FolderContentsContext(ctx, channelID, projectionParentID)
	if err != nil {
		return nil, mapMountDirectoryError(ctx, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	entries := make([]mountfs.SourceEntry, 0, len(contents.Folders)+len(contents.Files))
	for _, folder := range contents.Folders {
		if folder.ID == "" {
			return nil, mountDirectoryUnavailable()
		}
		entries = append(entries, mountfs.SourceEntry{
			ID:       folder.ID,
			ParentID: parentID,
			Name:     folder.Name,
			Kind:     mountfs.KindDirectory,
		})
	}
	for _, file := range contents.Files {
		if file.MsgID <= 0 {
			return nil, mountDirectoryUnavailable()
		}
		logicalSize := file.Size
		if file.Encrypted {
			logicalSize = file.PlaintextSize
		}
		if logicalSize < 0 {
			return nil, mountDirectoryUnavailable()
		}
		modTime := time.Time{}
		if file.UploadTime > 0 {
			modTime = time.Unix(file.UploadTime, 0)
		}
		messageID := strconv.FormatInt(file.MsgID, 10)
		entries = append(entries, mountfs.SourceEntry{
			ID:         "f:" + messageID,
			ParentID:   parentID,
			Name:       file.Name,
			Kind:       mountfs.KindFile,
			Size:       logicalSize,
			ModTime:    modTime,
			Encrypted:  file.Encrypted,
			ContentRef: messageID,
		})
	}
	return entries, nil
}

func mapMountDirectoryError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, mountfs.ErrAccessDenied), errors.Is(err, os.ErrPermission):
		return fmt.Errorf("%w: directory metadata access was denied", mountfs.ErrAccessDenied)
	default:
		return mountDirectoryUnavailable()
	}
}

func mountDirectoryUnavailable() error {
	return fmt.Errorf("%w: directory metadata could not be loaded", mountfs.ErrContentUnavailable)
}

func projectionFolderID(mountID string) (string, error) {
	if mountID == mountfs.RootID {
		return "", nil
	}
	if !strings.HasPrefix(mountID, "d:") || len(mountID) == len("d:") {
		return "", fmt.Errorf("mount: invalid directory id")
	}
	return mountID, nil
}

type mountContentAdapter struct {
	opener *mountcontent.Opener
}

func (adapter mountContentAdapter) OpenContent(ctx context.Context, channelID int64, entry mountfs.SourceEntry) (mountfs.RandomAccessContent, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: content context is missing", mountfs.ErrContentUnavailable)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if adapter.opener == nil {
		return nil, fmt.Errorf("%w: content reader is not ready", mountfs.ErrContentUnavailable)
	}
	if entry.Encrypted {
		return nil, fmt.Errorf("%w: encrypted files are unavailable in this release", mountfs.ErrAccessDenied)
	}
	messageID, err := strconv.ParseInt(entry.ContentRef, 10, 64)
	if err != nil || messageID <= 0 {
		return nil, fmt.Errorf("%w: invalid content reference", mountfs.ErrContentUnavailable)
	}
	reader, err := adapter.opener.Open(ctx, channelID, messageID)
	if err != nil {
		return nil, mapMountContentOpenError(err)
	}
	if reader.Size() != entry.Size {
		_ = reader.Close()
		return nil, fmt.Errorf("%w: projected content size does not match the resolved body", mountfs.ErrContentUnavailable)
	}
	return reader, nil
}

func mapMountContentOpenError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, mountcontent.ErrEncryptedUnsupported), errors.Is(err, media.ErrEncryptedUnsupported):
		return fmt.Errorf("%w: encrypted files are unavailable in this release", mountfs.ErrAccessDenied)
	case errors.Is(err, media.ErrFileNotFound),
		errors.Is(err, tgclient.ErrMessageNotFound),
		errors.Is(err, tgclient.ErrNotFile),
		errors.Is(err, tgclient.ErrEmptyDocument):
		return fmt.Errorf("%w: content body was not found", mountfs.ErrNotFound)
	default:
		return fmt.Errorf("%w: content could not be opened", mountfs.ErrContentUnavailable)
	}
}

func mountResponse(status mountdav.Status, drive Drive) MountResponse {
	if !status.Running {
		return MountResponse{Running: false, Error: status.Error}
	}
	return MountResponse{
		Running:      status.Running,
		URL:          status.URL,
		Mode:         status.Mode,
		Error:        status.Error,
		Drive:        drive,
		WindowsDrive: status.WindowsDrive,
		Commands: MountCommands{
			WindowsMap:    status.Commands.WindowsMap,
			WindowsUnmap:  status.Commands.WindowsUnmap,
			MacFinder:     status.Commands.MacFinder,
			LinuxMount:    status.Commands.LinuxMount,
			LinuxUnmount:  status.Commands.LinuxUnmount,
			ActiveOSMount: status.Commands.ActiveOSMount,
		},
	}
}
