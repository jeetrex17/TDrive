package remux

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"
)

// Samples returns one track's frames from the clusters in a byte range.
//
// The range comes from the cue index, so one segment of output is one
// contiguous stretch of the source and costs one read. Blocks belonging to
// other tracks are stepped over without being fetched, which is what stops the
// audio pass from dragging the video through memory: the caller runs this once
// per track over the same range.
//
// Nothing is remembered between calls. Segments are produced in whatever order
// a player asks for them, and a seek jumps straight into the middle of a file.
func (f *File) Samples(ctx context.Context, src Source, track Track, byteRange Range) ([]Sample, error) {
	if f.encodings[track.Number].refuse {
		return nil, fmt.Errorf("remux: track %d is compressed or encrypted: %w", track.Number, ErrUnsupported)
	}

	end := min(byteRange.End, f.dataEnd)
	r := newEBMLReader(ctx, src, byteRange.Start)
	var samples []Sample
	for r.pos < end {
		id, size, err := r.element()
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			return nil, fmt.Errorf("remux: walking clusters: %w", err)
		}
		if id != idCluster {
			if size < 0 {
				break
			}
			r.skip(size)
			continue
		}
		// A cluster that does not declare its size runs to the next top level
		// element, which readCluster works out for itself.
		clusterEnd := end
		if size >= 0 {
			clusterEnd = min(r.pos+size, end)
		}
		if err := f.readCluster(r, clusterEnd, track, &samples); err != nil {
			return nil, err
		}
	}
	return samples, nil
}

// readCluster appends one cluster's samples for a single track.
func (f *File) readCluster(r *ebmlReader, end int64, track Track, samples *[]Sample) error {
	var clusterTicks uint64
	for r.pos < end {
		start := r.pos
		id, size, err := r.element()
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil
			}
			return fmt.Errorf("remux: walking a cluster: %w", err)
		}
		if isSegmentLevel(id) {
			// A cluster that declared no size ends where the next top level
			// element begins, which is the only way a live muxed file can be
			// walked at all. Hand it back to the caller unread.
			r.seek(start)
			return nil
		}
		if size < 0 {
			return fmt.Errorf("remux: element %#x inside a cluster has no size: %w", id, ErrUnsupported)
		}
		stop := r.pos + size
		switch id {
		case idClusterTimestamp:
			if clusterTicks, err = r.uintValue(size); err != nil {
				return fmt.Errorf("remux: reading a cluster timestamp: %w", err)
			}
		case idSimpleBlock:
			parsed, ok, err := readBlock(r, stop, track.Number)
			if err != nil {
				return fmt.Errorf("remux: reading a simple block: %w", err)
			}
			if ok {
				f.emit(track, parsed, clusterTicks, 0, samples)
			}
		case idBlockGroup:
			if err := f.readBlockGroup(r, stop, track, clusterTicks, samples); err != nil {
				return err
			}
		}
		if r.pos < stop {
			r.skip(stop - r.pos)
		}
	}
	return nil
}

