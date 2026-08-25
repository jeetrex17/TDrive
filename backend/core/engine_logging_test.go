package core

import (
	"log/slog"
	"testing"

	"TDrive/backend/applog"
	"TDrive/backend/tgclient"

	"github.com/gotd/td/telegram"
)

func TestNewDoesNotInitializeProcessLogger(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("HOME", configDir)
	t.Setenv("XDG_CONFIG_HOME", configDir)
	t.Setenv("APPDATA", configDir)

	originalLogger := slog.Default()
	t.Cleanup(func() {
		applog.Close()
		slog.SetDefault(originalLogger)
	})

	engine, err := New(t.Context(), Config{
		TG:         tgclient.NewFake(1),
		SkipDBInit: true,
		Connect: func() (*telegram.Client, error) {
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(engine.Close)

	if got := slog.Default(); got != originalLogger {
		t.Fatal("New() initialized the process-wide application logger")
	}
}
