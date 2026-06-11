package thumbnail

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestIsImage(t *testing.T) {
	cases := map[string]bool{
		"photo.jpg":     true,
		"photo.JPG":     true,
		"a.jpeg":        true,
		"b.png":         true,
		"c.gif":         true,
		"d.webp":        true,
		"e.bmp":         true,
		"vector.svg":    false, // deliberately excluded
		"clip.mp4":      false,
		"doc.pdf":       false,
		"noext":         false,
		"trailing.jpg ": true, // trimmed
	}
	for name, want := range cases {
		if got := IsImage(name); got != want {
			t.Errorf("IsImage(%q) = %v, want %v", name, got, want)
		}
	}
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func TestGenerateDownscalesPreservingAspectRatio(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 2000, 1000))
	for y := 0; y < 1000; y++ {
		for x := 0; x < 2000; x++ {
			src.Set(x, y, color.RGBA{R: 10, G: 120, B: 200, A: 255})
		}
	}

	out, err := Generate(encodePNG(t, src), 512)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	decoded, format, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode thumbnail: %v", err)
	}
	if format != "jpeg" {
		t.Fatalf("format = %q, want jpeg", format)
	}
	b := decoded.Bounds()
	if b.Dx() != 512 || b.Dy() != 256 {
		t.Fatalf("thumbnail = %dx%d, want 512x256", b.Dx(), b.Dy())
	}
}

func TestGenerateKeepsSmallImageSize(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 80, 40))
	out, err := Generate(encodePNG(t, src), 512)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	decoded, _, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if b := decoded.Bounds(); b.Dx() != 80 || b.Dy() != 40 {
		t.Fatalf("thumbnail = %dx%d, want 80x40 (no upscale)", b.Dx(), b.Dy())
	}
}

func TestGenerateFlattensTransparencyOntoWhite(t *testing.T) {
	// Fully transparent source: after compositing it must read as white, not
	// the black that a naive JPEG encode of an alpha image produces.
	src := image.NewRGBA(image.Rect(0, 0, 64, 64))
	out, err := Generate(encodePNG(t, src), 512)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	decoded, _, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	r, g, b, _ := decoded.At(32, 32).RGBA()
	if r < 0xf000 || g < 0xf000 || b < 0xf000 {
		t.Fatalf("center pixel = (%d,%d,%d), want near-white", r>>8, g>>8, b>>8)
	}
}

func TestGenerateRejectsNonImage(t *testing.T) {
	if _, err := Generate([]byte("this is not an image"), 512); err != ErrUnsupported {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

func TestGenerateRejectsOversizeDimensions(t *testing.T) {
	// A PNG header alone declares dimensions; DecodeConfig reads it without
	// any pixel data, so we can exceed the guard cheaply.
	header := pngHeader(8000, 8000) // 64 MP > 24 MP cap
	if _, err := Generate(header, 512); err != ErrTooLarge {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

func TestGenerateRoundTripsJPEGSource(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 300, 300))
	for y := 0; y < 300; y++ {
		for x := 0; x < 300; x++ {
			src.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 90, A: 255})
		}
	}
	var jpegBuf bytes.Buffer
	if err := jpeg.Encode(&jpegBuf, src, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode jpeg source: %v", err)
	}
	out, err := Generate(jpegBuf.Bytes(), 128)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	decoded, _, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if b := decoded.Bounds(); b.Dx() != 128 || b.Dy() != 128 {
		t.Fatalf("thumbnail = %dx%d, want 128x128", b.Dx(), b.Dy())
	}
}

func TestFitWithin(t *testing.T) {
	cases := []struct {
		w, h, max    int
		wantW, wantH int
	}{
		{2000, 1000, 512, 512, 256},
		{1000, 2000, 512, 256, 512},
		{500, 500, 512, 500, 500}, // already within bounds
		{512, 512, 512, 512, 512},
		{1024, 1, 512, 512, 1}, // extreme aspect clamps to >= 1
	}
	for _, c := range cases {
		gotW, gotH := fitWithin(c.w, c.h, c.max)
		if gotW != c.wantW || gotH != c.wantH {
			t.Errorf("fitWithin(%d,%d,%d) = %dx%d, want %dx%d",
				c.w, c.h, c.max, gotW, gotH, c.wantW, c.wantH)
		}
	}
}

// pngHeader builds the PNG signature plus a single valid IHDR chunk declaring
// the given dimensions. image.DecodeConfig parses this without needing pixel
// data, which lets the oversize-dimension guard be exercised cheaply.
func pngHeader(w, h uint32) []byte {
	var buf bytes.Buffer
	buf.Write([]byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a})

	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], w)
	binary.BigEndian.PutUint32(ihdr[4:8], h)
	ihdr[8] = 8 // bit depth
	ihdr[9] = 2 // color type: truecolor

	length := make([]byte, 4)
	binary.BigEndian.PutUint32(length, 13)
	buf.Write(length)
	buf.WriteString("IHDR")
	buf.Write(ihdr)

	crc := crc32.ChecksumIEEE(append([]byte("IHDR"), ihdr...))
	sum := make([]byte, 4)
	binary.BigEndian.PutUint32(sum, crc)
	buf.Write(sum)

	return buf.Bytes()
}
