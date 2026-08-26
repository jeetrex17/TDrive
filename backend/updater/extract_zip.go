package updater

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// extractZipMaxBytes bounds the total uncompressed size so a corrupt or
// hostile archive cannot fill the disk. Real payloads are well under 200 MB.
const extractZipMaxBytes int64 = 2 << 30

var errZipSlip = errors.New("archive entry escapes the destination")

// extractZip unpacks src into destDir, rejecting entries that would land
// outside destDir. Symbolic links are skipped: the Windows payload never
// contains any, and honouring them would reopen the traversal hole.
func extractZip(src, destDir string) error {
	reader, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer reader.Close()

	root, err := filepath.Abs(destDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}

	var remaining = extractZipMaxBytes
	for _, entry := range reader.File {
		target, err := safeJoin(root, entry.Name)
		if err != nil {
			return err
		}
		mode := entry.Mode()
		switch {
		case mode&os.ModeSymlink != 0:
			continue
		case entry.FileInfo().IsDir():
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		written, err := extractEntry(entry, target, remaining)
		if err != nil {
			return fmt.Errorf("extract %s: %w", entry.Name, err)
		}
		remaining -= written
	}
	return nil
}

func extractEntry(entry *zip.File, target string, remaining int64) (int64, error) {
	rc, err := entry.Open()
	if err != nil {
		return 0, err
	}
	defer rc.Close()

	perm := entry.Mode().Perm()
	if perm == 0 {
		perm = 0o644
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm|0o600)
	if err != nil {
		return 0, err
	}
	written, err := io.Copy(out, io.LimitReader(rc, remaining+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return written, err
	}
	if written > remaining {
		return written, errors.New("archive exceeds the size limit")
	}
	return written, nil
}

// safeJoin resolves name under root and refuses absolute names, drive-letter
// prefixes and any ".." component that would escape root.
func safeJoin(root, name string) (string, error) {
	clean := strings.ReplaceAll(name, "\\", "/")
	if clean == "" || strings.HasPrefix(clean, "/") || strings.Contains(clean, ":") {
		return "", fmt.Errorf("%w: %q", errZipSlip, name)
	}
	for _, part := range strings.Split(clean, "/") {
		if part == ".." {
			return "", fmt.Errorf("%w: %q", errZipSlip, name)
		}
	}
	target := filepath.Join(root, filepath.FromSlash(clean))
	if target != root && !strings.HasPrefix(target, root+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q", errZipSlip, name)
	}
	return target, nil
}
