package thumbnail

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestGenerateLocalBoundsImageAndRestoresReader(t *testing.T) {
	raw := encodePNG(t, image.NewRGBA(image.Rect(0, 0, 900, 600)))
	source := bytes.NewReader(raw)
	_, _ = source.Seek(7, io.SeekStart)
	got, err := GenerateLocal(context.Background(), source, 256)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(got))
	// Native and portable decoders may round the minor edge differently.
	if err != nil || cfg.Width != 256 || cfg.Height < 170 || cfg.Height > 171 {
		t.Fatalf("dimensions = %v, %v", cfg, err)
	}
	if offset, _ := source.Seek(0, io.SeekCurrent); offset != 7 {
		t.Fatalf("reader position = %d, want 7", offset)
	}
}

func TestGenerateLocalNativeMemorySource(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "ios" {
		t.Skip("ImageIO-specific sampling guard")
	}
	if _, handled, _ := generateNativeBytes([]byte("invalid"), 512); !handled {
		t.Skip("native decoder disabled in this build")
	}
	// A decrypted source stays in memory. Its 25MP JPEG exceeds the portable
	// budget but must still use native sampled decoding without a plaintext file.
	var raw bytes.Buffer
	if err := jpeg.Encode(&raw, image.NewGray(image.Rect(0, 0, 5000, 5000)), nil); err != nil {
		t.Fatal(err)
	}
	got, err := GenerateLocal(context.Background(), bytes.NewReader(raw.Bytes()), 512)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(got))
	if err != nil || cfg.Width > 512 || cfg.Height > 512 {
		t.Fatalf("dimensions = %v, %v", cfg, err)
	}
}

func TestGenerateLocalOrientationAndMetadata(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 120, 60))
	draw.Draw(source, image.Rect(0, 0, 60, 60), &image.Uniform{color.RGBA{R: 255, A: 255}}, image.Point{}, draw.Src)
	draw.Draw(source, image.Rect(60, 0, 120, 60), &image.Uniform{color.RGBA{B: 255, A: 255}}, image.Point{}, draw.Src)
	raw := jpegWithOrientation(t, source, 6)
	got, err := GenerateLocal(context.Background(), bytes.NewReader(raw), 100)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(got))
	if err != nil || cfg.Width != 50 || cfg.Height != 100 {
		t.Fatalf("orientation dimensions = %v, %v", cfg, err)
	}
	if exifOrientation(got) != orientationNormal {
		t.Fatal("source EXIF orientation leaked into rendered preview")
	}
	decoded, _, err := image.Decode(bytes.NewReader(got))
	if err != nil {
		t.Fatal(err)
	}
	red, _, blue, _ := decoded.At(25, 20).RGBA()
	if red < blue*2 {
		t.Fatal("rotation did not put the red source half at the top")
	}
	red, _, blue, _ = decoded.At(25, 80).RGBA()
	if blue < red*2 {
		t.Fatal("rotation did not put the blue source half at the bottom")
	}
}

func TestGenerateLocalCancelledAdmission(t *testing.T) {
	localDecodeSlot <- struct{}{}
	defer func() { <-localDecodeSlot }()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := GenerateLocal(ctx, bytes.NewReader(nil), 256); done <- err }()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting decoder = %v", err)
	}
}

func TestGeneratePortableLocalGuards(t *testing.T) {
	for _, raw := range [][]byte{nil, []byte("invalid"), pngHeader(100000, 100000)} {
		if _, err := generatePortableLocal(context.Background(), raw, 256); err == nil {
			t.Fatal("unsafe source accepted")
		}
	}
	raw := encodePNG(t, image.NewRGBA(image.Rect(0, 0, 32, 16)))
	if _, err := generatePortableLocal(context.Background(), raw, 16); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := generatePortableLocal(ctx, raw, 16); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled decode = %v", err)
	}
}

func TestGenerateLocalRejectsInvalidWork(t *testing.T) {
	if _, err := GenerateLocal(nil, bytes.NewReader(nil), 256); err == nil {
		t.Fatal("nil context accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := GenerateLocal(ctx, bytes.NewReader(nil), 256); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled generation = %v", err)
	}
	for _, edge := range []int{0, -1, 1601} {
		if _, err := GenerateLocal(context.Background(), bytes.NewReader(nil), edge); err == nil {
			t.Fatalf("accepted edge %d", edge)
		}
	}
	if _, err := GenerateLocal(context.Background(), bytes.NewReader([]byte("invalid")), 256); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("malformed source = %v", err)
	}
}

func TestGenerateLocalNamedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(path, encodePNG(t, image.NewRGBA(image.Rect(0, 0, 640, 480))), 0600); err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	got, err := GenerateLocal(context.Background(), source, 256)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(got))
	if err != nil || cfg.Width > 256 || cfg.Height > 256 {
		t.Fatalf("dimensions = %v, %v", cfg, err)
	}
}

func TestGenerateLocalRejectsUnsafeFiles(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "oversized")
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(localSourceLimit + 1); err != nil {
		t.Fatal(err)
	}
	if _, err := GenerateLocal(context.Background(), file, 256); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized source = %v", err)
	}
	file.Close()
	if _, err := GenerateLocal(context.Background(), file, 256); err == nil {
		t.Fatal("closed file accepted")
	}
	directory, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	if _, err := GenerateLocal(context.Background(), directory, 256); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("directory source = %v", err)
	}
}
