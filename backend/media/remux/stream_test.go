package remux

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// These are the tests neither half of the package can write alone: they run a
// real Matroska file through the demuxer, the plan and the muxer, and then ask
// ffmpeg whether what came out is a video. Everything else in the package
// checks its own piece against its own expectations, which cannot catch two
// pieces that disagree.

// fileSource is a Source backed by a local file, standing in for the Telegram
// range reader the server passes in.
type fileSource struct {
	file *os.File
	size int64
}

func (f fileSource) ReadAt(_ context.Context, buf []byte, off int64) (int, error) {
	return f.file.ReadAt(buf, off)
}

func (f fileSource) Size() int64 { return f.size }

// encodeFixture builds a twelve second Matroska file with keyframes every two
// seconds, which gives the planner six cues to group into two segments.
func encodeFixture(t *testing.T, args ...string) string {
	t.Helper()
	requireFFTools(t)

	path := filepath.Join(t.TempDir(), "fixture.mkv")
	base := []string{
		"-y", "-v", "error",
		"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=24:duration=12",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=12:sample_rate=48000",
	}
	cmd := exec.Command("ffmpeg", append(append(base, args...), path)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg could not build the fixture: %v\n%s", err, out)
	}
	return path
}

func h264Fixture(t *testing.T) string {
	return encodeFixture(t,
		"-c:v", "libx264", "-preset", "ultrafast",
		"-g", "48", "-keyint_min", "48", "-sc_threshold", "0", "-bf", "2",
		"-c:a", "aac", "-b:a", "64k")
}

// hevcFixture is the pairing that actually matters: HEVC video with AC-3 audio
// is what a film remux looks like, and AC-3 is the one codec here whose
// configuration has to be probed from the audio rather than read from the file.
func hevcFixture(t *testing.T) string {
	return encodeFixture(t,
		"-c:v", "libx265", "-preset", "ultrafast",
		"-x265-params", "keyint=48:min-keyint=48:scenecut=0:log-level=error",
		"-tag:v", "hvc1",
		"-c:a", "ac3", "-b:a", "96k")
}

func openStream(t *testing.T, path string) *Stream {
	t.Helper()

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	t.Cleanup(func() { _ = file.Close() })
	info, err := file.Stat()
	if err != nil {
		t.Fatalf("stat fixture: %v", err)
	}

	stream, err := Open(t.Context(), fileSource{file: file, size: info.Size()})
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	return stream
}

// assemble writes one rendition's initialisation segment followed by every one
// of its media segments, which is what a player holds after loading it whole.
func assemble(t *testing.T, stream *Stream, rendition string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), rendition+".mp4")
	out, err := os.Create(path)
	if err != nil {
		t.Fatalf("create output: %v", err)
	}
	defer out.Close()

	init, ok := stream.Init(rendition)
	if !ok {
		t.Fatalf("no rendition named %q", rendition)
	}
	if _, err := out.Write(init); err != nil {
		t.Fatalf("write init: %v", err)
	}
	for i := range stream.SegmentCount() {
		segment, err := stream.Segment(t.Context(), rendition, i)
		if err != nil {
			t.Fatalf("mux segment %d of %s: %v", i, rendition, err)
		}
		if _, err := out.Write(segment); err != nil {
			t.Fatalf("write segment %d: %v", i, err)
		}
	}
	return path
}

// combine is what a player does with the renditions once it has them: one
// picture, one soundtrack, played together.
func combine(t *testing.T, video, audio string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "combined.mp4")
	out, err := exec.Command("ffmpeg", "-y", "-v", "error",
		"-i", video, "-i", audio, "-map", "0:v", "-map", "1:a", "-c", "copy", path).CombinedOutput()
	if err != nil {
		t.Fatalf("the two renditions do not combine: %v\n%s", err, out)
	}
	return path
}

func TestRemuxOfH264ProducesAPlayableFile(t *testing.T) {
	stream := openStream(t, h264Fixture(t))

	if got := stream.SegmentCount(); got != 2 {
		t.Errorf("planned %d segments, want 2 for twelve seconds at a six second target", got)
	}
	if got := stream.Duration(); got < 11500*time.Millisecond || got > 12500*time.Millisecond {
		t.Errorf("plan covers %v, want about twelve seconds", got)
	}

	assembled := combine(t, assemble(t, stream, VideoRendition), assemble(t, stream, AudioRendition(0)))
	if got := probe(t, assembled, "v", "codec_name"); got != "h264" {
		t.Errorf("video came out as %q, want h264", got)
	}
	if got := probe(t, assembled, "a", "codec_name"); got != "aac" {
		t.Errorf("audio came out as %q, want aac", got)
	}
	if got := probe(t, assembled, "v", "width"); got != "320" {
		t.Errorf("width came out as %q, want 320", got)
	}
	decodes(t, assembled)
}

