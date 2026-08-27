package file

import (
	"path"
	"strings"
)

func isIgnoredImportName(name string, directory bool) bool {
	lower := strings.ToLower(name)
	if lower == "" {
		return false
	}
	switch lower {
	case ".ds_store", "thumbs.db", "desktop.ini", ".localized":
		return true
	}
	if strings.HasPrefix(lower, "._") { // macOS AppleDouble sidecars
		return true
	}
	if directory {
		// Keep this deliberately conservative. Ambiguous output folders such as
		// build, dist, target, and vendor may be the content being archived.
		switch lower {
		case ".cache", ".git", ".hg", ".mypy_cache", ".nox", ".pytest_cache",
			".ruff_cache", ".svn", ".tox", ".venv", "__macosx", "__pycache__", "node_modules":
			return true
		}
	}
	return strings.HasSuffix(lower, ".pyc") ||
		strings.HasSuffix(lower, ".pyo") ||
		strings.HasSuffix(lower, ".tsbuildinfo")
}

// ignoredArchiveRoot returns the first excluded path prefix. Callers use the
// prefix as a set key so one pruned cache tree counts as one ignored entry,
// even when an archive lists every descendant separately.
func ignoredArchiveRoot(relativePath string, entryIsDir bool) string {
	cleaned := path.Clean(relativePath)
	if cleaned == "." || cleaned == "" {
		return ""
	}
	parts := strings.Split(cleaned, "/")
	for index, part := range parts {
		directory := index < len(parts)-1 || entryIsDir
		if isIgnoredImportName(part, directory) {
			return strings.Join(parts[:index+1], "/")
		}
	}
	return ""
}

func isIgnoredArchivePath(relativePath string, entryIsDir bool) bool {
	return ignoredArchiveRoot(relativePath, entryIsDir) != ""
}
