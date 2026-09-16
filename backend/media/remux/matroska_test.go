package remux

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The fixtures are checked against ffprobe rather than against anything this
// package produced, which is the only way the check means anything.
//
// testdata/clip.mkv is ten seconds of HEVC with two B frames alongside mono
// AC-3, muxed by ffmpeg:
//
//	ffmpeg -i source.mkv -t 10 -c:v libx265 -crf 34 -vf scale=640:360 \
//	       -x265-params bframes=2:keyint=48:min-keyint=48:scenecut=0 \
//	       -c:a copy -cluster_time_limit 1000 clip.mkv
//
// testdata/laced.mkv is hand written, because no encoder within reach produces
// the two things it covers: ffmpeg's muxer never laces a block and never wraps
// one in a BlockGroup, while plenty of real files do both. It carries AAC on
// track 1 through Xiph lacing, EBML lacing and block groups, AC-3 on track 2
// through fixed size lacing, the same AC-3 frames on track 3 with their
// syncword removed by header stripping, and those frames again on track 4 under
// zlib. ffmpeg demuxes all of it, which is what makes this a fixture rather than
// a guess.
//
// Both packet lists were recorded with:
//
//	ffprobe -v error -show_entries packet=stream_index,pts_time,duration_time,size,flags \
//	        -of csv=p=0 testdata/NAME.mkv > testdata/NAME.packets.csv

// packet is one row of that list.
type packet struct {
	at       float64 // milliseconds
	duration float64 // milliseconds
	size     int
	sync     bool
}

func ffprobePackets(t *testing.T, fixture string, stream int) []packet {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", fixture+".packets.csv"))
	if err != nil {
		t.Fatalf("read ground truth: %v", err)
	}
	var packets []packet
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		fields := strings.Split(line, ",")
		if len(fields) < 5 {
			t.Fatalf("ground truth row %q has %d fields, want 5", line, len(fields))
		}
		index, err := strconv.Atoi(fields[0])
		if err != nil || index != stream {
			continue
		}
		at, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			t.Fatalf("ground truth row %q: %v", line, err)
		}
		duration, _ := strconv.ParseFloat(fields[2], 64)
		size, err := strconv.Atoi(fields[3])
		if err != nil {
			t.Fatalf("ground truth row %q: %v", line, err)
		}
		// ffprobe prints seconds, but Matroska counts milliseconds and these
		// values are always whole ones, so rounding here is lossless and keeps
		// the comparison off floating point noise.
		packets = append(packets, packet{
			at:       math.Round(at * 1000),
			duration: math.Round(duration * 1000),
			size:     size,
			sync:     strings.Contains(fields[4], "K"),
		})
	}
	if len(packets) == 0 {
		t.Fatalf("no ground truth for %s stream %d", fixture, stream)
	}
	return packets
}

// fixtureSource serves a file the way the real one arrives, a range at a time,
// and records what was fetched so a test can hold Probe to its promise of
// reading the header rather than the file.
type fixtureSource struct {
	data    []byte
	read    int64
	highest int64
}

func openFixture(t *testing.T, name string) *fixtureSource {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	return &fixtureSource{data: data}
}

func (s *fixtureSource) ReadAt(_ context.Context, buf []byte, off int64) (int, error) {
	if off < 0 || off >= int64(len(s.data)) {
		return 0, io.EOF
	}
	n := copy(buf, s.data[off:])
	s.read += int64(n)
	s.highest = max(s.highest, off+int64(n))
	if n < len(buf) {
		return n, io.EOF
	}
	return n, nil
}

func (s *fixtureSource) Size() int64 { return int64(len(s.data)) }

// milliseconds puts a sample time back on the grid the container wrote it on,
// which is the only grid the two sides can be compared on.
func milliseconds(ticks int64, timescale uint32) float64 {
	return float64(ticks) * 1000 / float64(timescale)
}

func trackNumbered(t *testing.T, file *File, number uint64) Track {
	t.Helper()
	for _, track := range file.Tracks() {
		if track.Number == number {
			return track
		}
	}
	t.Fatalf("no track numbered %d", number)
	return Track{}
}

func mediaRange(file *File) Range {
	return Range{Start: file.Cues()[0].ClusterOffset, End: file.ClusterEnd()}
}

