package nativeplayer

import "testing"

func TestParseMPVVersionReadsReleaseAndGitBanners(t *testing.T) {
	t.Parallel()

	cases := map[string]mpvVersion{
		"mpv 0.34.1 Copyright © 2000-2021 mpv/MPlayer/mplayer2 projects\n built on Unknown\nFFmpeg library versions:\n libavutil 56.70.100": {0, 34},
		"mpv v0.41.0-12-gdeadbeef Copyright © 2000-2025 mpv/MPlayer/mplayer2 projects":                                                       {0, 41},
		"mpv 1.2.0 Copyright":                                                                                                                {1, 2},
		"error while loading shared libraries: libavcodec.so.61":                                                                             {},
		"": {},
	}
	for banner, want := range cases {
		if got := parseMPVVersion(banner); got != want {
			t.Fatalf("parseMPVVersion(%q) = %v, want %v", banner, got, want)
		}
	}
}

func TestMPVVersionComparisons(t *testing.T) {
	t.Parallel()

	v := mpvVersion{0, 34}
	if !v.atLeast(0, 34) || !v.atLeast(0, 30) || v.atLeast(0, 36) || v.atLeast(1, 0) {
		t.Fatalf("0.34 comparisons are wrong")
	}
	if !(mpvVersion{1, 0}).atLeast(0, 99) {
		t.Fatal("a new major must satisfy every older minor")
	}
	if (mpvVersion{}).known() || (mpvVersion{}).atLeast(0, 1) || (mpvVersion{}).String() != "unknown" {
		t.Fatal("the zero version must read as unknown and satisfy nothing")
	}
	if got := v.String(); got != "0.34" {
		t.Fatalf("String() = %q, want 0.34", got)
	}
}
