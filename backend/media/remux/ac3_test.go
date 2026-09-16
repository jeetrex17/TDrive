package remux

import "testing"

// A real AC-3 syncframe header, taken from a file encoded by ffmpeg 8.1 as
// mono, 48 kHz, 192 kbit/s. Synthetic bits would only prove the parser agrees
// with itself; these come from an encoder anyone can reproduce:
//
//	ffmpeg -f lavfi -i sine -t 30 -c:a ac3 -b:a 192k out.ac3
var realAC3Frame = []byte{
	0x0b, 0x77, 0x9e, 0x52, 0x14, 0x40, 0x2f, 0x84,
	0x2b, 0xc1, 0xc7, 0x7a, 0xaf, 0x5e, 0x1b, 0xe7,
}

func TestParseAC3SyncframeReadsARealFrame(t *testing.T) {
	config, err := ParseAC3Syncframe(realAC3Frame)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := config.SampleRate(); got != 48000 {
		t.Errorf("sample rate = %d, want 48000", got)
	}
	// ffprobe reports this stream as mono, which is acmod 1 with no LFE.
	if config.ChannelMode != 1 {
		t.Errorf("channel mode = %d, want 1 (mono)", config.ChannelMode)
	}
	if config.LFEOn {
		t.Error("LFE reported on a mono stream")
	}
	if got := config.ChannelCount(); got != 1 {
		t.Errorf("channel count = %d, want 1", got)
	}
	// AC-3 is bsid 8; anything above 10 would mean we misread it as E-AC-3.
	if config.BitstreamID != 8 {
		t.Errorf("bitstream id = %d, want 8", config.BitstreamID)
	}
	// 192 kbit/s at 48 kHz is frame size code 20, so bit rate code 10.
	if config.BitRateCode != 10 {
		t.Errorf("bit rate code = %d, want 10 (192 kbit/s)", config.BitRateCode)
	}
}

func TestParseAC3SyncframeRejectsRubbish(t *testing.T) {
	if _, err := ParseAC3Syncframe([]byte{0x00, 0x01}); err == nil {
		t.Error("accepted a frame too short to hold a header")
	}
	notAC3 := make([]byte, 16)
	if _, err := ParseAC3Syncframe(notAC3); err == nil {
		t.Error("accepted a frame with no syncword")
	}
}

func TestChannelCountIncludesLFE(t *testing.T) {
	// acmod 7 is 3 front + 2 surround; with LFE that is the familiar 5.1.
	surround := AC3Config{ChannelMode: 7, LFEOn: true}
	if got := surround.ChannelCount(); got != 6 {
		t.Errorf("5.1 channel count = %d, want 6", got)
	}
}

func TestSampleRateRejectsTheReservedCode(t *testing.T) {
	if got := (AC3Config{SampleRateCode: 3}).SampleRate(); got != 0 {
		t.Errorf("reserved sample rate code reported %d, want 0", got)
	}
}

func TestVideoCodecMapping(t *testing.T) {
	cases := map[string]VideoCodec{
		"V_MPEG4/ISO/AVC":  VideoAVC,
		"V_MPEGH/ISO/HEVC": VideoHEVC,
		"V_VP9":            VideoUnknown,
		"":                 VideoUnknown,
	}
	for id, want := range cases {
		if got := VideoCodecFor(id); got != want {
			t.Errorf("VideoCodecFor(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestAudioCodecMapping(t *testing.T) {
	cases := map[string]AudioCodec{
		"A_AAC":          AudioAAC,
		"A_AAC/MPEG4/LC": AudioAAC, // profile suffixes name the same stream
		"A_AC3":          AudioAC3,
		"A_EAC3":         AudioEAC3,
		"A_FLAC":         AudioFLAC,
		// Known formats that iOS cannot take inside MP4. These must report
		// unknown rather than be silently copied into a file that plays silence.
		"A_DTS":     AudioUnknown,
		"A_TRUEHD":  AudioUnknown,
		"A_OPUS":    AudioUnknown,
		"A_MPEG/L3": AudioUnknown,
	}
	for id, want := range cases {
		if got := AudioCodecFor(id); got != want {
			t.Errorf("AudioCodecFor(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestOnlyDolbyNeedsASyncframeProbe(t *testing.T) {
	// Every other format carries its MP4 decoder configuration verbatim in
	// CodecPrivate, so only these two have to be parsed out of the audio.
	for _, codec := range []AudioCodec{AudioAC3, AudioEAC3} {
		if !NeedsSyncframeProbe(codec) {
			t.Errorf("%q should need a syncframe probe", codec)
		}
	}
	for _, codec := range []AudioCodec{AudioAAC, AudioFLAC, AudioALAC} {
		if NeedsSyncframeProbe(codec) {
			t.Errorf("%q should read its configuration from CodecPrivate", codec)
		}
	}
}
