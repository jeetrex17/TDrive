package file

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"TDrive/backend/projection"
)

const (
	importConflictSkip     = "skip"
	importConflictKeepBoth = "keep-both"
)

// RunImportWithFolderMerge imports new files into matching destination folders.
// A conflicting file is skipped or imported as a separate copy.
func (s *Service) RunImportWithFolderMerge(ctx context.Context, channelID int64, paths []string, parentID string, encrypt, extractArchives bool, fileConflict string) error {
	if fileConflict != importConflictSkip && fileConflict != importConflictKeepBoth {
		return fmt.Errorf("import: choose Skip or Keep Both for matching filenames")
	}
	return s.runImport(ctx, channelID, paths, parentID, encrypt, extractArchives, fileConflict, nil)
}

type importSiblingName struct {
	parentID string
	key      string
}

func (s *Service) importSibling(parentID, name string, tasks *importTasks) (projection.Dirent, bool, error) {
	key, err := projection.CanonicalNameKey(name)
	if errors.Is(err, projection.ErrInvalidPortableName) {
		// Use ordinary import naming for a source name that isn't portable.
		// A sanitized alias could refer to a different source.
		return projection.Dirent{}, false, nil
	}
	if err != nil {
		return projection.Dirent{}, false, err
	}
	if _, buffered := tasks.pendingNames[importSiblingName{parentID: parentID, key: key}]; buffered {
		if err := tasks.flush(); err != nil {
			return projection.Dirent{}, false, err
		}
	}
	return projection.LiveDirentByName(s.DB, tasks.channelID, parentID, name)
}

// addFileTask queues a file unless it is ineligible or its name is already
// occupied by a file in a folder merge that skips conflicts.
func (s *Service) addFileTask(srcPath, label string, info os.FileInfo, parentID string, withinTree, temporary bool, tasks *importTasks) error {
	stored := uploadByteSize(info.Size(), tasks.encrypt)
	if stored == 0 {
		tasks.empty++
		return nil
	}
	if stored > s.largeFileMaxBytes() {
		tasks.oversize++
		return nil
	}
	if withinTree && tasks.fileConflict == importConflictSkip {
		entry, found, err := s.importSibling(parentID, filepath.Base(srcPath), tasks)
		if err != nil {
			return fmt.Errorf("check import file %q: %w", label, err)
		}
		if found && entry.ObjectKind == "file" {
			if err := tasks.reserveRemoteItem(); err != nil {
				return err
			}
			tasks.conflictsSkipped++
			tasks.observer.MarkConflictsSkipped(tasks.conflictsSkipped)
			return nil
		}
	}
	var retrySource ImportRetryFile
	if tasks.batch.retrySources != nil && !temporary && info.Mode().IsRegular() && filepath.IsAbs(srcPath) {
		retrySource = ImportRetryFile{Path: srcPath, ParentID: parentID, RelativePath: label,
			Size: info.Size(), ModifiedAt: importSourceModifiedAt(info), SkipExisting: withinTree && tasks.fileConflict == importConflictSkip}
	}
	return tasks.add(srcPath, parentID, label, retrySource)
}

// importTree builds or reuses folders before visiting their children and queues
// each file with its resolved destination.
func (s *Service) importTree(ctx context.Context, root, topName, parentID string, temporary bool, tasks *importTasks) error {
	topID, name, err := s.importTreeFolder(ctx, parentID, topName, true, tasks)
	if err != nil {
		return err
	}
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
			// Skip descendants of a directory the import could not open.
			return d.IsDir(), nil
		}
		if d.IsDir() {
			id, _, err := s.importTreeFolder(ctx, parentID, d.Name(), false, tasks)
			if err != nil {
				if isFatalImportError(err) {
					return false, err
				}
				tasks.addError(rel, err)
				return true, nil
			}
			dirIDs[rel] = id
			return false, nil
		}
		info, err := d.Info()
		if err != nil {
			tasks.addError(rel, err)
			return false, nil
		}
		return false, s.addFileTask(p, path.Join(name, rel), info, parentID, true, temporary, tasks)
	}, func(p string, err error) error {
		tasks.addError(filepath.Base(p), err)
		return nil
	})
}

func (s *Service) importTreeFolder(ctx context.Context, parentID, name string, topLevel bool, tasks *importTasks) (string, string, error) {
	if err := tasks.reserveRemoteItem(); err != nil {
		return "", "", err
	}
	if tasks.fileConflict != "" {
		entry, found, err := s.importSibling(parentID, strings.TrimSpace(name), tasks)
		if err != nil {
			return "", "", fmt.Errorf("check import folder %q: %w", name, err)
		}
		if found {
			if entry.ObjectKind != "folder" {
				return "", "", fmt.Errorf("a file named %q already exists here. Rename the folder or import a separate copy", name)
			}
			return entry.ObjectID, entry.DisplayName, nil
		}
	}
	if topLevel {
		freeName, err := projection.NextFreeFolderName(s.DB, tasks.channelID, parentID, name)
		if err != nil {
			return "", "", err
		}
		name = freeName
	}
	id, err := s.CreateFolder(ctx, tasks.channelID, name, parentID)
	if err != nil {
		return "", "", err
	}
	tasks.folders++
	return id, name, nil
}
