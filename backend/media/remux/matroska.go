package remux

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"math/bits"
	"slices"
	"time"
)

// Matroska is walked by hand here rather than through an EBML library.
//
// The libraries available unmarshal an element into a Go struct by reflection,
// which means asking one for a Segment asks it to hold every cluster in the
// file at once. This package exists because the file does not fit in memory and
// arrives over the network a range at a time, so the whole point is to touch a
// few kilobytes of header and then one stretch of clusters. What is left of
// EBML after that is a variable length integer and a two field element header,
// which is less code than the glue needed to keep a library off the clusters.

// EBML element IDs, written the way the specification quotes them: the length
// marker is part of the ID rather than stripped from it, so these are the bytes
// that appear in the file.
const (
	idEBMLHead = 0x1A45DFA3
	idDocType  = 0x4282

	idSegment = 0x18538067

	idSeekHead     = 0x114D9B74
	idSeek         = 0x4DBB
	idSeekID       = 0x53AB
	idSeekPosition = 0x53AC

	idInfo           = 0x1549A966
	idTimestampScale = 0x2AD7B1
	idDuration       = 0x4489

	idTracks               = 0x1654AE6B
	idTrackEntry           = 0xAE
	idTrackNumber          = 0xD7
	idTrackType            = 0x83
	idDefaultDuration      = 0x23E383
	idCodecID              = 0x86
	idCodecPrivate         = 0x63A2
	idTrackName            = 0x536E
	idLanguage             = 0x22B59C
	idVideo                = 0xE0
	idPixelWidth           = 0xB0
	idPixelHeight          = 0xBA
	idAudio                = 0xE1
	idSamplingFrequency    = 0xB5
	idChannels             = 0x9F
	idContentEncodings     = 0x6D80
	idContentEncoding      = 0x6240
	idContentEncodingScope = 0x5032
	idContentEncodingType  = 0x5033
	idContentCompression   = 0x5034
	idContentCompAlgo      = 0x4254
	idContentCompSettings  = 0x4255

	idChapters    = 0x1043A770
	idAttachments = 0x1941A469
	idTags        = 0x1254C367

	idCues               = 0x1C53BB6B
	idCuePoint           = 0xBB
	idCueTime            = 0xB3
	idCueTrackPositions  = 0xB7
	idCueClusterPosition = 0xF1

	idCluster          = 0x1F43B675
	idClusterTimestamp = 0xE7
	idSimpleBlock      = 0xA3
	idBlockGroup       = 0xA0
	idBlock            = 0xA1
	idBlockDuration    = 0x9B
	idReferenceBlock   = 0xFB
)

const (
	trackTypeVideo = 1
	trackTypeAudio = 2
)

// Matroska's own defaults, which apply when the element is simply absent. They
// are not arbitrary fallbacks: a file that omits TimestampScale means one
// millisecond, and reading it as zero would put every frame at time zero.
const (
	defaultTimestampScale = 1_000_000
	defaultSampleRate     = 8000
	defaultLanguage       = "eng"
)

// defaultVideoTimescale is what a video track gets when the file declares no
// frame interval, which is the case for anything variable frame rate. 90000 is
// the MPEG systems clock and divides the common rates cleanly enough.
const defaultVideoTimescale = 90000

// maxElementValue bounds what a single string or binary element may allocate.
// CodecPrivate is the largest one that matters and runs to a few kilobytes; a
// size larger than this came from a corrupt header, not from a real file.
const maxElementValue = 1 << 20

// maxScannedClusters bounds the fallback index. Each cluster holds a few
// seconds, so this covers a very long film; past it a file with no cues is
// better served unseekable than by tens of thousands of range reads.
const maxScannedClusters = 20000

// errCorrupt wraps the package sentinel so callers can answer a malformed file
// the same way they answer an unsupported one. Both mean "this will not play",
// which is a different answer from "the network failed, try again".
var errCorrupt = fmt.Errorf("remux: malformed matroska element: %w", ErrUnsupported)

