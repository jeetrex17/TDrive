package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const pathBlockMarker = "# TDrive CLI"

func runInstallCLI(args []string) error {
	opts, err := parseInstallArgs(args)
	if err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return fmt.Errorf("install-cli is not wired on Windows yet")
	}

	target := opts.target
	if target == "" {
		target, err = defaultCLIPath()
		if err != nil {
			return err
		}
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return err
	}
	if err := installCurrentExecutable(target, opts.force); err != nil {
		return err
	}

	dir := filepath.Dir(target)
	fmt.Printf("installed: %s\n", displayPath(target))
	if pathContains(dir) {
		fmt.Println("run: tdrive help")
		return nil
	}

	if opts.updateShell {
		rc, err := updateShellPath(dir)
		if err != nil {
			return err
		}
		fmt.Printf("updated: %s\n", displayPath(rc))
		fmt.Printf("run: source %s\n", displayPath(rc))
		return nil
	}

	rc := shellConfigPath()
	fmt.Printf("\nAdd this to %s:\n", displayPath(rc))
	fmt.Printf("export PATH=\"%s:$PATH\"\n", shellPathDir(dir))
	fmt.Printf("\nThen run:\nsource %s\n", displayPath(rc))
	return nil
}

func runUninstallCLI(args []string) error {
	target := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--target":
			if i+1 >= len(args) {
				return fmt.Errorf("usage: tdrive uninstall-cli [--target PATH]")
			}
			target = args[i+1]
			i++
		default:
			return fmt.Errorf("usage: tdrive uninstall-cli [--target PATH]")
		}
	}
	var err error
	if target == "" {
		target, err = defaultCLIPath()
		if err != nil {
			return err
		}
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return err
	}
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return err
	}
	fmt.Printf("removed: %s\n", displayPath(target))
	if rc, changed, err := removeShellPathBlock(); err != nil {
		return err
	} else if changed {
		fmt.Printf("updated: %s\n", displayPath(rc))
	}
	return nil
}

type installOptions struct {
	target      string
	updateShell bool
	force       bool
}

func parseInstallArgs(args []string) (installOptions, error) {
	var opts installOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--target":
			if i+1 >= len(args) {
				return installOptions{}, fmt.Errorf("usage: tdrive install-cli [--target PATH] [--update-shell] [--force]")
			}
			opts.target = args[i+1]
			i++
		case "--update-shell":
			opts.updateShell = true
		case "--force":
			opts.force = true
		default:
			return installOptions{}, fmt.Errorf("usage: tdrive install-cli [--target PATH] [--update-shell] [--force]")
		}
	}
	return opts, nil
}

func installCurrentExecutable(target string, force bool) error {
	src, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate current executable: %w", err)
	}
	src, err = filepath.Abs(src)
	if err != nil {
		return err
	}
	if src == target {
		return nil
	}
	if info, err := os.Lstat(target); err == nil {
		if info.IsDir() {
			return fmt.Errorf("%s is a directory", target)
		}
		if info.Mode()&os.ModeType != 0 && !force {
			return fmt.Errorf("%s exists and is not a regular file; use --force", target)
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	tmp, err := os.CreateTemp(filepath.Dir(target), ".tdrive-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if force {
		_ = os.Remove(target)
	}
	return os.Rename(tmpPath, target)
}

func updateShellPath(dir string) (string, error) {
	rc := shellConfigPath()
	if err := os.MkdirAll(filepath.Dir(rc), 0o755); err != nil {
		return "", err
	}
	content, err := os.ReadFile(rc)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if strings.Contains(string(content), pathBlockMarker) || strings.Contains(string(content), shellPathDir(dir)) {
		return rc, nil
	}
	line := fmt.Sprintf("\n%s\nexport PATH=\"%s:$PATH\"\n", pathBlockMarker, shellPathDir(dir))
	f, err := os.OpenFile(rc, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(line); err != nil {
		return "", err
	}
	return rc, nil
}

func removeShellPathBlock() (string, bool, error) {
	rc := shellConfigPath()
	content, err := os.ReadFile(rc)
	if os.IsNotExist(err) {
		return rc, false, nil
	}
	if err != nil {
		return "", false, err
	}
	lines := strings.Split(string(content), "\n")
	out := make([]string, 0, len(lines))
	changed := false
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == pathBlockMarker {
			changed = true
			if i+1 < len(lines) && strings.Contains(lines[i+1], "export PATH=") {
				i++
			}
			continue
		}
		out = append(out, lines[i])
	}
	if !changed {
		return rc, false, nil
	}
	next := strings.Join(out, "\n")
	next = strings.TrimRight(next, "\n") + "\n"
	if err := os.WriteFile(rc, []byte(next), 0o644); err != nil {
		return "", false, err
	}
	return rc, true, nil
}

func defaultCLIPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "bin", "tdrive"), nil
}

func shellConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".zshrc"
	}
	shell := filepath.Base(os.Getenv("SHELL"))
	switch shell {
	case "bash":
		return filepath.Join(home, ".bashrc")
	default:
		return filepath.Join(home, ".zshrc")
	}
}

func pathContains(dir string) bool {
	for _, part := range filepath.SplitList(os.Getenv("PATH")) {
		if part == dir {
			return true
		}
	}
	return false
}

func shellPathDir(dir string) string {
	home, err := os.UserHomeDir()
	if err == nil {
		if rel, relErr := filepath.Rel(home, dir); relErr == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			return "$HOME/" + filepath.ToSlash(rel)
		}
	}
	return dir
}

func displayPath(path string) string {
	home, err := os.UserHomeDir()
	if err == nil {
		if rel, relErr := filepath.Rel(home, path); relErr == nil {
			if rel == "." {
				return "~"
			}
			if !strings.HasPrefix(rel, "..") {
				return "~/" + filepath.ToSlash(rel)
			}
		}
	}
	return path
}
