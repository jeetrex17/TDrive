package remux

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestAVCCodecStringMatchesWhatFFmpegWrites(t *testing.T) {
	// ffmpeg's own HLS muxer is the oracle: it reads the same avcC and writes
	// the same attribute, so agreeing with it is the claim worth making.
	want := ffmpegCodecs(t, h264Fixture(t))
	got := CodecsAttribute(trackFrom(t, h264Fixture(t))...)
	if !strings.EqualFold(got, want) {
		t.Errorf("codecs = %q, ffmpeg writes %q", got, want)
	}
}

func TestHEVCCodecStringSaysHvc1AndAProfile(t *testing.T) {
	// ffmpeg declines to write a master playlist for HEVC in some builds, so
	// this checks the shape and the parts that are decidable from the fixture:
	// profile 1 is Main, which is what the encoder was asked for.
	tracks := trackFrom(t, hevcFixture(t))
	got := VideoCodecString(tracks[0])

	if !strings.HasPrefix(got, "hvc1.1.") {
		t.Errorf("codec string %q does not name hvc1 Main", got)
	}
	// Anything but hvc1 is refused outright by Safari.
	if strings.Contains(got, "hev1") {
		t.Errorf("codec string %q names hev1", got)
	}
	if !regexp.MustCompile(`^hvc1\.[0-9]+\.[0-9A-F]+\.[LH][0-9]+(\.[0-9A-F]+)*$`).MatchString(got) {
		t.Errorf("codec string %q is not the shape RFC 6381 defines", got)
	}
}

func TestACThreeNamesItself(t *testing.T) {
	tracks := trackFrom(t, hevcFixture(t))
	if got := AudioCodecString(tracks[1]); got != "ac-3" {
		t.Errorf("audio codec string = %q, want ac-3", got)
	}
}

func TestAConfigurationThatCannotBeReadIsLeftOut(t *testing.T) {
	// A wrong string is worse than a missing one, because a player believes it
	// and skips a stream it could have played.
	for _, track := range []Track{
		{Kind: TrackVideo, Video: VideoAVC, CodecPrivate: []byte{1, 2}},
		{Kind: TrackVideo, Video: VideoHEVC, CodecPrivate: []byte{1, 2, 3}},
		{Kind: TrackVideo, Video: VideoHEVC, CodecPrivate: make([]byte, 20)},
		{Kind: TrackVideo, Video: VideoUnknown},
		{Kind: TrackAudio, Audio: AudioUnknown},
	} {
		if got := CodecsAttribute(track); got != "" {
			t.Errorf("described an unreadable configuration as %q", got)
		}
	}
}

func TestCodecsAttributeSkipsOnlyWhatItCannotName(t *testing.T) {
	video := Track{Kind: TrackVideo, Video: VideoAVC, CodecPrivate: []byte{1, 0x4d, 0x40, 0x0d}}
	unreadable := Track{Kind: TrackAudio, Audio: AudioUnknown}
	known := Track{Kind: TrackAudio, Audio: AudioAC3}

	if got := CodecsAttribute(video, unreadable, known); got != "avc1.4d400d,ac-3" {
		t.Errorf("codecs = %q", got)
	}
}

// trackFrom probes a fixture and returns its carried tracks, video first.
func trackFrom(t *testing.T, path string) []Track {
	t.Helper()
	return openStream(t, path).Tracks()
}

// ffmpegCodecs reads the CODECS attribute out of a master playlist ffmpeg
// writes for the same file.
func ffmpegCodecs(t *testing.T, source string) string {
	t.Helper()
	dir := t.TempDir()
	out, err := exec.Command("ffmpeg", "-y", "-v", "error", "-i", source, "-c", "copy",
		"-f", "hls", "-hls_segment_type", "fmp4", "-hls_list_size", "0",
		"-master_pl_name", "master.m3u8", filepath.Join(dir, "out.m3u8")).CombinedOutput()
	if err != nil {
		t.Skipf("ffmpeg could not write a master playlist: %v\n%s", err, out)
	}
	master, err := os.ReadFile(filepath.Join(dir, "master.m3u8"))
	if err != nil {
		t.Skipf("ffmpeg wrote no master playlist: %v", err)
	}
	found := regexp.MustCompile(`CODECS="([^"]+)"`).FindSubmatch(master)
	if found == nil {
		t.Skipf("ffmpeg wrote no CODECS attribute:\n%s", master)
	}
	return string(found[1])
}