// File is the parsed head of a Matroska file: enough to describe the tracks and
// to turn a time range into a byte range, and nothing that requires holding a
// frame in memory.
type File struct {
	tracks         []Track
	cues           []CuePoint
	duration       time.Duration
	timestampScale uint64
	// encodings holds what has to be done to a track's frames before they are
	// worth anything, keyed by track number. Nothing about it belongs in Track,
	// which describes a stream rather than how the container mangled it.
	encodings map[uint64]encoding
	// dataStart is where the Segment's children begin. Every position Matroska
	// records inside a Segment, in the SeekHead and in the cue index alike, is
	// relative to this rather than to the file.
	dataStart  int64
	dataEnd    int64
	clusterEnd int64
}

// Tracks lists the video and audio tracks. Subtitles are left out: they need
// their own rendition and a conversion to WebVTT, neither of which a sample
// carrier can express.
func (f *File) Tracks() []Track { return f.tracks }

// Duration is what the file declares, which is what the playlist advertises.
func (f *File) Duration() time.Duration { return f.duration }

// TimestampScale is how many nanoseconds one of Matroska's own ticks is worth.
func (f *File) TimestampScale() uint64 { return f.timestampScale }

// Cues is the index, ordered by time, each entry naming a cluster that starts
// with a keyframe. ClusterOffset is an absolute file offset: Matroska stores it
// relative to the Segment, and the Segment's own start is added here so callers
// never have to know that.
func (f *File) Cues() []CuePoint { return f.cues }

// ClusterEnd is where the media data stops, and so where the last segment's
// byte range runs to. Cues and tags are normally written after the clusters, so
// this is usually the start of the Cues element rather than the end of the
// Segment.
func (f *File) ClusterEnd() int64 { return f.clusterEnd }

// Probe reads the head of a Matroska file: its tracks, its duration and its cue
// index.
//
// It deliberately never walks the clusters. The index almost always sits after
// them, which over a network source would mean pulling the whole file to find
// it, so the SeekHead at the front is followed straight to it instead. A file
// with neither cues nor a SeekHead falls back to reading cluster headers, which
// costs one small read per cluster rather than one per file.
func Probe(ctx context.Context, src Source) (*File, error) {
	r := newEBMLReader(ctx, src, 0)
	if err := readEBMLHead(r); err != nil {
		return nil, err
	}

	id, size, err := r.element()
	if err != nil || id != idSegment {
		return nil, fmt.Errorf("remux: no segment after the EBML header: %w", ErrUnsupported)
	}
	file := &File{
		timestampScale: defaultTimestampScale,
		encodings:      map[uint64]encoding{},
		dataStart:      r.pos,
		dataEnd:        src.Size(),
	}
	if size >= 0 && r.pos+size < file.dataEnd {
		file.dataEnd = r.pos + size
	}

	// Where the top level elements live, absolute, as named by the SeekHead.
	// What sits before the first cluster is read on the way past it instead,
	// because reading forward through one buffer is the whole saving: seeking
	// back to an element already fetched would cost a second range read.
	at := map[uint32]int64{}
	firstCluster := int64(-1)
	haveInfo, haveTracks := false, false
	for r.pos < file.dataEnd {
		start := r.pos
		id, size, err := r.element()
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			return nil, fmt.Errorf("remux: walking the segment: %w", err)
		}
		if id == idCluster {
			firstCluster = start
			break
		}
		if size < 0 {
			return nil, fmt.Errorf("remux: top level element %#x has no size: %w", id, ErrUnsupported)
		}
		stop := r.pos + size
		switch id {
		case idSeekHead:
			if err := readSeekHead(r, stop, file.dataStart, at, 1); err != nil {
				return nil, err
			}
		case idInfo:
			if err := file.readInfo(r, stop); err != nil {
				return nil, err
			}
			haveInfo = true
		case idTracks:
			if err := file.readTracks(r, stop); err != nil {
				return nil, err
			}
			haveTracks = true
		default:
			if _, seen := at[id]; !seen {
				at[id] = start
			}
		}
		r.skip(stop - r.pos)
	}
	if firstCluster < 0 {
		return nil, fmt.Errorf("remux: segment holds no clusters: %w", ErrUnsupported)
	}

	// Info has to be settled before the cues are read, because a cue time is
	// counted in the scale Info declares.
	if !haveInfo {
		if pos, ok := at[idInfo]; ok {
			if err := readAt(r, pos, idInfo, file.readInfo); err != nil {
				return nil, err
			}
		}
	}
	if !haveTracks {
		pos, ok := at[idTracks]
		if !ok {
			return nil, fmt.Errorf("remux: segment declares no tracks: %w", ErrUnsupported)
		}
		if err := readAt(r, pos, idTracks, file.readTracks); err != nil {
			return nil, err
		}
	}
	if len(file.tracks) == 0 {
		return nil, ErrNoPlayableTrack
	}

	// Anything written after the clusters ends the media data, which is what
	// the last segment's byte range has to stop at. Cues are the usual one,
	// tags and attachments the rest.
	file.clusterEnd = file.dataEnd
	for _, pos := range at {
		if pos > firstCluster && pos < file.clusterEnd {
			file.clusterEnd = pos
		}
	}

	if pos, ok := at[idCues]; ok {
		// A seek head that points at cues which are not there, because the file
		// was truncated after its clusters, must not cost the whole file: the
		// index below can be rebuilt without it.
		if err := readAt(r, pos, idCues, file.readCues); err != nil {
			file.cues = nil
		}
	}
	if len(file.cues) == 0 {
		file.cues = scanClusters(r, firstCluster, file.clusterEnd, file.timestampScale)
	}
	if len(file.cues) == 0 {
		// Without an index the file still plays, from the top, as one long
		// segment. That beats refusing it.
		file.cues = []CuePoint{{ClusterOffset: firstCluster}}
	}
	return file, nil
}

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

