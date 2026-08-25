package read

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

type PeerResolver interface {
	ResolvePeer(ctx context.Context, channelID int64) (tgclient.InputPeer, error)
}

type Service struct {
	DB    *sql.DB
	TG    tgclient.Client
	Peers PeerResolver
}

type Folder struct {
	ID       string
	Name     string
	ParentID string
	Revision int64
}

type File struct {
	MsgID         int64
	ContentMsgID  int64
	ContentHash   string
	Revision      int64
	UploadUUID    string
	PartCount     int
	Name          string
	Size          int64
	ParentID      string
	UploadTime    int64
	UploaderID    int64
	Encrypted     bool
	PlaintextSize int64
}

type FileSystem struct {
	Folders []Folder
	Files   []File
}

type SearchResult struct {
	Type          string
	ID            string
	Name          string
	ParentID      string
	Size          int64
	UploadTime    int64
	UploaderID    int64
	Encrypted     bool
	PlaintextSize int64
	Path          string
}

type TelegramFile struct {
	ID         int
	Name       string
	Size       int64
	AccessHash int64
	Date       int
}

func (s *Service) StorageUsed(channelID int64) (int64, error) {
	if err := s.ready(); err != nil {
		return 0, err
	}
	if channelID == 0 {
		return 0, nil
	}
	return projection.StorageUsed(s.DB, channelID)
}

func (s *Service) FolderContents(channelID int64, parentID string) (FileSystem, error) {
	return s.FolderContentsContext(context.Background(), channelID, parentID)
}

// FolderContentsContext is FolderContents with cancellation propagated to the
// projection queries. Callers serving bounded requests should prefer it.
func (s *Service) FolderContentsContext(ctx context.Context, channelID int64, parentID string) (FileSystem, error) {
	if ctx == nil {
		return FileSystem{}, fmt.Errorf("read: list folder contents: %w", projection.ErrInvalidContext)
	}
	if err := s.ready(); err != nil {
		return FileSystem{}, err
	}
	if channelID == 0 {
		return FileSystem{Folders: []Folder{}, Files: []File{}}, nil
	}

	folders, files, err := projection.ListFolderContentsContext(ctx, s.DB, channelID, parentID)
	if err != nil {
		return FileSystem{}, fmt.Errorf("read: list folder contents: %w", err)
	}

	out := FileSystem{
		Folders: make([]Folder, 0, len(folders)),
		Files:   make([]File, 0, len(files)),
	}
	for _, f := range folders {
		out.Folders = append(out.Folders, folderFromProjection(f))
	}
	for _, f := range files {
		out.Files = append(out.Files, fileFromProjection(f))
	}
	return out, nil
}

func (s *Service) Search(channelID int64, query string, limit int) ([]SearchResult, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if channelID == 0 {
		return []SearchResult{}, nil
	}

	query = strings.TrimSpace(query)
	if query == "" {
		return []SearchResult{}, nil
	}

	hits, err := projection.Search(s.DB, channelID, query, limit)
	if err != nil {
		return nil, err
	}

	pathCache := make(map[string]projection.FolderSlim, len(hits))
	results := make([]SearchResult, 0, len(hits))
	for _, h := range hits {
		switch h.Type {
		case "folder":
			path, err := buildFolderPathLazy(s.DB, channelID, h.ID, pathCache)
			if err != nil {
				return nil, err
			}
			results = append(results, SearchResult{
				Type:     "folder",
				ID:       h.ID,
				Name:     h.Name,
				ParentID: h.ParentID,
				Path:     path,
			})
		case "file":
			path, err := buildFolderPathLazy(s.DB, channelID, h.ParentID, pathCache)
			if err != nil {
				return nil, err
			}
			results = append(results, SearchResult{
				Type:          "file",
				ID:            fmt.Sprintf("%d", h.MsgID),
				Name:          h.Name,
				ParentID:      h.ParentID,
				Size:          h.Size,
				UploadTime:    h.Time,
				UploaderID:    h.UploaderID,
				Encrypted:     h.Encrypted,
				PlaintextSize: h.PlaintextSize,
				Path:          path,
			})
		}
	}
	return results, nil
}

func (s *Service) OrphanedFiles(channelID int64) ([]File, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if channelID == 0 {
		return []File{}, nil
	}
	files, err := projection.OrphanedFiles(s.DB, channelID)
	if err != nil {
		return nil, err
	}
	out := make([]File, 0, len(files))
	for _, f := range files {
		out = append(out, fileFromProjection(f))
	}
	return out, nil
}

// MediaFiles returns every image file across the channel, newest first, for
// the gallery view. "Image" is defined by thumbnail.IsImage so the gallery
// shows exactly the files a thumbnail can be produced for. Filtering happens
// here rather than in SQL because the projection stores no content type.
//
// Legacy files with broken parent chains are still included: the gallery is a
// flat index over every visible image in the drive, independent of folder
// structure.
func (s *Service) MediaFiles(channelID int64) ([]File, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if channelID == 0 {
		return []File{}, nil
	}
	all, err := projection.MediaFiles(s.DB, channelID)
	if err != nil {
		return nil, err
	}
	out := make([]File, 0, len(all))
	for _, f := range all {
		out = append(out, fileFromProjection(f))
	}
	return out, nil
}

