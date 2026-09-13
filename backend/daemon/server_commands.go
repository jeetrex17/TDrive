package daemon

import (
	"TDrive/backend/core"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func (s *Server) status(ctx context.Context) (Status, error) {
	out := Status{
		PID:             os.Getpid(),
		ActiveChannelID: s.engine.ActiveChannelID(),
		CurrentPath:     s.currentPath(),
	}
	if enc := s.engine.EncryptionService(); enc != nil {
		st, err := enc.StatusContext(ctx)
		if err != nil {
			return Status{}, err
		}
		out.VaultAvailable = st.Available
		out.VaultConfigured = st.PasswordSet
		out.VaultUnlocked = st.PasswordRemembered
		out.VaultHint = st.Hint
	}
	return out, nil
}

func (s *Server) pwd() (PathResponse, error) {
	drive, err := s.activeDrive()
	if err != nil {
		return PathResponse{}, err
	}
	return PathResponse{Drive: drive, CurrentPath: s.currentPath()}, nil
}

func (s *Server) cd(input string) (PathResponse, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	drive, err := s.activeDrive()
	if err != nil {
		return PathResponse{}, err
	}
	resolved, err := s.engine.ResolveFolderPath(drive.ID, s.currentPath(), input)
	if err != nil {
		return PathResponse{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == nil {
		s.state = newState()
	}
	s.state.CurrentDriveID = drive.ID
	s.state.setCWD(drive.ID, resolved.Path)
	if err := s.state.save(); err != nil {
		return PathResponse{}, err
	}
	return PathResponse{Drive: drive, CurrentPath: resolved.Path}, nil
}

func (s *Server) listPath(input string) (ListResponse, error) {
	drive, err := s.activeDrive()
	if err != nil {
		return ListResponse{}, err
	}
	resolved, err := s.engine.ResolveFolderPath(drive.ID, s.currentPath(), input)
	if err != nil {
		return ListResponse{}, err
	}
	fs, err := s.engine.ReadService().FolderContents(drive.ID, resolved.ID)
	if err != nil {
		return ListResponse{}, err
	}

	entries := make([]Entry, 0, len(fs.Folders)+len(fs.Files))
	for _, folder := range fs.Folders {
		entries = append(entries, Entry{
			Type: "folder",
			ID:   folder.ID,
			Name: folder.Name,
			Path: core.JoinRemotePath(resolved.Path, folder.Name),
		})
	}
	for _, file := range fs.Files {
		entries = append(entries, Entry{
			Type:       "file",
			ID:         strconv.FormatInt(file.MsgID, 10),
			MsgID:      file.MsgID,
			Name:       file.Name,
			Path:       core.JoinRemotePath(resolved.Path, file.Name),
			Size:       file.Size,
			UploadTime: file.UploadTime,
			Encrypted:  file.Encrypted,
		})
	}
	return ListResponse{Drive: drive, Path: resolved.Path, Entries: entries}, nil
}

func (s *Server) find(query string, limit int) (FindResponse, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return FindResponse{}, fmt.Errorf("query required")
	}
	drive, err := s.activeDrive()
	if err != nil {
		return FindResponse{}, err
	}
	if limit <= 0 {
		limit = 50
	}
	hits, err := s.engine.ReadService().Search(drive.ID, query, limit)
	if err != nil {
		return FindResponse{}, err
	}
	out := FindResponse{Drive: drive, Results: make([]Entry, 0, len(hits))}
	for _, hit := range hits {
		entry := Entry{
			Type:       hit.Type,
			ID:         hit.ID,
			Name:       hit.Name,
			Size:       hit.Size,
			UploadTime: hit.UploadTime,
		}
		if hit.Type == "folder" {
			path, err := s.engine.FolderPathByID(drive.ID, hit.ID)
			if err != nil {
				return FindResponse{}, err
			}
			entry.Path = path
		} else {
			if msgID, err := strconv.ParseInt(hit.ID, 10, 64); err == nil {
				entry.MsgID = msgID
			}
			parentPath, err := s.engine.FolderPathByID(drive.ID, hit.ParentID)
			if err != nil {
				return FindResponse{}, err
			}
			entry.Path = core.JoinRemotePath(parentPath, hit.Name)
		}
		out.Results = append(out.Results, entry)
	}
	return out, nil
}

func (s *Server) mkdir(input string, parents bool) (EntryResponse, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	drive, err := s.activeDrive()
	if err != nil {
		return EntryResponse{}, err
	}
	if parents {
		entry, err := s.ensureFolderPath(drive.ID, s.currentPath(), input)
		if err != nil {
			return EntryResponse{}, err
		}
		return EntryResponse{Drive: drive, Entry: entry}, nil
	}

	parent, err := s.engine.ResolveParentPath(drive.ID, s.currentPath(), input)
	if err != nil {
		return EntryResponse{}, err
	}
	if err := s.ensureNameFree(drive.ID, parent.FolderID, parent.Name, core.ResolvedEntry{}); err != nil {
		return EntryResponse{}, err
	}
	folder, err := s.engine.FolderService().Create(drive.ID, parent.Name, parent.FolderID)
	if err != nil {
		return EntryResponse{}, err
	}
	entry := Entry{
		Type: "folder",
		ID:   folder.ID,
		Name: folder.Name,
		Path: core.JoinRemotePath(parent.Path, folder.Name),
	}
	return EntryResponse{Drive: drive, Entry: entry}, nil
}

func (s *Server) remove(ctx context.Context, input string, recursive bool) (EntryResponse, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	drive, err := s.activeDrive()
	if err != nil {
		return EntryResponse{}, err
	}
	entry, err := s.engine.ResolveEntryPath(drive.ID, s.currentPath(), input)
	if err != nil {
		return EntryResponse{}, err
	}
	if entry.Path == "/" {
		return EntryResponse{}, fmt.Errorf("refusing to remove root")
	}

	switch entry.Type {
	case "file":
		if err := s.engine.FileService().Delete(ctx, drive.ID, int(entry.MsgID)); err != nil {
			return EntryResponse{}, err
		}
	case "folder":
		if !recursive {
			return EntryResponse{}, fmt.Errorf("%s is a folder; use rm -r", entry.Path)
		}
		if err := s.engine.FolderService().Delete(ctx, drive.ID, entry.ID); err != nil {
			return EntryResponse{}, err
		}
	default:
		return EntryResponse{}, fmt.Errorf("unsupported entry type %q", entry.Type)
	}
	return EntryResponse{Drive: drive, Entry: entryFromResolved(entry)}, nil
}

func (s *Server) move(ctx context.Context, source string, destination string) (EntryResponse, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	drive, err := s.activeDrive()
	if err != nil {
		return EntryResponse{}, err
	}
	src, err := s.engine.ResolveEntryPath(drive.ID, s.currentPath(), source)
	if err != nil {
		return EntryResponse{}, err
	}
	if src.Path == "/" {
		return EntryResponse{}, fmt.Errorf("refusing to move root")
	}

	dstAbs, err := core.NormalizeRemotePath(s.currentPath(), destination)
	if err != nil {
		return EntryResponse{}, err
	}
	if dstAbs == src.Path {
		return EntryResponse{Drive: drive, Entry: entryFromResolved(src)}, nil
	}
	targetParentID, targetParentPath, targetName, err := s.moveTarget(drive.ID, dstAbs, src.Name)
	if err != nil {
		return EntryResponse{}, err
	}
	if err := s.ensureNameFree(drive.ID, targetParentID, targetName, src); err != nil {
		return EntryResponse{}, err
	}

	switch src.Type {
	case "file":
		if src.ParentID != targetParentID {
			if err := s.engine.FileService().Move(ctx, drive.ID, int(src.MsgID), targetParentID); err != nil {
				return EntryResponse{}, err
			}
		}
		if src.Name != targetName {
			if err := s.engine.FileService().Rename(ctx, drive.ID, int(src.MsgID), targetName); err != nil {
				return EntryResponse{}, err
			}
		}
	case "folder":
		if src.ParentID != targetParentID {
			if err := s.engine.FolderService().Move(drive.ID, src.ID, targetParentID); err != nil {
				return EntryResponse{}, err
			}
		}
		if src.Name != targetName {
			if err := s.engine.FolderService().Rename(drive.ID, src.ID, targetName); err != nil {
				return EntryResponse{}, err
			}
		}
	default:
		return EntryResponse{}, fmt.Errorf("unsupported entry type %q", src.Type)
	}

	entry := entryFromResolved(src)
	entry.Name = targetName
	entry.Path = core.JoinRemotePath(targetParentPath, targetName)
	return EntryResponse{Drive: drive, Entry: entry}, nil
}

func (s *Server) upload(ctx context.Context, localPath string, remotePath string, encrypt bool, extract bool) (UploadResponse, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	localPath = strings.TrimSpace(localPath)
	if localPath == "" {
		return UploadResponse{}, fmt.Errorf("local path required")
	}
	if !filepath.IsAbs(localPath) {
		return UploadResponse{}, fmt.Errorf("local path must be absolute")
	}
	info, err := os.Stat(localPath)
	if err != nil {
		return UploadResponse{}, err
	}

	drive, err := s.activeDrive()
	if err != nil {
		return UploadResponse{}, err
	}
	if info.IsDir() || extract {
		parentID, parentPath, err := s.importTarget(drive.ID, remotePath)
		if err != nil {
			return UploadResponse{}, err
		}
		if err := s.engine.FileService().RunImport(ctx, drive.ID, []string{localPath}, parentID, encrypt, extract); err != nil {
			return UploadResponse{}, err
		}
		return UploadResponse{Drive: drive, Entry: folderEntry(parentID, parentPath)}, nil
	}

	parentID, parentPath, targetName, err := s.uploadTarget(drive.ID, remotePath, filepath.Base(localPath))
	if err != nil {
		return UploadResponse{}, err
	}
	if err := s.ensureNameFree(drive.ID, parentID, targetName, core.ResolvedEntry{}); err != nil {
		return UploadResponse{}, err
	}

	metas, err := s.engine.FileService().Upload(ctx, drive.ID, []string{localPath}, []string{parentID}, encrypt)
	if err != nil {
		return UploadResponse{}, err
	}
	if len(metas) == 0 {
		return UploadResponse{}, fmt.Errorf("upload produced no file")
	}
	meta := metas[0]
	if meta.Name != targetName {
		if err := s.engine.FileService().Rename(ctx, drive.ID, meta.MsgID, targetName); err != nil {
			return UploadResponse{}, err
		}
	}

	entry := Entry{
		Type:       "file",
		ID:         strconv.Itoa(meta.MsgID),
		MsgID:      int64(meta.MsgID),
		Name:       targetName,
		Path:       core.JoinRemotePath(parentPath, targetName),
		Size:       meta.Size,
		UploadTime: meta.UploadTime,
		Encrypted:  meta.Encrypted,
	}
	return UploadResponse{Drive: drive, Entry: entry}, nil
}

func (s *Server) download(ctx context.Context, remotePath string, localPath string) (DownloadResponse, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	localPath = strings.TrimSpace(localPath)
	if localPath == "" {
		return DownloadResponse{}, fmt.Errorf("local path required")
	}
	if !filepath.IsAbs(localPath) {
		return DownloadResponse{}, fmt.Errorf("local path must be absolute")
	}
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return DownloadResponse{}, err
	}

	drive, err := s.activeDrive()
	if err != nil {
		return DownloadResponse{}, err
	}
	resolved, err := s.engine.ResolveEntryPath(drive.ID, s.currentPath(), remotePath)
	if err != nil {
		return DownloadResponse{}, err
	}
	if resolved.Type != "file" {
		return DownloadResponse{}, fmt.Errorf("%s is not a file", resolved.Path)
	}

	entry := entryFromResolved(resolved)
	result := s.engine.FileService().Download(ctx, drive.ID, int(resolved.MsgID), int(resolved.MsgID), func(defaultName string) (string, error) {
		return localPath, nil
	})
	if result.Status != "success" {
		if result.Message == "" {
			result.Message = result.Status
		}
		return DownloadResponse{}, fmt.Errorf("%s", result.Message)
	}
	if result.SavedPath != "" {
		localPath = result.SavedPath
	}
	return DownloadResponse{Drive: drive, Entry: entry, SavedPath: localPath}, nil
}

func (s *Server) ensureFolderPath(channelID int64, cwd string, input string) (Entry, error) {
	abs, err := core.NormalizeRemotePath(cwd, input)
	if err != nil {
		return Entry{}, err
	}
	if abs == "/" {
		return Entry{Type: "folder", ID: "", Name: "", Path: "/"}, nil
	}

	curID := ""
	curPath := "/"
	var last Entry
	for _, part := range strings.Split(strings.Trim(abs, "/"), "/") {
		if part == "" {
			continue
		}
		fs, err := s.engine.ReadService().FolderContents(channelID, curID)
		if err != nil {
			return Entry{}, err
		}
		var found *Entry
		for _, folder := range fs.Folders {
			if folder.Name == part {
				entry := Entry{
					Type: "folder",
					ID:   folder.ID,
					Name: folder.Name,
					Path: core.JoinRemotePath(curPath, folder.Name),
				}
				found = &entry
				break
			}
		}
		if found == nil {
			for _, file := range fs.Files {
				if file.Name == part {
					return Entry{}, fmt.Errorf("%s exists and is not a folder", core.JoinRemotePath(curPath, part))
				}
			}
			folder, err := s.engine.FolderService().Create(channelID, part, curID)
			if err != nil {
				return Entry{}, err
			}
			entry := Entry{
				Type: "folder",
				ID:   folder.ID,
				Name: folder.Name,
				Path: core.JoinRemotePath(curPath, folder.Name),
			}
			found = &entry
		}
		curID = found.ID
		curPath = found.Path
		last = *found
	}
	return last, nil
}

func (s *Server) moveTarget(channelID int64, dstAbs string, sourceName string) (parentID string, parentPath string, name string, err error) {
	dst, err := s.engine.ResolveEntryPath(channelID, "/", dstAbs)
	if err == nil {
		if dst.Type != "folder" {
			return "", "", "", fmt.Errorf("destination exists and is not a folder: %s", dstAbs)
		}
		return dst.ID, dst.Path, sourceName, nil
	}
	if !errors.Is(err, core.ErrPathNotFound) {
		return "", "", "", err
	}

	parent, err := s.engine.ResolveParentPath(channelID, "/", dstAbs)
	if err != nil {
		return "", "", "", err
	}
	return parent.FolderID, parent.Path, parent.Name, nil
}

// importTarget resolves the destination folder for directory and archive
// imports. RunImport owns creating the imported top-level folders beneath it.
func (s *Server) importTarget(channelID int64, remotePath string) (folderID string, folderPath string, err error) {
	remotePath = strings.TrimSpace(remotePath)
	if remotePath == "" {
		folder, err := s.engine.ResolveFolderPath(channelID, s.currentPath(), ".")
		if err != nil {
			return "", "", err
		}
		return folder.ID, folder.Path, nil
	}

	dstAbs, err := core.NormalizeRemotePath(s.currentPath(), remotePath)
	if err != nil {
		return "", "", err
	}
	dst, err := s.engine.ResolveEntryPath(channelID, "/", dstAbs)
	if err == nil {
		if dst.Type != "folder" {
			return "", "", fmt.Errorf("destination exists and is not a folder: %s", dstAbs)
		}
		return dst.ID, dst.Path, nil
	}
	if !errors.Is(err, core.ErrPathNotFound) {
		return "", "", err
	}
	return "", "", fmt.Errorf("destination folder not found: %s", dstAbs)
}

func (s *Server) uploadTarget(channelID int64, remotePath string, defaultName string) (parentID string, parentPath string, name string, err error) {
	remotePath = strings.TrimSpace(remotePath)
	if remotePath == "" {
		folder, err := s.engine.ResolveFolderPath(channelID, s.currentPath(), ".")
		if err != nil {
			return "", "", "", err
		}
		return folder.ID, folder.Path, defaultName, nil
	}
	mustBeFolder := strings.HasSuffix(remotePath, "/")

	dstAbs, err := core.NormalizeRemotePath(s.currentPath(), remotePath)
	if err != nil {
		return "", "", "", err
	}
	dst, err := s.engine.ResolveEntryPath(channelID, "/", dstAbs)
	if err == nil {
		if dst.Type != "folder" {
			return "", "", "", fmt.Errorf("destination exists and is not a folder: %s", dstAbs)
		}
		return dst.ID, dst.Path, defaultName, nil
	}
	if !errors.Is(err, core.ErrPathNotFound) {
		return "", "", "", err
	}
	if mustBeFolder {
		return "", "", "", fmt.Errorf("destination folder not found: %s", dstAbs)
	}
	parent, err := s.engine.ResolveParentPath(channelID, "/", dstAbs)
	if err != nil {
		return "", "", "", err
	}
	return parent.FolderID, parent.Path, parent.Name, nil
}

func (s *Server) ensureNameFree(channelID int64, parentID string, name string, allow core.ResolvedEntry) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name required")
	}
	fs, err := s.engine.ReadService().FolderContents(channelID, parentID)
	if err != nil {
		return err
	}
	for _, folder := range fs.Folders {
		if folder.Name != name {
			continue
		}
		if allow.Type == "folder" && allow.ID == folder.ID {
			return nil
		}
		return fmt.Errorf("destination already exists: %s", name)
	}
	for _, file := range fs.Files {
		if file.Name != name {
			continue
		}
		if allow.Type == "file" && allow.MsgID == file.MsgID {
			return nil
		}
		return fmt.Errorf("destination already exists: %s", name)
	}
	return nil
}
