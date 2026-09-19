//go:build linux && !android

package nativeplayer

import (
	"fmt"
	"strings"
	"sync"
)

// mpvOutputLimit caps how much mpv terminal output is retained for diagnostics.
const mpvOutputLimit = 4096

// mpvOutput collects the tail of mpv's terminal output so a startup failure can
// report why mpv exited instead of only that its IPC socket never appeared.
type mpvOutput struct {
	mu   sync.Mutex
	data []byte
}

func (o *mpvOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.data = append(o.data, p...)
	if len(o.data) > mpvOutputLimit {
		o.data = o.data[len(o.data)-mpvOutputLimit:]
	}
	return len(p), nil
}

// Tail returns the retained output as a single line, or "" when mpv said nothing.
func (o *mpvOutput) Tail() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	fields := strings.Fields(string(o.data))
	return strings.Join(fields, " ")
}

// mpvStartupError explains an mpv process that exited before its IPC socket was
// ready, quoting mpv's own message when it produced one.
func mpvStartupError(exitErr error, output string) error {
	detail := output
	if detail == "" {
		detail = "mpv produced no output"
	}
	if exitErr != nil {
		return fmt.Errorf("native player: mpv exited during startup (%v): %s", exitErr, detail)
	}
	return fmt.Errorf("native player: mpv exited during startup: %s", detail)
}