func (s *Service) AllFileMsgIDs(channelID int64) ([]int, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if channelID == 0 {
		return []int{}, nil
	}
	ids64, err := projection.AllFileMsgIDs(s.DB, channelID)
	if err != nil {
		return nil, err
	}
	out := make([]int, 0, len(ids64))
	for _, id := range ids64 {
		out = append(out, int(id))
	}
	return out, nil
}

func (s *Service) FolderSize(channelID int64, folderID string) (int64, error) {
	if err := s.ready(); err != nil {
		return 0, err
	}
	if channelID == 0 {
		return 0, fmt.Errorf("no active channel")
	}
	return projection.FolderSize(s.DB, channelID, folderID)
}

func (s *Service) ChildFolderSizes(channelID int64, parentID string) (map[string]int64, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if channelID == 0 {
		return map[string]int64{}, nil
	}
	return projection.ChildFolderSizes(s.DB, channelID, parentID)
}

func (s *Service) TelegramRootFiles(ctx context.Context, channelID int64) ([]TelegramFile, error) {
	if channelID == 0 {
		return nil, nil
	}
	if s.TG == nil {
		return nil, fmt.Errorf("tg client not ready")
	}
	if s.Peers == nil {
		return nil, fmt.Errorf("peer resolver not ready")
	}
	peer, err := s.Peers.ResolvePeer(ctx, channelID)
	if err != nil {
		return nil, err
	}
	messages, err := s.TG.GetHistory(ctx, peer, 0, 0, 100)
	if err != nil {
		return nil, err
	}

	files := make([]TelegramFile, 0, len(messages))
	for _, msg := range messages {
		if !msg.HasMedia {
			continue
		}
		// Multipart part documents are internal artifacts, not user files. They
		// carry a TDX1 t=part header; skip them so they never surface at root.
		// Their msg_ids live in file_parts, not files, so the frontend's
		// unmanaged-file filter wouldn't otherwise exclude them.
		if op, err := projection.Parse(projection.ExtractHeaderLine(msg.Text)); err == nil && op.Type == projection.OpFilePart {
			continue
		}
		name := strings.TrimSpace(msg.DocumentName)
		if name == "" {
			name = "Unknown"
		}
		files = append(files, TelegramFile{
			ID:         int(msg.MsgID),
			Name:       name,
			Size:       msg.MediaSize,
			AccessHash: msg.DocumentAccessHash,
			Date:       int(msg.Date),
		})
	}
	return files, nil
}

func (s *Service) ready() error {
	if s.DB == nil {
		return fmt.Errorf("db not ready")
	}
	return nil
}

func folderFromProjection(f projection.FolderSlim) Folder {
	return Folder{ID: f.ID, Name: f.Name, ParentID: f.ParentID, Revision: f.Revision}
}

func fileFromProjection(f projection.FileSlim) File {
	return File{
		MsgID:         f.MsgID,
		ContentMsgID:  f.ContentMsgID,
		ContentHash:   f.ContentHash,
		Revision:      f.Revision,
		UploadUUID:    f.UploadUUID,
		PartCount:     f.PartCount,
		Name:          f.Name,
		Size:          f.Size,
		ParentID:      f.ParentID,
		UploadTime:    f.UploadTime,
		UploaderID:    f.UploaderID,
		Encrypted:     f.Encrypted,
		PlaintextSize: f.PlaintextSize,
	}
}

func buildFolderPath(folders map[string]projection.FolderSlim, folderID string) string {
	folderID = strings.TrimSpace(folderID)
	if folderID == projection.RootParent {
		return "My Drive"
	}
	names := make([]string, 0, 8)
	visited := make(map[string]bool)
	cur := folderID
	for cur != projection.RootParent && !visited[cur] {
		visited[cur] = true
		folder, ok := folders[cur]
		if !ok {
			break
		}
		if name := strings.TrimSpace(folder.Name); name != "" {
			names = append(names, name)
		}
		cur = strings.TrimSpace(folder.ParentID)
	}
	if len(names) == 0 {
		return "My Drive"
	}
	for i, j := 0, len(names)-1; i < j; i, j = i+1, j-1 {
		names[i], names[j] = names[j], names[i]
	}
	return "My Drive / " + strings.Join(names, " / ")
}

func buildFolderPathLazy(db *sql.DB, channelID int64, folderID string, cache map[string]projection.FolderSlim) (string, error) {
	folderID = strings.TrimSpace(folderID)
	if folderID == projection.RootParent {
		return "My Drive", nil
	}
	names := make([]string, 0, 8)
	visited := make(map[string]bool)
	cur := folderID
	for cur != projection.RootParent && !visited[cur] {
		visited[cur] = true
		folder, ok := cache[cur]
		if !ok {
			var found bool
			var err error
			folder, found, err = projection.FolderByID(db, channelID, cur)
			if err != nil {
				return "", err
			}
			if !found {
				break
			}
			cache[cur] = folder
		}
		if name := strings.TrimSpace(folder.Name); name != "" {
			names = append(names, name)
		}
		cur = strings.TrimSpace(folder.ParentID)
	}
	if len(names) == 0 {
		return "My Drive", nil
	}
	for i, j := 0, len(names)-1; i < j; i, j = i+1, j-1 {
		names[i], names[j] = names[j], names[i]
	}
	return "My Drive / " + strings.Join(names, " / "), nil
}
