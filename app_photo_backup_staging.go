package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"TDrive/backend/datadir"
	"TDrive/backend/photobackup"
)

const maxPhotoBackupResourceBytes int64 = 4 << 30

// photoBackupDeviceFolderKind is a folder the user picked on a phone. It is a
// separate kind from "folder" on purpose: a desktop folder is a filesystem
// path this process can open and walk, while this one is a location the host
// enumerates on our behalf. Keeping them apart means neither the desktop
// walker nor validatePhotoBackupFolderRoot has to learn about a root it could
// never os.Stat.
const photoBackupDeviceFolderKind = "device-folder"

// A device folder root is "<volume>:<relative/path>/" -- the media volume the
// host named, then the folder's position on it, always with a trailing slash
// so that one root is a prefix of another exactly when one folder contains the
// other. It is a location, not a path this process can open.
const maxPhotoBackupDeviceFolderRoot = 1024

func validatePhotoBackupDeviceFolderRoot(root string) (string, error) {
	volume, relative, found := strings.Cut(strings.TrimSpace(root), ":")
	if !found || len(root) > maxPhotoBackupDeviceFolderRoot || volume == "" || !isPhotoBackupVolumeName(volume) {
		return "", fmt.Errorf("photo backup: that folder cannot be read on this device")
	}
	// Rebuilt from its own components rather than trimmed: a root that reaches
	// the ledger with "." or ".." still in it would make the prefix test below
	// answer about a folder that does not exist.
	components := make([]string, 0, 8)
	for component := range strings.SplitSeq(relative, "/") {
		if component == "" || component == "." || component == ".." {
			continue
		}
		components = append(components, component)
	}
	if len(components) == 0 {
		// The whole volume is the library, which is what the "All photos and
		// videos" source already is. Two sources for one set of files would
		// only make the destination depend on which ran first.
		return "", fmt.Errorf("photo backup: choose a folder inside your storage, or use All photos and videos")
	}
	return volume + ":" + strings.Join(components, "/") + "/", nil
}

// Volume names are the host's own: MediaStore's on Android ("external_primary",
// or a card's lowercased UUID such as "1aef-2b03"), and "files" on iOS, where
// there is no media index and a picked folder is a place in the Files app that
// the host holds a security-scoped bookmark to. Anything else did not come
// from a host, and a root is a database key, so it is checked rather than
// trusted.
func isPhotoBackupVolumeName(volume string) bool {
	if len(volume) > 64 {
		return false
	}
	for _, r := range volume {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func photoBackupDeviceFolderSourceID(root string) string { return "tree:" + root }

// photoBackupDeviceFolderName is the folder's own name, for the places that
// speak to the user about a root rather than a stored source.
func photoBackupDeviceFolderName(root string) string {
	trimmed := strings.TrimSuffix(root, "/")
	if index := strings.LastIndexAny(trimmed, "/:"); index >= 0 {
		trimmed = trimmed[index+1:]
	}
	return trimmed
}

// photoBackupOverlappingDeviceFolder names the already-selected folder that
// contains the candidate, or that the candidate contains, and "" when they are
// unrelated.
//
// Overlapping folders are refused rather than merged. The ledger would dedupe
// the uploads themselves -- both sources report the same MediaStore identity --
// but the file would land under whichever source happened to reach it first,
// so the same photo's place in the drive would depend on scan order. A folder
// the user cannot predict the location of is worse than one they were asked to
// pick again.
func photoBackupOverlappingDeviceFolder(existing []photobackup.Source, candidateID, candidateRoot string) string {
	for _, source := range existing {
		if source.Kind != photoBackupDeviceFolderKind || source.ID == candidateID {
			continue
		}
		if strings.HasPrefix(candidateRoot, source.Root) || strings.HasPrefix(source.Root, candidateRoot) {
			name := source.Name
			if name == "" {
				name = photoBackupDeviceFolderName(source.Root)
			}
			return name
		}
	}
	return ""
}

func validatePhotoBackupFolderRoot(root string) (string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", photobackup.ErrInvalid
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("photo backup: source must be a readable folder")
	}
	if resolved, resolveErr := filepath.EvalSymlinks(absolute); resolveErr == nil {
		absolute = resolved
	}
	dataRoot, _ := datadir.Dir()
	cacheRoot, _ := datadir.CacheDir()
	for _, owned := range []string{dataRoot, cacheRoot} {
		if pathsOverlap(absolute, owned) {
			return "", fmt.Errorf("photo backup: TDrive data folders cannot be backup sources")
		}
	}
	return absolute, nil
}

func pathsOverlap(a, b string) bool {
	contains := func(parent, child string) bool {
		rel, err := filepath.Rel(parent, child)
		return err == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
	}
	return contains(a, b) || contains(b, a)
}

func validatePhotoBackupPath(path string, asset photobackup.Asset, source photobackup.Source) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("photo backup: invalid materialized file")
	}
	if info.Size() > maxPhotoBackupResourceBytes {
		return fmt.Errorf("photo backup: resource exceeds the 4 GiB limit")
	}
	if asset.Size > 0 && info.Size() != asset.Size {
		return fmt.Errorf("photo backup: materialized size changed")
	}
	if asset.Path == "" {
		return validateNativePhotoBackupPath(path)
	}
	wantVersion := strconv.FormatInt(info.ModTime().UnixNano(), 10) + ":" + strconv.FormatInt(info.Size(), 10)
	if asset.Version != wantVersion {
		return fmt.Errorf("photo backup: source version changed")
	}
	root, err := filepath.Abs(source.Root)
	if err != nil {
		return err
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, absolute)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("photo backup: file escaped source root")
	}
	return nil
}

