package remux

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
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
