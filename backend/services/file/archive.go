package file

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"strings"
)

// Archive reading for folder/archive import. This layer only enumerates and
// streams the contents of a supported archive; the import orchestrator turns
// those entries into folders and uploads. It uses the standard library only and
// is deliberately strict: entries that escape the archive root (zip-slip) and
// symlink/hardlink/device entries are skipped, never returned or streamed.

// ArchiveEntry describes one safe member of an archive.
type ArchiveEntry struct {
	RelPath string // cleaned, forward-slash path relative to the archive root
	Size    int64  // uncompressed size in bytes (0 for directories)
	IsDir   bool
}

type archiveKind int

const (
	archiveUnknown archiveKind = iota
	archiveZip
	archiveTar
	archiveTarGz
)

func classifyArchive(p string) archiveKind {
	lower := strings.ToLower(p)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return archiveZip
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return archiveTarGz
	case strings.HasSuffix(lower, ".tar"):
		return archiveTar
	default:
		return archiveUnknown
	}
}

// IsArchive reports whether path has a supported archive extension
// (.zip, .tar, .tar.gz, .tgz), case-insensitive.
func IsArchive(p string) bool {
	return classifyArchive(p) != archiveUnknown
}

// sanitizeArchivePath normalizes an archive entry name to a safe, forward-slash
// relative path. ok is false when the entry would escape the archive root (an
// absolute path or any ".." segment), in which case the caller must skip it.
func sanitizeArchivePath(name string) (rel string, ok bool) {
	clean := path.Clean(strings.ReplaceAll(name, "\\", "/"))
	switch {
	case clean == "" || clean == ".":
		return "", false
	case path.IsAbs(clean):
		return "", false
	case clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../"):
		return "", false
	default:
		return clean, true
	}
}

// ScanArchive lists the safe entries of an archive for the pre-upload plan.
// Directory entries are returned with IsDir true. Symlinks, hardlinks, devices,
// and zip-slip entries are skipped.
func ScanArchive(p string) ([]ArchiveEntry, error) {
	switch classifyArchive(p) {
	case archiveZip:
		return scanZip(p)
	case archiveTar:
		return scanTar(p, false)
	case archiveTarGz:
		return scanTar(p, true)
	default:
		return nil, fmt.Errorf("unsupported archive type: %s", p)
	}
}

// StreamArchiveFiles calls fn for each safe regular-file entry in archive order,
// passing a reader valid only for that call. It applies the same skip/sanitize
// rules as ScanArchive and stops at the first error fn returns.
//
// For tar.gz this decompresses independently of ScanArchive, so a caller that
// both scans and streams pays decompression twice. That keeps the planning pass
// (counts, oversize checks, folder set) cleanly separate from the upload pass
// and is an acceptable trade for v1.
func StreamArchiveFiles(p string, fn func(e ArchiveEntry, r io.Reader) error) error {
	switch classifyArchive(p) {
	case archiveZip:
		return streamZip(p, fn)
	case archiveTar:
		return streamTar(p, false, fn)
	case archiveTarGz:
		return streamTar(p, true, fn)
	default:
		return fmt.Errorf("unsupported archive type: %s", p)
	}
}

func scanZip(p string) ([]ArchiveEntry, error) {
	zr, err := zip.OpenReader(p)
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}
	defer zr.Close()

	entries := make([]ArchiveEntry, 0, len(zr.File))
	for _, f := range zr.File {
		fi := f.FileInfo()
		if fi.Mode()&os.ModeSymlink != 0 {
			continue
		}
		rel, ok := sanitizeArchivePath(f.Name)
		if !ok {
			continue
		}
		if fi.IsDir() {
			entries = append(entries, ArchiveEntry{RelPath: rel, IsDir: true})
			continue
		}
		if f.UncompressedSize64 > math.MaxInt64 {
			continue // implausible declared size; treat as malformed and skip
		}
		entries = append(entries, ArchiveEntry{RelPath: rel, Size: int64(f.UncompressedSize64)})
	}
	return entries, nil
}

func streamZip(p string, fn func(e ArchiveEntry, r io.Reader) error) error {
	zr, err := zip.OpenReader(p)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer zr.Close()

	for _, f := range zr.File {
		fi := f.FileInfo()
		if fi.Mode()&os.ModeSymlink != 0 || fi.IsDir() {
			continue
		}
		rel, ok := sanitizeArchivePath(f.Name)
		if !ok {
			continue
		}
		if f.UncompressedSize64 > math.MaxInt64 {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("open zip entry %q: %w", f.Name, err)
		}
		err = fn(ArchiveEntry{RelPath: rel, Size: int64(f.UncompressedSize64)}, rc)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func scanTar(p string, gzipped bool) ([]ArchiveEntry, error) {
	var entries []ArchiveEntry
	err := withTarReader(p, gzipped, func(tr *tar.Reader) error {
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			rel, ok := sanitizeArchivePath(hdr.Name)
			if !ok {
				continue
			}
			switch {
			case isTarDir(hdr):
				entries = append(entries, ArchiveEntry{RelPath: rel, IsDir: true})
			case isTarRegular(hdr):
				entries = append(entries, ArchiveEntry{RelPath: rel, Size: hdr.Size})
			default:
				// symlink, hardlink, device, fifo: skip
			}
		}
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

func streamTar(p string, gzipped bool, fn func(e ArchiveEntry, r io.Reader) error) error {
	return withTarReader(p, gzipped, func(tr *tar.Reader) error {
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			if !isTarRegular(hdr) {
				continue
			}
			rel, ok := sanitizeArchivePath(hdr.Name)
			if !ok {
				continue
			}
			if err := fn(ArchiveEntry{RelPath: rel, Size: hdr.Size}, tr); err != nil {
				return err
			}
		}
	})
}

func isTarDir(hdr *tar.Header) bool {
	return hdr.Typeflag == tar.TypeDir
}

func isTarRegular(hdr *tar.Header) bool {
	return hdr.Typeflag == tar.TypeReg || hdr.Typeflag == tar.TypeRegA
}

// withTarReader opens p as a tar stream (optionally gzip-wrapped) and invokes fn
// with the reader, closing everything afterward.
func withTarReader(p string, gzipped bool, fn func(tr *tar.Reader) error) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()

	var src io.Reader = f
	if gzipped {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return fmt.Errorf("gzip: %w", err)
		}
		// Treat the input as a single gzip stream; do not continue into any
		// bytes appended after it.
		gz.Multistream(false)
		defer gz.Close()
		src = gz
	}
	return fn(tar.NewReader(src))
}
