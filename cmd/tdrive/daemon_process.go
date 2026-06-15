package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"TDrive/backend/daemon"
	"TDrive/backend/processlock"
)

const daemonStartTimeout = 5 * time.Second

func newDaemonClient() (*daemon.Client, error) {
	if err := ensureDaemonRunning(); err != nil {
		return nil, err
	}
	return daemon.NewClient()
}

func ensureDaemonRunning() error {
	if daemonIsReady() {
		return nil
	}
	if ok, err := waitForExistingBackend(); ok || err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "starting TDrive daemon...")
	if err := startDaemonBackground(false); err != nil {
		return err
	}
	return nil
}

func startDaemonBackground(verbose bool) error {
	if daemonIsReady() {
		if verbose {
			fmt.Println("TDrive daemon already running")
		}
		return nil
	}
	if ok, err := waitForExistingBackend(); ok || err != nil {
		if err == nil && verbose {
			fmt.Println("TDrive daemon already running")
		}
		return err
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate tdrive executable: %w", err)
	}
	logPath, err := daemonLogPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return fmt.Errorf("create daemon log dir: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open daemon log: %w", err)
	}
	defer func() { _ = logFile.Close() }()

	cmd := exec.Command(exe, "daemon", "start")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil
	detachDaemonCommand(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}
	if cmd.Process != nil {
		_ = cmd.Process.Release()
	}

	if err := waitForDaemon(daemonStartTimeout); err != nil {
		return fmt.Errorf("%w. If TDrive GUI is open, close it first. Log: %s", err, logPath)
	}
	if verbose {
		fmt.Printf("TDrive daemon started\nlog: %s\n", logPath)
	}
	return nil
}

func waitForExistingBackend() (bool, error) {
	info, active, err := processlock.ReadActive()
	if err != nil || !active {
		return false, nil
	}
	switch info.Role {
	case "gui":
		return true, fmt.Errorf("TDrive GUI is running (pid %d). Close it before using the CLI daemon", info.PID)
	case "daemon":
		if err := waitForDaemon(daemonStartTimeout); err != nil {
			return true, fmt.Errorf("TDrive daemon is starting but not ready: %w", err)
		}
		return true, nil
	default:
		return true, fmt.Errorf("TDrive backend is already running as %s (pid %d)", info.Role, info.PID)
	}
}

func waitForDaemon(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		c, err := daemon.NewClient()
		if err == nil {
			if _, err = c.Status(); err == nil {
				return nil
			}
		}
		if err != nil {
			last = err
		}
		time.Sleep(100 * time.Millisecond)
	}
	if last != nil {
		return fmt.Errorf("daemon did not become ready: %w", last)
	}
	return errors.New("daemon did not become ready")
}

func waitForDaemonStop(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !daemonIsReady() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func daemonIsReady() bool {
	c, err := daemon.NewClient()
	if err != nil {
		return false
	}
	_, err = c.Status()
	return err == nil
}

func daemonLogPath() (string, error) {
	dir, err := tdriveConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "daemon.log"), nil
}

func tdriveConfigDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "TDrive"), nil
}

func isDaemonNotRunning(err error) bool {
	return err != nil && strings.Contains(err.Error(), "daemon is not running")
}