// readEBMLHead answers "is this even a Matroska file" before a single cluster
// is touched, which is the cheapest question the caller asks.
func readEBMLHead(r *ebmlReader) error {
	id, size, err := r.element()
	if err != nil || id != idEBMLHead || size < 0 {
		return fmt.Errorf("remux: no EBML header at the start of the file: %w", ErrUnsupported)
	}
	docType := ""
	err = r.walk(r.pos+size, func(id uint32, size int64) error {
		if id != idDocType {
			return nil
		}
		docType, err = r.stringValue(size)
		return err
	})
	if err != nil {
		// A file that runs out inside its own header is not a Matroska file
		// that failed to read, it is something else entirely.
		return fmt.Errorf("remux: reading the EBML header: %w: %w", err, ErrUnsupported)
	}
	// WebM is a Matroska profile with a shorter element list, so it parses here
	// identically and is worth accepting rather than turning away.
	if docType != "matroska" && docType != "webm" {
		return fmt.Errorf("remux: EBML doctype %q is not matroska: %w", docType, ErrUnsupported)
	}
	return nil
}

// readSeekHead records where the top level elements live. Following it is what
// keeps Probe off the clusters: the index it points at is almost always past
// the end of the media data.
func readSeekHead(r *ebmlReader, end, dataStart int64, at map[uint32]int64, depth int) error {
	var nested int64 = -1
	err := r.walk(end, func(id uint32, size int64) error {
		if id != idSeek {
			return nil
		}
		var target uint32
		var position int64 = -1
		err := r.walk(r.pos+size, func(id uint32, size int64) error {
			switch id {
			case idSeekID:
				raw, err := r.binaryValue(size)
				if err != nil || len(raw) == 0 || len(raw) > 4 {
					return errCorrupt
				}
				for _, b := range raw {
					target = target<<8 | uint32(b)
				}
			case idSeekPosition:
				value, err := r.uintValue(size)
				if err != nil {
					return err
				}
				position = dataStart + int64(value)
			}
			return nil
		})
		if err != nil || position < 0 || target == 0 {
			return err
		}
		if target == idSeekHead {
			// Some muxers leave a stub at the front whose only job is to name
			// the real head, written after the clusters.
			nested = position
			return nil
		}
		if _, seen := at[target]; !seen {
			at[target] = position
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("remux: reading the seek head: %w", err)
	}
	if nested < 0 || depth <= 0 {
		return nil
	}
	r.seek(nested)
	id, size, err := r.element()
	if err != nil || id != idSeekHead || size < 0 {
		return nil
	}
	return readSeekHead(r, r.pos+size, dataStart, at, depth-1)
}

// readAt re-seats the reader on a top level element the SeekHead named and
// reads it there, for the elements that sit past the clusters. Cues almost
// always do, which is the one seek Probe is willing to pay for.
func readAt(r *ebmlReader, off int64, want uint32, read func(*ebmlReader, int64) error) error {
	r.seek(off)
	id, size, err := r.element()
	if err != nil {
		return fmt.Errorf("remux: reading element %#x at %d: %w", want, off, err)
	}
	if id != want || size < 0 {
		return fmt.Errorf("remux: expected element %#x at %d, found %#x: %w", want, off, id, ErrUnsupported)
	}
	return read(r, r.pos+size)
}

func (f *File) readInfo(r *ebmlReader, end int64) error {
	var ticks float64
	err := r.walk(end, func(id uint32, size int64) error {
		var err error
		switch id {
		case idTimestampScale:
			f.timestampScale, err = r.uintValue(size)
		case idDuration:
			ticks, err = r.floatValue(size)
		}
		return err
	})
	if err != nil {
		return fmt.Errorf("remux: reading segment info: %w", err)
	}
	if f.timestampScale == 0 {
		f.timestampScale = defaultTimestampScale
	}
	// Duration counts the segment's own ticks and is routinely fractional,
	// which is why it is a float rather than an integer in the first place.
	f.duration = time.Duration(ticks * float64(f.timestampScale))
	return nil
}

func (f *File) readTracks(r *ebmlReader, end int64) error {
	err := r.walk(end, func(id uint32, size int64) error {
		if id != idTrackEntry {
			return nil
		}
		track, encoded, err := readTrackEntry(r, r.pos+size)
		if err != nil || track.Kind == 0 {
			return err
		}
		if encoded.refuse {
			// Copying a frame this package cannot undo produces a file that
			// opens cleanly and then decodes to noise, which is far worse than
			// refusing the track. Clearing the codec keeps it out of BestAudio
			// and FirstVideo; the number is remembered so a caller that asks
			// for it anyway gets an error rather than rubbish.
			track.Video, track.Audio = VideoUnknown, AudioUnknown
		}
		if encoded.refuse || len(encoded.prefix) > 0 {
			f.encodings[track.Number] = encoded
		}
		f.tracks = append(f.tracks, track)
		return nil
	})
	if err != nil {
		return fmt.Errorf("remux: reading tracks: %w", err)
	}
	return nil
}

func readTrackEntry(r *ebmlReader, end int64) (Track, encoding, error) {
	track := Track{SampleRate: defaultSampleRate, Channels: 1, Language: defaultLanguage}
	var trackType uint64
	var codecID string
	var frequency float64
	var encoded encoding

	err := r.walk(end, func(id uint32, size int64) error {
		var err error
		switch id {
		case idTrackNumber:
			track.Number, err = r.uintValue(size)
		case idTrackType:
			trackType, err = r.uintValue(size)
		case idDefaultDuration:
			var ns uint64
			ns, err = r.uintValue(size)
			track.DefaultDuration = time.Duration(ns)
		case idCodecID:
			codecID, err = r.stringValue(size)
		case idCodecPrivate:
			// Carried through byte for byte: for H.264 and HEVC this is already
			// the exact payload of avcC and hvcC.
			track.CodecPrivate, err = r.binaryValue(size)
		case idTrackName:
			track.Name, err = r.stringValue(size)
		case idLanguage:
			track.Language, err = r.stringValue(size)
		case idContentEncodings:
			encoded, err = readContentEncodings(r, r.pos+size)
		case idVideo:
			err = r.walk(r.pos+size, func(id uint32, size int64) error {
				var err error
				switch id {
				case idPixelWidth:
					track.Width, err = r.uintValue(size)
				case idPixelHeight:
					track.Height, err = r.uintValue(size)
				}
				return err
			})
		case idAudio:
			err = r.walk(r.pos+size, func(id uint32, size int64) error {
				var err error
				switch id {
				case idSamplingFrequency:
					frequency, err = r.floatValue(size)
				case idChannels:
					var channels uint64
					channels, err = r.uintValue(size)
					track.Channels = uint16(channels)
				}
				return err
			})
		}
		return err
	})
	if err != nil {
		return Track{}, encoding{}, err
	}

	switch trackType {
	case trackTypeVideo:
		track.Kind = TrackVideo
		track.Video = VideoCodecFor(codecID)
		track.Timescale = videoTimescale(track.DefaultDuration)
		// The audio defaults seeded above would otherwise describe a video
		// track as 8 kHz mono.
		track.SampleRate, track.Channels = 0, 0
	case trackTypeAudio:
		track.Kind = TrackAudio
		track.Audio = AudioCodecFor(codecID)
		if frequency > 0 {
			track.SampleRate = uint32(math.Round(frequency))
		}
		// Sample rate is the one timescale an audio track can have without
		// rounding a frame boundary, since every frame is a whole number of
		// samples.
		track.Timescale = track.SampleRate
	}
	return track, encoded, nil
}

// encoding is what a track's ContentEncodings element asks a reader to do to a
// frame before the frame means anything.
type encoding struct {
	// prefix is the run of bytes header stripping took off the front of every
	// frame, to be put back.
	prefix []byte
	// refuse marks an encoding this package will not undo.
	refuse bool
}

// compHeaderStripping is the one ContentEncoding undone here, and it is not
// compression at all: the muxer noticed every frame of a track opening with the
// same bytes and dropped them, leaving the bytes themselves in
// ContentCompSettings. Older mkvmerge turns it on by default for some audio
// tracks, so refusing it would tell a viewer their film has no playable sound
// when it has perfectly good AC-3 a byte copy away.
const compHeaderStripping = 3

// readContentEncodings reads what was done to a track's frames.
//
// Everything but header stripping is refused. zlib, bzlib and lzo1x are real
// compression, rare enough in the wild that carrying a decompressor for them is
// a poor trade, and encryption is not this package's business at all. The
// defaults Matroska gives these fields all point at compression, so an element
// this reader did not understand fails closed rather than silently copying
// frames it has not undone.
func readContentEncodings(r *ebmlReader, end int64) (encoding, error) {
	var out encoding
	err := r.walk(end, func(id uint32, size int64) error {
		if id != idContentEncoding {
			return nil
		}
		var kind, algo uint64
		scope := uint64(1)
		var settings []byte
		err := r.walk(r.pos+size, func(id uint32, size int64) error {
			var err error
			switch id {
			case idContentEncodingScope:
				scope, err = r.uintValue(size)
			case idContentEncodingType:
				kind, err = r.uintValue(size)
			case idContentCompression:
				err = r.walk(r.pos+size, func(id uint32, size int64) error {
					var err error
					switch id {
					case idContentCompAlgo:
						algo, err = r.uintValue(size)
					case idContentCompSettings:
						settings, err = r.binaryValue(size)
					}
					return err
				})
			}
			return err
		})
		if err != nil {
			return err
		}
		// Scope says what the encoding was applied to. One that spares the
		// frames still covers the codec private data, which is copied into the
		// MP4 sample entry verbatim, so it is no more usable.
		if kind != 0 || algo != compHeaderStripping || scope&1 == 0 {
			out.refuse = true
			return nil
		}
		out.prefix = settings
		return nil
	})
	return out, err
}

// videoTimescale picks a tick rate that divides the frame interval evenly.
//
// Carrying Matroska's milliseconds forward puts 23.976 frames per second on a
// grid it does not fit: 1001/24000 of a second is 41.708 ms, and a frame
// duration rounded to 42 ms drifts by a frame every twenty minutes. Rounding
// the rate instead and scaling by a thousand lands on 24000, where the interval
// is exactly 1001 ticks. The same falls out for 29.97 and 59.94.
func videoTimescale(frame time.Duration) uint32 {
	if frame <= 0 {
		return defaultVideoTimescale
	}
	rate := math.Round(float64(time.Second) / float64(frame))
	if rate < 1 || rate > 1000 {
		return defaultVideoTimescale
	}
	return uint32(rate) * 1000
}

func (f *File) readCues(r *ebmlReader, end int64) error {
	seen := map[int64]bool{}
	err := r.walk(end, func(id uint32, size int64) error {
		if id != idCuePoint {
			return nil
		}
		var ticks uint64
		var position int64 = -1
		err := r.walk(r.pos+size, func(id uint32, size int64) error {
			switch id {
			case idCueTime:
				value, err := r.uintValue(size)
				ticks = value
				return err
			case idCueTrackPositions:
				return r.walk(r.pos+size, func(id uint32, size int64) error {
					if id != idCueClusterPosition {
						return nil
					}
					value, err := r.uintValue(size)
					if err == nil && position < 0 {
						position = int64(value)
					}
					return err
				})
			}
			return nil
		})
		if err != nil || position < 0 {
			return err
		}
		// Every track that carries cues points at the same clusters, so without
		// this the index repeats each cluster once per track.
		offset := f.dataStart + position
		if seen[offset] {
			return nil
		}
		seen[offset] = true
		f.cues = append(f.cues, CuePoint{
			Time:          time.Duration(ticks * f.timestampScale),
			ClusterOffset: offset,
		})
		return nil
	})
	if err != nil {
		return fmt.Errorf("remux: reading cues: %w", err)
	}
	// The specification requires cue points in time order and real files honour
	// it, but the planner turns consecutive entries into byte ranges and a
	// single entry out of place would silently shift every segment after it.
	slices.SortStableFunc(f.cues, func(a, b CuePoint) int { return cmp.Compare(a.Time, b.Time) })
	return nil
}

// scanClusters builds an index out of the cluster headers themselves, for a
// file whose cues are missing or unreachable. Each cluster costs one small read
// instead of the whole file being pulled, but it is still one read per cluster,
// so this is the last resort rather than the normal path.
func scanClusters(r *ebmlReader, from, end int64, scale uint64) []CuePoint {
	var cues []CuePoint
	r.seek(from)
	for r.pos < end && len(cues) < maxScannedClusters {
		start := r.pos
		id, size, err := r.element()
		if err != nil || size < 0 {
			break
		}
		stop := r.pos + size
		if id != idCluster {
			r.skip(stop - r.pos)
			continue
		}
		ticks, ok := clusterTimestamp(r, stop)
		if !ok {
			break
		}
		cues = append(cues, CuePoint{Time: time.Duration(ticks * scale), ClusterOffset: start})
		r.seek(stop)
	}
	return cues
}

// clusterTimestamp reads the timestamp a cluster opens with, stopping at the
// first block so that indexing a cluster never reads its media.
func clusterTimestamp(r *ebmlReader, end int64) (uint64, bool) {
	for r.pos < end {
		id, size, err := r.element()
		if err != nil || size < 0 {
			return 0, false
		}
		if id == idClusterTimestamp {
			ticks, err := r.uintValue(size)
			return ticks, err == nil
		}
		if id == idSimpleBlock || id == idBlockGroup {
			return 0, false
		}
		r.skip(size)
	}
	return 0, false
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