// readBlockGroup reads the wrapped form of a block.
//
// It exists because a Block carries no keyframe flag of its own: inside a
// BlockGroup a frame is a keyframe exactly when nothing references an earlier
// one, and that reference is allowed to be written after the block. So the
// group has to be read whole before anything can be emitted. Encoders that
// write only SimpleBlocks are common enough that this path is easy to leave
// untested and then discover on a file whose every frame looks like a keyframe.
func (f *File) readBlockGroup(r *ebmlReader, end int64, track Track, clusterTicks uint64, samples *[]Sample) error {
	var parsed block
	var ticks uint64
	mine, referenced := false, false

	err := r.walk(end, func(id uint32, size int64) error {
		switch id {
		case idBlock:
			found, ok, err := readBlock(r, r.pos+size, track.Number)
			parsed, mine = found, ok
			return err
		case idBlockDuration:
			value, err := r.uintValue(size)
			ticks = value
			return err
		case idReferenceBlock:
			referenced = true
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("remux: reading a block group: %w", err)
	}
	if !mine {
		return nil
	}
	parsed.sync = !referenced
	f.emit(track, parsed, clusterTicks, int64(ticks)*int64(f.timestampScale), samples)
	return nil
}

// block is one parsed SimpleBlock or Block.
type block struct {
	// frames are subslices of one payload read, so a laced block costs a single
	// allocation rather than one per frame.
	frames [][]byte
	// relative is the block's offset from its cluster's timestamp, in the
	// segment's ticks. It is signed: a frame may be shown before the cluster it
	// is stored in nominally starts.
	relative int16
	sync     bool
}

// readBlock parses a block's header and frames, or reports that the block
// belongs to another track after reading only its track number.
func readBlock(r *ebmlReader, end int64, number uint64) (block, bool, error) {
	got, _, err := r.vint()
	if err != nil {
		return block{}, false, err
	}
	if got != number {
		return block{}, false, nil
	}
	if end-r.pos < 3 || end-r.pos > maxBlockSize {
		return block{}, false, errCorrupt
	}

	payload := make([]byte, end-r.pos)
	if err := r.readFull(payload); err != nil {
		return block{}, false, err
	}
	frames, err := splitLaced(payload[3:], payload[2])
	if err != nil {
		return block{}, false, err
	}
	return block{
		frames:   frames,
		relative: int16(binary.BigEndian.Uint16(payload[:2])),
		// Only a SimpleBlock states this. Inside a BlockGroup the bit is spare
		// and the caller overwrites it from the reference instead.
		sync: payload[2]&0x80 != 0,
	}, true, nil
}

// maxBlockSize bounds one block's allocation. A single coded frame runs to a
// few megabytes at most, even for a lossless intra codec.
const maxBlockSize = 64 << 20

// emit converts a block's frames into samples in the track's own timescale.
//
// The block's timestamp is taken as written. CodecDelay, which ffmpeg attaches
// to anything with encoder pre-roll, is deliberately not subtracted: expressing
// it in MP4 needs an edit list, and what ignoring it leaves behind is 5 ms on
// AC-3 and 21 ms on AAC, both well inside what anyone can see against the
// picture. That is a decision rather than an oversight.
func (f *File) emit(track Track, b block, clusterTicks uint64, blockDuration int64, samples *[]Sample) {
	base := (int64(clusterTicks) + int64(b.relative)) * int64(f.timestampScale)

	// Laced frames share one timestamp on disk and are spread across the block
	// by the track's nominal frame interval. Without one the block's own
	// duration divides evenly, which is the fallback the specification names.
	spacing := int64(track.DefaultDuration)
	if spacing <= 0 && len(b.frames) > 1 && blockDuration > 0 {
		spacing = blockDuration / int64(len(b.frames))
	}
	duration := spacing
	if blockDuration > 0 && len(b.frames) == 1 {
		duration = blockDuration
	}

	prefix := f.encodings[track.Number].prefix
	for i, frame := range b.frames {
		*samples = append(*samples, Sample{
			Data:     restoreHeader(prefix, frame),
			PTS:      ticksOf(base+int64(i)*spacing, track.Timescale),
			Duration: ticksOf(duration, track.Timescale),
			Sync:     b.sync,
		})
	}
}

// restoreHeader puts back the bytes header stripping removed.
//
// The frame gets a fresh array rather than growing in place, because a laced
// block's frames are slices of one read that sit end to end: writing a prefix
// in front of one would write over the one before it.
func restoreHeader(prefix, frame []byte) []byte {
	if len(prefix) == 0 {
		return frame
	}
	whole := make([]byte, 0, len(prefix)+len(frame))
	return append(append(whole, prefix...), frame...)
}

// ticksOf converts a nanosecond instant or span into a track's own ticks.
//
// Rounding rather than truncating matters here. The value arriving has already
// been quantised once, to Matroska's millisecond grid, and truncating on top of
// that would push every frame consistently early instead of leaving the error
// inside half a tick.
func ticksOf(ns int64, timescale uint32) int64 {
	scale := int64(timescale)
	half := int64(time.Second) / 2
	if ns < 0 {
		return -((-ns*scale + half) / int64(time.Second))
	}
	return (ns*scale + half) / int64(time.Second)
}

// splitLaced divides a block's payload into its frames.
//
// Lacing packs several frames into one block to save the header bytes, and
// three incompatible ways of writing the sizes are all in use. Audio blocks use
// it routinely and video blocks effectively never do, so a reader that handled
// only the unlaced case would carry the picture perfectly and lose most of the
// sound.
func splitLaced(payload []byte, flags byte) ([][]byte, error) {
	switch (flags >> 1) & 0x03 {
	case 0:
		return [][]byte{payload}, nil
	case 1:
		return splitXiph(payload)
	case 2:
		return splitFixed(payload)
	default:
		return splitEBML(payload)
	}
}

// splitXiph reads sizes written as runs of 255, the scheme Ogg uses.
func splitXiph(payload []byte) ([][]byte, error) {
	count, payload, err := laceCount(payload)
	if err != nil {
		return nil, err
	}
	sizes := make([]int, count)
	for i := range count - 1 {
		size := 0
		for {
			if len(payload) == 0 {
				return nil, errCorrupt
			}
			step := int(payload[0])
			payload = payload[1:]
			size += step
			if step != 0xFF {
				break
			}
		}
		sizes[i] = size
	}
	return cutFrames(payload, sizes)
}

// splitFixed reads the scheme that writes no sizes at all because every frame
// is the same length, which is what constant bitrate audio produces.
func splitFixed(payload []byte) ([][]byte, error) {
	count, payload, err := laceCount(payload)
	if err != nil {
		return nil, err
	}
	if len(payload)%count != 0 {
		return nil, errCorrupt
	}
	size := len(payload) / count
	frames := make([][]byte, count)
	for i := range count {
		frames[i] = payload[i*size : (i+1)*size]
	}
	return frames, nil
}

// splitEBML reads sizes written as a first value and then signed differences,
// which is what makes this scheme cheaper than Xiph for frames of similar size.
func splitEBML(payload []byte) ([][]byte, error) {
	count, payload, err := laceCount(payload)
	if err != nil {
		return nil, err
	}
	if count == 1 {
		return [][]byte{payload}, nil
	}
	sizes := make([]int, count)
	first, width, ok := readVInt(payload)
	if !ok {
		return nil, errCorrupt
	}
	sizes[0] = int(first)
	payload = payload[width:]
	for i := 1; i < count-1; i++ {
		delta, width, ok := readSignedVInt(payload)
		if !ok {
			return nil, errCorrupt
		}
		sizes[i] = sizes[i-1] + int(delta)
		payload = payload[width:]
	}
	return cutFrames(payload, sizes)
}

// laceCount reads the frame count every lacing scheme opens with, stored one
// less than the real number because a lace of none makes no sense.
func laceCount(payload []byte) (int, []byte, error) {
	if len(payload) == 0 {
		return 0, nil, errCorrupt
	}
	return int(payload[0]) + 1, payload[1:], nil
}

// cutFrames hands each frame its share of the payload. The last frame takes
// whatever the sizes did not account for, which is how every scheme stores it.
func cutFrames(payload []byte, sizes []int) ([][]byte, error) {
	frames := make([][]byte, len(sizes))
	off := 0
	for i := range len(sizes) - 1 {
		if sizes[i] < 0 || off+sizes[i] > len(payload) {
			return nil, errCorrupt
		}
		frames[i] = payload[off : off+sizes[i]]
		off += sizes[i]
	}
	frames[len(sizes)-1] = payload[off:]
	return frames, nil
}

// isSegmentLevel reports whether an ID belongs directly to the Segment, which
// is how a cluster that declared no length is known to have ended.
func isSegmentLevel(id uint32) bool {
	switch id {
	case idSeekHead, idInfo, idTracks, idChapters, idCluster, idCues, idAttachments, idTags:
		return true
	}
	return false
}
