package thumbnail

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestIsVideoMatchesTheGalleryPredicate(t *testing.T) {
	for _, name := range []string{"clip.mp4", "Clip.MOV", "a.mkv", "b.webm", "c.AVI", "d.m2ts"} {
		if !IsVideo(name) {
			t.Errorf("IsVideo(%q) = false", name)
		}
	}
	for _, name := range []string{"photo.jpg", "notes.txt", "archive.zip", "", "noext", "video.mp4.txt"} {
		if IsVideo(name) {
			t.Errorf("IsVideo(%q) = true", name)
		}
	}
}

func TestGenerateVideoPosterRefusesWhatItCannotDraw(t *testing.T) {
	// A still is not a poster request; the image path already owns those.
	if _, err := GenerateVideoPoster(context.Background(), "photo.jpg", 512); !errors.Is(err, ErrPosterUnsupported) {
		t.Fatalf("image = %v, want ErrPosterUnsupported", err)
	}
	// Invalid requests are caller errors, not "no picture": they must not be
	// mistaken for a video that merely could not be decoded.
	for _, edge := range []int{0, -1, 4000} {
		if _, err := GenerateVideoPoster(context.Background(), "clip.mp4", edge); err == nil || errors.Is(err, ErrPosterUnsupported) {
			t.Fatalf("maxEdge %d = %v, want a plain error", edge, err)
		}
	}
}

func TestGenerateVideoPosterReportsAnUndecodableFileWithoutFailingTheUpload(t *testing.T) {
	// Right extension, no video inside. This is the shape of the failure the
	// caller must treat as "no picture" and carry on from.
	path := filepath.Join(t.TempDir(), "broken.mp4")
	if err := os.WriteFile(path, []byte("not a video"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := GenerateVideoPoster(context.Background(), path, 512); !errors.Is(err, ErrPosterUnsupported) {
		t.Fatalf("broken video = %v, want ErrPosterUnsupported", err)
	}
}

func TestGenerateVideoPosterHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := GenerateVideoPoster(ctx, "clip.mp4", 512); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled = %v", err)
	}
}

// The real thing, against a video committed to this repo. Skipped where no mpv
// is reachable, because that is a legitimate environment rather than a failure:
// the caller's contract is that a poster is optional.
func TestGenerateVideoPosterDrawsARealFrame(t *testing.T) {
	source, err := filepath.Abs(filepath.Join("..", "..", "assets", "tdrive-banner-loop-v4.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Skip("sample video unavailable")
	}
	poster, err := GenerateVideoPoster(context.Background(), source, 320)
	if errors.Is(err, ErrPosterUnsupported) {
		t.Skip("no decoder available in this environment")
	}
	if err != nil {
		t.Fatalf("poster: %v", err)
	}
	if len(poster) < 512 {
		t.Fatalf("poster is %d bytes, too small to be a frame", len(poster))
	}
	// JPEG, because that is what every other rendition is and what the cache
	// and the wire both assume.
	if len(poster) < 3 || poster[0] != 0xFF || poster[1] != 0xD8 || poster[2] != 0xFF {
		t.Fatalf("poster is not a JPEG: % x", poster[:min(3, len(poster))])
	}
}
