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
