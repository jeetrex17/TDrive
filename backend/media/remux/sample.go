package remux

import "time"

// Sample is one coded frame on its way from Matroska into MP4, and it is the
// contract the demuxer and the muxer meet at.
//
// Data is the frame exactly as Matroska stored it. No conversion happens here:
// Matroska carries H.264 and HEVC as length-prefixed NAL units, which is the
// same framing MP4 wants, so the bytes are copied rather than rewritten.
type Sample struct {
	// Data is the coded frame.
	Data []byte
	// PTS is when the frame is shown, in the track's timescale.
	PTS int64
	// DTS is when the frame must be decoded, in the same timescale.
	//
	// Matroska does not store this. It records presentation order only, while
	// MP4's trun demands decode order plus a composition offset, so for any
	// stream with B-frames this is reconstructed rather than read. See
	// reorderDTS.
	DTS int64
	// Duration is how long the frame occupies, in the track's timescale. Zero
	// means unknown, in which case the muxer derives it from the next sample.
	Duration int64
	// Sync marks a frame that can be decoded without any earlier frame, which
	// is where a segment is allowed to begin.
	Sync bool
}

// CompositionOffset is the signed gap MP4 stores to recover presentation order
// from decode order.
func (s Sample) CompositionOffset() int32 {
	return int32(s.PTS - s.DTS)
}

// TrackKind separates the two track types this package handles. Subtitles are
// deliberately absent for now: they need their own rendition and a conversion
// to WebVTT, which is later work.
type TrackKind int

const (
	TrackVideo TrackKind = iota + 1
	TrackAudio
)

// Track is one elementary stream selected out of a Matroska file.
type Track struct {
	// Number is Matroska's own track number, used to match blocks to tracks.
	Number uint64
	Kind   TrackKind
	// Video and Audio name the MP4 sample entry. Exactly one is set.
	Video VideoCodec
	Audio AudioCodec
	// CodecPrivate is Matroska's decoder configuration. For H.264 and HEVC it
	// is already the exact payload of avcC and hvcC, so building those boxes is
	// a copy. AC-3 and E-AC-3 have none and are probed from their first frame.
	CodecPrivate []byte
	// Timescale is the unit PTS and DTS are expressed in, in ticks per second.
	//
	// Matroska's own timestamps are milliseconds by default, which is too
	// coarse to carry into MP4: at 23.976 frames per second the rounding
	// accumulates into visible drift. Tracks are given a timescale that divides
	// their frame or sample rate evenly instead.
	Timescale uint32
	// DefaultDuration is the nominal gap between frames, in nanoseconds, when
	// the file declares one.
	DefaultDuration time.Duration
	// Language is an ISO 639-2 code, used to label alternate audio renditions.
	Language string
	// Name is the human label the file gives the track, if any.
	Name string
	// Width and Height describe a video track in pixels.
	Width, Height uint64
	// SampleRate and Channels describe an audio track.
	SampleRate uint32
	Channels   uint16
}

// Playable reports whether this track can be copied into MP4 for iOS.
func (t Track) Playable() bool {
	switch t.Kind {
	case TrackVideo:
		return t.Video != VideoUnknown
	case TrackAudio:
		return t.Audio != AudioUnknown
	default:
		return false
	}
}

// BestAudio picks the audio track to carry.
//
// It deliberately does not honour Matroska's default-track flag. Blu-ray
// remuxes routinely default to DTS or TrueHD, which iOS cannot decode at all,
// while carrying a Dolby or AAC track alongside for compatibility. Choosing the
// best playable track rather than the declared default is the cheapest thing
// that turns a silent file into a working one.
//
// Among playable tracks the one with the most channels wins, then the earliest,
// so a surround mix is preferred to a stereo downmix of the same content.
func BestAudio(tracks []Track) (Track, bool) {
	var best Track
	found := false
	for _, track := range tracks {
		if track.Kind != TrackAudio || !track.Playable() {
			continue
		}
		if !found || track.Channels > best.Channels {
			best = track
			found = true
		}
	}
	return best, found
}

// FirstVideo returns the first playable video track.
func FirstVideo(tracks []Track) (Track, bool) {
	for _, track := range tracks {
		if track.Kind == TrackVideo && track.Playable() {
			return track, true
		}
	}
	return Track{}, false
}
