package auth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
)

func TestPrivateSessionStorageMissingSession(t *testing.T) {
	storage := &privateSessionStorage{path: filepath.Join(t.TempDir(), "session.json")}

	data, err := storage.LoadSession(context.Background())
	if !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("LoadSession error = %v, want session.ErrNotFound", err)
	}
	if data != nil {
		t.Fatalf("LoadSession data = %q, want nil", data)
	}
}

func TestPrivateSessionStorageReplacesSession(t *testing.T) {
	storage := &privateSessionStorage{path: filepath.Join(t.TempDir(), "session.json")}
	ctx := context.Background()

	if err := storage.StoreSession(ctx, []byte("old session")); err != nil {
		t.Fatalf("store old session: %v", err)
	}
	if err := storage.StoreSession(ctx, []byte("new session")); err != nil {
		t.Fatalf("store new session: %v", err)
	}
	data, err := storage.LoadSession(ctx)
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	if got, want := string(data), "new session"; got != want {
		t.Fatalf("session = %q, want %q", got, want)
	}
}

func TestConnectWithOptionsSecuresExistingSession(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows os.FileMode does not represent ACL permissions")
	}
	useTemporaryConfigDir(t)
	if err := SaveImpCredentials(123, "api hash"); err != nil {
		t.Fatalf("SaveImpCredentials: %v", err)
	}

	sessionPath := filepath.Join(filepath.Dir(GetConfigPath()), "session.json")
	if err := os.WriteFile(sessionPath, []byte("existing session"), 0o600); err != nil {
		t.Fatalf("write session: %v", err)
	}
	if err := os.Chmod(sessionPath, 0o644); err != nil {
		t.Fatalf("make session permissive: %v", err)
	}

	if _, err := ConnectWithOptions(telegram.Options{}); err != nil {
		t.Fatalf("ConnectWithOptions: %v", err)
	}
	info, err := os.Stat(sessionPath)
	if err != nil {
		t.Fatalf("stat session: %v", err)
	}
	if got := info.Mode().Perm(); got != privateFileMode {
		t.Fatalf("session mode = %04o, want %04o", got, privateFileMode)
	}
}

func TestConnectWithOptionsDoesNotCreateEmptySession(t *testing.T) {
	useTemporaryConfigDir(t)
	if err := SaveImpCredentials(123, "api hash"); err != nil {
		t.Fatalf("SaveImpCredentials: %v", err)
	}

	if _, err := ConnectWithOptions(telegram.Options{}); err != nil {
		t.Fatalf("ConnectWithOptions: %v", err)
	}
	sessionPath := filepath.Join(filepath.Dir(GetConfigPath()), "session.json")
	if _, err := os.Stat(sessionPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("session stat error = %v, want os.ErrNotExist", err)
	}
}
