package remux

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"math"
	"math/bits"
)

// ebmlBuffer is sized for the two things the reader does: pulling a few hundred
// bytes of header fields, and streaming a cluster's frames. A range read over
// the network has a fixed cost whatever its length, so the buffer is far larger
// than bufio's default.
const ebmlBuffer = 64 << 10

// ebmlReader walks EBML elements over a Source, buffering so a run of small
// fields costs one range read rather than one per field, and tracking the
// absolute file offset because every position Matroska records is a file offset
// once the Segment's own start is added.
type ebmlReader struct {
	ctx context.Context
	src Source
	br  *bufio.Reader
	pos int64
}

func newEBMLReader(ctx context.Context, src Source, off int64) *ebmlReader {
	r := &ebmlReader{ctx: ctx, src: src}
	r.seek(off)
	return r
}

// seek re-seats the reader, throwing the buffer away. Skipping forward uses it
// whenever the distance exceeds what is already buffered: over a network source
// reading bytes in order to discard them is the expensive mistake.
func (r *ebmlReader) seek(off int64) {
	section := sectionOf(r.ctx, r.src, off, r.src.Size()-off)
	if r.br == nil {
		r.br = bufio.NewReaderSize(section, ebmlBuffer)
	} else {
		r.br.Reset(section)
	}
	r.pos = off
}

func (r *ebmlReader) skip(n int64) {
	switch {
	case n <= 0:
		return
	case n <= int64(r.br.Buffered()):
		_, _ = r.br.Discard(int(n))
		r.pos += n
	default:
		r.seek(r.pos + n)
	}
}

func (r *ebmlReader) readByte() (byte, error) {
	b, err := r.br.ReadByte()
	if err != nil {
		return 0, err
	}
	r.pos++
	return b, nil
}

func (r *ebmlReader) readFull(buf []byte) error {
	if _, err := io.ReadFull(r.br, buf); err != nil {
		return err
	}
	r.pos += int64(len(buf))
	return nil
}

// element reads the next element header. A size of -1 means the element
// declares an unknown length, which live muxers write for the Segment and for
// clusters and which readers are required to accept.
func (r *ebmlReader) element() (uint32, int64, error) {
	first, err := r.readByte()
	if err != nil {
		return 0, 0, err
	}
	// An element ID keeps its length marker, unlike every other EBML integer:
	// the marker is what makes IDs of different widths unambiguous.
	width := bits.LeadingZeros8(first) + 1
	if width > 4 {
		return 0, 0, errCorrupt
	}
	id := uint32(first)
	for range width - 1 {
		b, err := r.readByte()
		if err != nil {
			return 0, 0, err
		}
		id = id<<8 | uint32(b)
	}

	value, width, err := r.vint()
	if err != nil {
		return 0, 0, err
	}
	if value == uint64(1)<<(7*width)-1 {
		return id, -1, nil
	}
	return id, int64(value), nil
}

// vint reads an EBML variable length integer, dropping its length marker, and
// reports how many bytes it spanned.
func (r *ebmlReader) vint() (uint64, int, error) {
	first, err := r.readByte()
	if err != nil {
		return 0, 0, err
	}
	width := bits.LeadingZeros8(first) + 1
	if width > 8 {
		return 0, 0, errCorrupt
	}
	value := uint64(first) & (uint64(1)<<(8-width) - 1)
	for range width - 1 {
		b, err := r.readByte()
		if err != nil {
			return 0, 0, err
		}
		value = value<<8 | uint64(b)
	}
	return value, width, nil
}

// walk reads the children of an element ending at end, handing each to visit.
// visit consumes as much of an element's payload as it wants and walk steps
// over the rest, so a reader only pays for the fields it cares about.
func (r *ebmlReader) walk(end int64, visit func(id uint32, size int64) error) error {
	for r.pos < end {
		start := r.pos
		id, size, err := r.element()
		if err != nil {
			return err
		}
		if size < 0 || r.pos+size > end {
			return errCorrupt
		}
		stop := r.pos + size
		if err := visit(id, size); err != nil {
			return err
		}
		if r.pos < stop {
			r.skip(stop - r.pos)
		}
		if r.pos <= start {
			return errCorrupt
		}
	}
	return nil
}

// uintValue reads an unsigned integer of the element's own width, which EBML
// stores big endian in as few bytes as it needs.
func (r *ebmlReader) uintValue(size int64) (uint64, error) {
	if size < 0 || size > 8 {
		return 0, errCorrupt
	}
	var buf [8]byte
	if err := r.readFull(buf[:size]); err != nil {
		return 0, err
	}
	var value uint64
	for _, b := range buf[:size] {
		value = value<<8 | uint64(b)
	}
	return value, nil
}

func (r *ebmlReader) floatValue(size int64) (float64, error) {
	var buf [8]byte
	switch size {
	case 0:
		return 0, nil
	case 4:
		if err := r.readFull(buf[:4]); err != nil {
			return 0, err
		}
		return float64(math.Float32frombits(binary.BigEndian.Uint32(buf[:4]))), nil
	case 8:
		if err := r.readFull(buf[:8]); err != nil {
			return 0, err
		}
		return math.Float64frombits(binary.BigEndian.Uint64(buf[:8])), nil
	default:
		return 0, errCorrupt
	}
}

func (r *ebmlReader) binaryValue(size int64) ([]byte, error) {
	if size < 0 || size > maxElementValue {
		return nil, errCorrupt
	}
	buf := make([]byte, size)
	if err := r.readFull(buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func (r *ebmlReader) stringValue(size int64) (string, error) {
	buf, err := r.binaryValue(size)
	if err != nil {
		return "", err
	}
	// Matroska pads strings with trailing zeroes rather than trimming the
	// element, so a codec ID compared byte for byte would never match.
	return string(bytes.TrimRight(buf, "\x00")), nil
}

// readVInt reads an EBML variable length integer out of a buffer, for the one
// place that holds the bytes already: the lacing header inside a block.
func readVInt(buf []byte) (uint64, int, bool) {
	if len(buf) == 0 {
		return 0, 0, false
	}
	width := bits.LeadingZeros8(buf[0]) + 1
	if width > 8 || width > len(buf) {
		return 0, 0, false
	}
	value := uint64(buf[0]) & (uint64(1)<<(8-width) - 1)
	for _, b := range buf[1:width] {
		value = value<<8 | uint64(b)
	}
	return value, width, true
}

// readSignedVInt reads the signed form EBML lacing uses for its size
// differences, which is the unsigned value biased by half its range.
func readSignedVInt(buf []byte) (int64, int, bool) {
	value, width, ok := readVInt(buf)
	if !ok {
		return 0, 0, false
	}
	return int64(value) - (int64(1)<<(7*width-1) - 1), width, true
}
