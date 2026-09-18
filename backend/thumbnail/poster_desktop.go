//go:build darwin || linux || windows

package thumbnail

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"time"
)

// posterTimeout bounds one extraction. A frame comes from a seek and a single
// decode, so a video that has not produced one by now is not going to: a
// damaged file can otherwise hold a decoder open for as long as it likes, and
// this runs while someone is waiting for an upload.
const posterTimeout = 20 * time.Second

// generateVideoPoster draws one frame with the mpv that already ships for
// playback.
//
// mpv rather than a platform framework, on all three desktops, for two reasons.
// It is one implementation instead of three, and its codec coverage is the
// widest available here -- AVFoundation would not open the .mkv files that
// prompted this, and Media Foundation would not open several others. The binary
// is already bundled and already validated at package time, so this adds a
// caller, not a dependency.
func generateVideoPoster(ctx context.Context, path string, maxEdge int) ([]byte, error) {
	binary, err := findPosterMPV()
	if err != nil {
		return nil, err
	}
	// mpv writes the frame as a file: --vo=image has no way to write to a pipe,
	// and a temp directory of our own means the output is whatever landed in an
	// empty directory rather than a name mpv chose and might change.
	outDir, err := os.MkdirTemp("", "tdrive-poster-")
	if err != nil {
		return nil, fmt.Errorf("thumbnail: poster workspace: %w", err)
	}
	defer os.RemoveAll(outDir)

	ctx, cancel := context.WithTimeout(ctx, posterTimeout)
	defer cancel()

	// Only options that predate the oldest mpv this app ships against. The
	// Linux AppImage falls back to the distribution's mpv, which has been seen
	// as old as 0.34, and mpv exits rather than ignoring an option it does not
	// know -- so a newer flag here would not degrade, it would produce nothing.
	command := exec.CommandContext(ctx, binary,
		"--no-config",
		"--really-quiet",
		"--no-audio",
		"--no-sub",
		"--frames=1",
		"--start="+strconv.FormatFloat(posterSeekFraction*100, 'f', 0, 64)+"%",
		"--vo=image",
		"--vo-image-format=jpg",
		"--vo-image-jpeg-quality=85",
		"--vo-image-outdir="+outDir,
		path,
	)
	command.Stdout = nil
	command.Stderr = nil
	if err := command.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("%w: decode timed out", ErrPosterUnsupported)
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// A non-zero exit is an unreadable file or a codec this mpv lacks.
		// Neither is an upload failure.
		return nil, fmt.Errorf("%w: %v", ErrPosterUnsupported, err)
	}

	frame, err := readSinglePosterFrame(outDir)
	if err != nil {
		return nil, err
	}
	// Reuse the image pipeline for the resize and re-encode, so a poster is
	// bounded and oriented by exactly the same code as every other rendition.
	return Generate(frame, maxEdge)
}

// readSinglePosterFrame returns the one file mpv wrote, refusing anything else.
// An empty directory means mpv exited cleanly without decoding anything, which
// happens with a file that has no video stream at all.
func readSinglePosterFrame(dir string) ([]byte, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("thumbnail: read poster frame: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("thumbnail: read poster frame: %w", err)
		}
		if len(raw) > 0 {
			return raw, nil
		}
	}
	return nil, fmt.Errorf("%w: no frame decoded", ErrPosterUnsupported)
}

// findPosterMPV locates the mpv that ships with the app, falling back to one on
// PATH. The bundled copy is preferred on every platform because it is the build
// whose codec set was validated at package time.
func findPosterMPV() (string, error) {
	name := "mpv"
	if runtime.GOOS == "windows" {
		name = "mpv.exe"
	}
	for _, candidate := range posterMPVCandidates(name) {
		if candidate == "" {
			continue
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}
	return "", fmt.Errorf("%w: no mpv available", ErrPosterUnsupported)
}

func posterMPVCandidates(name string) []string {
	executable, err := os.Executable()
	if err != nil {
		return nil
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	dir := filepath.Dir(executable)
	candidates := []string{
		// Windows, and any layout that puts the sidecar beside the binary.
		filepath.Join(dir, name),
		filepath.Join(dir, "media", name),
	}
	if runtime.GOOS == "darwin" {
		// <App>.app/Contents/MacOS/TDrive -> Contents/Resources/media/mpv,
		// which is where scripts/package-mpv-darwin.sh puts it.
		candidates = append(candidates, filepath.Join(dir, "..", "Resources", "media", name))
	}
	if runtime.GOOS == "linux" {
		candidates = append(candidates,
			filepath.Join(dir, "..", "lib", "tdrive", name),
			filepath.Join(dir, "..", "lib", "TDrive", name),
		)
	}
	return candidates
}
