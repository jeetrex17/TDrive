//go:build linux && !android

package nativeplayer

import (
	"fmt"
	"log/slog"
	"regexp"
)

// mediaURLPattern matches the tokenized loopback URL mpv is given. mpv echoes
// it in error output, and the token must not end up in the log file.
var mediaURLPattern = regexp.MustCompile(`http://127\.0\.0\.1:\d+/media/\S+`)

// linuxNativeLogf records native-player diagnostics in the app log, where a
// user can find them after a failed open. The desktop launcher discards
// stderr, so nowhere else would they ever be seen.
func linuxNativeLogf(format string, args ...any) {
	message := mediaURLPattern.ReplaceAllString(fmt.Sprintf(format, args...), "<media-url>")
	slog.Info("native player: " + message)
}