func TestProbeDescribesTracksTheWayFFprobeDoes(t *testing.T) {
	src := openFixture(t, "clip.mkv")
	file, err := Probe(t.Context(), src)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}

	video, ok := FirstVideo(file.Tracks())
	if !ok {
		t.Fatal("no playable video track")
	}
	// ffprobe: hevc, 640x360, r_frame_rate 24/1, extradata_size 2429.
	if video.Video != VideoHEVC {
		t.Errorf("video codec = %q, want hvc1", video.Video)
	}
	if video.Width != 640 || video.Height != 360 {
		t.Errorf("video size = %dx%d, want 640x360", video.Width, video.Height)
	}
	if len(video.CodecPrivate) != 2429 {
		t.Errorf("codec private = %d bytes, want the 2429 ffprobe reports as extradata", len(video.CodecPrivate))
	}
	// 24 frames a second, on a tick rate the frame interval divides exactly.
	if video.Timescale != 24000 {
		t.Errorf("video timescale = %d, want 24000", video.Timescale)
	}

	audio, ok := BestAudio(file.Tracks())
	if !ok {
		t.Fatal("no playable audio track")
	}
	// ffprobe: ac3, 48000 Hz, mono.
	if audio.Audio != AudioAC3 {
		t.Errorf("audio codec = %q, want ac-3", audio.Audio)
	}
	if audio.SampleRate != 48000 || audio.Channels != 1 {
		t.Errorf("audio = %d Hz, %d channels, want 48000 Hz mono", audio.SampleRate, audio.Channels)
	}
	if audio.Timescale != 48000 {
		t.Errorf("audio timescale = %d, want the sample rate", audio.Timescale)
	}
	// AC-3 is the one format Matroska gives no codec private, which is why the
	// muxer has to probe a syncframe for it.
	if len(audio.CodecPrivate) != 0 {
		t.Errorf("AC-3 carried %d bytes of codec private", len(audio.CodecPrivate))
	}
	if !NeedsSyncframeProbe(audio.Audio) {
		t.Error("AC-3 should need a syncframe probe")
	}

	if file.TimestampScale() != 1_000_000 {
		t.Errorf("timestamp scale = %d, want one millisecond", file.TimestampScale())
	}
	// ffprobe reports the format duration as 10.016 seconds.
	if diff := file.Duration() - 10016*time.Millisecond; diff < -2*time.Millisecond || diff > 2*time.Millisecond {
		t.Errorf("duration = %v, want 10.016s", file.Duration())
	}
}

func TestProbeRefusesWhatIsNotMatroska(t *testing.T) {
	// An MP4 is the other container this app serves, so it is the one most
	// likely to arrive here by mistake.
	mp4 := append([]byte{0, 0, 0, 0x18}, []byte("ftypisomiso2avc1mp41")...)
	full := openFixture(t, "clip.mkv")

	for _, test := range []struct {
		name string
		data []byte
	}{
		{"an MP4", mp4},
		{"noise", make([]byte, 4096)},
		{"nothing", nil},
		{"an EBML header cut short", full.data[:12]},
	} {
		if _, err := Probe(t.Context(), &fixtureSource{data: test.data}); !errors.Is(err, ErrUnsupported) {
			t.Errorf("probing %s returned %v, want ErrUnsupported", test.name, err)
		}
	}
}

func TestProbeFindsTheIndexWithoutReadingTheClusters(t *testing.T) {
	src := openFixture(t, "clip.mkv")
	file, err := Probe(t.Context(), src)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}

	// The cues sit after every cluster, so an index found without pulling the
	// file can only have come from following the seek head.
	if src.highest < file.ClusterEnd() {
		t.Errorf("probe never read as far as the cues at %d, reaching only %d", file.ClusterEnd(), src.highest)
	}
	if src.read > src.Size()/4 {
		t.Errorf("probe read %d bytes of a %d byte file", src.read, src.Size())
	}
}

