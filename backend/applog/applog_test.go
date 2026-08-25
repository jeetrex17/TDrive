package applog

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenLogFileWritesUnderConfigDirTDrive(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)
	t.Setenv("HOME", configDir) // covers os.UserConfigDir on platforms that fall back to HOME

	path, file, err := openLogFile()
	if err != nil {
		t.Fatalf("openLogFile: %v", err)
	}
	defer file.Close()

	if filepath.Base(path) != LogFileName {
		t.Fatalf("log file name = %q, want %q", filepath.Base(path), LogFileName)
	}
	if filepath.Base(filepath.Dir(path)) != "TDrive" {
		t.Fatalf("log file dir = %q, want a TDrive subdirectory", filepath.Dir(path))
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != privateFileMode {
		t.Fatalf("log file mode = %v (err=%v), want %v", info, err, privateFileMode)
	}
}

func TestOpenLogFileTruncatesOnEachCall(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)
	t.Setenv("HOME", configDir)

	_, first, err := openLogFile()
	if err != nil {
		t.Fatalf("openLogFile (first): %v", err)
	}
	if _, err := first.WriteString("stale line from a previous run\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	first.Close()

	path, second, err := openLogFile()
	if err != nil {
		t.Fatalf("openLogFile (second): %v", err)
	}
	defer second.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(data), "stale line") {
		t.Fatalf("log file was not truncated on reopen, contains stale content: %q", data)
	}
}

func TestRedactMasksSensitiveAttributeKeysCaseInsensitively(t *testing.T) {
	sensitive := []string{
		"password", "Password", "PASSPHRASE",
		"master_key", "masterKey",
		"api_hash", "apiHash", "api_key",
		"auth_key",
		"session_data",
		"capability_url", "capabilityURL",
		"secret_value", "lock_token", "Token",
	}
	for _, key := range sensitive {
		got := redact(nil, slog.String(key, "super-secret-value"))
		if got.Value.String() != "[redacted]" {
			t.Errorf("redact(%q) = %q, want [redacted]", key, got.Value.String())
		}
	}
}

func TestRedactLeavesOrdinaryAttributesAlone(t *testing.T) {
	ordinary := map[string]string{
		"path":          "/Docs/photo.png",
		"content_hash":  "abc123",
		"sha256":        "deadbeef",
		"object_id":     "f:1699",
		"size":          "15500",
		"method":        "PUT",
		"status":        "201",
		"drive_id":      "42",
		"revision":      "2",
		"error_message": "not found",
	}
	for key, value := range ordinary {
		got := redact(nil, slog.String(key, value))
		if got.Value.String() != value {
			t.Errorf("redact(%q) = %q, want unchanged %q (ordinary logging fields must not be masked)", key, got.Value.String(), value)
		}
	}
}

// TestInitIsIdempotent deliberately does not compare against the logger from
// before any call to Init: initOnce and slog's default logger are both
// process-global, so under `go test -count=2+` a later iteration would
// already observe an initialized state before it does anything, making a
// before/after comparison order-dependent and flaky. Comparing two calls
// against each other, entirely within this test, avoids that regardless of
// process history.
func TestInitIsIdempotent(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)
	t.Setenv("HOME", configDir)
	t.Cleanup(Close)

	Init()
	first := slog.Default()
	Init()
	second := slog.Default()
	if second != first {
		t.Fatal("Init is not idempotent: a second call changed the default logger")
	}
}
