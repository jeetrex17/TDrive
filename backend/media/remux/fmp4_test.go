package remux

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Eyevinn/mp4ff/mp4"
)

func TestFmp4SampleDurations(t *testing.T) {
	t.Run("declared durations win", func(t *testing.T) {
		samples := []Sample{
			{DTS: 0, Duration: 1024},
			{DTS: 1024, Duration: 1024},
			{DTS: 2048, Duration: 1000},
		}
		got := sampleDurations(samples, Track{Timescale: 48000})
		want := []uint32{1024, 1024, 1000}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("sample %d: got %d, want %d", i, got[i], want[i])
			}
		}
	})

	t.Run("derived from decode times, not a constant", func(t *testing.T) {
		// A 23.976 fps stream in a 24000 timescale: the gaps are not all equal,
		// and a constant would drift.
		samples := []Sample{{DTS: 0}, {DTS: 1001}, {DTS: 2002}, {DTS: 3004}}
		got := sampleDurations(samples, Track{Timescale: 24000})
		want := []uint32{1001, 1001, 1002}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("sample %d: got %d, want %d", i, got[i], want[i])
			}
		}
	})

	t.Run("last sample falls back to the declared frame gap", func(t *testing.T) {
		samples := []Sample{{DTS: 0}, {DTS: 1001}}
		track := Track{Timescale: 24000, DefaultDuration: 41708333 * time.Nanosecond}
		got := sampleDurations(samples, track)
		if got[1] != 1000 {
			t.Fatalf("last duration: got %d, want 1000", got[1])
		}
	})

	t.Run("last sample falls back to its predecessor", func(t *testing.T) {
		samples := []Sample{{DTS: 0}, {DTS: 1024}, {DTS: 2048}}
		got := sampleDurations(samples, Track{Timescale: 48000})
		if got[2] != got[1] {
			t.Fatalf("last duration: got %d, want %d", got[2], got[1])
		}
	})
}

func TestFmp4SampleFlags(t *testing.T) {
	if flags := sampleFlags(TrackVideo, Sample{Sync: true}); flags != mp4.SyncSampleFlags {
		t.Errorf("sync video frame: got %#x, want %#x", flags, mp4.SyncSampleFlags)
	}
	nonSync := sampleFlags(TrackVideo, Sample{})
	if mp4.IsSyncSampleFlags(nonSync) {
		t.Errorf("non-sync video frame read back as sync: %#x", nonSync)
	}
	if nonSync&mp4.NonSyncSampleFlags == 0 {
		t.Errorf("non-sync video frame is missing is_non_sync_sample: %#x", nonSync)
	}
	// A Matroska file that forgets to flag its audio blocks would otherwise
	// produce a segment no player can start at.
	if flags := sampleFlags(TrackAudio, Sample{Sync: false}); flags != mp4.SyncSampleFlags {
		t.Errorf("audio frame: got %#x, want %#x", flags, mp4.SyncSampleFlags)
	}
}

func TestFmp4EntrySampleRate(t *testing.T) {
	if got := entrySampleRate(48000); got != 48000 {
		t.Errorf("48 kHz: got %d", got)
	}
	// 96 kHz does not fit sixteen bits; zero defers to the decoder config.
	if got := entrySampleRate(96000); got != 0 {
		t.Errorf("96 kHz: got %d, want 0", got)
	}
}

func TestFmp4FlacConfig(t *testing.T) {
	streamInfo := bytes.Repeat([]byte{0x11}, flacStreamInfoSize)

	t.Run("marker and block header", func(t *testing.T) {
		private := []byte("fLaC")
		private = append(private, 0x80, 0x00, 0x00, flacStreamInfoSize)
		private = append(private, streamInfo...)
		box, err := flacConfig(private)
		if err != nil {
			t.Fatal(err)
		}
		if len(box.MetadataBlocks) != 1 || !bytes.Equal(box.MetadataBlocks[0].BlockData, streamInfo) {
			t.Fatalf("got %d blocks, want STREAMINFO alone", len(box.MetadataBlocks))
		}
	})

	t.Run("bare STREAMINFO", func(t *testing.T) {
		box, err := flacConfig(streamInfo)
		if err != nil {
			t.Fatal(err)
		}
		if !box.MetadataBlocks[0].LastMetadataBlockFlag {
			t.Error("STREAMINFO is not marked as the last block")
		}
	})

	t.Run("seek table skipped", func(t *testing.T) {
		private := []byte("fLaC")
		private = append(private, 0x03, 0x00, 0x00, 0x08) // SEEKTABLE, 8 bytes
		private = append(private, bytes.Repeat([]byte{0}, 8)...)
		private = append(private, 0x80, 0x00, 0x00, flacStreamInfoSize)
		private = append(private, streamInfo...)
		box, err := flacConfig(private)
		if err != nil {
			t.Fatal(err)
		}
		if len(box.MetadataBlocks) != 1 || !bytes.Equal(box.MetadataBlocks[0].BlockData, streamInfo) {
			t.Fatal("seek table was not dropped")
		}
	})

	if _, err := flacConfig([]byte("fLaC")); err == nil {
		t.Error("headers with no STREAMINFO were accepted")
	}
}

