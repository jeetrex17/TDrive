package file

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"path/filepath"
	"strings"

	"TDrive/backend/projection"
)

// Folder and archive import. PlanImport walks a selection to produce counts for
// the import dialog; RunImport recreates the folders and uploads the files.
//
// Archives chosen for extraction are unpacked to a temp directory and then
// imported exactly like a folder, so every file funnels through the existing
// Upload path (one batch, shared concurrency, projection, and progress events)
// rather than a second upload code path. The trade is temporary disk for an
// extracted archive's contents; it is removed when the import returns.

const (
	archiveExtractedFileLimit            = 10000
	archiveExtractedBytesLimitFloor      = 512 * 1024 * 1024
	archiveExtractedBytesLimitMultiplier = 8
)

// ImportPlan is the summary shown in the import dialog before confirming.
type ImportPlan struct {
	Files    int      `json:"files"`    // files that would upload (excludes oversize)
	Folders  int      `json:"folders"`  // folders that would be created
	Bytes    int64    `json:"bytes"`    // total plaintext bytes of the uploadable files
	Oversize int      `json:"oversize"` // files skipped for exceeding the per-file limit
	Archives int      `json:"archives"` // archive items in the selection
	MaxBytes int64    `json:"maxBytes"` // active per-file limit, for messaging
	Errors   []string `json:"errors"`   // items that could not be scanned
}

type archiveExtractStats struct {
	files    int
	bytes    int64
	oversize int
}

// isJunkName reports OS bookkeeping files that should never be uploaded.
func isJunkName(name string) bool {
	switch name {
	case ".DS_Store", "Thumbs.db", "desktop.ini", ".localized", "__MACOSX":
		return true
	}
	return strings.HasPrefix(name, "._") // macOS AppleDouble sidecars
}

func isJunkArchivePath(rel string) bool {
	for _, part := range strings.Split(path.Clean(rel), "/") {
		if isJunkName(part) || strings.EqualFold(part, "__MACOSX") {
			return true
		}
	}
	return false
}

func archiveDuplicateRoot(entries []ArchiveEntry, topName string) string {
	topName = strings.TrimSpace(topName)
	if topName == "" {
		return ""
	}
	seenUploadable := false
	root := ""
	for _, e := range entries {
		if e.IsDir || isJunkArchivePath(e.RelPath) {
			continue
		}
		first, rest, hasSlash := strings.Cut(e.RelPath, "/")
		if !hasSlash || rest == "" || !strings.EqualFold(first, topName) {
			return ""
		}
		if root == "" {
			root = first
		}
		seenUploadable = true
	}
	if !seenUploadable {
		return ""
	}
	return path.Clean(root)
}

func stripArchiveRoot(rel, root string) string {
	if root == "" {
		return rel
	}
	if rel == root {
		return ""
	}
	prefix := root + "/"
	if strings.HasPrefix(rel, prefix) {
		return strings.TrimPrefix(rel, prefix)
	}
	return rel
}

// archiveFolderName is the folder name an extracted archive imports into: its
// base name with the archive extension stripped (e.g. backup.tar.gz -> backup).
func archiveFolderName(p string) string {
	base := filepath.Base(p)
	lower := strings.ToLower(base)
	for _, ext := range []string{".tar.gz", ".tgz", ".tar", ".zip"} {
		if strings.HasSuffix(lower, ext) {
			return base[:len(base)-len(ext)]
		}
	}
	return base
}

// PlanImport scans the selected paths and returns the counts for the import
// dialog. encrypt and extractArchives must match what RunImport will be called
// with so the totals line up. It never mutates anything.
func (s *Service) PlanImport(paths []string, encrypt, extractArchives bool) ImportPlan {
	plan := ImportPlan{MaxBytes: s.largeFileMaxBytes()}
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			plan.Errors = append(plan.Errors, fmt.Sprintf("%s: %v", filepath.Base(p), err))
			continue
		}
		switch {
		case info.IsDir():
			plan.Folders++ // the top folder itself
			s.planFolder(p, encrypt, &plan)
		case IsArchive(p) && extractArchives:
			plan.Archives++
			s.planArchive(p, encrypt, &plan)
		default:
			if IsArchive(p) {
				plan.Archives++
			}
			s.planFile(info.Size(), encrypt, &plan)
		}
	}
	return plan
}

func (s *Service) planFile(size int64, encrypt bool, plan *ImportPlan) {
	if uploadByteSize(size, encrypt) > s.largeFileMaxBytes() {
		plan.Oversize++
		return
	}
	plan.Files++
	plan.Bytes += size
}

