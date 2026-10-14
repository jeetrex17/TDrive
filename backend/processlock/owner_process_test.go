package processlock

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"
)

// A separate process establishes crash recovery, not merely seeded metadata.
func TestLockOwnerProcess(t *testing.T) {
	mode := os.Getenv("TDRIVE_TEST_LOCK_OWNER")
	if mode == "" {
		return
	}
	if mode == "legacy" {
		path, err := Path()
		if err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, fileMode)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.NewEncoder(f).Encode(Info{Role: "gui", PID: os.Getpid(), StartedAt: time.Now().Unix()}); err != nil {
			t.Fatal(err)
		}
		if err := f.Sync(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := f.Close(); err != nil {
				t.Error(err)
			}
		})
	} else {
		lock, err := Acquire("gui")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := lock.Release(); err != nil {
				t.Error(err)
			}
		})
	}
	fmt.Println("owner ready")
	_, _ = io.Copy(io.Discard, os.Stdin)
}

type ownerProcess struct {
	cmd    *exec.Cmd
	waited bool
}

func startOwnerProcess(t *testing.T, mode string) *ownerProcess {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLockOwnerProcess$")
	cmd.Env = append(os.Environ(), "TDRIVE_TEST_LOCK_OWNER="+mode)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close() })
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	owner := &ownerProcess{cmd: cmd}
	t.Cleanup(func() {
		if !owner.waited {
			_ = stdin.Close()
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "owner ready\n" {
		t.Fatalf("owner readiness=%q error=%v", line, err)
	}
	return owner
}

func (p *ownerProcess) crash(t *testing.T) {
	t.Helper()
	if err := p.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	err := p.cmd.Wait()
	p.waited = true
	if _, ok := errors.AsType[*exec.ExitError](err); !ok {
		t.Fatalf("crashed owner exit=%v", err)
	}
}

func TestAcquireAfterOwnerProcessDiesWithoutRelease(t *testing.T) {
	withConfigHome(t)
	owner := startOwnerProcess(t, "kernel")
	if lock, err := Acquire("daemon"); !errors.Is(err, ErrAlreadyRunning) {
		if lock != nil {
			_ = lock.Release()
		}
		t.Fatalf("live owner allowed competing backend: %v", err)
	}
	owner.crash(t)
	_, active, err := ReadActive()
	if err != nil || active {
		t.Fatalf("dead owner active=%v error=%v", active, err)
	}
	lock, err := Acquire("daemon")
	if err != nil {
		t.Fatalf("crashed owner blocked new backend: %v", err)
	}
	t.Cleanup(func() {
		if err := lock.Release(); err != nil {
			t.Error(err)
		}
	})
}

func TestUpgradeDoesNotTakeOverLiveLegacyOwner(t *testing.T) {
	withConfigHome(t)
	owner := startOwnerProcess(t, "legacy")
	if lock, err := Acquire("daemon"); !errors.Is(err, ErrAlreadyRunning) {
		if lock != nil {
			_ = lock.Release()
		}
		t.Fatalf("live older backend lost exclusion: %v", err)
	}
	owner.crash(t)
	lock, err := Acquire("gui")
	if err != nil {
		t.Fatalf("dead legacy owner blocked migration: %v", err)
	}
	t.Cleanup(func() {
		if err := lock.Release(); err != nil {
			t.Error(err)
		}
	})
	info, err := Read()
	if err != nil || info.LockVersion != 1 {
		t.Fatalf("migrated metadata=%+v error=%v", info, err)
	}
}
