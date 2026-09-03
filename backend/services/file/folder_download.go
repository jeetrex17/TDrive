package file

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

const (
	maxConcurrentFolderFiles = 2
	// Six attempts with 25ms doubling (~775ms total) outlive Windows
	// Defender/indexer holds on freshly written files while staying fast
	// on POSIX. Callers report any retained staging path.
	folderStagingCleanupAttempts    = 6
	folderStagingCleanupBaseBackoff = 25 * time.Millisecond
)

type ChooseDirectoryFunc func(defaultName string) (string, error)

type folderDownloadFile struct {
	source       projection.DownloadFile
	relativePath string
}

type folderDownloadPlan struct {
	directories []string
	files       []folderDownloadFile
}

// DownloadFolder restores one revision-pinned folder tree beneath the selected
// destination parent. Work stays in a private sibling staging directory and is
// atomically published only after every worker succeeds.
func (s *Service) DownloadFolder(
	ctx context.Context,
	channelID int64,
	folderID string,
	chooseDirectory ChooseDirectoryFunc,
) (result DownloadResult) {
	if err := s.ready(); err != nil {
		return DownloadResult{Status: "error", Message: err.Error()}
	}
	if ctx == nil {
		return DownloadResult{Status: "error", Message: "Download failed: context is nil"}
	}
	if channelID <= 0 {
		return DownloadResult{Status: "error", Message: "Drive ID not found"}
	}
	if chooseDirectory == nil {
		return DownloadResult{Status: "error", Message: "Failed to choose download location: folder dialog not ready"}
	}

	manifest, err := projection.BuildFolderDownloadManifestContext(ctx, s.DB, channelID, folderID)
	if err != nil {
		return folderDownloadFailure(ctx, fmt.Errorf("Could not prepare folder download: %w", err))
	}
	plan, err := buildFolderDownloadPlan(ctx, manifest)
	if err != nil {
		return folderDownloadFailure(ctx, err)
	}

	var masterKey []byte
	if manifestContainsEncryptedFiles(manifest) {
		masterKey, err = s.requireEncryptionKey(true)
		defer clearOwnedKey(masterKey)
		if err != nil {
			return DownloadResult{Status: "error", Message: err.Error()}
		}
	}

	var peer tgclient.InputPeer
	if len(plan.files) > 0 {
		if s.TG == nil {
			return DownloadResult{Status: "error", Message: "Connection error: tg client not ready"}
		}
		if s.Peers == nil {
			return DownloadResult{Status: "error", Message: "Connection error: peer resolver not ready"}
		}
		peer, err = s.Peers.ResolvePeer(ctx, channelID)
		if err != nil {
			return folderDownloadFailure(ctx, fmt.Errorf("Error: %w", err))
		}
	}
	if err := ctx.Err(); err != nil {
		return folderDownloadFailure(ctx, err)
	}

	destinationParent, err := chooseDirectory(manifest.Root.Name)
	if err != nil {
		return DownloadResult{Status: "error", Message: "Failed to choose download location: " + err.Error()}
	}
	if destinationParent == "" {
		return DownloadResult{Status: "canceled", Message: "Folder download canceled"}
	}
	if err := ctx.Err(); err != nil {
		return folderDownloadFailure(ctx, err)
	}
	if err := validateDestinationParent(destinationParent); err != nil {
		return DownloadResult{Status: "error", Message: "Disk Error: " + err.Error()}
	}
	parentPath, stagingPath, err := createFolderDownloadStaging(destinationParent)
	if err != nil {
		return DownloadResult{Status: "error", Message: "Disk Error: " + err.Error()}
	}
	published := false
	defer func() {
		if published {
			return
		}
		if cleanupErr := removeFolderDownloadStaging(stagingPath, os.RemoveAll, time.Sleep); cleanupErr != nil {
			result = DownloadResult{
				Status: "error",
				Message: fmt.Sprintf(
					"Cleanup failed; partial download may remain at %s: %v",
					stagingPath,
					cleanupErr,
				),
			}
		}
	}()

	finalPath, err := joinWithinRoot(parentPath, manifest.Root.Name)
	if err != nil {
		return DownloadResult{Status: "error", Message: "Disk Error: " + err.Error()}
	}
	if _, err := os.Lstat(finalPath); err == nil {
		return DownloadResult{Status: "error", Message: fmt.Sprintf("Destination already exists: %s", finalPath)}
	} else if !os.IsNotExist(err) {
		return DownloadResult{Status: "error", Message: "Disk Error: " + err.Error()}
	}

	for _, relativePath := range plan.directories {
		if err := ctx.Err(); err != nil {
			return folderDownloadFailure(ctx, err)
		}
		directoryPath, err := joinWithinRoot(stagingPath, relativePath)
		if err != nil {
			return DownloadResult{Status: "error", Message: "Disk Error: " + err.Error()}
		}
		if err := os.MkdirAll(directoryPath, 0o755); err != nil {
			return DownloadResult{Status: "error", Message: "Disk Error: " + err.Error()}
		}
	}

	progress := newFolderDownloadProgress(
		s, folderID, manifest.Root.Name, plan.files,
		manifest.TotalStoredBytes, manifest.TotalOutputBytes,
	)
	progress.emitInitial()
	if err := s.downloadFolderFiles(ctx, peer, stagingPath, plan.files, masterKey, progress); err != nil {
		return folderDownloadFailure(ctx, err)
	}
	if err := ctx.Err(); err != nil {
		return folderDownloadFailure(ctx, err)
	}
	if err := publishFolderDownload(stagingPath, finalPath); err != nil {
		if os.IsExist(err) {
			return DownloadResult{Status: "error", Message: fmt.Sprintf("Destination already exists: %s", finalPath)}
		}
		return DownloadResult{Status: "error", Message: "Disk Error: " + err.Error()}
	}
	published = true
	progress.emitTerminal()
	return DownloadResult{Status: "success", Message: "Folder download complete", SavedPath: finalPath}
}