func TestCuesPointAtRealClustersInAbsoluteOffsets(t *testing.T) {
	src := openFixture(t, "clip.mkv")
	file, err := Probe(t.Context(), src)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}

	var keyframes []float64
	for _, p := range ffprobePackets(t, "clip", 0) {
		if p.sync {
			keyframes = append(keyframes, p.at)
		}
	}
	cues := file.Cues()
	if len(cues) != len(keyframes) {
		t.Fatalf("%d cues, but ffprobe reports %d keyframes", len(cues), len(keyframes))
	}

	for i, cue := range cues {
		if want := time.Duration(keyframes[i]) * time.Millisecond; cue.Time != want {
			t.Errorf("cue %d at %v, want the keyframe at %v", i, cue.Time, want)
		}
		// Matroska stores a cue position relative to the segment, so an offset
		// that was not converted lands somewhere in the middle of an element
		// rather than on a cluster header. Getting this wrong shifts every
		// segment and shows up only as broken playback.
		if !startsWithClusterID(src.data, cue.ClusterOffset) {
			t.Errorf("cue %d offset %d does not begin a cluster", i, cue.ClusterOffset)
		}
		if i > 0 && cue.ClusterOffset <= cues[i-1].ClusterOffset {
			t.Errorf("cue %d offset %d does not follow %d", i, cue.ClusterOffset, cues[i-1].ClusterOffset)
		}
	}
	if last := cues[len(cues)-1].ClusterOffset; file.ClusterEnd() <= last {
		t.Errorf("cluster end %d is not past the last cue at %d", file.ClusterEnd(), last)
	}
}

func startsWithClusterID(data []byte, off int64) bool {
	if off < 0 || off+4 > int64(len(data)) {
		return false
	}
	head := data[off : off+4]
	return head[0] == 0x1F && head[1] == 0x43 && head[2] == 0xB6 && head[3] == 0x75
}

func TestSamplesMatchTheFramesFFprobeReports(t *testing.T) {
	src := openFixture(t, "clip.mkv")
	file, err := Probe(t.Context(), src)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	video, _ := FirstVideo(file.Tracks())
	audio, _ := BestAudio(file.Tracks())

	for _, test := range []struct {
		name   string
		stream int
		track  Track
	}{
		{"video", 0, video},
		{"audio", 1, audio},
	} {
		t.Run(test.name, func(t *testing.T) {
			samples, err := file.Samples(t.Context(), src, test.track, mediaRange(file))
			if err != nil {
				t.Fatalf("samples: %v", err)
			}
			want := ffprobePackets(t, "clip", test.stream)
			if len(samples) != len(want) {
				t.Fatalf("%d samples, want %d", len(samples), len(want))
			}
			for i := range want {
				if len(samples[i].Data) != want[i].size {
					t.Fatalf("sample %d is %d bytes, want %d", i, len(samples[i].Data), want[i].size)
				}
				if samples[i].Sync != want[i].sync {
					t.Errorf("sample %d sync = %v, want %v", i, samples[i].Sync, want[i].sync)
				}
				// Both sides on the millisecond grid the file was written on,
				// which is all the resolution it has; the track's own ticks are
				// finer on purpose.
				if got := milliseconds(samples[i].PTS, test.track.Timescale); got != want[i].at {
					t.Fatalf("sample %d at %.3f ms, want %.3f", i, got, want[i].at)
				}
				if got := milliseconds(samples[i].Duration, test.track.Timescale); math.Abs(got-want[i].duration) > 1 {
					t.Errorf("sample %d runs %.3f ms, want %.3f", i, got, want[i].duration)
				}
			}
		})
	}
}

