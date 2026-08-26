package nativeplayer

import (
	"encoding/json"
	"io"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestRectValidRejectsNonFiniteAndUnboundedCoordinates(t *testing.T) {
	if !(Rect{X: -20, Y: 10, Width: 1920, Height: 1080}).Valid() {
		t.Fatal("ordinary viewport was rejected")
	}
	for _, rect := range []Rect{
		{Width: math.Inf(1), Height: 100},
		{X: math.NaN(), Width: 100, Height: 100},
		{Y: 1_000_001, Width: 100, Height: 100},
		{Width: 0, Height: 100},
		{Width: 100, Height: -1},
	} {
		if rect.Valid() {
			t.Fatalf("Rect.Valid(%+v) = true, want false", rect)
		}
	}
}

func TestStateFromMPVPropertiesDerivesParityContract(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]any
		want   State
	}{
		{
			name: "opening while mpv is idle",
			values: map[string]any{
				"idle-active": true,
				"volume":      float64(100),
				"speed":       float64(1),
			},
			want: State{Status: StatusOpening, Paused: true, Volume: 1, Rate: 1, Loading: true},
		},
		{
			name: "buffering active playback",
			values: map[string]any{
				"time-pos":               float64(12),
				"duration":               float64(100),
				"volume":                 float64(75),
				"speed":                  float64(1.25),
				"paused-for-cache":       true,
				"cache-buffering-state":  float64(42),
				"demuxer-cache-duration": float64(8),
			},
			want: State{
				Status:      StatusBuffering,
				CurrentTime: 12,
				Duration:    100,
				Buffered:    []BufferedRange{{Start: 12, End: 20}},
				Volume:      0.75,
				Rate:        1.25,
				Loading:     true,
			},
		},
		{
			name: "EOF wins over idle and pause",
			values: map[string]any{
				"time-pos":    float64(91),
				"duration":    float64(100),
				"pause":       true,
				"eof-reached": true,
				"idle-active": true,
				"volume":      float64(50),
				"speed":       float64(1),
			},
			want: State{
				Status:      StatusEnded,
				Paused:      true,
				CurrentTime: 100,
				Duration:    100,
				Volume:      0.5,
				Rate:        1,
				EOF:         true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stateFromMPVProperties(tt.values); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("stateFromMPVProperties() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestStateFromMPVPropertiesExtractsAudioAndSubtitleTracks(t *testing.T) {
	values := map[string]any{
		"volume": float64(100),
		"speed":  float64(1),
		"track-list": []any{
			map[string]any{"id": float64(1), "type": "video", "codec": "hevc", "selected": true},
			map[string]any{
				"id": float64(2), "type": "audio", "title": "Main", "lang": "eng",
				"codec": "eac3", "selected": true, "default": true,
			},
			map[string]any{
				"id": float64(3), "type": "sub", "title": "Signs", "lang": "eng",
				"codec": "ass", "forced": true,
			},
			map[string]any{"id": "bad", "type": "audio"},
		},
	}
	want := []Track{
		{ID: 2, Type: TrackTypeAudio, Title: "Main", Language: "eng", Codec: "eac3", Selected: true, Default: true},
		{ID: 3, Type: TrackTypeSubtitle, Title: "Signs", Language: "eng", Codec: "ass", Forced: true},
	}

	if got := stateFromMPVProperties(values).Tracks; !reflect.DeepEqual(got, want) {
		t.Fatalf("Tracks = %#v, want %#v", got, want)
	}
}

func TestTrackJSONContractIncludesEmptyMetadata(t *testing.T) {
	payload, err := json.Marshal(Track{ID: 7, Type: TrackTypeSubtitle})
	if err != nil {
		t.Fatalf("marshal Track: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatalf("unmarshal Track: %v", err)
	}
	for _, key := range []string{"id", "type", "title", "language", "codec", "selected", "default", "forced"} {
		if _, ok := fields[key]; !ok {
			t.Fatalf("track payload %s is missing %q", payload, key)
		}
	}
}

func TestTerminalStateUsesSafeFixedDiagnostics(t *testing.T) {
	state := terminalState(StatusFailed)
	if state.Status != StatusFailed || state.Error != ErrPlayerExited.Error() || !state.Paused {
		t.Fatalf("terminalState(StatusFailed) = %#v", state)
	}
	if got := terminalState(StatusClosed); got.Status != StatusClosed || got.Error != "" || !got.Paused {
		t.Fatalf("terminalState(StatusClosed) = %#v", got)
	}
}

func TestSidecarExitStatus(t *testing.T) {
	if got := sidecarExitStatus(false, State{}); got != StatusFailed {
		t.Fatalf("unexpected sidecar exit status = %q, want failed", got)
	}
	if got := sidecarExitStatus(false, State{EOF: true}); got != StatusEnded {
		t.Fatalf("EOF sidecar exit status = %q, want ended", got)
	}
	if got := sidecarExitStatus(true, State{}); got != StatusClosed {
		t.Fatalf("requested sidecar exit status = %q, want closed", got)
	}
}

func TestLinuxDisplayModeSelection(t *testing.T) {
	tests := []struct {
		name                    string
		gdkBackend, sessionType string
		waylandDisplay, display string
		want                    linuxDisplayMode
	}{
		{name: "explicit x11 wins", gdkBackend: "x11", sessionType: "wayland", waylandDisplay: "wayland-0", display: ":0", want: linuxDisplayX11Embedded},
		{name: "explicit wayland", gdkBackend: "wayland", waylandDisplay: "wayland-0", display: ":0", want: linuxDisplayWaylandStandalone},
		{name: "explicit wayland without compositor", gdkBackend: "wayland", display: ":0", want: linuxDisplayUnavailable},
		{name: "wayland preferred backend list", gdkBackend: "wayland,x11", waylandDisplay: "wayland-0", display: ":0", want: linuxDisplayWaylandStandalone},
		{name: "backend list falls back to x11", gdkBackend: "wayland,x11", display: ":0", want: linuxDisplayX11Embedded},
		{name: "wayland session", sessionType: "wayland", waylandDisplay: "wayland-0", display: ":0", want: linuxDisplayWaylandStandalone},
		{name: "wayland only environment", waylandDisplay: "wayland-0", want: linuxDisplayWaylandStandalone},
		{name: "wayland environment with xwayland", waylandDisplay: "wayland-0", display: ":0", want: linuxDisplayWaylandStandalone},
		{name: "x11 display", display: ":0", want: linuxDisplayX11Embedded},
		{name: "headless", want: linuxDisplayUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := selectLinuxDisplayMode(tt.gdkBackend, tt.sessionType, tt.waylandDisplay, tt.display); got != tt.want {
				t.Fatalf("selectLinuxDisplayMode() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSidecarPreflightIsOptIn(t *testing.T) {
	if sidecarPreflightEnabled("", "") {
		t.Fatal("sidecar preflight must be disabled by default")
	}
	if !sidecarPreflightEnabled("1", "") {
		t.Fatal("sidecar preflight was not enabled explicitly")
	}
	if sidecarPreflightEnabled("1", "1") {
		t.Fatal("explicit skip must override explicit enable")
	}
}

func TestMPVPreflightInvocationKeepsURLOutOfArguments(t *testing.T) {
	const secretURL = "http://127.0.0.1:1234/media/opaque-bearer-token"
	args, stdin := mpvPreflightInvocation(secretURL)
	for _, arg := range args {
		if strings.Contains(arg, secretURL) || strings.Contains(arg, "opaque-bearer-token") {
			t.Fatalf("preflight argument leaked media URL: %q", arg)
		}
	}
	if len(args) == 0 || args[len(args)-1] != "--playlist=-" {
		t.Fatalf("preflight args = %q, want stdin playlist", args)
	}
	payload, err := io.ReadAll(stdin)
	if err != nil {
		t.Fatalf("read preflight stdin: %v", err)
	}
	if got := string(payload); got != "#EXTM3U\n"+secretURL+"\n" {
		t.Fatalf("preflight stdin = %q", got)
	}
}