func TestFmp4AlacConfig(t *testing.T) {
	cookie := bytes.Repeat([]byte{0x22}, 24)

	bare := alacConfig(cookie)
	if got := bare.Size(); got != uint64(8+4+len(cookie)) {
		t.Errorf("bare cookie: size %d", got)
	}

	// The same cookie with ffmpeg's box header still attached must not be
	// wrapped a second time.
	wrapped := append([]byte{0x00, 0x00, 0x00, 0x24}, []byte("alac")...)
	wrapped = append(wrapped, 0x00, 0x00, 0x00, 0x00)
	wrapped = append(wrapped, cookie...)
	if got := alacConfig(wrapped).Size(); got != bare.Size() {
		t.Errorf("wrapped cookie: size %d, want %d", got, bare.Size())
	}
}

// TestFmp4NegativeCompositionOffsets is the bug this muxer exists to avoid: a
// composition offset written unsigned wraps to about four billion, and the
// frame is shown two years into the film instead of one frame later.
func TestFmp4NegativeCompositionOffsets(t *testing.T) {
	const timescale = 24000
	const gap = 1001
	// Ten minutes in, so a segment-relative presentation time could not pass by
	// accident: PTS and the base decode time share the presentation timeline.
	const base = 10 * 60 * timescale

	// Decode order for an IPBBB group, presented as I B B B P.
	presentation := []int64{base, base + 4*gap, base + 1*gap, base + 2*gap, base + 3*gap}
	samples := make([]Sample, len(presentation))
	for i, pts := range presentation {
		samples[i] = Sample{Data: []byte{byte(i)}, PTS: pts, Sync: i == 0}
	}

	track := Track{
		Number:          1,
		Kind:            TrackVideo,
		Video:           VideoAVC,
		Timescale:       timescale,
		Width:           320,
		Height:          240,
		DefaultDuration: time.Duration(gap) * time.Second / timescale,
	}

	// Anchored at the first frame's presentation time, which is what the
	// playlist advertises for a segment beginning on a keyframe.
	seg, err := MediaSegment(
		[]Track{track},
		map[uint64][]Sample{1: samples},
		7,
		map[uint64]uint64{1: base},
	)
	if err != nil {
		t.Fatal(err)
	}

	trun, tfdt := fmp4ReadTrun(t, seg, 1)
	if tfdt.BaseMediaDecodeTime() != base {
		t.Fatalf("tfdt: got %d, want %d", tfdt.BaseMediaDecodeTime(), base)
	}
	if trun.Version != 1 {
		t.Fatalf("trun version %d cannot carry signed offsets", trun.Version)
	}

	negatives := 0
	decode := uint64(base)
	for i, s := range trun.Samples {
		if s.CompositionTimeOffset < 0 {
			negatives++
		}
		// The point of the offsets: the presentation times come back exactly.
		got := int64(decode) + int64(s.CompositionTimeOffset)
		if want := presentation[i]; got != want {
			t.Errorf("sample %d presents at %d, want %d", i, got, want)
		}
		decode += uint64(s.Dur)
	}
	if negatives == 0 {
		t.Error("no negative composition offsets, so the signed path went untested")
	}
}

