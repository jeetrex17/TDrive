package file

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"path/filepath"
	"strings"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
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
	maxImportItems                       = 10_000
	maxImportArchiveScanEntries          = 100_000
	importUploadBatchSize                = 128
	maxImportErrorDetails                = 20
)

var (
	errImportItemLimit   = errors.New("import exceeds the remote item limit")
	errArchiveScanLimit = errors.New("archive has too many entries to inspect safely")
)

// ImportPlan is the summary shown in the import dialog before confirming.
type ImportPlan struct {
	Files    int   `json:"files"`    // files that would upload (excludes oversize)
	Folders  int   `json:"folders"`  // folders that would be created
	Bytes    int64 `json:"bytes"`    // total plaintext bytes of the uploadable files
	Oversize int   `json:"oversize"` // files skipped for exceeding the per-file limit
	Archives int   `json:"archives"` // archive items in the selection
	Ignored  int   `json:"ignored"`  // generated/cache roots pruned from the selection
	MaxBytes int64 `json:"maxBytes"` // active per-file limit, for messaging
	MaxItems int   `json:"maxItems"` // maximum files + folders in one import
	// LimitExceeded is set after counting one item beyond MaxItems. Planning
	// stops there so an accidental huge tree cannot consume unbounded memory.
	LimitExceeded bool     `json:"limitExceeded"`
	ErrorCount    int      `json:"errorCount"`
	Errors        []string `json:"errors"` // items that could not be scanned
}

type archiveExtractStats struct {
	files    int
	bytes    int64
	oversize int
	ignored  int
}