func (s *Service) downloadFolderFiles(
	ctx context.Context,
	peer tgclient.InputPeer,
	stagingRoot string,
	files []folderDownloadFile,
	masterKey []byte,
	progress *folderDownloadProgress,
) error {
	if len(files) == 0 {
		return ctx.Err()
	}

	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	workerCount := min(maxConcurrentFolderFiles, len(files))
	jobs := make(chan int)
	var workers sync.WaitGroup
	var firstErr error
	var firstErrOnce sync.Once

	recordError := func(err error) {
		firstErrOnce.Do(func() {
			firstErr = err
			cancel()
		})
	}
	for range workerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				file := files[index]
				destination, err := joinWithinRoot(stagingRoot, file.relativePath)
				if err != nil {
					recordError(err)
					return
				}
				err = s.downloadProjectedFileToPath(
					workerCtx, peer, file.source, destination, masterKey,
					func(done, _ int64) { progress.report(index, file.relativePath, done) },
				)
				if err != nil {
					recordError(fmt.Errorf("Couldn't download %q: %w", filepath.ToSlash(file.relativePath), err))
					return
				}
				progress.complete(index, file.relativePath)
			}
		}()
	}

sendLoop:
	for index := range files {
		select {
		case jobs <- index:
		case <-workerCtx.Done():
			break sendLoop
		}
	}
	close(jobs)
	workers.Wait()
	if firstErr != nil {
		return firstErr
	}
	return ctx.Err()
}

