package updater

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// assetTagPattern pulls the release tag out of a cached payload name so the
// cache can be pruned by version without a manifest.
var assetTagPattern = regexp.MustCompile(`^TDrive-(v[0-9][^-]*)-`)

func defaultCacheDir() string {
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "TDrive", "updates")
}

// pruneCache removes partial downloads and payloads that are no older than
// what is already running. It runs once at startup, before any download can
// begin, so it never races an in-flight transfer.
func pruneCache(dir string, current Version) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			continue
		}
		if strings.HasSuffix(name, partSuffix) {
			_ = os.Remove(filepath.Join(dir, name))
			continue
		}
		match := assetTagPattern.FindStringSubmatch(name)
		if match == nil {
			continue
		}
		version, err := ParseVersion(match[1])
		if err != nil || !version.Newer(current) {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

// pruneCacheExcept drops every cached payload other than keep, so switching
// to a newer release does not leave the superseded download behind.
func pruneCacheExcept(dir, keep string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || name == keep || !assetTagPattern.MatchString(name) {
			continue
		}
		_ = os.Remove(filepath.Join(dir, name))
	}
}
