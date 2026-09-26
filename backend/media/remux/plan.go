package remux

import "time"

// The segment plan is what makes on-demand remuxing affordable.
//
// Every entry names one stretch of the source file, so a player asking for a
// segment turns into exactly one Telegram range read. Nothing has to be muxed
// speculatively, nothing is held in memory, and seeking anywhere in a two-hour
// file costs the same as playing from the start.
//
// The plan is published before a single byte of media is fetched, because the
// playlist needs every segment's duration up front and those come from the
// file's own cue index rather than from muxing.

// Range is a half-open stretch of the source file, [Start, End).
type Range struct {
	Start int64
	End   int64
}

// Len is the number of bytes the range covers.
func (r Range) Len() int64 {
	if r.End <= r.Start {
		return 0
	}
	return r.End - r.Start
}

// CuePoint is one entry of Matroska's index: a moment in the file and the
// cluster that holds it. Cues sit at keyframes, which is exactly where a
// segment is allowed to begin.
type CuePoint struct {
	Time          time.Duration
	ClusterOffset int64
}

// Segment is one unit of the plan.
type Segment struct {
	// Index is the segment's position, and its name in the playlist.
	Index int
	// Start is when this segment begins on the presentation timeline.
	Start time.Duration
	// Duration is how long it runs. The playlist advertises this, and the
	// player adds these up to turn a seek into a segment number, which is why
	// they must be honest rather than nominal.
	Duration time.Duration
	// Bytes is the stretch of the source holding this segment's clusters.
	Bytes Range
}

// targetSegment is what each segment aims for. Apple's authoring guidance puts
// VOD segments around six seconds: shorter multiplies request count over a long
// film, longer makes a seek fetch more than it needs.
const targetSegment = 6 * time.Second

// PlanSegments groups a file's cue points into segments of roughly the target
// duration.
//
// Cue spacing in real files is irregular, following the encoder's keyframe
// decisions rather than any clock, so segments come out with varying durations.
// That is fine and expected: HLS advertises each segment's real length. What is
// not negotiable is that a segment starts on a keyframe, so cues are grouped
// rather than split.
//
// end is the byte offset the last segment runs to, normally the start of the
// Cues element or the end of the file, and total is the file's duration.
func PlanSegments(cues []CuePoint, end int64, total time.Duration) []Segment {
	if len(cues) == 0 || end <= 0 {
		return nil
	}

	segments := make([]Segment, 0, len(cues))
	start := 0
	for start < len(cues) {
		// Take cues until the run is at least the target length. Taking the
		// first one past the target rather than stopping short keeps segments
		// near six seconds instead of consistently under it.
		next := start + 1
		for next < len(cues) && cues[next].Time-cues[start].Time < targetSegment {
			next++
		}

		segmentEnd := end
		if next < len(cues) {
			segmentEnd = cues[next].ClusterOffset
		}
		finish := total
		if next < len(cues) {
			finish = cues[next].Time
		}

		// A cue index that is not sorted, or one whose last entry sits past the
		// declared duration, would otherwise produce a negative length that the
		// playlist cannot express.
		duration := max(0, finish-cues[start].Time)

		segments = append(segments, Segment{
			Index:    len(segments),
			Start:    cues[start].Time,
			Duration: duration,
			Bytes:    Range{Start: cues[start].ClusterOffset, End: segmentEnd},
		})
		start = next
	}
	return segments
}

// SegmentAt finds the segment covering a moment, which is how a seek resolves.
// A time past the end returns the last segment rather than nothing, so a player
// scrubbing to the very end still gets media.
func SegmentAt(segments []Segment, at time.Duration) (Segment, bool) {
	if len(segments) == 0 {
		return Segment{}, false
	}
	for _, segment := range segments {
		if at < segment.Start+segment.Duration {
			return segment, true
		}
	}
	return segments[len(segments)-1], true
}

// TotalDuration sums the plan. HLS defines a VOD presentation's duration as the
// sum of its segments, so this is the number the player shows, and it is the
// reason the durations above have to be real.
func TotalDuration(segments []Segment) time.Duration {
	var total time.Duration
	for _, segment := range segments {
		total += segment.Duration
	}
	return total
}
