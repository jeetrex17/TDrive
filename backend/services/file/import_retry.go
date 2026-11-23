package file

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"TDrive/backend/projection"
)

var (
	errImportRetrySourceChanged   = errors.New("the original file changed. Select it again to upload the current version")
	errImportRetrySourceMissing   = errors.New("the original file is missing. Restore it or select it again")
	errImportRetryNameUnconfirmed = errors.New("wait for the original destination to sync before selecting this file again")
	errImportRetryNameConflict    = errors.New("a file with this name already exists in the original destination")
)

// ImportRetryFile is a session-only retry source. Only the relative path is
// suitable for display or for Activity history.
type ImportRetryFile struct {
	Path         string `json:"path"`
	ParentID     string `json:"parentId"`
	RelativePath string `json:"relativePath"`
	Size         int64  `json:"size"`
	ModifiedAt   string `json:"modifiedAt"`
	Message      string `json:"message"`
	Code         string `json:"code"`
	SkipExisting bool   `json:"skipExisting,omitzero"`
}

// RunImportWithRetry returns the original sources whose attempts finished
// with a safely retryable failure. Extracted copies and entries the walker
// never reached have no reusable source.
func (s *Service) RunImportWithRetry(ctx context.Context, channelID int64, paths []string, parentID string, encrypt, extractArchives bool, fileConflict string) ([]ImportRetryFile, error) {
	if fileConflict != "" && fileConflict != importConflictSkip && fileConflict != importConflictKeepBoth {
		return nil, fmt.Errorf("import: choose Skip or Keep Both for matching filenames")
	}
	var retryFiles []ImportRetryFile
	err := s.runImport(ctx, channelID, paths, parentID, encrypt, extractArchives, fileConflict, &retryFiles)
	return retryFiles, err
}

func (s *Service) uploadImportWindow(ctx context.Context, channelID int64, batch *importBatch, encrypt bool, options uploadOptions, retryFiles *[]ImportRetryFile) error {
	observer := newFailureUploadObserver(ctx, options.observer)
	options.observer = observer
	_, err := s.upload(ctx, channelID, batch.paths, batch.parents, encrypt, options)
	unconfirmed := make(map[importSiblingName]struct{})
	for i, source := range batch.retrySources {
		failure, ok := observer.failures[options.idOffset+i]
		if !ok {
			continue
		}
		if uploadOutcomeUnconfirmed(failure.Code) {
			if sibling, ok := importRetrySibling(batch.paths[i], batch.parents[i]); ok {
				unconfirmed[sibling] = struct{}{}
			}
		}
		if source.Path == "" || !failure.Retryable {
			continue
		}
		source.Message = strings.ReplaceAll(failure.Message, source.Path, failure.Name)
		source.Code = failure.Code
		*retryFiles = append(*retryFiles, source)
	}
	if len(unconfirmed) > 0 {
		*retryFiles = slices.DeleteFunc(*retryFiles, func(source ImportRetryFile) bool {
			return importRetryWaitsForSync(source, unconfirmed)
		})
	}
	return err
}

// RetryImportedFiles uses the existing upload windows and resolved parent IDs
// without recreating folders or touching files already sent by the import.
func (s *Service) RetryImportedFiles(ctx context.Context, channelID int64, sources []ImportRetryFile, encrypt bool) ([]Metadata, []UploadFailure, error) {
	if len(sources) > maxImportItems {
		return nil, nil, fmt.Errorf("%w: keep the batch under %d files", errImportItemLimit, maxImportItems)
	}
	cancelWindow, closeWindow := s.openUploadWindow(0, len(sources))
	defer closeWindow()
	var files []Metadata
	var failures []UploadFailure
	var firstErr error
	for start := 0; start < len(sources); {
		end := retryImportWindowEnd(sources, start)
		window := sources[start:end]
		paths := make([]string, len(window))
		parents := make([]string, len(window))
		for i, source := range window {
			paths[i], parents[i] = source.Path, source.ParentID
		}
		uploaded, failed, err := s.uploadWithFailures(ctx, channelID, paths, parents, encrypt, uploadOptions{
			idOffset: start, pausable: true, expectedImportSources: window,
			cancelWindow: cancelWindow, prioritizeFatalImportError: true,
		})
		files = append(files, uploaded...)
		failures = append(failures, failed...)
		if isFatalImportError(err) {
			failures = s.finishDeferredImportRetry(ctx, cancelWindow, sources, end, failures, err)
			return files, failures, err
		}
		if firstErr == nil {
			firstErr = err
		}
		start = end
	}
	return files, failures, firstErr
}

