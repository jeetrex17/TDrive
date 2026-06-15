package core

import (
	"fmt"
	"path"
	"strings"

	"TDrive/backend"
	"TDrive/backend/projection"
)

type ResolvedFolder struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

type ResolvedParent struct {
	FolderID string `json:"folder_id"`
	Path     string `json:"path"`
	Name     string `json:"name"`
}

type ResolvedEntry struct {
	Type       string `json:"type"`
	ID         string `json:"id,omitempty"`
	MsgID      int64  `json:"msg_id,omitempty"`
	Name       string `json:"name"`
	ParentID   string `json:"parent_id"`
	Path       string `json:"path"`
	Size       int64  `json:"size,omitempty"`
	UploadTime int64  `json:"upload_time,omitempty"`
	Encrypted  bool   `json:"encrypted,omitempty"`
}

func NormalizeRemotePath(cwd string, input string) (string, error) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		cwd = "/"
	}
	if !strings.HasPrefix(cwd, "/") {
		cwd = "/" + cwd
	}

	input = strings.TrimSpace(input)
	if input == "" {
		input = "."
	}

	var p string
	if strings.HasPrefix(input, "/") {
		p = input
	} else {
		p = path.Join(cwd, input)
	}
	p = path.Clean(p)
	if p == "." {
		p = "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p, nil
}

func JoinRemotePath(parent string, name string) string {
	parent = strings.TrimSpace(parent)
	name = strings.Trim(strings.TrimSpace(name), "/")
	if parent == "" || parent == "/" {
		if name == "" {
			return "/"
		}
		return "/" + name
	}
	if name == "" {
		return parent
	}
	return path.Join(parent, name)
}

func (e *Engine) FolderPathByID(channelID int64, folderID string) (string, error) {
	if e == nil {
		return "", fmt.Errorf("backend not ready")
	}
	if channelID == 0 {
		return "", fmt.Errorf("no active drive")
	}
	if folderID == projection.RootParent {
		return "/", nil
	}
	if backend.DB == nil {
		return "", fmt.Errorf("db not ready")
	}
	folders, err := projection.ListAllFolders(backend.DB, channelID)
	if err != nil {
		return "", err
	}
	byID := make(map[string]projection.FolderSlim, len(folders))
	for _, folder := range folders {
		byID[folder.ID] = folder
	}
	return folderPathFromMap(byID, folderID)
}

func SplitRemotePath(abs string) (parent string, name string, err error) {
	abs, err = NormalizeRemotePath("/", abs)
	if err != nil {
		return "", "", err
	}
	if abs == "/" {
		return "", "", fmt.Errorf("path must not be root")
	}
	name = path.Base(abs)
	if name == "." || name == "/" || name == "" {
		return "", "", fmt.Errorf("path name required")
	}
	return path.Dir(abs), name, nil
}

func (e *Engine) ResolveFolderPath(channelID int64, cwd string, input string) (ResolvedFolder, error) {
	if e == nil {
		return ResolvedFolder{}, fmt.Errorf("backend not ready")
	}
	if channelID == 0 {
		return ResolvedFolder{}, fmt.Errorf("no active drive")
	}

	abs, err := NormalizeRemotePath(cwd, input)
	if err != nil {
		return ResolvedFolder{}, err
	}
	if abs == "/" {
		return ResolvedFolder{ID: projection.RootParent, Path: abs}, nil
	}

	cur := projection.RootParent
	for _, part := range strings.Split(strings.Trim(abs, "/"), "/") {
		if part == "" {
			continue
		}
		fs, err := e.ReadService().FolderContents(channelID, cur)
		if err != nil {
			return ResolvedFolder{}, err
		}
		var next string
		for _, folder := range fs.Folders {
			if folder.Name == part {
				next = folder.ID
				break
			}
		}
		if next == "" {
			return ResolvedFolder{}, fmt.Errorf("folder not found: %s", abs)
		}
		cur = next
	}

	return ResolvedFolder{ID: cur, Path: abs}, nil
}