func (s *Service) planFolder(root string, encrypt bool, plan *ImportPlan) {
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			plan.Errors = append(plan.Errors, fmt.Sprintf("%s: %v", filepath.Base(p), err))
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if p == root {
			return nil // counted by the caller
		}
		if d.Type()&os.ModeSymlink != 0 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if isJunkName(d.Name()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			plan.Folders++
			return nil
		}
		info, err := d.Info()
		if err != nil {
			plan.Errors = append(plan.Errors, fmt.Sprintf("%s: %v", d.Name(), err))
			return nil
		}
		s.planFile(info.Size(), encrypt, plan)
		return nil
	})
}

func (s *Service) planArchive(p string, encrypt bool, plan *ImportPlan) {
	entries, err := ScanArchive(p)
	if err != nil {
		// Corrupt or unreadable: RunImport falls back to uploading it as a file.
		plan.Errors = append(plan.Errors, fmt.Sprintf("%s: cannot read archive (%v), will upload as a file", filepath.Base(p), err))
		if info, statErr := os.Stat(p); statErr == nil {
			s.planFile(info.Size(), encrypt, plan)
		}
		return
	}
	plan.Folders++ // the archive-named folder
	stripRoot := archiveDuplicateRoot(entries, archiveFolderName(p))
	// Count folders the way import actually creates them: from the parent dirs
	// of real files. Empty directory entries are not recreated (extraction
	// streams files only), so they are intentionally not counted here.
	dirs := map[string]struct{}{}
	for _, e := range entries {
		if e.IsDir || isJunkArchivePath(e.RelPath) {
			continue
		}
		rel := stripArchiveRoot(e.RelPath, stripRoot)
		if rel == "" || isJunkArchivePath(rel) {
			continue
		}
		s.planFile(e.Size, encrypt, plan)
		for d := path.Dir(rel); d != "."; d = path.Dir(d) {
			dirs[d] = struct{}{}
		}
	}
	plan.Folders += len(dirs)
}

// importTasks accumulates the file uploads and folder/skip counts produced while
// walking a selection.
type importTasks struct {
	paths    []string
	parents  []string
	folders  int
	oversize int
	errors   []string
}

func (t *importTasks) add(srcPath, parentID string) {
	t.paths = append(t.paths, srcPath)
	t.parents = append(t.parents, parentID)
}

func (t *importTasks) addError(name string, err error) {
	t.errors = append(t.errors, fmt.Sprintf("%s: %v", name, err))
}

