package main

import (
	"bytes"
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

func TestIOSXcodeGenerationRunsArchivePatcher(t *testing.T) {
	taskfile := readBuildMetadataFile(t, "build/ios/Taskfile.yml")

	arguments := [][]byte{
		[]byte("go run build/ios/scripts/patch_xcode_project.go"),
		[]byte(`TDRIVE_IOS_MIN_VERSION: '{{.MIN_IOS_VERSION}}'`),
		[]byte(`TDRIVE_IOS_MARKETING_VERSION: '{{.APP_VERSION}}'`),
		[]byte(`TDRIVE_IOS_BUILD_NUMBER: '{{.IOS_BUILD_NUMBER}}'`),
		[]byte(`-minimum-version "$TDRIVE_IOS_MIN_VERSION"`),
		[]byte(`-marketing-version "$TDRIVE_IOS_MARKETING_VERSION"`),
		[]byte(`-build-number "$TDRIVE_IOS_BUILD_NUMBER"`),
		[]byte("build/ios/Info.plist"),
		[]byte("build/ios/scripts/patch_xcode_project.go"),
		[]byte("build/ios/scripts/build_xcode_archive.sh"),
	}
	for _, argument := range arguments {
		if !bytes.Contains(taskfile, argument) {
			t.Errorf("iOS Xcode generator does not invoke archive patcher with %q", argument)
		}
	}
}

func TestIOSXcodeArchiveScriptCreatesOutputDirectory(t *testing.T) {
	script := readBuildMetadataFile(t, "build/ios/scripts/build_xcode_archive.sh")
	if !bytes.Contains(script, []byte(`mkdir -p "${APP_ROOT}/bin"`)) {
		t.Error("iOS Xcode archive script does not create its Go archive output directory")
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
