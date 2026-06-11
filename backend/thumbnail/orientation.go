package thumbnail

import (
	"encoding/binary"
	"image"
)

// EXIF orientation handling. Phone cameras commonly store a landscape sensor
// frame plus an orientation tag rather than rotating the pixels, so a portrait
// photo decodes sideways. The browser honors the tag when it renders the full
// image, but our generated JPEG thumbnails would not unless we bake the
// rotation in. We parse just the orientation tag (the one EXIF field we need)
// and apply the matching transform; anything we cannot parse falls back to
// orientation 1 (no change).

const (
	orientationNormal = 1 // also the value we return on any parse failure
	orientationMax    = 8
)

// exifOrientation returns the EXIF orientation (1..8) for a JPEG, or 1 when the
// data is not a JPEG, carries no EXIF, or cannot be parsed. It never panics on
// malformed input: every read is bounds-checked and bails to 1.
func exifOrientation(data []byte) int {
	// JPEG SOI.
	if len(data) < 2 || data[0] != 0xFF || data[1] != 0xD8 {
		return orientationNormal
	}

	// Walk JPEG segments looking for APP1 (0xFFE1) carrying an "Exif\0\0" header.
	i := 2
	for i+4 <= len(data) {
		if data[i] != 0xFF {
			return orientationNormal // not at a marker; give up
		}
		marker := data[i+1]
		// Standalone markers (RSTn, SOI, EOI) have no length payload; SOS (0xDA)
		// begins entropy-coded data we will not scan past.
		if marker == 0xD9 || marker == 0xDA {
			return orientationNormal
		}
		segLen := int(binary.BigEndian.Uint16(data[i+2 : i+4]))
		if segLen < 2 {
			return orientationNormal
		}
		segStart := i + 4
		segEnd := i + 2 + segLen
		if segEnd > len(data) {
			return orientationNormal
		}
		if marker == 0xE1 && segStart+6 <= segEnd && string(data[segStart:segStart+4]) == "Exif" {
			if o, ok := orientationFromTIFF(data[segStart+6 : segEnd]); ok {
				return o
			}
			return orientationNormal
		}
		i = segEnd
	}
	return orientationNormal
}

// orientationFromTIFF parses the TIFF block that follows the "Exif\0\0" header
// and returns the IFD0 Orientation tag (0x0112). Bounds are checked throughout.
func orientationFromTIFF(tiff []byte) (int, bool) {
	if len(tiff) < 8 {
		return 0, false
	}
	var order binary.ByteOrder
	switch string(tiff[0:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 0, false
	}
	if order.Uint16(tiff[2:4]) != 0x002A {
		return 0, false
	}

	ifdOffset := int(order.Uint32(tiff[4:8]))
	if ifdOffset < 8 || ifdOffset+2 > len(tiff) {
		return 0, false
	}
	count := int(order.Uint16(tiff[ifdOffset : ifdOffset+2]))
	entry := ifdOffset + 2
	const entrySize = 12
	for n := 0; n < count; n++ {
		if entry+entrySize > len(tiff) {
			return 0, false
		}
		tag := order.Uint16(tiff[entry : entry+2])
		if tag == 0x0112 { // Orientation, stored as a SHORT in the entry's value slot
			v := int(order.Uint16(tiff[entry+8 : entry+10]))
			if v >= orientationNormal && v <= orientationMax {
				return v, true
			}
			return 0, false
		}
		entry += entrySize
	}
	return 0, false
}

// applyOrientation returns img transformed so that orientation 1 (upright) is
// the result. Orientations 5..8 swap width and height. The input is already a
// downscaled thumbnail, so the per-pixel copy is cheap.
func applyOrientation(img image.Image, orientation int) image.Image {
	if orientation <= orientationNormal || orientation > orientationMax {
		return img
	}

	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if orientation >= 5 { // transpose/rotate cases swap the axes
		dw, dh = h, w
	}

	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := img.At(b.Min.X+x, b.Min.Y+y)
			dx, dy := orientedCoord(orientation, x, y, w, h)
			dst.Set(dx, dy, c)
		}
	}
	return dst
}

// orientedCoord maps a source pixel (x, y) to its destination for the given
// EXIF orientation. Values follow the EXIF spec (1 = upright).
func orientedCoord(orientation, x, y, w, h int) (int, int) {
	switch orientation {
	case 2: // mirror horizontal
		return w - 1 - x, y
	case 3: // rotate 180
		return w - 1 - x, h - 1 - y
	case 4: // mirror vertical
		return x, h - 1 - y
	case 5: // mirror horizontal, then rotate 270 CW (transpose)
		return y, x
	case 6: // rotate 90 CW
		return h - 1 - y, x
	case 7: // mirror horizontal, then rotate 90 CW (transverse)
		return h - 1 - y, w - 1 - x
	case 8: // rotate 270 CW
		return y, w - 1 - x
	default:
		return x, y
	}
}
