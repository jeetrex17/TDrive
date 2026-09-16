package remux

import (
	"context"
	"fmt"
	"time"
)

// Stream is one source file prepared for HLS delivery, and it is the only type
// the server layer needs to know about.
//
// Opening costs two small reads near either end of the file: the header for the
// track layout, and the cue index for the segment plan. No media is pulled
// until a player asks for a segment, and then only the bytes that segment
// covers. That is what makes remuxing a two-hour film affordable over a link
// where every byte is a Telegram request.
//
// Video and audio are published as separate renditions. A film with a
// commentary track cannot offer the choice any other way: HLS changes audio by
// changing which rendition plays, and a track muxed into the video segments can
// never be switched away from.
type Stream struct {
	src      Source
	file     *File
	video    Track
	audio    []Track
	inits    map[string][]byte
	segments []Segment
	bitrate  int
}

// Open prepares a source for playback, or reports why it cannot be played.
func Open(ctx context.Context, src Source) (*Stream, error) {
	file, err := Probe(ctx, src)
	if err != nil {
		return nil, err
	}

	video, audio, err := carry(file.Tracks())
	if err != nil {
		return nil, err
	}

	// Without an index there is nothing to plan from, and building one would
	// mean reading the whole file. Every muxer in circulation writes cues, so
	// this is a corrupt or truncated file rather than an unusual one.
	segments := PlanSegments(file.Cues(), file.ClusterEnd(), file.Duration())
	if len(segments) == 0 {
		return nil, fmt.Errorf("remux: %w: file carries no cue index to seek by", ErrUnsupported)
	}

	stream := &Stream{
		src:      src,
		file:     file,
		video:    video,
		audio:    audio,
		inits:    make(map[string][]byte, len(audio)+1),
		segments: segments,
		bitrate:  averageBitrate(src.Size(), TotalDuration(segments)),
	}
	if err := stream.describe(ctx); err != nil {
		return nil, err
	}
	return stream, nil
}

// carry chooses the tracks to repackage.
//
// A file with no audio at all is a legitimate thing to play, so video alone is
// accepted. A file whose audio exists but cannot be copied is not: that is the
// Blu-ray remux case, where the only audio is DTS or TrueHD, and answering it
// with a silent film would look like a bug in playback rather than a limit of
// the device.
//
// Every playable audio track is carried, not just the best one, so the viewer
// gets the same choice the file offers. The order is the file's own, with the
// best track first, because the first is the one a player starts with.
func carry(tracks []Track) (Track, []Track, error) {
	video, ok := FirstVideo(tracks)
	if !ok {
		return Track{}, nil, fmt.Errorf("remux: %w: no video track this platform can decode", ErrUnsupported)
	}

	best, hasAudio := BestAudio(tracks)
	if !hasAudio {
		for _, track := range tracks {
			if track.Kind == TrackAudio {
				return Track{}, nil, ErrNoPlayableTrack
			}
		}
		return video, nil, nil
	}

	audio := []Track{best}
	for _, track := range tracks {
		if track.Kind == TrackAudio && track.Playable() && track.Number != best.Number {
			audio = append(audio, track)
		}
	}
	return video, audio, nil
}

// describe builds the initialisation segment for every rendition.
//
// AC-3 and E-AC-3 carry no CodecPrivate, because everything dac3 and dec3 need
// is already in the syncframe header of the audio itself, so describing such a
// track means looking at one frame of it. Those are the only media reads Open
// makes, and they stay inside the first segment.
func (s *Stream) describe(ctx context.Context) error {
	first := s.segments[0].Bytes

	video, err := InitSegment([]Track{s.video}, nil)
	if err != nil {
		return err
	}
	s.inits[VideoRendition] = video

	for i, track := range s.audio {
		frames := map[uint64][]byte{}
		if NeedsSyncframeProbe(track.Audio) {
			samples, err := s.file.Samples(ctx, s.src, track, first)
			if err != nil {
				return fmt.Errorf("remux: read %s configuration: %w", track.Audio, err)
			}
			if len(samples) == 0 {
				return fmt.Errorf("remux: %w: %s track has no frame to describe it", ErrUnsupported, track.Audio)
			}
			frames[track.Number] = samples[0].Data
		}
		init, err := InitSegment([]Track{track}, frames)
		if err != nil {
			return err
		}
		s.inits[AudioRendition(i)] = init
	}
	return nil
}

