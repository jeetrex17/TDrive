package media

import (
	"encoding/binary"
	"io"

	tdcrypto "TDrive/backend/crypto"
)

const (
	// mp4IndexWarmBlocks caps how much of an MP4 index is warmed ahead of the
	// player. 16 MiB covers the moov atom of a feature-length remux; anything
	// larger streams through the read-ahead window as the player walks it.
	mp4IndexWarmBlocks = 16

	// mp4MaxTopLevelAtoms bounds the walk to the handful of atoms that ever
	// precede the media data.
	mp4MaxTopLevelAtoms = 16
)

// warmIndex finds the MP4 container index and pulls it in the background
// while the player is still reading the head. A player cannot show a frame
// before it has the whole index, which lives after the media data unless the
// file was written for streaming, and left to itself it fetches that index
// one block at a time only once the head has arrived.
func (s *Session) warmIndex() {
	if !isMP4Name(s.file.Name) || s.file.StoredSize <= rangeUploadBoundary {
		return
	}
	readAt := func(off int64, n int) ([]byte, error) {
		buf := make([]byte, n)
		read, err := s.ReadAt(s.ctx, buf, off)
		if read < n {
			if err == nil {
				err = io.ErrUnexpectedEOF
			}
			return nil, err
		}
		return buf, nil
	}
	index, ok := mp4IndexOffset(readAt, s.Size())
	if !ok {
		return
	}
	stored := index
	if s.file.Encrypted {
		if stored, ok = tdcrypto.StoredOffset(index); !ok {
			return
		}
	}
	s.prefetchStoredFrom(stored, mp4IndexWarmBlocks)
}

// prefetchStoredFrom warms up to maxBlocks whole blocks of the stored stream,
// starting with the block that contains off and crossing segment boundaries.
func (s *Session) prefetchStoredFrom(off int64, maxBlocks int) {
	for range maxBlocks {
		if off >= s.file.StoredSize {
			return
		}
		seg, ok := s.segmentFor(off)
		if !ok {
			return
		}
		start := blockStartFor(off - seg.start)
		s.reader.prefetchBlock(seg.ref, start)
		off = seg.start + start + rangeUploadBoundary
	}
}

func isMP4Name(name string) bool {
	info, ok := streamTypeForName(name)
	return ok && (info.mime == "video/mp4" || info.mime == "video/quicktime")
}

// mp4IndexOffset walks the top-level atoms of an MP4 through readAt and
// reports where the moov atom starts when it sits after the media data. It
// gives up quietly on anything it does not understand: warming is only an
// optimisation, and a wrong guess would cost a block of traffic.
func mp4IndexOffset(readAt func(off int64, n int) ([]byte, error), size int64) (int64, bool) {
	var pos int64
	for range mp4MaxTopLevelAtoms {
		if pos+8 > size {
			return 0, false
		}
		header, err := readAt(pos, int(min(16, size-pos)))
		if err != nil || len(header) < 8 {
			return 0, false
		}
		atomSize := int64(binary.BigEndian.Uint32(header[:4]))
		headerLen := int64(8)
		switch atomSize {
		case 0:
			// The atom runs to the end of the file, so nothing follows it.
			return 0, false
		case 1:
			if len(header) < 16 {
				return 0, false
			}
			atomSize = int64(binary.BigEndian.Uint64(header[8:16]))
			headerLen = 16
		}
		if atomSize < headerLen {
			return 0, false
		}
		switch string(header[4:8]) {
		case "moov":
			// The index is up front; the head reads the player makes cover it.
			return 0, false
		case "mdat":
			if next := pos + atomSize; next < size {
				return next, true
			}
			return 0, false
		}
		pos += atomSize
	}
	return 0, false
}