// Files whose original policy was Skip wait for any earlier matching name
// to settle, so this window can check a projected receipt before sending.
func retryImportWindowEnd(sources []ImportRetryFile, start int) int {
	pending := make(map[importSiblingName]struct{})
	end := min(len(sources), start+importUploadBatchSize)
	for i := start; i < end; i++ {
		source := sources[i]
		sibling, ok := importRetrySibling(source.Path, source.ParentID)
		if !ok {
			continue
		}
		if _, exists := pending[sibling]; exists && source.SkipExisting {
			return i
		}
		pending[sibling] = struct{}{}
	}
	return end
}

func (s *Service) finishDeferredImportRetry(ctx context.Context, window *uploadWindow, sources []ImportRetryFile, start int, failures []UploadFailure, err error) []UploadFailure {
	unconfirmed := make(map[importSiblingName]struct{})
	for _, failure := range failures {
		if uploadOutcomeUnconfirmed(failure.Code) {
			source := sources[failure.Index]
			if sibling, ok := importRetrySibling(source.Path, source.ParentID); ok {
				unconfirmed[sibling] = struct{}{}
			}
		}
	}
	for i, failure := range failures {
		if failure.Retryable && importRetryWaitsForSync(sources[failure.Index], unconfirmed) {
			failures[i] = classifyUploadFailure(ctx, failure.Index, failure.Name, errImportRetryNameUnconfirmed)
		}
	}
	s.uploadCancelMu.Lock()
	defer s.uploadCancelMu.Unlock()
	for i := start; i < len(sources); i++ {
		source := sources[i]
		name := filepath.Base(source.Path)
		switch {
		case ctx.Err() != nil || window.skipped[i]:
			failures = append(failures, classifyUploadFailure(ctx, i, name, errUploadCanceled))
		case importRetryWaitsForSync(source, unconfirmed):
			failures = append(failures, classifyUploadFailure(ctx, i, name, errImportRetryNameUnconfirmed))
		default:
			failures = append(failures, notSentFailure(i, name, err))
		}
	}
	return failures
}

func importRetryWaitsForSync(source ImportRetryFile, unconfirmed map[importSiblingName]struct{}) bool {
	if !source.SkipExisting || len(unconfirmed) == 0 {
		return false
	}
	sibling, ok := importRetrySibling(source.Path, source.ParentID)
	_, pending := unconfirmed[sibling]
	return ok && pending
}

func importRetrySibling(path, parentID string) (importSiblingName, bool) {
	key, err := projection.CanonicalNameKey(filepath.Base(path))
	return importSiblingName{parentID: parentID, key: key}, err == nil
}

func uploadOutcomeUnconfirmed(code string) bool {
	return code == "outcome_unknown" || code == "projection_pending"
}

func importSourceModifiedAt(info os.FileInfo) string {
	return info.ModTime().UTC().Format(time.RFC3339Nano)
}

func (source *ImportRetryFile) matches(info os.FileInfo) bool {
	return info.Mode().IsRegular() && info.Size() == source.Size && importSourceModifiedAt(info) == source.ModifiedAt
}

func (s *Service) checkImportRetryName(channelID int64, source *ImportRetryFile, name string) error {
	if !source.SkipExisting {
		return nil
	}
	sibling, found, err := projection.LiveDirentByName(s.DB, channelID, source.ParentID, name)
	if errors.Is(err, projection.ErrInvalidPortableName) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("check original destination: %w", err)
	}
	if found && sibling.ObjectKind == "file" {
		return errImportRetryNameConflict
	}
	return nil
}
