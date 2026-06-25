package nativeplayer

import (
	"bytes"
	"fmt"
	"image"

	// Register the decoders for the formats the seek-thumbnail generator emits.
	_ "image/jpeg"
	_ "image/png"

	xdraw "golang.org/x/image/draw"
)

// overlayBitmap is a decoded, scaled seek-thumbnail ready to be handed to a
// Windows DIB section. Pixels are 32-bit BGRA, top-down (row 0 is the top),
// tightly packed at a stride of Width*4 bytes. Width and Height are in physical
// pixels. The format matches a BITMAPINFOHEADER with a negative height, so the
// Windows side can copy Pixels straight into a CreateDIBSection buffer.
//
// This type and composeSeekThumbnail are deliberately platform-neutral so the
// image pipeline is unit-testable off Windows; only the DIB upload is OS code.
type overlayBitmap struct {
	Width  int
	Height int
	Pixels []byte
}

// maxOverlayDimension bounds the rendered overlay so a malformed size can never
// allocate an unreasonable buffer. The seek preview is a small thumbnail; this
// is purely a safety ceiling, not the expected size.
const maxOverlayDimension = 2048

// composeSeekThumbnail decodes a JPEG/PNG seek-thumbnail and renders it into a
// width×height BGRA buffer. The source thumbnail already carries the video's
// aspect ratio, so it is scaled directly to the requested preview size with a
// high-quality kernel. width and height are physical pixels.
func composeSeekThumbnail(data []byte, width, height int) (*overlayBitmap, error) {
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("native overlay: invalid size %dx%d", width, height)
	}
	if width > maxOverlayDimension || height > maxOverlayDimension {
		return nil, fmt.Errorf("native overlay: size %dx%d exceeds limit", width, height)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("native overlay: empty thumbnail")
	}

	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("native overlay: decode thumbnail: %w", err)
	}

	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Src, nil)

	// image.NewRGBA is top-down RGBA; convert in place to the BGRA byte order a
	// Windows 32bpp DIB expects. Seek thumbnails are opaque, but we copy alpha
	// through so a future translucent source still renders correctly.
	pix := dst.Pix
	for i := 0; i+3 < len(pix); i += 4 {
		pix[i], pix[i+2] = pix[i+2], pix[i]
	}

	return &overlayBitmap{Width: width, Height: height, Pixels: pix}, nil
}
