//go:build darwin

package updater

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestBundlePathFromExecutable(t *testing.T) {
	got, err := bundlePathFromExecutable("/Applications/TDrive.app/Contents/MacOS/TDrive")
	if err != nil || got != "/Applications/TDrive.app" {
		t.Fatalf("got %q, %v", got, err)
	}
	for _, bad := range []string{"/usr/local/bin/tdrive", "/Applications/TDrive.app/Contents/Resources/x", "/Users/me/build/Contents/MacOS/TDrive"} {
		if _, err := bundlePathFromExecutable(bad); err == nil {
			t.Errorf("bundlePathFromExecutable(%q) succeeded, want error", bad)
		}
	}
}

func TestDarwinTargetRejectsTranslocation(t *testing.T) {
	installer := darwinInstaller{executable: func() (string, error) {
		return "/private/var/folders/xx/T/AppTranslocation/ABCD/d/TDrive.app/Contents/MacOS/TDrive", nil
	}}
	_, err := installer.Target()
	var notInstallable *NotInstallableError
	if err == nil || !errors.As(err, &notInstallable) {
		t.Fatalf("Target = %v, want NotInstallableError", err)
	}
	installer = darwinInstaller{executable: func() (string, error) { return "/usr/local/bin/tdrive", nil }}
	if _, err := installer.Target(); err == nil {
		t.Fatalf("non-bundle executable must not be installable")
	}
}

// makeBundle writes a minimal .app skeleton whose executable prints marker.
func makeBundle(t *testing.T, path, marker string) {
	t.Helper()
	macos := filepath.Join(path, "Contents", "MacOS")
	if err := os.MkdirAll(macos, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "Contents", "Info.plist"), []byte("<plist/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(macos, "TDrive"), []byte("#!/bin/sh\necho "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestDarwinInstallSwapsBundleFromDittoArchive(t *testing.T) {
	work := t.TempDir()
	// Build the payload the way release.yml does: ditto -c -k --keepParent.
	source := filepath.Join(work, "src", "TDrive-macos-arm64.app")
	makeBundle(t, source, "new")
	payload := filepath.Join(work, "TDrive-v9.9.9-macos-arm64.zip")
	if out, err := exec.Command("/usr/bin/ditto", "-c", "-k", "--sequesterRsrc", "--keepParent", source, payload).CombinedOutput(); err != nil {
		t.Fatalf("ditto: %v: %s", err, out)
	}

	apps := filepath.Join(work, "Applications")
	installed := filepath.Join(apps, "My TDrive.app") // user-renamed bundle
	makeBundle(t, installed, "old")
	target := Target{Path: installed, Kind: "bundle"}

	if err := (darwinInstaller{}).Install(payload, target); err != nil {
		t.Fatalf("Install: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(installed, "Contents", "MacOS", "TDrive"))
	if err != nil || string(got) != "#!/bin/sh\necho new\n" {
		t.Fatalf("installed executable = %q, %v", got, err)
	}
	info, err := os.Stat(filepath.Join(installed, "Contents", "MacOS", "TDrive"))
	if err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("executable bit lost: %v %v", info, err)
	}
	previous, err := os.ReadFile(filepath.Join(installed+previousSuffix, "Contents", "MacOS", "TDrive"))
	if err != nil || string(previous) != "#!/bin/sh\necho old\n" {
		t.Fatalf("previous bundle = %q, %v", previous, err)
	}
	entries, _ := os.ReadDir(apps)
	for _, e := range entries {
		if e.Name() != "My TDrive.app" && e.Name() != "My TDrive.app"+previousSuffix {
			t.Fatalf("unexpected leftover in Applications: %s", e.Name())
		}
	}

	if err := (darwinInstaller{}).Cleanup(target); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if exists(installed + previousSuffix) {
		t.Fatalf("previous bundle must be removed by Cleanup")
	}
}

func TestDarwinInstallLeavesTargetUntouchedOnBadPayload(t *testing.T) {
	work := t.TempDir()
	installed := filepath.Join(work, "TDrive.app")
	makeBundle(t, installed, "old")

	// Two bundles in one archive is ambiguous; nothing may change.
	src := filepath.Join(work, "src")
	makeBundle(t, filepath.Join(src, "A.app"), "a")
	makeBundle(t, filepath.Join(src, "B.app"), "b")
	payload := filepath.Join(work, "bad.zip")
	if out, err := exec.Command("/usr/bin/ditto", "-c", "-k", src, payload).CombinedOutput(); err != nil {
		t.Fatalf("ditto: %v: %s", err, out)
	}
	err := (darwinInstaller{}).Install(payload, Target{Path: installed, Kind: "bundle"})
	if err == nil {
		t.Fatalf("expected an error for an ambiguous payload")
	}
	got, _ := os.ReadFile(filepath.Join(installed, "Contents", "MacOS", "TDrive"))
	if string(got) != "#!/bin/sh\necho old\n" {
		t.Fatalf("target changed after a failed install: %q", got)
	}
	if exists(installed + previousSuffix) {
		t.Fatalf("no previous copy may exist after a failed install")
	}
	if entries, _ := os.ReadDir(work); len(entries) != 3 { // TDrive.app, src, bad.zip
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("staging leftovers: %v", names)
	}
}
