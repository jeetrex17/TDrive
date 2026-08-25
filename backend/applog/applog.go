// Package applog wires up heavy, shareable diagnostic logging for local
// development (wails dev, and the CLI daemon started in the foreground).
// Init is called once at the GUI or daemon process boundary, so reusable
// backend components can use the top-level slog functions (slog.Debug,
// slog.Info, ...) without owning a process-global file handle.
//
// Output goes to both stderr (visible directly in a `wails dev` terminal)
// and a log file in the app's config directory, so a session's log is easy
// to grab and share afterward too. The file is truncated on each Init so a
// shared log reflects only the current run, not accumulated history.
package applog

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	privateDirMode  os.FileMode = 0o700
	privateFileMode os.FileMode = 0o600
	LogFileName                 = "tdrive-dev.log"
)

var (
	initOnce sync.Once
	logFile  *os.File
	logPath  string
)

// sensitiveAttrKeys are matched as case-insensitive substrings against every
// logged attribute key. Any match replaces the value with "[redacted]" --
// a defense-in-depth backstop, not a substitute for callers simply never
// logging secrets (master keys, Telegram credentials/session data, vault
// passwords, WebDAV capability tokens) in the first place.
var sensitiveAttrKeys = []string{
	"password", "passphrase",
	"masterkey", "master_key",
	"apihash", "api_hash", "apikey", "api_key",
	"authkey", "auth_key",
	"sessiondata", "session_data",
	"capability", "capabilityurl", "capability_url",
	"secret", "token",
}

// Init sets slog's default logger. Safe to call more than once within one
// process lifetime -- only the first call takes effect.
func Init() {
	initOnce.Do(func() {
		var writer io.Writer = os.Stderr
		if path, file, err := openLogFile(); err == nil {
			logPath = path
			logFile = file
			writer = io.MultiWriter(os.Stderr, file)
		}
		handler := slog.NewTextHandler(writer, &slog.HandlerOptions{
			Level:       slog.LevelDebug,
			AddSource:   true,
			ReplaceAttr: redact,
		})
		slog.SetDefault(slog.New(handler))
		if logPath != "" {
			slog.Info("applog: logging initialized", "file", logPath)
		} else {
			slog.Info("applog: logging initialized (stderr only, log file unavailable)")
		}
	})
}

func openLogFile() (string, *os.File, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", nil, err
	}
	appDir := filepath.Join(configDir, "TDrive")
	if err := os.MkdirAll(appDir, privateDirMode); err != nil {
		return "", nil, err
	}
	path := filepath.Join(appDir, LogFileName)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, privateFileMode)
	if err != nil {
		return "", nil, err
	}
	return path, file, nil
}

func redact(_ []string, attr slog.Attr) slog.Attr {
	lowerKey := strings.ToLower(attr.Key)
	for _, sensitive := range sensitiveAttrKeys {
		if strings.Contains(lowerKey, sensitive) {
			attr.Value = slog.StringValue("[redacted]")
			return attr
		}
	}
	return attr
}

// Close flushes and closes the log file, if one was opened. Safe to call
// even if Init was never called or the file failed to open.
func Close() {
	if logFile != nil {
		_ = logFile.Close()
		logFile = nil
	}
}