func buildFolderDownloadPlan(ctx context.Context, manifest projection.FolderDownloadManifest) (folderDownloadPlan, error) {
	if err := ctx.Err(); err != nil {
		return folderDownloadPlan{}, err
	}
	if len(manifest.Folders) == 0 || manifest.Root.ID == "" || manifest.Folders[0].ID != manifest.Root.ID {
		return folderDownloadPlan{}, fmt.Errorf("Could not prepare folder download: invalid root")
	}

	directoryPaths := make(map[string]string, len(manifest.Folders))
	directoryPaths[manifest.Root.ID] = ""
	plan := folderDownloadPlan{
		directories: make([]string, 0, len(manifest.Folders)-1),
		files:       make([]folderDownloadFile, 0, len(manifest.Files)),
	}
	seenPaths := make(map[string]struct{}, len(manifest.Folders)+len(manifest.Files))

	for _, folder := range manifest.Folders[1:] {
		if err := ctx.Err(); err != nil {
			return folderDownloadPlan{}, err
		}
		parentPath, found := directoryPaths[folder.ParentID]
		if !found {
			return folderDownloadPlan{}, fmt.Errorf("Could not prepare folder download: folder %q has no parent", folder.Name)
		}
		relativePath := filepath.Join(parentPath, folder.Name)
		if err := validateRelativeDownloadPath(relativePath); err != nil {
			return folderDownloadPlan{}, err
		}
		pathKey, err := canonicalDownloadPathKey(relativePath)
		if err != nil {
			return folderDownloadPlan{}, err
		}
		if _, exists := seenPaths[pathKey]; exists {
			return folderDownloadPlan{}, fmt.Errorf("Could not prepare folder download: duplicate path %q", relativePath)
		}
		seenPaths[pathKey] = struct{}{}
		directoryPaths[folder.ID] = relativePath
		plan.directories = append(plan.directories, relativePath)
	}

	for _, file := range manifest.Files {
		if err := ctx.Err(); err != nil {
			return folderDownloadPlan{}, err
		}
		parentPath, found := directoryPaths[file.ParentID]
		if !found {
			return folderDownloadPlan{}, fmt.Errorf("Could not prepare folder download: file %q has no parent", file.Name)
		}
		relativePath := filepath.Join(parentPath, file.Name)
		if err := validateRelativeDownloadPath(relativePath); err != nil {
			return folderDownloadPlan{}, err
		}
		pathKey, err := canonicalDownloadPathKey(relativePath)
		if err != nil {
			return folderDownloadPlan{}, err
		}
		if _, exists := seenPaths[pathKey]; exists {
			return folderDownloadPlan{}, fmt.Errorf("Could not prepare folder download: duplicate path %q", relativePath)
		}
		seenPaths[pathKey] = struct{}{}
		plan.files = append(plan.files, folderDownloadFile{source: file, relativePath: relativePath})
	}
	return plan, nil
}

func manifestContainsEncryptedFiles(manifest projection.FolderDownloadManifest) bool {
	for _, file := range manifest.Files {
		if file.Encrypted {
			return true
		}
	}
	return false
}

func removeFolderDownloadStaging(
	path string,
	remove func(string) error,
	sleep func(time.Duration),
) error {
	var err error
	for attempt := range folderStagingCleanupAttempts {
		if err = remove(path); err == nil || os.IsNotExist(err) {
			return nil
		}
		if attempt+1 < folderStagingCleanupAttempts {
			sleep(folderStagingCleanupBaseBackoff << attempt)
		}
	}
	return fmt.Errorf(
		"remove staging directory %q after %d attempts: %w",
		path,
		folderStagingCleanupAttempts,
		err,
	)
}

func validateDestinationParent(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("download destination is not a directory")
	}
	return nil
}

func validateRelativeDownloadPath(path string) error {
	if path == "" || filepath.IsAbs(path) {
		return fmt.Errorf("Could not prepare folder download: unsafe path %q", path)
	}
	clean := filepath.Clean(path)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("Could not prepare folder download: unsafe path %q", path)
	}
	return nil
}

func canonicalDownloadPathKey(path string) (string, error) {
	clean := filepath.Clean(path)
	parts := strings.Split(clean, string(filepath.Separator))
	keys := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		key, err := projection.CanonicalNameKey(part)
		if err != nil {
			return "", fmt.Errorf("Could not prepare folder download: unsafe path %q: %w", path, err)
		}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return "", fmt.Errorf("Could not prepare folder download: unsafe path %q", path)
	}
	return strings.Join(keys, "/"), nil
}