func TestSamplesCoverOnlyTheRequestedRange(t *testing.T) {
	// A player that seeks asks for one segment out of the middle, and what
	// comes back has to be the frames living in those bytes rather than the
	// whole file.
	src := openFixture(t, "clip.mkv")
	file, err := Probe(t.Context(), src)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	video, _ := FirstVideo(file.Tracks())
	cues := file.Cues()

	all := ffprobePackets(t, "clip", 0)
	middle := Range{Start: cues[2].ClusterOffset, End: cues[3].ClusterOffset}
	samples, err := file.Samples(t.Context(), src, video, middle)
	if err != nil {
		t.Fatalf("samples: %v", err)
	}
	if len(samples) >= len(all)/2 {
		t.Fatalf("one segment returned %d of the file's %d frames", len(samples), len(all))
	}

	// Clusters are stored in order, so the frames in one stretch of bytes are
	// one unbroken run of the file's frames. Where that run starts is what a
	// mistaken byte range gets wrong.
	start := -1
	for i, p := range all {
		if p.size == len(samples[0].Data) && p.at == milliseconds(samples[0].PTS, video.Timescale) {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("the first frame of the segment, %d bytes at %.0f ms, is not in the file",
			len(samples[0].Data), milliseconds(samples[0].PTS, video.Timescale))
	}
	if start+len(samples) > len(all) {
		t.Fatalf("the segment runs %d frames from frame %d, past the end of the file", len(samples), start)
	}
	for i, sample := range samples {
		want := all[start+i]
		if len(sample.Data) != want.size || milliseconds(sample.PTS, video.Timescale) != want.at {
			t.Fatalf("segment frame %d is %d bytes at %.0f ms, want %d bytes at %.0f",
				i, len(sample.Data), milliseconds(sample.PTS, video.Timescale), want.size, want.at)
		}
	}
	// The segment is advertised as starting at its cue, so the keyframe that
	// cue names has to be in there.
	if !samples[0].Sync || milliseconds(samples[0].PTS, video.Timescale) != float64(cues[2].Time.Milliseconds()) {
		t.Errorf("segment opens with a %v frame at %.0f ms, want the keyframe at %v",
			samples[0].Sync, milliseconds(samples[0].PTS, video.Timescale), cues[2].Time)
	}

	// Nothing is remembered between calls, because a player seeking asks for
	// segments in whatever order it likes.
	again, err := file.Samples(t.Context(), src, video, middle)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if len(again) != len(samples) || again[0].PTS != samples[0].PTS {
		t.Errorf("second pass over the same range differed: %d samples starting at %d", len(again), again[0].PTS)
	}
}

func TestLacedBlocksBecomeOneSamplePerFrame(t *testing.T) {
	src := openFixture(t, "laced.mkv")
	file, err := Probe(t.Context(), src)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}

	for _, test := range []struct {
		name   string
		number uint64
		stream int
		frame  int64 // samples between frames, in the track's own ticks
	}{
		{"xiph and ebml lacing in and out of block groups", 1, 0, 1024},
		{"fixed size lacing", 2, 1, 1536},
	} {
		t.Run(test.name, func(t *testing.T) {
			track := trackNumbered(t, file, test.number)
			samples, err := file.Samples(t.Context(), src, track, mediaRange(file))
			if err != nil {
				t.Fatalf("samples: %v", err)
			}
			want := ffprobePackets(t, "laced", test.stream)
			if len(samples) != len(want) {
				t.Fatalf("%d samples, want %d: a block's laced frames were collapsed or split wrongly", len(samples), len(want))
			}
			for i := range want {
				if len(samples[i].Data) != want[i].size {
					t.Fatalf("sample %d is %d bytes, want %d", i, len(samples[i].Data), want[i].size)
				}
				if samples[i].Sync != want[i].sync {
					t.Errorf("sample %d sync = %v, want %v", i, samples[i].Sync, want[i].sync)
				}
				// Laced frames are spread across their block by the track's
				// frame interval, which does not land on the millisecond grid
				// the block timestamp was written on. One container tick is the
				// most the two can differ by.
				if got := milliseconds(samples[i].PTS, track.Timescale); math.Abs(got-want[i].at) > 1 {
					t.Fatalf("sample %d at %.3f ms, want %.3f", i, got, want[i].at)
				}
			}
			// The failure a naive lacing reader produces is every frame in a
			// block sharing the block's own timestamp.
			seen := map[int64]bool{}
			for _, sample := range samples {
				if seen[sample.PTS] {
					t.Fatalf("two samples share the timestamp %d", sample.PTS)
				}
				seen[sample.PTS] = true
			}
			for i := 1; i < len(samples); i++ {
				if gap := samples[i].PTS - samples[i-1].PTS; gap < test.frame-int64(track.Timescale)/1000 || gap > test.frame+int64(track.Timescale)/1000 {
					t.Fatalf("samples %d and %d are %d ticks apart, want about %d", i-1, i, gap, test.frame)
				}
			}
		})
	}
}

