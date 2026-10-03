package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"TDrive/backend/datadir"
	fileservice "TDrive/backend/services/file"

	"github.com/wailsapp/wails/v3/pkg/application"
)

const uploadSourceDir = "resumable-upload-sources"

func durableUploadSourceRoot() (string, error) {
	base, err := datadir.Dir()
	if err != nil {
		return "", err
	}
	root := filepath.Join(base, uploadSourceDir)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	if err := excludeUploadSourceFromBackup(root); err != nil {
		return "", err
	}
	return root, nil
}

func (a *App) stageMobileUploadPaths(ctx context.Context, paths []string, encrypt bool) ([]string, []string, error) {
	if encrypt || !application.System.IsMobile() {
		return paths, nil, nil
	}
	selected := make([]string, len(paths))
	copy(selected, paths)
	staged := make([]string, 0)
	resolved := make(map[string]string)
	for i, path := range paths {
		if prior, ok := resolved[path]; ok {
			selected[i] = prior
			continue
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			a.removeUploadSourceCopies(staged)
			if err != nil {
				return nil, nil, fmt.Errorf("inspect upload source: %w", err)
			}
			return nil, nil, fmt.Errorf("upload source is not a regular file")
		}
		if info.Size() <= fileservice.MinResumableUploadBytes {
			continue
		}
		a.transferMu.Lock()
		_, pickerCopy := a.pickerSources[path]
		delete(a.pickerSources, path)
		a.transferMu.Unlock()
		stage, err := stageMobileUploadSource(ctx, path, info, pickerCopy)
		if err != nil {
			a.removeUploadSourceCopies(staged)
			return nil, nil, err
		}
		selected[i] = stage
		resolved[path] = stage
		staged = append(staged, stage)
	}
	return selected, staged, nil
}

func stageMobileUploadSource(ctx context.Context, source string, before os.FileInfo, move bool) (string, error) {
	if !before.Mode().IsRegular() || before.Size() < 0 {
		return "", fmt.Errorf("upload source is not a regular file")
	}
	name := filepath.Base(source)
	if name == "." || name == ".." || name == string(filepath.Separator) {
		return "", fmt.Errorf("upload source has no filename")
	}
	root, err := durableUploadSourceRoot()
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(root, "source-")
	if err != nil {
		return "", err
	}
	destination := filepath.Join(dir, name)
	moved := false
	defer func() {
		if err != nil {
			if moved {
				if restoreErr := os.Rename(destination, source); restoreErr != nil {
					slog.Warn("restore picker source after staging failure", "error", restoreErr)
				}
			} else {
				_ = os.Remove(destination)
			}
			_ = os.Remove(dir)
		}
	}()
	if err = excludeUploadSourceFromBackup(dir); err != nil {
		return "", err
	}
	if move {
		if err = excludeUploadSourceFromBackup(source); err != nil {
			return "", err
		}
		if err = os.Rename(source, destination); err == nil {
			moved = true
			if err = excludeUploadSourceFromBackup(destination); err != nil {
				return "", err
			}
		}
	}
	if !move || err != nil {
		err = copyUploadSource(ctx, source, destination, before)
		if err != nil {
			return "", err
		}
	}
	if err = os.Chmod(destination, 0o600); err != nil {
		return "", err
	}
	if err = excludeUploadSourceFromBackup(destination); err != nil {
		return "", err
	}
	if err = syncUploadSourceFile(destination); err != nil {
		return "", err
	}
	var after os.FileInfo
	after, err = os.Lstat(destination)
	if err != nil {
		return "", err
	}
	if !after.Mode().IsRegular() || after.Size() != before.Size() {
		err = fmt.Errorf("upload source changed while staging")
		return "", err
	}
	if err = syncUploadSourceDirectory(dir); err != nil {
		return "", err
	}
	if err = syncUploadSourceDirectory(root); err != nil {
		return "", err
	}
	if move {
		removePickerSource(source)
	}
	return destination, nil
}

func removePickerSource(path string) {
	_ = os.Remove(path) // Rename may have moved it already.
	if runtime.GOOS == "android" {
		_ = os.Remove(filepath.Dir(path)) // Android gives each picker copy a private directory.
	}
}

func copyUploadSource(ctx context.Context, source, destination string, before os.FileInfo) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := excludeUploadSourceFromBackup(destination); err != nil {
		return errors.Join(err, out.Close())
	}
	_, copyErr := io.Copy(out, &uploadContextReader{ctx: ctx, file: in})
	if copyErr == nil {
		copyErr = out.Sync()
	}
	closeErr := out.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return err
	}
	after, err := in.Stat()
	if err != nil {
		return err
	}
	if after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return fmt.Errorf("upload source changed while staging")
	}
	return nil
}

type uploadContextReader struct {
	ctx  context.Context
	file *os.File
}

func (r *uploadContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.file.Read(p)
}

func syncUploadSourceDirectory(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func syncUploadSourceFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// removeUploadSourceCopy never touches caller-owned files. The exact two-level
// shape and non-symlink parent guard against a corrupted journal path.
func removeUploadSourceCopy(path string) error {
	root, err := durableUploadSourceRoot()
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || filepath.IsAbs(rel) {
		return nil
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) != 2 || !strings.HasPrefix(parts[0], "source-") || parts[1] == "" || parts[1] == "." || parts[1] == ".." {
		return nil
	}
	dir := filepath.Join(root, parts[0])
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Remove(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (a *App) removeUploadSourceCopies(paths []string) {
	for _, path := range paths {
		if err := removeUploadSourceCopy(path); err != nil {
			slog.Warn("remove upload source copy", "error", err)
		}
	}
}

func (a *App) releaseUnreferencedUploadSources(svc *fileservice.Service, paths []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, path := range paths {
		if path == "" {
			continue
		}
		retained, err := svc.StagedSourceReferenced(ctx, path)
		if err != nil {
			slog.Warn("check upload source copy", "error", err)
			continue
		}
		if !retained {
			a.removeUploadSourceCopies([]string{path})
		}
	}
}

// Startup is the only point where no staging attempt can be in progress.
// Prune copies left between a crash and the first durable journal write.
func (a *App) sweepUnreferencedUploadSources() {
	root, err := durableUploadSourceRoot()
	if err != nil {
		slog.Warn("open upload source directory", "error", err)
		return
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		slog.Warn("list upload source directory", "error", err)
		return
	}
	svc := a.fileService()
	if svc == nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "source-") {
			continue
		}
		children, err := os.ReadDir(filepath.Join(root, entry.Name()))
		if err != nil {
			continue
		}
		paths := make([]string, 0, len(children))
		for _, child := range children {
			if !child.IsDir() {
				paths = append(paths, filepath.Join(root, entry.Name(), child.Name()))
			}
		}
		if len(paths) == 0 {
			_ = os.Remove(filepath.Join(root, entry.Name()))
			continue
		}
		a.releaseUnreferencedUploadSources(svc, paths)
	}
}