func TestRemuxOfHEVCAndAC3ProducesAPlayableFile(t *testing.T) {
	stream := openStream(t, hevcFixture(t))

	// Anything but hvc1 is refused outright by Safari, so the tag is the whole
	// point of carrying HEVC at all.
	assembled := combine(t, assemble(t, stream, VideoRendition), assemble(t, stream, AudioRendition(0)))
	if got := probe(t, assembled, "v", "codec_tag_string"); got != "hvc1" {
		t.Errorf("video tagged %q, want hvc1", got)
	}
	// AC-3 has no CodecPrivate, so a correct dac3 here means the syncframe
	// probe read the right sample rate out of the audio itself.
	if got := probe(t, assembled, "a", "codec_name"); got != "ac3" {
		t.Errorf("audio came out as %q, want ac3", got)
	}
	if got := probe(t, assembled, "a", "sample_rate"); got != "48000" {
		t.Errorf("audio sample rate came out as %q, want 48000", got)
	}
	decodes(t, assembled)
}

func TestFramesSurviveTheRemuxIntact(t *testing.T) {
	// A remux copies frames, so the count must match exactly. A muxer that
	// drops the tail of a segment still plays, and still looks fine, which is
	// why this is worth asserting rather than trusting the decode.
	source := h264Fixture(t)
	stream := openStream(t, source)
	assembled := combine(t, assemble(t, stream, VideoRendition), assemble(t, stream, AudioRendition(0)))

	for _, stream := range []string{"v", "a"} {
		before := countPackets(t, source, stream)
		after := countPackets(t, assembled, stream)
		if before != after {
			t.Errorf("%s stream: %d packets in, %d out", stream, before, after)
		}
	}
}

func TestASegmentCanBeMuxedWithoutTheOnesBeforeIt(t *testing.T) {
	// This is the property that makes seeking cheap: a player jumping into the
	// middle of a film must not cost everything up to that point.
	stream := openStream(t, h264Fixture(t))
	last := stream.SegmentCount() - 1
	if last < 1 {
		t.Fatal("fixture did not produce enough segments to seek within")
	}

	segment, err := stream.Segment(t.Context(), VideoRendition, last)
	if err != nil {
		t.Fatalf("mux the last segment first: %v", err)
	}
	if len(segment) == 0 {
		t.Fatal("last segment came out empty")
	}
}

func TestOutOfRangeSegmentsAreRefused(t *testing.T) {
	stream := openStream(t, h264Fixture(t))
	for _, index := range []int{-1, stream.SegmentCount()} {
		if _, err := stream.Segment(t.Context(), VideoRendition, index); err == nil {
			t.Errorf("muxed segment %d, which is not in the plan", index)
		}
	}
	for _, rendition := range []string{"", "x", "a9", "a01", "../v"} {
		if _, err := stream.Segment(t.Context(), rendition, 0); err == nil {
			t.Errorf("muxed a segment of %q, which this stream does not publish", rendition)
		}
	}
}

func TestPlaylistMatchesTheSegmentsOnOffer(t *testing.T) {
	stream := openStream(t, h264Fixture(t))
	playlist, ok := stream.Media(VideoRendition)
	if !ok {
		t.Fatal("the stream publishes no video rendition")
	}

	for i := range stream.SegmentCount() {
		if !strings.Contains(playlist, SegmentName(i)) {
			t.Errorf("playlist does not list %s\n%s", SegmentName(i), playlist)
		}
	}
	if strings.Contains(playlist, SegmentName(stream.SegmentCount())) {
		t.Error("playlist lists a segment the stream cannot produce")
	}
}

func TestAFileWithNothingPlayableIsRefused(t *testing.T) {
	requireFFTools(t)
	// MPEG-4 Part 2 video with MPEG audio is a legal Matroska pairing that MP4
	// cannot usefully carry to an Apple device, so there is nothing a
	// repackage could achieve.
	path := encodeFixture(t, "-c:v", "mpeg4", "-q:v", "5", "-c:a", "mp2", "-b:a", "128k")

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer file.Close()
	info, _ := file.Stat()

	if _, err := Open(t.Context(), fileSource{file: file, size: info.Size()}); err == nil {
		t.Error("accepted a file with no track this platform can decode")
	}
}

func TestSomethingThatIsNotMatroskaIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not.mkv")
	if err := os.WriteFile(path, []byte("this is not a container at all"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open file: %v", err)
	}
	defer file.Close()
	info, _ := file.Stat()

	if _, err := Open(t.Context(), fileSource{file: file, size: info.Size()}); err == nil {
		t.Error("accepted a file that is not Matroska")
	}
}

func requireFFTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
}

// probe asks ffprobe for one field of one stream, so a failure names the field
// that was wrong rather than dumping a wall of output.
func probe(t *testing.T, path, stream, field string) string {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error",
		"-select_streams", stream,
		"-show_entries", "stream="+field,
		"-of", "default=noprint_wrappers=1:nokey=1",
		path).Output()
	if err != nil {
		t.Fatalf("ffprobe %s of %s: %v", field, stream, err)
	}
	return strings.TrimSpace(string(out))
}