func TestBlockGroupKeyframesComeFromTheReference(t *testing.T) {
	// A Block inside a BlockGroup carries no keyframe flag at all: it is a
	// keyframe exactly when nothing references an earlier frame. A reader that
	// went by the SimpleBlock flag would call every one of these a keyframe and
	// let a segment start in the middle of a group of pictures.
	src := openFixture(t, "laced.mkv")
	file, err := Probe(t.Context(), src)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	want := ffprobePackets(t, "laced", 0)
	references := 0
	for _, p := range want {
		if !p.sync {
			references++
		}
	}
	if references == 0 {
		t.Fatal("the fixture has no referencing block groups, so this proves nothing")
	}

	samples, err := file.Samples(t.Context(), src, trackNumbered(t, file, 1), mediaRange(file))
	if err != nil {
		t.Fatalf("samples: %v", err)
	}
	got := 0
	for _, sample := range samples {
		if !sample.Sync {
			got++
		}
	}
	if got != references {
		t.Errorf("%d samples are not keyframes, want the %d ffprobe reports", got, references)
	}
}

func TestHeaderStrippedFramesArePutBack(t *testing.T) {
	// Track 3 carries the same AC-3 frames as track 2 with the two byte
	// syncword every one of them opens with dropped by a content encoding.
	// Older mkvmerge does that by default, and refusing it would tell a viewer
	// their film has no playable sound when it has perfectly good AC-3.
	src := openFixture(t, "laced.mkv")
	file, err := Probe(t.Context(), src)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}

	track := trackNumbered(t, file, 3)
	if !track.Playable() {
		t.Fatalf("a header stripped AC-3 track came back unplayable as %q", track.Audio)
	}
	restored, err := file.Samples(t.Context(), src, track, mediaRange(file))
	if err != nil {
		t.Fatalf("samples: %v", err)
	}
	// ffprobe reports these frames whole, because ffmpeg puts the syncword back
	// too. Coming up two bytes short on every frame is the failure.
	want := ffprobePackets(t, "laced", 2)
	if len(restored) != len(want) {
		t.Fatalf("%d samples, want %d", len(restored), len(want))
	}
	for i := range want {
		if len(restored[i].Data) != want[i].size {
			t.Errorf("restored frame %d is %d bytes, want %d", i, len(restored[i].Data), want[i].size)
		}
	}

	// The same frames ride on track 2 with nothing taken off them, so the two
	// have to come out byte for byte the same.
	plain, err := file.Samples(t.Context(), src, trackNumbered(t, file, 2), mediaRange(file))
	if err != nil {
		t.Fatalf("samples of the untouched track: %v", err)
	}
	for i := range restored {
		if !bytes.Equal(restored[i].Data, plain[i].Data) {
			t.Fatalf("restored frame %d differs from the same frame carried whole", i)
		}
	}

	// The syncword is what the muxer reads to fill dac3, so the proof that it
	// is back is that the parser takes the frame.
	config, err := ParseAC3Syncframe(restored[0].Data)
	if err != nil {
		t.Fatalf("parsing a restored frame: %v", err)
	}
	if config.SampleRate() != 48000 || config.ChannelCount() != 1 {
		t.Errorf("restored frame is %d Hz and %d channels, want 48000 mono", config.SampleRate(), config.ChannelCount())
	}
	// Without the prefix the same parser refuses the same frame, so the check
	// above is not passing on a stream that never needed the work.
	if _, err := ParseAC3Syncframe(restored[0].Data[2:]); err == nil {
		t.Error("the parser accepted a frame with its syncword still missing")
	}
}

func TestRestoringAHeaderDoesNotShareAnArray(t *testing.T) {
	// Appending to the prefix in place is the tempting version, and it is wrong
	// the moment that slice has room to spare. A laced block's frames are
	// restored one after another, so the second would land in the same array
	// and write over the first, and the file would carry a run of frames that
	// are all the last one.
	prefix := make([]byte, 2, 64)
	prefix[0], prefix[1] = 0x0B, 0x77

	first := restoreHeader(prefix, []byte{1, 2, 3})
	second := restoreHeader(prefix, []byte{4, 5, 6})
	if want := []byte{0x0B, 0x77, 1, 2, 3}; !bytes.Equal(first, want) {
		t.Errorf("the first frame is %v after a second was restored, want %v", first, want)
	}
	if want := []byte{0x0B, 0x77, 4, 5, 6}; !bytes.Equal(second, want) {
		t.Errorf("the second frame is %v, want %v", second, want)
	}
}

