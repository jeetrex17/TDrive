package remux

import (
	"context"
	"errors"
	"io"
)

// Source is the file being remuxed, read in ranges rather than as a whole.
//
// This shape is the point of the package. The bytes live in a Telegram channel
// and arrive on demand, so nothing here may assume the file is local or that it
// fits in memory: a two-hour remux is tens of gigabytes. Every read is a range
// read, and the segment plan exists so each one maps to a single contiguous
// stretch of the source.
type Source interface {
	// ReadAt fills buf from off. It follows io.ReaderAt semantics: a short read
	// returns an error, and io.EOF means the file ended inside the range.
	ReadAt(ctx context.Context, buf []byte, off int64) (int, error)
	// Size is the length of the file in bytes.
	Size() int64
}

// ErrUnsupported reports a file this package will not remux: an unrecognised
// container, a track pairing iOS cannot take, or an encoding feature that
// cannot be copied. It is deliberately distinct from a read failure, because
// the caller answers the two differently: one is "this file cannot play here",
// the other is "try again".
var ErrUnsupported = errors.New("remux: source cannot be repackaged for this platform")

// ErrNoPlayableTrack reports a file whose video can be copied but whose audio
// cannot, or which has no copyable track at all.
//
// This is worth its own error because it is the common Blu-ray remux case:
// video that iOS decodes happily alongside DTS or TrueHD that it cannot. The
// caller should say so rather than play a silent film.
var ErrNoPlayableTrack = errors.New("remux: no audio track that this platform can decode")

// rangeReader adapts a Source to io.ReaderAt for the duration of one call,
// binding the context so a cancelled request stops pulling bytes.
type rangeReader struct {
	ctx context.Context
	src Source
}

func (r rangeReader) ReadAt(buf []byte, off int64) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.src.ReadAt(r.ctx, buf, off)
}

// readerAt binds a Source and a context into the standard interface the
// container parsers expect.
func readerAt(ctx context.Context, src Source) io.ReaderAt {
	return rangeReader{ctx: ctx, src: src}
}

// sectionOf returns a reader over one stretch of the source, which is how a
// segment is produced: the plan names a byte range and the demuxer walks only
// that.
func sectionOf(ctx context.Context, src Source, off, length int64) *io.SectionReader {
	return io.NewSectionReader(readerAt(ctx, src), off, length)
}