func archiveDuplicateRoot(entries []ArchiveEntry, topName string) string {
	topName = strings.TrimSpace(topName)
	if topName == "" {
		return ""
	}
	seenUploadable := false
	root := ""
	for _, e := range entries {
		if e.IsDir || isIgnoredArchivePath(e.RelPath, e.IsDir) {
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
	return s.planImport(context.Background(), paths, encrypt, extractArchives)
}

func (s *Service) planImport(ctx context.Context, paths []string, encrypt, extractArchives bool) ImportPlan {
	plan := ImportPlan{MaxBytes: s.largeFileMaxBytes(), MaxItems: maxImportItems}
	for _, p := range paths {
		if err := ctx.Err(); err != nil {
			plan.addError("import canceled", err)
			break
		}
		info, err := os.Stat(p)
		if err != nil {
			plan.addError(filepath.Base(p), err)
			continue
		}
		var planErr error
		switch {
		case info.IsDir():
			planErr = plan.addFolder() // explicit top-level choices are preserved
			if planErr == nil {
				planErr = s.planFolder(ctx, p, encrypt, &plan)
			}
		case IsArchive(p) && extractArchives:
			plan.Archives++
			planErr = s.planArchive(ctx, p, encrypt, &plan)
		default:
			if IsArchive(p) {
				plan.Archives++
			}
			planErr = s.planFile(info.Size(), encrypt, &plan)
		}
		if errors.Is(planErr, errImportItemLimit) {
			break
		}
	}
	return plan
}

func (p *ImportPlan) addError(name string, err error) {
	p.ErrorCount++
	if len(p.Errors) < maxImportErrorDetails {
		p.Errors = append(p.Errors, fmt.Sprintf("%s: %v", name, err))
	}
}

func (p *ImportPlan) checkItemLimit() error {
	if p.Files+p.Folders <= p.MaxItems {
		return nil
	}
	p.LimitExceeded = true
	return errImportItemLimit
}

func (p *ImportPlan) addFolder() error {
	p.Folders++
	return p.checkItemLimit()
}

func (s *Service) planFile(size int64, encrypt bool, plan *ImportPlan) error {
	if uploadByteSize(size, encrypt) > s.largeFileMaxBytes() {
		plan.Oversize++
		return nil
	}
	plan.Files++
	plan.Bytes += size
	return plan.checkItemLimit()
}

func (s *Service) planFolder(ctx context.Context, root string, encrypt bool, plan *ImportPlan) error {
	err := walkImportDescendants(ctx, root, func(_ string, _ string, d os.DirEntry) (bool, error) {
		if d.Type()&os.ModeSymlink != 0 {
			return true, nil
		}
		if isIgnoredImportName(d.Name(), d.IsDir()) {
			plan.Ignored++
			return d.IsDir(), nil
		}
		if d.IsDir() {
			return false, plan.addFolder()
		}
		info, err := d.Info()
		if err != nil {
			plan.addError(d.Name(), err)
			return false, nil
		}
		return false, s.planFile(info.Size(), encrypt, plan)
	}, func(p string, err error) error {
		plan.addError(filepath.Base(p), err)
		return nil
	})
	if errors.Is(err, errImportItemLimit) {
		return err
	}
	return nil
}

func (s *Service) planArchive(ctx context.Context, p string, encrypt bool, plan *ImportPlan) error {
	entries, ignored, err := scanArchiveForImport(ctx, p)
	plan.Ignored += ignored
	if err != nil {
		if errors.Is(err, errImportItemLimit) {
			plan.LimitExceeded = true
			return err
		}
		// Corrupt or unreadable: RunImport falls back to uploading it as a file.
		plan.addError(filepath.Base(p), fmt.Errorf("cannot read archive (%v), will upload as a file", err))
		if info, statErr := os.Stat(p); statErr == nil {
			return s.planFile(info.Size(), encrypt, plan)
		}
		return nil
	}
	if err := plan.addFolder(); err != nil { // the archive-named folder
		return err
	}
	stripRoot := archiveDuplicateRoot(entries, archiveFolderName(p))
	// Count folders the way import actually creates them: from the parent dirs
	// of real files. Empty directory entries are not recreated (extraction
	// streams files only), so they are intentionally not counted here.
	dirs := map[string]struct{}{}
	for _, e := range entries {
		rel := stripArchiveRoot(e.RelPath, stripRoot)
		if rel == "" {
			continue
		}
		if err := s.planFile(e.Size, encrypt, plan); err != nil {
			return err
		}
		for d := path.Dir(rel); d != "."; d = path.Dir(d) {
			if _, exists := dirs[d]; exists {
				continue
			}
			dirs[d] = struct{}{}
			if err := plan.addFolder(); err != nil {
				return err
			}
		}
	}
	return nil
}

// scanArchiveForImport streams archive metadata into a bounded, policy-filtered
// slice. Tar scans stop at the admission limit; zip still requires Go's central
// directory index, but TDrive no longer duplicates an unbounded list in memory.
func scanArchiveForImport(ctx context.Context, archivePath string) ([]ArchiveEntry, int, error) {
	return scanArchiveForImportLimit(ctx, archivePath, maxImportArchiveScanEntries)
}

func scanArchiveForImportLimit(ctx context.Context, archivePath string, maxScanned int) ([]ArchiveEntry, int, error) {
	if ctx == nil {
		return nil, 0, fmt.Errorf("archive scan requires a context")
	}
	if maxScanned <= 0 {
		return nil, 0, fmt.Errorf("archive scan limit must be positive")
	}
	entries := make([]ArchiveEntry, 0, min(maxImportItems, 256))
	ignoredRoots := make(map[string]struct{})
	seen := 0
	err := forEachArchiveEntry(archivePath, func(entry ArchiveEntry) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		seen++
		if seen > maxScanned {
			return errArchiveScanLimit
		}
		if root := ignoredArchiveRoot(entry.RelPath, entry.IsDir); root != "" {
			if len(ignoredRoots) < maxImportItems {
				ignoredRoots[root] = struct{}{}
			}
			return nil
		}
		if entry.IsDir {
			return nil
		}
		if len(entries) >= maxImportItems {
			return errImportItemLimit
		}
		entries = append(entries, entry)
		return nil
	})
	return entries, len(ignoredRoots), err
}

type importBatch struct {
	paths   []string
	parents []string
}

func newImportBatch() *importBatch {
	return &importBatch{
		paths:   make([]string, 0, importUploadBatchSize),
		parents: make([]string, 0, importUploadBatchSize),
	}
}

func (b *importBatch) add(srcPath, parentID string) bool {
	b.paths = append(b.paths, srcPath)
	b.parents = append(b.parents, parentID)
	return len(b.paths) == importUploadBatchSize
}

func (b *importBatch) len() int {
	return len(b.paths)
}

func (b *importBatch) reset() {
	clear(b.paths)
	clear(b.parents)
	b.paths = b.paths[:0]
	b.parents = b.parents[:0]
}

type importBatchUploader func(paths, parents []string, idOffset int) error

// importTasks retains only one bounded upload window plus bounded diagnostics.
// The synchronous uploader provides natural backpressure to the filesystem
// walk; it never schedules the next window while this one is active.
type importTasks struct {
	batch        *importBatch
	upload       importBatchUploader
	nextUploadID int
	files        int
	remoteItems  int
	folders      int
	oversize     int
	ignored      int
	errorCount   int
	errors       []string
	warnf        WarnFunc
}

func (t *importTasks) reserveRemoteItem() error {
	if t.remoteItems >= maxImportItems {
		return errImportItemLimit
	}
	t.remoteItems++
	return nil
}

func (t *importTasks) add(srcPath, parentID string) error {
	if err := t.reserveRemoteItem(); err != nil {
		return err
	}
	t.files++
	if t.batch.add(srcPath, parentID) {
		return t.flush()
	}
	return nil
}

func (t *importTasks) flush() error {
	if t.batch.len() == 0 {
		return nil
	}
	batchSize := t.batch.len()
	err := t.upload(t.batch.paths, t.batch.parents, t.nextUploadID)
	t.nextUploadID += batchSize
	t.batch.reset()
	if err != nil && t.warnf != nil {
		t.warnf("import: upload window failed: %v\n", err)
	}
	if isFatalImportError(err) {
		return err
	}
	if err != nil {
		t.addError("upload window", err)
	}
	return nil
}

func isFatalImportError(err error) bool {
	return errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, errImportItemLimit) ||
		errors.Is(err, errUploadProjection) ||
		errors.Is(err, tgclient.ErrFloodWait) ||
		errors.Is(err, tgclient.ErrSendOutcomeUnknown)
}

