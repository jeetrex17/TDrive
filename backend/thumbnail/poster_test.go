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
	// The two refusals mean different things to a caller, and the difference is
	// the whole contract: "no picture" is published, a bad request is a bug.
	for _, testCase := range []struct {
		name        string
		path        string
		maxEdge     int
		unsupported bool
	}{
		// A still is not a poster request; the image path already owns those.
		{name: "image", path: "photo.jpg", maxEdge: 512, unsupported: true},
		{name: "no extension", path: "clip", maxEdge: 512, unsupported: true},
		{name: "extension only looks like one", path: "clip.mp4.txt", maxEdge: 512, unsupported: true},
		{name: "empty path", path: "", maxEdge: 512},
		{name: "blank path", path: "   ", maxEdge: 512},
		{name: "no edge", path: "clip.mp4", maxEdge: 0},
		{name: "negative edge", path: "clip.mp4", maxEdge: -1},
		{name: "edge past the cache's limit", path: "clip.mp4", maxEdge: 4000},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := GenerateVideoPoster(context.Background(), testCase.path, testCase.maxEdge)
			if err == nil {
				t.Fatalf("%q at %d succeeded", testCase.path, testCase.maxEdge)
			}
			if errors.Is(err, ErrPosterUnsupported) != testCase.unsupported {
				t.Fatalf("%q at %d = %v, unsupported = %t, want %t",
					testCase.path, testCase.maxEdge, err, !testCase.unsupported, testCase.unsupported)
			}
		})
	}
	if _, err := GenerateVideoPoster(nil, "clip.mp4", 512); err == nil { //nolint:staticcheck // the nil context is the case under test
		t.Fatal("nil context succeeded")
	}
}

// The seek policy is stated once and read by three implementations -- mpv's
// --start=N%, and the percentage each mobile bridge applies to a declared
// duration. Nothing else keeps them from drifting apart.
func TestPosterSeekPolicyIsOneStatement(t *testing.T) {
	if want := int(posterSeekFraction * 100); posterSeekPercent != want {
		t.Fatalf("posterSeekPercent = %d, want %d", posterSeekPercent, want)
	}
	if posterSeekPercent < 1 || posterSeekPercent > 50 {
		t.Fatalf("posterSeekPercent = %d, want a frame past the opening but not past the point", posterSeekPercent)
	}
	// The fallback is a seek within a video, so it has to be reachable well
	// inside the window an extraction is allowed to take.
	if posterFallbackOffset <= 0 || posterFallbackOffset >= posterTimeout {
		t.Fatalf("posterFallbackOffset = %v, want inside the %v extraction budget", posterFallbackOffset, posterTimeout)
	}
}

// A poster that cannot be drawn must not wedge the decode slot it took: the
// next upload's thumbnail, poster or not, waits on that same slot.
func TestGenerateVideoPosterReleasesTheDecodeSlot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken.mp4")
	if err := os.WriteFile(path, []byte("not a video"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := GenerateVideoPoster(context.Background(), path, 512); err == nil {
		t.Fatal("a file that is not a video produced a poster")
	}
	select {
	case localDecodeSlot <- struct{}{}:
		<-localDecodeSlot
	default:
		t.Fatal("the decode slot is still held after a failed poster")
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
