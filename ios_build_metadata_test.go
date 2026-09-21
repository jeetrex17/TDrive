package main

import (
	"os"
	"regexp"
	"testing"
)

func TestIOSPlistVersionsMatchBuildConfig(t *testing.T) {
	config := readBuildMetadataFile(t, "build/config.yml")
	match := regexp.MustCompile(`(?m)^  version: "([^"]+)"$`).FindSubmatch(config)
	if len(match) != 2 {
		t.Fatal("build/config.yml info.version not found")
	}
	version := string(match[1])

	tests := []struct {
		path         string
		shortVersion string
	}{
		{path: "build/ios/Info.plist", shortVersion: version},
		{path: "build/ios/Info.dev.plist", shortVersion: version + "-dev"},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			plist := readBuildMetadataFile(t, test.path)
			if got := plistString(plist, "CFBundleShortVersionString"); got != test.shortVersion {
				t.Fatalf("CFBundleShortVersionString = %q, want %q", got, test.shortVersion)
			}
			if got := plistString(plist, "CFBundleVersion"); got != version {
				t.Fatalf("CFBundleVersion = %q, want %q", got, version)
			}
		})
	}
}

func readBuildMetadataFile(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return contents
}

func plistString(plist []byte, key string) string {
	pattern := `<key>` + regexp.QuoteMeta(key) + `</key>\s*<string>([^<]+)</string>`
	match := regexp.MustCompile(pattern).FindSubmatch(plist)
	if len(match) != 2 {
		return ""
	}
	return string(match[1])
}
