// Package thumbnail turns full-resolution images into small JPEG previews
// for the gallery grid. It is deliberately pure: given raw image bytes it
// returns thumbnail bytes, with no knowledge of Telegram, encryption, or the
// projection. The on-disk LRU cache lives alongside in cache.go.
package thumbnail

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"path/filepath"
	"strings"

	"golang.org/x/image/draw"

	// Register the decoders we support. JPEG is imported non-blank because we
	// also encode with it; the rest are blank imports for their init() side
	// effect (registering with image.Decode / image.DecodeConfig).
	_ "image/gif"
	_ "image/png"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"
)

const (
	// DefaultMaxEdge is the longest side, in pixels, of a generated thumbnail.
	// 512 keeps the grid crisp at the largest zoom level while staying small
	// over the Wails bridge (a 512px JPEG is ~20-40 KB).
	DefaultMaxEdge = 512

	// maxSourcePixels guards against decompression bombs: an image declaring
	// more pixels than this is rejected before we allocate its pixel buffer.
	// 24 MP comfortably covers phone and DSLR photos.
	maxSourcePixels = 24_000_000

	jpegQuality = 82
)

var (
	// ErrUnsupported means the bytes are not a decodable image in a format we
	// handle. Callers map this to a neutral placeholder tile.
	ErrUnsupported = errors.New("thumbnail: unsupported image")

	// ErrTooLarge means the source image's declared dimensions exceed the
	// decode guard. The original is fine; we just will not thumbnail it.
	ErrTooLarge = errors.New("thumbnail: source dimensions too large")
)

// imageExts is the set of raster formats we can both decode and downscale.
// This is intentionally the gallery's definition of "a photo": SVG is left
// out because rasterizing untrusted vector art server-side is a footgun, and
// it is not a photo in any case.
var imageExts = map[string]bool{
	"jpg":  true,
	"jpeg": true,
	"png":  true,
	"gif":  true,
	"webp": true,
	"bmp":  true,
}

// IsImage reports whether a filename looks like a gallery image, by extension.
// The gallery shows exactly the files this returns true for, because they are
// exactly the files Generate can produce a thumbnail from.
func IsImage(name string) bool {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(strings.TrimSpace(name))), ".")
	return imageExts[ext]
}

// Generate decodes src and returns a JPEG thumbnail whose longest edge is at
// most maxEdge, preserving aspect ratio. Images already within maxEdge are
// re-encoded at their original size. Transparent pixels are flattened onto a
// white background so PNG/WebP transparency does not turn black in JPEG.
func Generate(src []byte, maxEdge int) ([]byte, error) {
	if maxEdge <= 0 {
		maxEdge = DefaultMaxEdge
	}

	// Cheap dimension check before the expensive full decode.
	cfg, _, err := image.DecodeConfig(bytes.NewReader(src))
	if err != nil {
		return nil, ErrUnsupported
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, ErrUnsupported
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxSourcePixels {
		return nil, ErrTooLarge
	}

	img, _, err := image.Decode(bytes.NewReader(src))
	if err != nil {
		return nil, ErrUnsupported
	}

	// Downscale first, then bake in any EXIF rotation. Orienting the small
	// thumbnail (not the full-size decode) keeps the transform cheap.
	dst := render(img, maxEdge)
	dst = applyOrientation(dst, exifOrientation(src))

	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// render returns an opaque RGBA image fitted within maxEdge. It always paints
// onto a white background first so alpha channels flatten cleanly.
func render(img image.Image, maxEdge int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	nw, nh := fitWithin(w, h, maxEdge)

	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)

	if nw == w && nh == h {
		draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Over)
		return dst
	}
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
	return dst
}

// fitWithin returns the largest (w, h) preserving aspect ratio with both sides
// at most maxEdge. Inputs at or under maxEdge are returned unchanged.
func fitWithin(w, h, maxEdge int) (int, int) {
	if w <= maxEdge && h <= maxEdge {
		return w, h
	}
	if w >= h {
		nh := int(float64(h) * float64(maxEdge) / float64(w))
		if nh < 1 {
			nh = 1
		}
		return maxEdge, nh
	}
	nw := int(float64(w) * float64(maxEdge) / float64(h))
	if nw < 1 {
		nw = 1
	}
	return nw, maxEdge
}
