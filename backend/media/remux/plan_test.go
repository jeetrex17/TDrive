package remux

import (
	"strings"
	"testing"
	"time"
)

// cuesEvery builds an index with keyframes at a fixed spacing, which is the
// easy case real encoders rarely produce.
func cuesEvery(spacing time.Duration, count int, bytesPerCue int64) []CuePoint {
	cues := make([]CuePoint, count)
	for i := range cues {
		cues[i] = CuePoint{
			Time:          time.Duration(i) * spacing,
			ClusterOffset: int64(i) * bytesPerCue,
		}
	}
	return cues
}

func TestSegmentsReachTheTargetWithoutSplittingAKeyframeRun(t *testing.T) {
	// Keyframes every two seconds: three of them make six.
	cues := cuesEvery(2*time.Second, 12, 1000)
	segments := PlanSegments(cues, 12000, 24*time.Second)

	if len(segments) != 4 {
		t.Fatalf("segment count = %d, want 4 for 24s at a 6s target", len(segments))
	}
	for _, segment := range segments {
		if segment.Duration != 6*time.Second {
			t.Errorf("segment %d ran %v, want 6s", segment.Index, segment.Duration)
		}
	}
}

func TestSegmentsCoverTheFileWithoutGapsOrOverlap(t *testing.T) {
	// A player walks these end to end, so a gap loses media and an overlap
	// makes it decode the same frames twice.
	cues := cuesEvery(2500*time.Millisecond, 9, 800)
	const end = int64(9000)
	segments := PlanSegments(cues, end, 22500*time.Millisecond)

	if segments[0].Bytes.Start != cues[0].ClusterOffset {
		t.Errorf("first segment starts at %d, want %d", segments[0].Bytes.Start, cues[0].ClusterOffset)
	}
	for i := 1; i < len(segments); i++ {
		if segments[i].Bytes.Start != segments[i-1].Bytes.End {
			t.Errorf("gap between segment %d and %d: %d != %d",
				i-1, i, segments[i-1].Bytes.End, segments[i].Bytes.Start)
		}
		if segments[i].Start != segments[i-1].Start+segments[i-1].Duration {
			t.Errorf("timeline gap before segment %d", i)
		}
	}
	if last := segments[len(segments)-1]; last.Bytes.End != end {
		t.Errorf("last segment ends at %d, want %d", last.Bytes.End, end)
	}
}

func TestIrregularKeyframesProduceHonestDurations(t *testing.T) {
	// Real encoders place keyframes on scene changes, not on a clock. The
	// playlist advertises each segment's real length, so these must not be
	// rounded to the target.
	cues := []CuePoint{
		{Time: 0, ClusterOffset: 0},
		{Time: 1 * time.Second, ClusterOffset: 100},
		{Time: 9 * time.Second, ClusterOffset: 900},
		{Time: 10 * time.Second, ClusterOffset: 1000},
	}
	segments := PlanSegments(cues, 1400, 14*time.Second)

	total := TotalDuration(segments)
	if total != 14*time.Second {
		t.Errorf("plan covers %v, want the full 14s", total)
	}
	for _, segment := range segments {
		if segment.Duration <= 0 {
			t.Errorf("segment %d has no duration", segment.Index)
		}
	}
}

func TestLastSegmentRunsToTheEndOfTheMedia(t *testing.T) {
	cues := cuesEvery(6*time.Second, 3, 1000)
	segments := PlanSegments(cues, 5000, 20*time.Second)

	last := segments[len(segments)-1]
	if last.Start+last.Duration != 20*time.Second {
		t.Errorf("plan ends at %v, want 20s", last.Start+last.Duration)
	}
	if last.Bytes.End != 5000 {
		t.Errorf("last segment ends at byte %d, want 5000", last.Bytes.End)
	}
}

func TestPlanRefusesNonsense(t *testing.T) {
	if got := PlanSegments(nil, 1000, time.Second); got != nil {
		t.Error("planned segments from no cues")
	}
	if got := PlanSegments(cuesEvery(time.Second, 3, 10), 0, time.Second); got != nil {
		t.Error("planned segments for a file with no length")
	}
}

func TestSegmentAtResolvesASeek(t *testing.T) {
	cues := cuesEvery(2*time.Second, 12, 1000)
	segments := PlanSegments(cues, 12000, 24*time.Second)

	for _, at := range []time.Duration{0, 3 * time.Second, 5999 * time.Millisecond} {
		segment, ok := SegmentAt(segments, at)
		if !ok || segment.Index != 0 {
			t.Errorf("seek to %v landed on segment %d, want 0", at, segment.Index)
		}
	}
	segment, _ := SegmentAt(segments, 7*time.Second)
	if segment.Index != 1 {
		t.Errorf("seek to 7s landed on segment %d, want 1", segment.Index)
	}
	// Scrubbing to the very end must still return media rather than nothing.
	segment, ok := SegmentAt(segments, time.Hour)
	if !ok || segment.Index != len(segments)-1 {
		t.Error("a seek past the end did not land on the last segment")
	}
}

func TestPlaylistIsWhatAppleAsksFor(t *testing.T) {
	cues := cuesEvery(2*time.Second, 6, 1000)
	segments := PlanSegments(cues, 6000, 12*time.Second)
	playlist := Playlist(segments)

	for _, required := range []string{
		"#EXTM3U",
		"#EXT-X-VERSION:7", // the floor for fragmented MP4
		"#EXT-X-PLAYLIST-TYPE:VOD",
		`#EXT-X-MAP:URI="init.mp4"`, // mandatory for fragmented MP4
		"#EXT-X-ENDLIST",            // without it the player refuses to seek
	} {
		if !strings.Contains(playlist, required) {
			t.Errorf("playlist is missing %s\n%s", required, playlist)
		}
	}
	if strings.Count(playlist, "#EXTINF:") != len(segments) {
		t.Errorf("playlist has %d segments, want %d", strings.Count(playlist, "#EXTINF:"), len(segments))
	}
}

func TestTargetDurationNeverUnderstatesASegment(t *testing.T) {
	// A player that meets a segment longer than advertised may stall, so this
	// rounds up rather than to nearest.
	segments := []Segment{{Duration: 6200 * time.Millisecond}}
	playlist := Playlist(segments)
	if !strings.Contains(playlist, "#EXT-X-TARGETDURATION:7") {
		t.Errorf("target duration understates a 6.2s segment\n%s", playlist)
	}
}

func TestSegmentNamesRoundTrip(t *testing.T) {
	for _, index := range []int{0, 1, 42, 1000} {
		parsed, ok := ParseSegmentName(SegmentName(index))
		if !ok || parsed != index {
			t.Errorf("%q parsed as %d, %v", SegmentName(index), parsed, ok)
		}
	}
}

func TestSegmentNameParsingRefusesAnythingElse(t *testing.T) {
	// This runs on untrusted request paths, so it refuses rather than guesses.
	for _, name := range []string{
		"", ".m4s", "init.mp4", "1.mp4", "-1.m4s", "1.5.m4s",
		"../1.m4s", "01.m4s", "1e3.m4s", "99999999999.m4s",
	} {
		if _, ok := ParseSegmentName(name); ok {
			t.Errorf("accepted %q as a segment name", name)
		}
	}
}