func TestFmp4SegmentAddressing(t *testing.T) {
	track := Track{Number: 9, Kind: TrackAudio, Audio: AudioAAC, Timescale: 48000, Channels: 2, SampleRate: 48000}
	samples := []Sample{
		{Data: []byte{1, 2}, PTS: 0, DTS: 0, Duration: 1024},
		{Data: []byte{3, 4}, PTS: 1024, DTS: 1024, Duration: 1024},
	}
	seg, err := MediaSegment([]Track{track}, map[uint64][]Sample{9: samples}, 3, map[uint64]uint64{9: 0})
	if err != nil {
		t.Fatal(err)
	}

	parsed := fmp4Decode(t, seg)
	traf := parsed.Segments[0].Fragments[0].Moof.Traf
	if !traf.Tfhd.DefaultBaseIfMoof() {
		t.Error("tfhd does not use movie-fragment-relative addressing")
	}
	if traf.Tfhd.HasBaseDataOffset() {
		t.Error("tfhd carries an absolute base data offset")
	}
	if traf.Tfdt == nil {
		t.Fatal("traf has no tfdt")
	}
	if got := parsed.Segments[0].Fragments[0].Moof.Mfhd.SequenceNumber; got != 3 {
		t.Errorf("sequence number: got %d, want 3", got)
	}

	// A track with no samples in this segment must not get a traf that claims a
	// decode time it does not have.
	if _, err := MediaSegment([]Track{track}, nil, 4, map[uint64]uint64{9: 0}); err == nil {
		t.Error("an empty segment was accepted")
	}
	if _, err := MediaSegment([]Track{track}, map[uint64][]Sample{9: samples}, 5, nil); err == nil {
		t.Error("a segment with no base decode time was accepted")
	}
}

func TestFmp4DecodeTimesFilledIn(t *testing.T) {
	// Presentation order only, as Matroska stores it.
	samples := []Sample{{PTS: 0, Sync: true}, {PTS: 3000}, {PTS: 1000}, {PTS: 2000}}
	for i := range samples {
		samples[i].Data = []byte{byte(i)}
	}
	if hasDecodeTimes(samples) {
		t.Fatal("decode times reported as present before they were assigned")
	}

	track := Track{Number: 1, Kind: TrackVideo, Video: VideoAVC, Timescale: 1000, Width: 16, Height: 16}
	if _, err := MediaSegment([]Track{track}, map[uint64][]Sample{1: samples}, 0, map[uint64]uint64{1: 0}); err != nil {
		t.Fatal(err)
	}
	if !MonotonicDecodeTimes(samples) {
		t.Error("decode times were not reconstructed in place")
	}
}

