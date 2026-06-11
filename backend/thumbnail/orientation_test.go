package thumbnail

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

// jpegWithOrientation encodes img to JPEG and splices in a minimal EXIF APP1
// segment carrying a single IFD0 Orientation tag (big-endian TIFF).
func jpegWithOrientation(t *testing.T, img image.Image, orientation int) []byte {
	t.Helper()
	var jb bytes.Buffer
	if err := jpeg.Encode(&jb, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	raw := jb.Bytes()

	tiff := []byte{'M', 'M', 0x00, 0x2A, 0x00, 0x00, 0x00, 0x08} // header, IFD0 at offset 8
	ifd := []byte{0x00, 0x01}                                    // one entry
	ifd = append(ifd, 0x01, 0x12, 0x00, 0x03, 0x00, 0x00, 0x00, 0x01,
		byte(orientation>>8), byte(orientation), 0x00, 0x00) // Orientation SHORT
	ifd = append(ifd, 0x00, 0x00, 0x00, 0x00) // next IFD = none

	payload := append([]byte("Exif\x00\x00"), append(tiff, ifd...)...)
	segLen := len(payload) + 2
	app1 := []byte{0xFF, 0xE1, byte(segLen >> 8), byte(segLen)}
	app1 = append(app1, payload...)

	out := make([]byte, 0, len(raw)+len(app1))
	out = append(out, raw[0:2]...) // SOI
	out = append(out, app1...)
	out = append(out, raw[2:]...)
	return out
}

func TestExifOrientationReadsTag(t *testing.T) {
	data := jpegWithOrientation(t, image.NewRGBA(image.Rect(0, 0, 4, 2)), 6)
	if got := exifOrientation(data); got != 6 {
		t.Fatalf("orientation = %d, want 6", got)
	}
}

func TestExifOrientationDefaultsToNormal(t *testing.T) {
	var plain bytes.Buffer
	if err := jpeg.Encode(&plain, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil); err != nil {
		t.Fatalf("encode: %v", err)
	}
	cases := map[string][]byte{
		"plain jpeg (no exif)": plain.Bytes(),
		"not an image":         []byte("this is not an image at all"),
		"png":                  pngHeader(10, 10),
		"too short":            {0xFF},
	}
	for name, data := range cases {
		if got := exifOrientation(data); got != orientationNormal {
			t.Errorf("%s: orientation = %d, want 1", name, got)
		}
	}
}

func TestApplyOrientationRotate90(t *testing.T) {
	// 2x1 strip: red then blue. Orientation 6 (rotate 90 CW) -> 1x2 column.
	src := image.NewRGBA(image.Rect(0, 0, 2, 1))
	red := color.RGBA{R: 255, A: 255}
	blue := color.RGBA{B: 255, A: 255}
	src.Set(0, 0, red)
	src.Set(1, 0, blue)

	out := applyOrientation(src, 6)
	b := out.Bounds()
	if b.Dx() != 1 || b.Dy() != 2 {
		t.Fatalf("dims = %dx%d, want 1x2", b.Dx(), b.Dy())
	}
	if r, _, _, _ := out.At(0, 0).RGBA(); r>>8 != 255 {
		t.Fatalf("top pixel should be red")
	}
	if _, _, bl, _ := out.At(0, 1).RGBA(); bl>>8 != 255 {
		t.Fatalf("bottom pixel should be blue")
	}
}

func TestApplyOrientationNormalIsNoop(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 3, 2))
	if out := applyOrientation(src, 1); out != image.Image(src) {
		t.Fatalf("orientation 1 should return the source unchanged")
	}
	if out := applyOrientation(src, 99); out != image.Image(src) {
		t.Fatalf("out-of-range orientation should return the source unchanged")
	}
}

func TestGenerateBakesOrientation(t *testing.T) {
	// A 1600x800 landscape with orientation 6 must come out portrait: render
	// fits it to 512x256, then the rotation swaps to 256x512.
	src := image.NewRGBA(image.Rect(0, 0, 1600, 800))
	for y := 0; y < 800; y++ {
		for x := 0; x < 1600; x++ {
			src.Set(x, y, color.RGBA{R: 40, G: 90, B: 160, A: 255})
		}
	}
	out, err := Generate(jpegWithOrientation(t, src, 6), 512)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	decoded, _, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if b := decoded.Bounds(); b.Dx() != 256 || b.Dy() != 512 {
		t.Fatalf("oriented thumbnail = %dx%d, want 256x512", b.Dx(), b.Dy())
	}
}