func (t *importTasks) addError(name string, err error) {
	t.errorCount++
	if len(t.errors) < maxImportErrorDetails {
		t.errors = append(t.errors, fmt.Sprintf("%s: %v", name, err))
	}
}

// RunImport recreates the folder structure of the selected paths under parentID
// and uploads their files. Top-level folder names that collide with an existing
// folder are suffixed (Name (2)); archives chosen for extraction are unpacked to
// a temp dir and imported like folders. Progress flows through import_start, the
// aggregate import events, and import_complete.
func (s *Service) RunImport(ctx context.Context, channelID int64, paths []string, parentID string, encrypt, extractArchives bool) error {
	if ctx == nil {
		return fmt.Errorf("import: context is required")
	}
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
	plan := s.planImport(ctx, paths, encrypt, extractArchives)
	if err := ctx.Err(); err != nil {
		return err
	}
	if plan.LimitExceeded {
		return fmt.Errorf("%w: keep the selection under %d files and folders", errImportItemLimit, plan.MaxItems)
	}
	peer, err := s.Peers.ResolvePeer(ctx, channelID)
	if err != nil {
		return err
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
	s.emitEvent("import_uploading", map[string]any{"files": plan.Files, "folders": plan.Folders})
	observer := newImportUploadObserver(s, plan.Files)
	observer.EmitInitial()
	tasks := &importTasks{
		batch: newImportBatch(),
		warnf: s.warnf,
	}
	tasks.upload = func(batchPaths, batchParents []string, idOffset int) error {
		_, err := s.upload(ctx, channelID, batchPaths, batchParents, encrypt, uploadOptions{
			observer: observer,
			peer:     &peer,
			idOffset: idOffset,
		})
		return err
	}
	var fatalErr error
	for _, p := range paths {
		if err := ctx.Err(); err != nil {
			fatalErr = err
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
				if isFatalImportError(err) {
					fatalErr = err
					break
				}
				tasks.addError(filepath.Base(p), err)
			}
		case IsArchive(p) && extractArchives:
			s.emitEvent("import_progress", map[string]any{"label": "Extracting " + filepath.Base(p)})
			root, err := ensureTmp()
			if err != nil {
				return fmt.Errorf("create temp dir: %w", err)
			}
			dir, stats, err := s.extractArchiveToTemp(ctx, p, root, encrypt)
			tasks.oversize += stats.oversize
			tasks.ignored += stats.ignored
			if err != nil {
				if isFatalImportError(err) {
					fatalErr = err
					break
				}
				tasks.addError(filepath.Base(p), fmt.Errorf("extract failed, uploading as a file: %w", err))
				if err := s.addFileTask(p, info.Size(), parent, encrypt, tasks); err != nil {
					fatalErr = err
				}
				continue
			}
			if err := s.importTree(ctx, channelID, dir, archiveFolderName(p), parent, encrypt, tasks); err != nil {
				if isFatalImportError(err) {
					fatalErr = err
					break
				}
				tasks.addError(filepath.Base(p), err)
			}
		default:
			if err := s.addFileTask(p, info.Size(), parent, encrypt, tasks); err != nil {
				fatalErr = err
			}
		}
		if fatalErr != nil {
			break
		}
	}
	if fatalErr == nil {
		if err := tasks.flush(); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				fatalErr = ctxErr
			} else {
				fatalErr = err
			}
		}
	}
	uploaded, observerFailed, uploadReasons := observer.Summary()
	failed := max(plan.Files-uploaded, observerFailed)
	errorsOut := append([]string(nil), tasks.errors...)
	for _, reason := range uploadReasons {
		if len(errorsOut) >= maxImportErrorDetails {
			break
		}
		errorsOut = append(errorsOut, reason)
	}
	s.emitEvent("import_complete", map[string]any{
		"uploaded":   uploaded,
		"failed":     failed,
		"folders":    tasks.folders,
		"oversize":   tasks.oversize,
		"ignored":    tasks.ignored,
		"errorCount": tasks.errorCount,
		"errors":     errorsOut,
	})
	if fatalErr != nil {
		return fatalErr
	}
	return nil
}