func countPackets(t *testing.T, path, stream string) int {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error",
		"-select_streams", stream,
		"-count_packets", "-show_entries", "stream=nb_read_packets",
		"-of", "default=noprint_wrappers=1:nokey=1",
		path).Output()
	if err != nil {
		t.Fatalf("ffprobe count packets of %s: %v", stream, err)
	}
	count, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("ffprobe returned %q for a packet count", out)
	}
	return count
}

// decodes runs the file through a real decoder and fails on anything it
// complains about. A container can satisfy every structural assertion above and
// still hand a decoder frames it cannot use.
func decodes(t *testing.T, path string) {
	t.Helper()
	out, err := exec.Command("ffmpeg", "-v", "error", "-i", path, "-f", "null", "-").CombinedOutput()
	if err != nil {
		t.Fatalf("decoding failed: %v\n%s", err, out)
	}
	if len(out) > 0 {
		t.Errorf("decoder complained:\n%s", out)
	}
}

func TestTheTimelineSurvivesTheRemux(t *testing.T) {
	// This is the assertion that catches drift, which is the failure that hides
	// from every other check here. A remux that loses the shape of the timeline
	// still decodes, still reports the right duration, and still passes every
	// structural test above, while sliding further out of step the longer it
	// plays.
	//
	// The comparison is made after the renditions are put back together,
	// because that is the state a player actually holds: on its own the video
	// carries composition offsets that run negative, and any reader of one of
	// those shifts it by a frame to compensate.
	//
	// Each track is allowed one constant offset, measured and then required to
	// stay constant. Audio has one: Matroska's CodecDelay records the encoder's
	// pre-roll, 21ms for AAC, and the demuxer deliberately does not subtract it
	// because expressing it in MP4 needs an edit list. A fixed offset that small
	// is inaudible against the picture. A growing one is the bug.
	source := h264Fixture(t)
	stream := openStream(t, source)
	ours := combine(t, assemble(t, stream, VideoRendition), assemble(t, stream, AudioRendition(0)))

	for _, track := range []string{"v", "a"} {
		want := packetTimes(t, source, track)
		got := packetTimes(t, ours, track)
		if len(want) != len(got) {
			t.Errorf("%s stream: %d packets in, %d out", track, len(want), len(got))
			continue
		}

		offset := got[0] - want[0]
		// Well inside the range where a viewer could tell picture and sound
		// apart, which is what makes a constant offset tolerable at all.
		if offset > 0.030 || offset < -0.030 {
			t.Errorf("%s stream starts %.0fms from where the file puts it", track, offset*1000)
		}
		for i := range want {
			// Matroska keeps timestamps in milliseconds while the remux picks a
			// timescale that divides the frame rate evenly, so the two disagree
			// below a millisecond by construction.
			if drift := (got[i] - want[i]) - offset; drift > 0.0015 || drift < -0.0015 {
				t.Fatalf("%s packet %d has drifted %.1fms from the file", track, i, drift*1000)
			}
		}
	}
}

func TestSegmentsJoinWithoutAGapInEitherTrack(t *testing.T) {
	// Audio frames do not break on video keyframes, so a segment's audio starts
	// before its video does. A packager that anchors both to the segment
	// boundary leaves a few tens of milliseconds of silence at every join, which
	// is audible on every one and drifts the track against the picture.
	stream := openStream(t, h264Fixture(t))
	ours := combine(t, assemble(t, stream, VideoRendition), assemble(t, stream, AudioRendition(0)))

	for _, stream := range []string{"v", "a"} {
		times := packetTimes(t, ours, stream)
		if len(times) < 2 {
			t.Fatalf("%s stream has nothing to check", stream)
		}
		// ffprobe reports packets in decode order, which for a stream with
		// B-frames is not the order they are shown in.
		slices.Sort(times)
		var longest float64
		for i := 1; i < len(times); i++ {
			if gap := times[i] - times[i-1]; gap > longest {
				longest = gap
			}
		}
		// Frames arrive every 42ms of video or 21ms of audio. Anything much
		// past that is a hole where a segment was joined.
		if longest > 0.060 {
			t.Errorf("%s stream has a %.3fs hole in it", stream, longest)
		}
	}
}

func packetTimes(t *testing.T, path, stream string) []float64 {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error",
		"-select_streams", stream,
		"-show_entries", "packet=pts_time",
		"-of", "default=noprint_wrappers=1:nokey=1",
		path).Output()
	if err != nil {
		t.Fatalf("ffprobe packet times of %s: %v", stream, err)
	}
	var times []float64
	for line := range strings.FieldsSeq(string(out)) {
		at, err := strconv.ParseFloat(line, 64)
		if err != nil {
			t.Fatalf("ffprobe returned %q for a timestamp", line)
		}
		times = append(times, at)
	}
	return times
}