func (e *Engine) ResolveParentPath(channelID int64, cwd string, input string) (ResolvedParent, error) {
	abs, err := NormalizeRemotePath(cwd, input)
	if err != nil {
		return ResolvedParent{}, err
	}
	parentPath, name, err := SplitRemotePath(abs)
	if err != nil {
		return ResolvedParent{}, err
	}
	parent, err := e.ResolveFolderPath(channelID, "/", parentPath)
	if err != nil {
		return ResolvedParent{}, err
	}
	return ResolvedParent{FolderID: parent.ID, Path: parent.Path, Name: name}, nil
}

func (e *Engine) ResolveEntryPath(channelID int64, cwd string, input string) (ResolvedEntry, error) {
	if e == nil {
		return ResolvedEntry{}, fmt.Errorf("backend not ready")
	}
	if channelID == 0 {
		return ResolvedEntry{}, fmt.Errorf("no active drive")
	}

	abs, err := NormalizeRemotePath(cwd, input)
	if err != nil {
		return ResolvedEntry{}, err
	}
	if abs == "/" {
		return ResolvedEntry{Type: "folder", ID: projection.RootParent, Path: "/", Name: "", ParentID: projection.RootParent}, nil
	}

	parentPath, name, err := SplitRemotePath(abs)
	if err != nil {
		return ResolvedEntry{}, err
	}
	parent, err := e.ResolveFolderPath(channelID, "/", parentPath)
	if err != nil {
		return ResolvedEntry{}, err
	}
	fs, err := e.ReadService().FolderContents(channelID, parent.ID)
	if err != nil {
		return ResolvedEntry{}, err
	}

	var folders []ResolvedEntry
	for _, folder := range fs.Folders {
		if folder.Name == name {
			folders = append(folders, ResolvedEntry{
				Type:     "folder",
				ID:       folder.ID,
				Name:     folder.Name,
				ParentID: folder.ParentID,
				Path:     abs,
			})
		}
	}
	var files []ResolvedEntry
	for _, file := range fs.Files {
		if file.Name == name {
			files = append(files, ResolvedEntry{
				Type:       "file",
				ID:         fmt.Sprintf("%d", file.MsgID),
				MsgID:      file.MsgID,
				Name:       file.Name,
				ParentID:   file.ParentID,
				Path:       abs,
				Size:       file.Size,
				UploadTime: file.UploadTime,
				Encrypted:  file.Encrypted,
			})
		}
	}

	switch {
	case len(folders) > 0 && len(files) > 0:
		return ResolvedEntry{}, fmt.Errorf("path is ambiguous: %s", abs)
	case len(folders) == 1:
		return folders[0], nil
	case len(folders) > 1:
		return ResolvedEntry{}, fmt.Errorf("folder path is ambiguous: %s", abs)
	case len(files) == 1:
		return files[0], nil
	case len(files) > 1:
		return ResolvedEntry{}, fmt.Errorf("file path is ambiguous: %s", abs)
	default:
		return ResolvedEntry{}, fmt.Errorf("path not found: %s", abs)
	}
}

func folderPathFromMap(folders map[string]projection.FolderSlim, folderID string) (string, error) {
	folderID = strings.TrimSpace(folderID)
	if folderID == projection.RootParent {
		return "/", nil
	}

	names := make([]string, 0, 8)
	visited := make(map[string]bool)
	cur := folderID
	for cur != projection.RootParent {
		if visited[cur] {
			return "", fmt.Errorf("folder path contains a cycle at %s", cur)
		}
		visited[cur] = true
		folder, ok := folders[cur]
		if !ok {
			return "", fmt.Errorf("folder path is broken at %s", cur)
		}
		names = append(names, folder.Name)
		cur = strings.TrimSpace(folder.ParentID)
	}

	for i, j := 0, len(names)-1; i < j; i, j = i+1, j-1 {
		names[i], names[j] = names[j], names[i]
	}
	return "/" + strings.Join(names, "/"), nil
}