// addFileTask queues a single file for upload, skipping (and counting) it if it
// exceeds the per-file limit.
func (s *Service) addFileTask(srcPath string, size int64, parentID string, encrypt bool, tasks *importTasks) error {
	if uploadByteSize(size, encrypt) > s.largeFileMaxBytes() {
		tasks.oversize++
		return nil
	}
	return tasks.add(srcPath, parentID)
}

// importTree creates a top folder (collision-suffixed) under parentID, mirrors
// the directory structure beneath root, and queues every file for upload. The
// depth-first walk always creates a directory before visiting its children.
func (s *Service) importTree(ctx context.Context, channelID int64, root, topName, parentID string, encrypt bool, tasks *importTasks) error {
	freeName, err := projection.NextFreeFolderName(s.DB, channelID, parentID, topName)
	if err != nil {
		return err
	}
	if err := tasks.reserveRemoteItem(); err != nil {
		return err
	}
	topID, err := s.CreateFolder(ctx, channelID, freeName, parentID)
	if err != nil {
		return err
	}
	tasks.folders++

	// Maps a cleaned forward-slash relative directory to its created folder ID.
	dirIDs := map[string]string{".": topID}

	return walkImportDescendants(ctx, root, func(p, rel string, d os.DirEntry) (bool, error) {
		if d.Type()&os.ModeSymlink != 0 {
			return true, nil
		}
		if isIgnoredImportName(d.Name(), d.IsDir()) {
			tasks.ignored++
			return d.IsDir(), nil
		}
		parentID := dirIDs[path.Dir(rel)]
		if parentID == "" {
			// A parent we failed to create earlier; skip this dangling entry.
			return d.IsDir(), nil
		}

		if d.IsDir() {
			if err := tasks.reserveRemoteItem(); err != nil {
				return false, err
			}
			id, err := s.CreateFolder(ctx, channelID, d.Name(), parentID)
			if err != nil {
				tasks.addError(rel, err)
				return true, nil
			}
			dirIDs[rel] = id
			tasks.folders++
			return false, nil
		}

		info, err := d.Info()
		if err != nil {
			tasks.addError(rel, err)
			return false, nil
		}
		return false, s.addFileTask(p, info.Size(), parentID, encrypt, tasks)
	}, func(p string, err error) error {
		tasks.addError(filepath.Base(p), err)
		return nil
	})
}

// extractArchiveToTemp unpacks an archive's safe regular-file entries into a new
// temp directory under tmpRoot, preserving relative structure, and returns the
// directory. StreamArchiveFiles already drops unsafe (zip-slip) and symlink
// entries; the prefix check below is defense in depth.
func (s *Service) extractArchiveToTemp(ctx context.Context, archivePath, tmpRoot string, encrypt bool) (string, archiveExtractStats, error) {
	if err := ctx.Err(); err != nil {
		return "", archiveExtractStats{}, err
	}
	dir, err := os.MkdirTemp(tmpRoot, "arc-")
	if err != nil {
		return "", archiveExtractStats{}, err
	}
	entries, ignored, err := scanArchiveForImport(ctx, archivePath)
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
	stats := archiveExtractStats{ignored: ignored}
	err = StreamArchiveFiles(archivePath, func(e ArchiveEntry, r io.Reader) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if root := ignoredArchiveRoot(e.RelPath, e.IsDir); root != "" {
			return nil
		}
		rel := stripArchiveRoot(e.RelPath, stripRoot)
		if rel == "" || isIgnoredArchivePath(rel, e.IsDir) {
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
		written, copyErr := io.Copy(f, io.LimitReader(&contextReader{ctx: ctx, source: r}, entryReadLimit))
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
