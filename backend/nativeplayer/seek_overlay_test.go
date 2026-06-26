package nativeplayer

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func solidPNG(t *testing.T, w, h int, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i+3 < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func near(a, b uint8) bool {
	d := int(a) - int(b)
	if d < 0 {
		d = -d
	}
	return d <= 2
}

// composeSeekThumbnail must produce a tightly packed, top-down BGRA buffer at
// the requested size, with the source color landing in B,G,R,A byte order (the
// layout a Windows 32bpp DIB section consumes).
func TestComposeSeekThumbnailBGRAOrder(t *testing.T) {
	want := color.RGBA{R: 200, G: 30, B: 40, A: 255}
	src := solidPNG(t, 8, 8, want)

	bmp, err := composeSeekThumbnail(src, 16, 9)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if bmp.Width != 16 || bmp.Height != 9 {
		t.Fatalf("size = %dx%d, want 16x9", bmp.Width, bmp.Height)
	}
	if len(bmp.Pixels) != 16*9*4 {
		t.Fatalf("len = %d, want %d", len(bmp.Pixels), 16*9*4)
	}

	// A solid source stays solid through scaling; check a center pixel is BGRA.
	idx := (4*16 + 8) * 4
	b, g, r, a := bmp.Pixels[idx], bmp.Pixels[idx+1], bmp.Pixels[idx+2], bmp.Pixels[idx+3]
	if !near(r, want.R) || !near(g, want.G) || !near(b, want.B) || a != 255 {
		t.Fatalf("center BGRA = %d,%d,%d,%d; want B=%d G=%d R=%d A=255", b, g, r, a, want.B, want.G, want.R)
	}
}

func TestComposeSeekThumbnailRejectsBadInput(t *testing.T) {
	valid := solidPNG(t, 4, 4, color.RGBA{R: 10, G: 20, B: 30, A: 255})
	cases := []struct {
		name string
		data []byte
		w, h int
	}{
		{"zero width", valid, 0, 9},
		{"negative height", valid, 16, -1},
		{"empty data", nil, 16, 9},
		{"oversized", valid, maxOverlayDimension + 1, 9},
		{"undecodable", []byte("not an image"), 16, 9},
	}
	for _, c := range cases {
		if _, err := composeSeekThumbnail(c.data, c.w, c.h); err == nil {
			t.Errorf("%s: expected an error, got nil", c.name)
		}
	}
}