func TestCompressedTrackIsRefusedRatherThanCorrupted(t *testing.T) {
	// Track 4 is really compressed, with zlib. ffprobe reads it perfectly well,
	// so the refusal is a choice: carrying a decompressor for a format this
	// rare is a poor trade, and copying the frames verbatim would build a file
	// that opens cleanly and then decodes to noise.
	src := openFixture(t, "laced.mkv")
	file, err := Probe(t.Context(), src)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	ffprobePackets(t, "laced", 3)

	track := trackNumbered(t, file, 4)
	if track.Playable() {
		t.Error("a zlib compressed track was offered as playable")
	}
	if _, err := file.Samples(t.Context(), src, track, mediaRange(file)); !errors.Is(err, ErrUnsupported) {
		t.Errorf("samples of a compressed track returned %v, want ErrUnsupported", err)
	}
	if chosen, ok := BestAudio(file.Tracks()); ok && chosen.Number == 4 {
		t.Error("BestAudio chose the compressed track")
	}
}

func TestAnIndexIsRebuiltWhenTheCuesAreGone(t *testing.T) {
	// A file truncated after its last cluster still plays and still seeks, by
	// reading the cluster headers instead. That costs a read per cluster, so it
	// is the fallback rather than the path Probe takes when cues exist.
	whole := openFixture(t, "clip.mkv")
	file, err := Probe(t.Context(), whole)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	cut := &fixtureSource{data: whole.data[:file.ClusterEnd()]}

	scanned, err := Probe(t.Context(), cut)
	if err != nil {
		t.Fatalf("probe without cues: %v", err)
	}
	if len(scanned.Cues()) <= len(file.Cues()) {
		t.Fatalf("scanned index has %d entries, want more than the %d cues", len(scanned.Cues()), len(file.Cues()))
	}
	for i, cue := range scanned.Cues() {
		if !startsWithClusterID(cut.data, cue.ClusterOffset) {
			t.Fatalf("scanned entry %d offset %d does not begin a cluster", i, cue.ClusterOffset)
		}
		if i > 0 && cue.Time < scanned.Cues()[i-1].Time {
			t.Errorf("scanned entry %d at %v goes back from %v", i, cue.Time, scanned.Cues()[i-1].Time)
		}
	}
	// Every cluster a real cue named has to appear in the rebuilt index too, or
	// seeking would land somewhere the previous index would not have.
	offsets := map[int64]bool{}
	for _, cue := range scanned.Cues() {
		offsets[cue.ClusterOffset] = true
	}
	for _, cue := range file.Cues() {
		if !offsets[cue.ClusterOffset] {
			t.Errorf("cluster %d is missing from the rebuilt index", cue.ClusterOffset)
		}
	}
}

func TestVideoTimescaleKeepsNTSCRatesExact(t *testing.T) {
	for _, test := range []struct {
		name  string
		frame time.Duration
		want  uint32
		ticks int64
	}{
		{"24 fps", 41666666, 24000, 1000},
		{"23.976 fps", 41708333, 24000, 1001},
		{"25 fps", 40 * time.Millisecond, 25000, 1000},
		{"29.97 fps", 33366666, 30000, 1001},
		{"59.94 fps", 16683333, 60000, 1001},
	} {
		got := videoTimescale(test.frame)
		if got != test.want {
			t.Errorf("%s: timescale = %d, want %d", test.name, got, test.want)
		}
		// The whole point of the choice: the frame interval is a whole number
		// of ticks, so no frame time ever has to be rounded and the rounding
		// cannot accumulate into drift.
		if ticks := ticksOf(int64(test.frame), got); ticks != test.ticks {
			t.Errorf("%s: a frame is %d ticks, want %d", test.name, ticks, test.ticks)
		}
	}
	// Variable frame rate declares no interval at all.
	if got := videoTimescale(0); got != 90000 {
		t.Errorf("timescale with no frame interval = %d, want 90000", got)
	}
}