// averageBitrate is what the master playlist advertises. It is taken from the
// source's own size because the remux copies every frame, so the repackaged
// stream carries the same payload within a rounding error of container
// overhead.
func averageBitrate(size int64, duration time.Duration) int {
	if size <= 0 || duration <= 0 {
		return 0
	}
	return int(float64(size) * 8 / duration.Seconds())
}

// Master is the playlist a player is pointed at. It names the video and every
// audio track on offer.
func (s *Stream) Master() string {
	return MasterPlaylist(s.video, s.audio, s.bitrate)
}

// HasRendition reports whether this stream publishes a rendition by that name.
// The server asks before doing any work, so a name it does not publish is a
// missing resource rather than a failed remux.
func (s *Stream) HasRendition(name string) bool {
	_, ok := s.trackFor(name)
	return ok
}

// Media is the playlist for one rendition. Every rendition shares the segment
// plan, because they are cut at the same moments out of the same file.
func (s *Stream) Media(rendition string) (string, bool) {
	if _, ok := s.trackFor(rendition); !ok {
		return "", false
	}
	return Playlist(s.segments), true
}

// Init is the initialisation segment for one rendition, which EXT-X-MAP points
// at and every player fetches before any media.
func (s *Stream) Init(rendition string) ([]byte, bool) {
	init, ok := s.inits[rendition]
	return init, ok
}

// Duration is the presentation length, as the sum of the plan.
func (s *Stream) Duration() time.Duration {
	return TotalDuration(s.segments)
}

// SegmentCount is how many media segments each rendition's playlist lists.
func (s *Stream) SegmentCount() int {
	return len(s.segments)
}

// Tracks are the tracks being carried, video first.
func (s *Stream) Tracks() []Track {
	return append([]Track{s.video}, s.audio...)
}

// trackFor resolves a rendition name to the track behind it.
func (s *Stream) trackFor(rendition string) (Track, bool) {
	index, isAudio, ok := ParseRendition(rendition)
	switch {
	case !ok:
		return Track{}, false
	case !isAudio:
		return s.video, true
	case index < len(s.audio):
		return s.audio[index], true
	default:
		return Track{}, false
	}
}

// Segment muxes one media segment of one rendition.
//
// This is where the work is: one range read over the clusters the plan
// assigned, then a moof and an mdat wrapped around the frames inside. Nothing
// is retained between calls, so segments can be produced in any order and
// seeking to the end of a film costs what playing from the start costs.
func (s *Stream) Segment(ctx context.Context, rendition string, index int) ([]byte, error) {
	track, ok := s.trackFor(rendition)
	if !ok {
		return nil, fmt.Errorf("remux: no rendition named %q", rendition)
	}
	if index < 0 || index >= len(s.segments) {
		return nil, fmt.Errorf("remux: segment %d is outside a plan of %d", index, len(s.segments))
	}
	plan := s.segments[index]

	frames, err := s.file.Samples(ctx, s.src, track, plan.Bytes)
	if err != nil {
		return nil, fmt.Errorf("remux: segment %d of %s: %w", index, rendition, err)
	}
	if len(frames) == 0 {
		return nil, fmt.Errorf("remux: segment %d of %s carries no frames", index, rendition)
	}

	// The track is anchored at its own first frame rather than at the segment's
	// nominal start, because only video actually begins there. Segments break on
	// keyframes and audio frames do not line up with them, so a segment's audio
	// typically starts a few tens of milliseconds early. Anchoring it to the
	// video boundary would leave that much silence at every join and shift the
	// whole track against the picture.
	base := map[uint64]uint64{track.Number: uint64(max(frames[0].PTS, 0))}
	samples := map[uint64][]Sample{track.Number: frames}
	// Fragment sequence numbers are one-based in ISO BMFF while segment names
	// are zero-based in the playlist, so the two differ by one on purpose.
	return MediaSegment([]Track{track}, samples, uint32(index+1), base)
}
