//go:build darwin

package updater

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const dittoTimeout = 2 * time.Minute

type darwinInstaller struct {
	executable func() (string, error)
}

func newPlatformInstaller() Installer {
	return darwinInstaller{executable: os.Executable}
}

func (d darwinInstaller) Target() (Target, error) {
	exe, err := d.executable()
	if err != nil {
		return Target{}, err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	bundle, err := bundlePathFromExecutable(exe)
	if err != nil {
		return Target{}, &NotInstallableError{Reason: "TDrive isn't running from an app bundle, so it can't replace itself. Download the new version manually."}
	}
	if isTranslocated(bundle) {
		return Target{}, &NotInstallableError{Reason: "TDrive is running from a temporary location. Move it to the Applications folder to enable automatic updates."}
	}
	return Target{Path: bundle, Kind: "bundle"}, nil
}

// bundlePathFromExecutable maps …/X.app/Contents/MacOS/bin to …/X.app.
func bundlePathFromExecutable(exe string) (string, error) {
	macos := filepath.Dir(exe)
	contents := filepath.Dir(macos)
	bundle := filepath.Dir(contents)
	if filepath.Base(macos) != "MacOS" || filepath.Base(contents) != "Contents" || !strings.HasSuffix(bundle, ".app") {
		return "", fmt.Errorf("%s is not inside an app bundle", exe)
	}
	return bundle, nil
}

// isTranslocated detects Gatekeeper's App Translocation: a quarantined app
// launched from Downloads runs from a read-only random mount, so replacing
// "itself" would touch a copy nobody launches again.
func isTranslocated(path string) bool {
	return strings.Contains(path, "/AppTranslocation/")
}

func (d darwinInstaller) Install(payload string, target Target) error {
	parent := filepath.Dir(target.Path)
	staging := stagingPath(parent)
	if err := os.RemoveAll(staging); err != nil {
		return fmt.Errorf("clear staging: %w", err)
	}
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return fmt.Errorf("create staging: %w", err)
	}
	defer os.RemoveAll(staging)

	// ditto preserves the bundle's permissions, resource forks and extended
	// attributes exactly as the release workflow packed them; Go's archive/zip
	// would drop the executable bits on the sidecar binaries.
	ctx, cancel := context.WithTimeout(context.Background(), dittoTimeout)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "/usr/bin/ditto", "-x", "-k", payload, staging).CombinedOutput(); err != nil {
		return fmt.Errorf("unpack update: %w: %s", err, strings.TrimSpace(string(out)))
	}

	app, err := findBundle(staging)
	if err != nil {
		return err
	}
	if err := validateBundle(app); err != nil {
		return err
	}
	// The payload never carries a quarantine flag (it was written by this
	// process, not a browser), but a security tool may add one to anything
	// new on disk. Clearing it is harmless and keeps Gatekeeper quiet.
	_ = exec.Command("/usr/bin/xattr", "-dr", "com.apple.quarantine", app).Run()

	if err := swapPaths(target.Path, app); err != nil {
		return err
	}
	// LaunchServices caches bundle metadata by path and mtime; touching the
	// bundle makes the Dock and Finder pick up the new version's icon/plist.
	now := time.Now()
	_ = os.Chtimes(target.Path, now, now)
	return nil
}

// findBundle returns the single .app directory at the top level of dir,
// ignoring the __MACOSX resource-fork folder ditto may leave behind.
func findBundle(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var apps []string
	for _, entry := range entries {
		if entry.IsDir() && strings.HasSuffix(entry.Name(), ".app") {
			apps = append(apps, filepath.Join(dir, entry.Name()))
		}
	}
	if len(apps) != 1 {
		return "", errNoAppInPayload
	}
	return apps[0], nil
}

func validateBundle(app string) error {
	if _, err := os.Stat(filepath.Join(app, "Contents", "Info.plist")); err != nil {
		return fmt.Errorf("payload app is missing Info.plist: %w", err)
	}
	entries, err := os.ReadDir(filepath.Join(app, "Contents", "MacOS"))
	if err != nil {
		return errNoExecutableInBundle
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return nil
		}
	}
	return errNoExecutableInBundle
}

func (d darwinInstaller) Relaunch(target Target, waitPID int) error {
	// "open -n" asks LaunchServices for a brand-new instance even though the
	// current one is still running; the new process idles on the pid handshake
	// until this one has released the backend lock.
	cmd := exec.Command("/usr/bin/open", "-n", target.Path, "--args", WaitForPIDFlag, strconv.Itoa(waitPID))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("open new version: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (d darwinInstaller) Cleanup(target Target) error {
	err := os.RemoveAll(target.Path + previousSuffix)
	if stagingErr := removeStaging(filepath.Dir(target.Path)); stagingErr != nil && err == nil {
		err = stagingErr
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