func joinWithinRoot(root string, relativePath string) (string, error) {
	target := filepath.Join(root, relativePath)
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return "", err
	}
	if relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes download root: %q", relativePath)
	}
	return target, nil
}

func folderDownloadFailure(ctx context.Context, err error) DownloadResult {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		(ctx != nil && (errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded))) {
		return DownloadResult{Status: "canceled", Message: "Folder download canceled"}
	}
	return DownloadResult{Status: "error", Message: err.Error()}
}

type folderDownloadProgress struct {
	service          *Service
	folderID         string
	folderName       string
	filesTotal       int
	fileSizes        []int64
	bytesTotal       int64
	outputBytesTotal int64

	mu             sync.Mutex
	reported       []int64
	completed      []bool
	filesCompleted int
	bytesCompleted int64
	lastEmit       time.Time
}

func newFolderDownloadProgress(
	service *Service,
	folderID string,
	folderName string,
	files []folderDownloadFile,
	bytesTotal int64,
	outputBytesTotal int64,
) *folderDownloadProgress {
	fileSizes := make([]int64, len(files))
	for i, file := range files {
		fileSizes[i] = file.source.StoredSize
	}
	return &folderDownloadProgress{
		service: service, folderID: folderID, folderName: folderName,
		filesTotal: len(files), fileSizes: fileSizes,
		bytesTotal: bytesTotal, outputBytesTotal: outputBytesTotal,
		reported: make([]int64, len(files)), completed: make([]bool, len(files)),
	}
}

func (p *folderDownloadProgress) emitInitial() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.emitLocked("", false, true)
}

func (p *folderDownloadProgress) report(index int, currentFile string, done int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if index < 0 || index >= len(p.reported) || p.completed[index] {
		return
	}
	if done < 0 {
		done = 0
	}
	if done > p.fileSizes[index] {
		done = p.fileSizes[index]
	}
	if done <= p.reported[index] {
		return
	}
	p.bytesCompleted += done - p.reported[index]
	p.reported[index] = done
	p.emitLocked(currentFile, false, false)
}

func (p *folderDownloadProgress) complete(index int, currentFile string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if index < 0 || index >= len(p.reported) || p.completed[index] {
		return
	}
	p.bytesCompleted += p.fileSizes[index] - p.reported[index]
	p.reported[index] = p.fileSizes[index]
	p.completed[index] = true
	p.filesCompleted++
	p.emitLocked(currentFile, false, true)
}

func (p *folderDownloadProgress) emitTerminal() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.filesCompleted = p.filesTotal
	p.bytesCompleted = p.bytesTotal
	p.emitLocked("", true, true)
}

func (p *folderDownloadProgress) emitLocked(currentFile string, terminal bool, force bool) {
	now := time.Now()
	if !force && !p.lastEmit.IsZero() && now.Sub(p.lastEmit) < 100*time.Millisecond {
		return
	}
	percent := 0.0
	switch {
	case terminal:
		percent = 100
	case p.bytesTotal > 0:
		percent = float64(p.bytesCompleted) / float64(p.bytesTotal) * 100
	case p.filesTotal > 0:
		percent = float64(p.filesCompleted) / float64(p.filesTotal) * 100
	}
	if !terminal && percent >= 100 {
		percent = 99.9
	}
	percent = min(max(percent, 0), 100)
	p.service.emitEvent("folder_download_progress", map[string]any{
		"folder_id":          p.folderID,
		"folder_name":        p.folderName,
		"current_file":       filepath.ToSlash(currentFile),
		"files_completed":    p.filesCompleted,
		"files_total":        p.filesTotal,
		"bytes_completed":    p.bytesCompleted,
		"bytes_total":        p.bytesTotal,
		"output_bytes_total": p.outputBytesTotal,
		"percent":            percent,
	})
	p.lastEmit = now
}