// TestInitSegmentTagsHEVCAsHvc1 pins the one tag Safari refuses to play.
func TestInitSegmentTagsHEVCAsHvc1(t *testing.T) {
	fmp4RequireFFmpeg(t)
	dir := t.TempDir()
	source := fmp4EncodeSource(t, dir, "hevc.mp4", "libx265")

	tracks, _ := fmp4ReadFragmented(t, source)
	video := tracks[0]
	if video.Video != VideoHEVC {
		t.Fatalf("expected an HEVC track, got %q", video.Video)
	}

	init, err := InitSegment([]Track{video}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(init, []byte("hvc1")) || bytes.Contains(init, []byte("hev1")) {
		t.Fatal("init segment is not tagged hvc1")
	}

	stream := fmp4Probe(t, fmp4WriteFile(t, dir, "hevc-init.mp4", init), "v")
	if stream.CodecTag != "hvc1" {
		t.Errorf("codec tag: got %q, want hvc1", stream.CodecTag)
	}
	if stream.CodecName != "hevc" {
		t.Errorf("codec: got %q, want hevc", stream.CodecName)
	}
}

// TestFmp4RoundTrip is the test that matters: ffprobe, not this package, says
// whether what came out is a video file.
func TestFmp4RoundTrip(t *testing.T) {
	fmp4RequireFFmpeg(t)
	dir := t.TempDir()
	source := fmp4EncodeSource(t, dir, "source.mp4", "libx264")
	tracks, samples := fmp4ReadFragmented(t, source)
	if len(tracks) != 2 {
		t.Fatalf("expected a video and an audio track, got %d", len(tracks))
	}

	init, err := InitSegment(tracks, fmp4FirstFrames(tracks, samples))
	if err != nil {
		t.Fatal(err)
	}
	base := map[uint64]uint64{}
	for _, track := range tracks {
		base[track.Number] = uint64(samples[track.Number][0].PTS)
	}
	segment, err := MediaSegment(tracks, samples, 1, base)
	if err != nil {
		t.Fatal(err)
	}

	out := fmp4WriteFile(t, dir, "out.mp4", append(init, segment...))

	video := fmp4Probe(t, out, "v")
	if video.CodecTag != "avc1" {
		t.Errorf("video tag: got %q, want avc1", video.CodecTag)
	}
	if video.Width != 320 || video.Height != 240 {
		t.Errorf("frame size: got %dx%d, want 320x240", video.Width, video.Height)
	}
	if want := fmp4TimeBase(tracks[0].Timescale); video.TimeBase != want {
		t.Errorf("video time base: got %q, want %q", video.TimeBase, want)
	}
	if got, want := video.Packets, len(samples[tracks[0].Number]); got != want {
		t.Errorf("video packets: got %d, want %d", got, want)
	}

	audio := fmp4Probe(t, out, "a")
	if audio.CodecName != "aac" {
		t.Errorf("audio codec: got %q, want aac", audio.CodecName)
	}
	if want := fmp4TimeBase(tracks[1].Timescale); audio.TimeBase != want {
		t.Errorf("audio time base: got %q, want %q", audio.TimeBase, want)
	}
	if got, want := audio.Packets, len(samples[tracks[1].Number]); got != want {
		t.Errorf("audio packets: got %d, want %d", got, want)
	}

	// Packet counts and tags can all be right in a file that will not decode.
	decode := exec.Command("ffmpeg", "-v", "error", "-i", out, "-f", "null", "-")
	if output, err := decode.CombinedOutput(); err != nil || len(output) > 0 {
		t.Fatalf("decoding the remux failed: %v\n%s", err, output)
	}
}

func TestFmp4RoundTripAC3(t *testing.T) {
	fmp4RequireFFmpeg(t)
	dir := t.TempDir()
	// AC-3 is the case with no CodecPrivate at all: dac3 has to come from the
	// first frame's syncframe header.
	source := filepath.Join(dir, "ac3.mp4")
	fmp4Run(t, "ffmpeg", "-y", "-f", "lavfi", "-i", "sine=frequency=440:duration=1",
		"-c:a", "ac3", "-ac", "2", "-ar", "48000",
		"-movflags", "+frag_keyframe+empty_moov+default_base_moof+delay_moov", source)

	tracks, samples := fmp4ReadFragmented(t, source)
	track := tracks[0]
	track.Audio = AudioAC3
	// Matroska gives AC-3 no CodecPrivate, so the probe is the only source.
	track.CodecPrivate = nil

	if _, err := InitSegment([]Track{track}, nil); err == nil {
		t.Error("an AC-3 track with no frame to probe was accepted")
	}

	init, err := InitSegment([]Track{track}, fmp4FirstFrames([]Track{track}, samples))
	if err != nil {
		t.Fatal(err)
	}
	segment, err := MediaSegment([]Track{track}, samples, 1, map[uint64]uint64{track.Number: 0})
	if err != nil {
		t.Fatal(err)
	}

	stream := fmp4Probe(t, fmp4WriteFile(t, dir, "ac3-out.mp4", append(init, segment...)), "a")
	if stream.CodecName != "ac3" {
		t.Errorf("codec: got %q, want ac3", stream.CodecName)
	}
	if stream.SampleRate != "48000" {
		t.Errorf("sample rate: got %q, want 48000", stream.SampleRate)
	}
	if stream.Channels != 2 {
		t.Errorf("channels: got %d, want 2", stream.Channels)
	}
}

// Helpers below. They exist to turn an ffmpeg-made MP4 back into this package's
// own Track and Sample, so the round trip starts from real coded frames without
// needing the Matroska demuxer.

func fmp4RequireFFmpeg(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not on PATH", tool)
		}
	}
}

func fmp4Run(t *testing.T, name string, args ...string) {
	t.Helper()
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", name, err, out)
	}
}

func fmp4WriteFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func fmp4EncodeSource(t *testing.T, dir, name, encoder string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	fmp4Run(t, "ffmpeg", "-y",
		"-f", "lavfi", "-i", "testsrc=size=320x240:rate=25:duration=1",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1",
		"-c:v", encoder, "-pix_fmt", "yuv420p", "-g", "25",
		"-c:a", "aac", "-shortest",
		"-movflags", "+frag_keyframe+empty_moov+default_base_moof", path)
	return path
}