func stagePhotoBackupFile(ctx context.Context, path string, asset photobackup.Asset) (string, func(), error) {
	if err := ctx.Err(); err != nil {
		return "", func() {}, err
	}
	expected, err := os.Lstat(path)
	if err != nil {
		return "", func() {}, err
	}
	if expected.Size() > maxPhotoBackupResourceBytes {
		return "", func() {}, fmt.Errorf("photo backup: resource exceeds the 4 GiB limit")
	}
	source, err := os.Open(path)
	if err != nil {
		return "", func() {}, err
	}
	defer source.Close()
	opened, err := source.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(expected, opened) {
		return "", func() {}, fmt.Errorf("photo backup: source changed before staging")
	}
	cache, err := datadir.CacheDir()
	if err != nil {
		return "", func() {}, err
	}
	root := filepath.Join(cache, "photo-backup-desktop")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", func() {}, err
	}
	dir, err := os.MkdirTemp(root, "resource-")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	name := filepath.Base(asset.Name)
	if name == "." || name == "" {
		name = "photo-backup-media"
	}
	destination := filepath.Join(dir, name)
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	copied, copyErr := io.Copy(output, io.LimitReader(photoBackupReader{ctx, source}, maxPhotoBackupResourceBytes+1))
	closeErr := output.Close()
	after, statErr := source.Stat()
	if copyErr != nil || closeErr != nil || statErr != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("photo backup: stage source: %w", errors.Join(copyErr, closeErr, statErr))
	}
	if copied > maxPhotoBackupResourceBytes {
		cleanup()
		return "", func() {}, fmt.Errorf("photo backup: resource exceeds the 4 GiB limit")
	}
	wantVersion := strconv.FormatInt(after.ModTime().UnixNano(), 10) + ":" + strconv.FormatInt(after.Size(), 10)
	if !os.SameFile(opened, after) || wantVersion != asset.Version || copied != opened.Size() {
		cleanup()
		return "", func() {}, fmt.Errorf("photo backup: source changed while staging")
	}
	return destination, cleanup, nil
}

type photoBackupReader struct {
	ctx    context.Context
	source io.Reader
}

func (r photoBackupReader) Read(bytes []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.source.Read(bytes)
}

func validateNativePhotoBackupPath(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil || !filepath.IsAbs(path) {
		return fmt.Errorf("photo backup: invalid staging path")
	}
	cache, err := datadir.CacheDir()
	if err != nil {
		return err
	}
	roots := []string{filepath.Join(cache, "photo-backup-stage")}
	if userCache, cacheErr := os.UserCacheDir(); cacheErr == nil {
		roots = append(roots, filepath.Join(userCache, "TDrivePhotoBackup"))
	}
	for index, root := range roots {
		resolvedPath, pathErr := filepath.EvalSymlinks(abs)
		resolvedRoot, rootErr := filepath.EvalSymlinks(root)
		if pathErr != nil || rootErr != nil {
			continue
		}
		rel, relErr := filepath.Rel(resolvedRoot, resolvedPath)
		if relErr != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if index == 0 {
			// Android preserves the original filename below its stable media-ID
			// directory. Match that native contract rather than rejecting every
			// successfully staged resource as an unexpected nested path.
			parts := strings.Split(filepath.ToSlash(rel), "/")
			if len(parts) != 2 {
				continue
			}
			id := strings.TrimPrefix(strings.TrimPrefix(parts[0], "image-"), "video-")
			if id == parts[0] {
				continue
			}
			if value, err := strconv.ParseInt(id, 10, 64); err != nil || value <= 0 {
				continue
			}
		}
		if index == 1 {
			parts := strings.Split(filepath.ToSlash(rel), "/")
			if len(parts) != 2 || !isUUID(parts[0]) {
				continue
			}
		}
		return nil
	}
	return fmt.Errorf("photo backup: path is outside native staging")
}

func isUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	for index, char := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		if !strings.ContainsRune("0123456789abcdefABCDEF", char) {
			return false
		}
	}
	return true
}
