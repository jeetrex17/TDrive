package mountdav

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"TDrive/backend/mountfs"

	"golang.org/x/net/webdav"
)

// FileSystem adapts a protocol-neutral mountfs.FS to x/net/webdav's
// filesystem contract. Every mutating operation is denied unconditionally.
type FileSystem struct {
	fs *mountfs.FS
}

func NewFileSystem(fs *mountfs.FS) *FileSystem {
	return &FileSystem{fs: fs}
}

func (*FileSystem) Mkdir(context.Context, string, os.FileMode) error {
	return os.ErrPermission
}

func (*FileSystem) RemoveAll(context.Context, string) error {
	return os.ErrPermission
}

func (*FileSystem) Rename(context.Context, string, string) error {
	return os.ErrPermission
}

func (fs *FileSystem) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	_, entry, err := fs.lookup(ctx, "stat", name)
	if err != nil {
		return nil, err
	}
	return newFileInfo(entry), nil
}

func (fs *FileSystem) OpenFile(ctx context.Context, name string, flag int, _ os.FileMode) (webdav.File, error) {
	if !readOnlyFlags(flag) {
		return nil, pathError("open", name, os.ErrPermission)
	}
	clean, entry, err := fs.lookup(ctx, "open", name)
	if err != nil {
		return nil, err
	}
	return fs.openEntry(ctx, clean, entry)
}

func (fs *FileSystem) lookup(ctx context.Context, operation, name string) (string, mountfs.Entry, error) {
	if fs == nil || fs.fs == nil {
		return "", mountfs.Entry{}, fmt.Errorf("mountdav: filesystem not ready")
	}
	clean, err := cleanWebDAVName(name)
	if err != nil {
		return "", mountfs.Entry{}, pathError(operation, name, err)
	}
	entry, err := fs.fs.Stat(ctx, clean)
	if err != nil {
		return "", mountfs.Entry{}, mapMountFSError(operation, clean, err)
	}
	return clean, entry, nil
}

func (fs *FileSystem) openEntry(ctx context.Context, clean string, entry mountfs.Entry) (webdav.File, error) {
	if entry.Kind == mountfs.KindDirectory {
		children, err := fs.fs.ReadDir(ctx, clean)
		if err != nil {
			return nil, mapMountFSError("readdir", clean, err)
		}
		infos := make([]os.FileInfo, len(children))
		for index, child := range children {
			infos[index] = newFileInfo(child)
		}
		return newDirectoryFile(newFileInfo(entry), infos), nil
	}
	file, err := fs.fs.Open(ctx, clean)
	if err != nil {
		return nil, mapMountFSError("open", clean, err)
	}
	return newRandomAccessFile(ctx, file, newFileInfo(entry)), nil
}

func readOnlyFlags(flag int) bool {
	const writeFlags = os.O_WRONLY | os.O_RDWR | os.O_APPEND | os.O_CREATE | os.O_TRUNC | os.O_EXCL
	return flag&writeFlags == 0
}

func cleanWebDAVName(name string) (string, error) {
	if name == "" {
		return "/", nil
	}
	if !utf8.ValidString(name) || !strings.HasPrefix(name, "/") || strings.ContainsRune(name, 0) || strings.Contains(name, `\`) {
		return "", os.ErrInvalid
	}
	if name == "/" {
		return name, nil
	}
	if strings.HasSuffix(name, "/") {
		name = strings.TrimSuffix(name, "/")
	}
	for _, component := range strings.Split(strings.TrimPrefix(name, "/"), "/") {
		if component == "" || component == "." || component == ".." {
			return "", os.ErrInvalid
		}
	}
	return name, nil
}

func mapMountFSError(operation, name string, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, mountfs.ErrNotFound), errors.Is(err, mountfs.ErrNotDirectory):
		return pathError(operation, name, os.ErrNotExist)
	case errors.Is(err, mountfs.ErrInvalidPath), errors.Is(err, mountfs.ErrIsDirectory):
		return pathError(operation, name, os.ErrInvalid)
	default:
		return pathError(operation, name, err)
	}
}

func pathError(operation, name string, err error) error {
	return &os.PathError{Op: operation, Path: name, Err: err}
}