func fmp4ReadFragmented(t *testing.T, path string) ([]Track, map[uint64][]Sample) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	parsed, err := mp4.DecodeFile(file)
	if err != nil {
		t.Fatal(err)
	}

	var tracks []Track
	samples := map[uint64][]Sample{}
	for _, trak := range parsed.Init.Moov.Traks {
		track := Track{
			Number:    uint64(trak.Tkhd.TrackID),
			Timescale: trak.Mdia.Mdhd.Timescale,
		}
		stsd := trak.Mdia.Minf.Stbl.Stsd
		switch {
		case stsd.AvcX != nil:
			track.Kind, track.Video = TrackVideo, VideoAVC
			track.Width, track.Height = uint64(stsd.AvcX.Width), uint64(stsd.AvcX.Height)
			buf := bytes.Buffer{}
			if err := stsd.AvcX.AvcC.DecConfRec.Encode(&buf); err != nil {
				t.Fatal(err)
			}
			track.CodecPrivate = buf.Bytes()
		case stsd.HvcX != nil:
			track.Kind, track.Video = TrackVideo, VideoHEVC
			track.Width, track.Height = uint64(stsd.HvcX.Width), uint64(stsd.HvcX.Height)
			buf := bytes.Buffer{}
			if err := stsd.HvcX.HvcC.DecConfRec.Encode(&buf); err != nil {
				t.Fatal(err)
			}
			track.CodecPrivate = buf.Bytes()
		case stsd.Mp4a != nil:
			track.Kind, track.Audio = TrackAudio, AudioAAC
			track.Channels, track.SampleRate = stsd.Mp4a.ChannelCount, uint32(stsd.Mp4a.SampleRate)
			track.CodecPrivate = stsd.Mp4a.Esds.DecConfigDescriptor.DecSpecificInfo.DecConfig
		case stsd.AC3 != nil:
			track.Kind, track.Audio = TrackAudio, AudioAC3
			track.Channels, track.SampleRate = stsd.AC3.ChannelCount, uint32(stsd.AC3.SampleRate)
		default:
			t.Fatalf("track %d has no sample entry this test understands", trak.Tkhd.TrackID)
		}
		tracks = append(tracks, track)

		var trex *mp4.TrexBox
		for _, candidate := range parsed.Init.Moov.Mvex.Trexs {
			if candidate.TrackID == trak.Tkhd.TrackID {
				trex = candidate
			}
		}
		for _, segment := range parsed.Segments {
			for _, frag := range segment.Fragments {
				full, err := frag.GetFullSamples(trex)
				if err != nil {
					t.Fatal(err)
				}
				for _, s := range full {
					samples[track.Number] = append(samples[track.Number], Sample{
						Data:     s.Data,
						PTS:      int64(s.DecodeTime) + int64(s.CompositionTimeOffset),
						DTS:      int64(s.DecodeTime),
						Duration: int64(s.Dur),
						Sync:     s.IsSync(),
					})
				}
			}
		}
	}
	return tracks, samples
}

func fmp4FirstFrames(tracks []Track, samples map[uint64][]Sample) map[uint64][]byte {
	frames := map[uint64][]byte{}
	for _, track := range tracks {
		if run := samples[track.Number]; len(run) > 0 {
			frames[track.Number] = run[0].Data
		}
	}
	return frames
}

func fmp4ReadTrun(t *testing.T, segment []byte, trackID uint32) (*mp4.TrunBox, *mp4.TfdtBox) {
	t.Helper()
	parsed := fmp4Decode(t, segment)
	for _, traf := range parsed.Segments[0].Fragments[0].Moof.Trafs {
		if traf.Tfhd.TrackID == trackID {
			return traf.Trun, traf.Tfdt
		}
	}
	t.Fatalf("no traf for track %d", trackID)
	return nil, nil
}

func fmp4Decode(t *testing.T, segment []byte) *mp4.File {
	t.Helper()
	parsed, err := mp4.DecodeFile(bytes.NewReader(segment))
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func fmp4TimeBase(timescale uint32) string {
	return "1/" + strconv.Itoa(int(timescale))
}

type fmp4Stream struct {
	CodecName  string `json:"codec_name"`
	CodecTag   string `json:"codec_tag_string"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	TimeBase   string `json:"time_base"`
	SampleRate string `json:"sample_rate"`
	Channels   int    `json:"channels"`
	Packets    int    `json:"nb_read_packets,string"`
}

func fmp4Probe(t *testing.T, path, kind string) fmp4Stream {
	t.Helper()
	cmd := exec.Command("ffprobe", "-v", "error", "-select_streams", kind,
		"-count_packets", "-show_streams", "-of", "json", path)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", path, err)
	}
	var probed struct {
		Streams []fmp4Stream `json:"streams"`
	}
	if err := json.Unmarshal(out, &probed); err != nil {
		t.Fatal(err)
	}
	if len(probed.Streams) == 0 {
		t.Fatalf("ffprobe found no %q stream in %s", kind, path)
	}
	return probed.Streams[0]
}