// RunImport recreates the folder structure of the selected paths under parentID
// and uploads their files. Top-level folder names that collide with an existing
// folder are suffixed (Name (2)); archives chosen for extraction are unpacked to
// a temp dir and imported like folders. Progress flows through import_start, the
// per-file upload_* events, and import_complete.
func (s *Service) RunImport(ctx context.Context, channelID int64, paths []string, parentID string, encrypt, extractArchives bool) error {
	if err := s.ready(); err != nil {
		return err
	}
	if s.TG == nil || s.Peers == nil {
		return fmt.Errorf("tg client not ready")
	}
	if channelID == 0 {
		return fmt.Errorf("no active channel")
	}
	if s.CreateFolder == nil {
		return fmt.Errorf("folder creation not wired")
	}
	parent, err := s.validParent(channelID, parentID, "parent")
	if err != nil {
		return err
	}
	if encrypt {
		masterKey, err := s.masterKeyForUpload(channelID, true)
		clearOwnedKey(masterKey)
		if err != nil {
			return err
		}
		if s.WriteCiphertextTemp == nil {
			return fmt.Errorf("encryption upload not ready")
		}
	}

	// The extraction temp dir is created lazily on the first archive we actually
	// extract, so a folder-only import never touches /tmp.
	var tmpRoot string
	defer func() {
		if tmpRoot != "" {
			_ = os.RemoveAll(tmpRoot)
		}
	}()
	ensureTmp := func() (string, error) {
		if tmpRoot == "" {
			dir, err := os.MkdirTemp("", "tdrive-import-*")
			if err != nil {
				return "", err
			}
			tmpRoot = dir
		}
		return tmpRoot, nil
	}

	// Announce the import immediately so the bell shows activity through the
	// prepare, extract, and folder-creation phases, before any uploads begin.
	s.emitEvent("import_start")

	tasks := &importTasks{}
	for _, p := range paths {
		if ctx.Err() != nil {
			break
		}
		info, err := os.Stat(p)
		if err != nil {
			tasks.addError(filepath.Base(p), err)
			continue
		}
		switch {
		case info.IsDir():
			s.emitEvent("import_progress", map[string]any{"label": "Adding " + filepath.Base(p)})
			if err := s.importTree(ctx, channelID, p, filepath.Base(p), parent, encrypt, tasks); err != nil {
				tasks.addError(filepath.Base(p), err)
			}
		case IsArchive(p) && extractArchives:
			s.emitEvent("import_progress", map[string]any{"label": "Extracting " + filepath.Base(p)})
			root, err := ensureTmp()
			if err != nil {
				return fmt.Errorf("create temp dir: %w", err)
			}
			dir, stats, err := s.extractArchiveToTemp(p, root, encrypt)
			tasks.oversize += stats.oversize
			if err != nil {
				tasks.addError(filepath.Base(p), fmt.Errorf("extract failed, uploading as a file: %w", err))
				s.addFileTask(p, info.Size(), parent, encrypt, tasks)
				continue
			}
			if err := s.importTree(ctx, channelID, dir, archiveFolderName(p), parent, encrypt, tasks); err != nil {
				tasks.addError(filepath.Base(p), err)
			}
		default:
			s.addFileTask(p, info.Size(), parent, encrypt, tasks)
		}
	}

	// Switch the bell to the upload phase now that the folders exist and the
	// file list is known, so the progress denominator is the real upload count.
	s.emitEvent("import_uploading", map[string]any{"files": len(tasks.paths), "folders": tasks.folders})

	uploaded := 0
	var uploadErr error
	if len(tasks.paths) > 0 {
		metas, err := s.Upload(ctx, channelID, tasks.paths, tasks.parents, encrypt)
		uploaded = len(metas)
		uploadErr = err
	}

	s.emitEvent("import_complete", map[string]any{
		"uploaded": uploaded,
		"failed":   len(tasks.paths) - uploaded,
		"folders":  tasks.folders,
		"oversize": tasks.oversize,
		"errors":   tasks.errors,
	})
	// Per-file upload failures are already reported through import_complete (and
	// the upload_error events). Returning them here too made the frontend
	// overwrite that accurate summary with a generic "Import failed", so log and
	// swallow; only genuine pre-import errors above reject the call.
	if uploadErr != nil {
		s.warnf("import: some uploads failed: %v\n", uploadErr)
	}
	return nil
}

// addFileTask queues a single file for upload, skipping (and counting) it if it
// exceeds the per-file limit.
func (s *Service) addFileTask(srcPath string, size int64, parentID string, encrypt bool, tasks *importTasks) {
	if uploadByteSize(size, encrypt) > s.largeFileMaxBytes() {
		tasks.oversize++
		return
	}
	tasks.add(srcPath, parentID)
}

