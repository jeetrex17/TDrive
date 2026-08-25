package mountdav

import (
	"context"
	"os"
	"path"

	"TDrive/backend/mountfs"

	"golang.org/x/net/webdav"
)

// preflightPropfind captures exactly the metadata tree permitted by the
// validated Depth header. x/net/webdav then walks this immutable snapshot, so
// a later source failure cannot silently turn a 207 response into partial data.
func preflightPropfind(ctx context.Context, fs *FileSystem, name string, includeChildren bool) (*propfindSnapshot, error) {
	clean, root, err := fs.lookup(ctx, "propfind", name)
	if err != nil {
		return nil, err
	}
	snapshot := &propfindSnapshot{
		entries:  map[string]fileInfo{clean: newFileInfo(root)},
		children: make(map[string][]os.FileInfo),
	}
	if !includeChildren || root.Kind != mountfs.KindDirectory {
		return snapshot, nil
	}
	children, err := fs.fs.ReadDir(ctx, clean)
	if err != nil {
		return nil, mapMountFSError("propfind", clean, err)
	}
	infos := make([]os.FileInfo, len(children))
	for index, child := range children {
		childInfo := newFileInfo(child)
		childPath := path.Join(clean, child.Name)
		snapshot.entries[childPath] = childInfo
		infos[index] = childInfo
	}
	snapshot.children[clean] = infos
	return snapshot, nil
}

type propfindSnapshot struct {
	entries  map[string]fileInfo
	children map[string][]os.FileInfo
}

func (*propfindSnapshot) Mkdir(context.Context, string, os.FileMode) error {
	return os.ErrPermission
}

func (*propfindSnapshot) RemoveAll(context.Context, string) error {
	return os.ErrPermission
}

func (*propfindSnapshot) Rename(context.Context, string, string) error {
	return os.ErrPermission
}

func (snapshot *propfindSnapshot) Stat(_ context.Context, name string) (os.FileInfo, error) {
	_, info, err := snapshot.lookup("stat", name)
	if err != nil {
		return nil, err
	}
	return info, nil
}

func (snapshot *propfindSnapshot) OpenFile(_ context.Context, name string, flag int, _ os.FileMode) (webdav.File, error) {
	if !readOnlyFlags(flag) {
		return nil, pathError("open", name, os.ErrPermission)
	}
	clean, info, err := snapshot.lookup("open", name)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return newDirectoryFile(info, snapshot.children[clean]), nil
	}
	return newMetadataFile(info), nil
}

func (snapshot *propfindSnapshot) lookup(operation, name string) (string, fileInfo, error) {
	clean, err := cleanWebDAVName(name)
	if err != nil {
		return "", fileInfo{}, pathError(operation, name, err)
	}
	info, found := snapshot.entries[clean]
	if !found {
		return "", fileInfo{}, pathError(operation, clean, os.ErrNotExist)
	}
	return clean, info, nil
}