// importTree creates a top folder (collision-suffixed) under parentID, mirrors
// the directory structure beneath root, and queues every file for upload. The
// walk is lexical, so a directory is always created before its children.
func (s *Service) importTree(ctx context.Context, channelID int64, root, topName, parentID string, encrypt bool, tasks *importTasks) error {
	freeName, err := projection.NextFreeFolderName(s.DB, channelID, parentID, topName)
	if err != nil {
		return err
	}
	topID, err := s.CreateFolder(channelID, freeName, parentID)
	if err != nil {
		return err
	}
	tasks.folders++

	// Maps a cleaned forward-slash relative directory to its created folder ID.
	dirIDs := map[string]string{".": topID}

	return filepath.WalkDir(root, func(p string, d os.DirEntry, walkErr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if walkErr != nil {
			tasks.addError(filepath.Base(p), walkErr)
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if p == root {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if isJunkName(d.Name()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			tasks.addError(filepath.Base(p), err)
			return nil
		}
		rel = filepath.ToSlash(rel)
		parentID := dirIDs[path.Dir(rel)]
		if parentID == "" {
			// A parent we failed to create earlier; skip this dangling entry.
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if d.IsDir() {
			id, err := s.CreateFolder(channelID, d.Name(), parentID)
			if err != nil {
				tasks.addError(rel, err)
				return filepath.SkipDir
			}
			dirIDs[rel] = id
			tasks.folders++
			return nil
		}

		info, err := d.Info()
		if err != nil {
			tasks.addError(rel, err)
			return nil
		}
		s.addFileTask(p, info.Size(), parentID, encrypt, tasks)
		return nil
	})
}

// extractArchiveToTemp unpacks an archive's safe regular-file entries into a new
// temp directory under tmpRoot, preserving relative structure, and returns the
// directory. StreamArchiveFiles already drops unsafe (zip-slip) and symlink
// entries; the prefix check below is defense in depth.
func (s *Service) extractArchiveToTemp(archivePath, tmpRoot string, encrypt bool) (string, archiveExtractStats, error) {
	dir, err := os.MkdirTemp(tmpRoot, "arc-")
	if err != nil {
		return "", archiveExtractStats{}, err
	}
	entries, err := ScanArchive(archivePath)
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", archiveExtractStats{}, err
	}
	stripRoot := archiveDuplicateRoot(entries, archiveFolderName(archivePath))
	base := filepath.Clean(dir) + string(os.PathSeparator)
	// Cap each extracted file just past the per-file upload limit. A larger file
	// could not be uploaded anyway, and the cap stops a decompression bomb (a
	// tiny entry that expands to fill the disk) from writing more than the limit;
	// importTree then skips the oversize result.
	limit := s.largeFileMaxBytes()
	readLimit := limit
	if readLimit < math.MaxInt64 {
		readLimit++
	}
	totalLimit := s.maxArchiveExtractBytes()
	stats := archiveExtractStats{}
	err = StreamArchiveFiles(archivePath, func(e ArchiveEntry, r io.Reader) error {
		if isJunkArchivePath(e.RelPath) {
			return nil
		}
		rel := stripArchiveRoot(e.RelPath, stripRoot)
		if rel == "" || isJunkArchivePath(rel) {
			return nil
		}
		if e.Size < 0 {
			return fmt.Errorf("archive entry %q has negative size", rel)
		}
		if uploadByteSize(e.Size, encrypt) > limit {
			stats.oversize++
			return nil
		}
		if stats.files >= archiveExtractedFileLimit {
			return fmt.Errorf("archive contains more than %d uploadable files", archiveExtractedFileLimit)
		}
		remaining := totalLimit - stats.bytes
		if remaining <= 0 || e.Size > remaining {
			return fmt.Errorf("archive expands past %s", humanByteSize(totalLimit))
		}

		dest := filepath.Join(dir, filepath.FromSlash(rel))
		if !strings.HasPrefix(dest, base) {
			return fmt.Errorf("unsafe archive entry path: %s", rel)
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		f, actualDest, err := createUniqueExtractFile(dest)
		if err != nil {
			return err
		}
		remainingReadLimit := remaining
		if remainingReadLimit < math.MaxInt64 {
			remainingReadLimit++
		}
		entryReadLimit := minInt64(readLimit, remainingReadLimit)
		written, copyErr := io.Copy(f, io.LimitReader(r, entryReadLimit))
		closeErr := f.Close()
		if copyErr != nil {
			_ = os.Remove(actualDest)
			return copyErr
		}
		if closeErr != nil {
			_ = os.Remove(actualDest)
			return closeErr
		}
		if written > remaining {
			_ = os.Remove(actualDest)
			return fmt.Errorf("archive expands past %s", humanByteSize(totalLimit))
		}
		if uploadByteSize(written, encrypt) > limit {
			stats.oversize++
			_ = os.Remove(actualDest)
			return nil
		}
		stats.files++
		stats.bytes += written
		return nil
	})
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", stats, err
	}
	return dir, stats, nil
}

// createUniqueExtractFile opens dest for writing without ever overwriting. If
// the path is already taken (an exact duplicate archive entry, or a case-only
// collision on a case-insensitive filesystem), it falls back to "name (2).ext",
// "name (3).ext", and so on, so both colliding entries survive instead of one
// silently clobbering the other. Returns the file and the path actually used.
func createUniqueExtractFile(dest string) (*os.File, string, error) {
	const flags = os.O_CREATE | os.O_EXCL | os.O_WRONLY
	if f, err := os.OpenFile(dest, flags, 0o644); err == nil {
		return f, dest, nil
	} else if !os.IsExist(err) {
		return nil, "", err
	}
	dir := filepath.Dir(dest)
	ext := filepath.Ext(dest)
	stem := strings.TrimSuffix(filepath.Base(dest), ext)
	for k := 2; k < 10000; k++ {
		cand := filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, k, ext))
		f, err := os.OpenFile(cand, flags, 0o644)
		if err == nil {
			return f, cand, nil
		}
		if !os.IsExist(err) {
			return nil, "", err
		}
	}
	return nil, "", fmt.Errorf("could not find a free name for %q", filepath.Base(dest))
}

func (s *Service) maxArchiveExtractBytes() int64 {
	limit := s.largeFileMaxBytes()
	if limit > math.MaxInt64/archiveExtractedBytesLimitMultiplier {
		return math.MaxInt64
	}
	total := limit * archiveExtractedBytesLimitMultiplier
	if total < archiveExtractedBytesLimitFloor {
		return archiveExtractedBytesLimitFloor
	}
	return total
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
