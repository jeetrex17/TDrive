package nativeplayer

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
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
			name: "opening defaults missing volume and speed like libmpv",
			values: map[string]any{
				"idle-active": true,
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
	if got := sidecarExitStatus(false, false, nil); got != StatusFailed {
		t.Fatalf("unexpected sidecar exit status = %q, want failed", got)
	}
	if got := sidecarExitStatus(false, false, nil); got != StatusFailed {
		t.Fatalf("clean embedded exit status = %q, want failed", got)
	}
	if got := sidecarExitStatus(true, false, errors.New("killed")); got != StatusClosed {
		t.Fatalf("requested sidecar exit status = %q, want closed", got)
	}
	if got := sidecarExitStatus(false, true, nil); got != StatusClosed {
		t.Fatalf("clean standalone exit status = %q, want closed", got)
	}
	if got := sidecarExitStatus(false, true, errors.New("exit 2")); got != StatusFailed {
		t.Fatalf("failed standalone exit status = %q, want failed", got)
	}
}

func TestEndedStateCanResumeAfterSeek(t *testing.T) {
	ended := endedState(State{
		Status:      StatusPlaying,
		CurrentTime: 9,
		Duration:    10,
		Volume:      0.8,
		Rate:        1.25,
	})
	if ended.Status != StatusEnded || !ended.EOF || !ended.Paused || ended.CurrentTime != 10 {
		t.Fatalf("endedState() = %#v", ended)
	}

	resumed := normalizeState(State{
		Status:      StatusPlaying,
		CurrentTime: 4,
		Duration:    ended.Duration,
		Volume:      ended.Volume,
		Rate:        ended.Rate,
	})
	if resumed.Status != StatusPlaying || resumed.EOF || resumed.Paused || resumed.CurrentTime != 4 {
		t.Fatalf("resumed state = %#v", resumed)
	}
}

func TestMPVEventStatusClassifiesEndFileReasons(t *testing.T) {
	tests := []struct {
		name   string
		event  mpvIPCEvent
		status PlaybackStatus
		ok     bool
	}{
		{name: "load failure", event: mpvIPCEvent{Event: "end-file", Reason: "error"}, status: StatusFailed, ok: true},
		{name: "ordinary eof", event: mpvIPCEvent{Event: "end-file", Reason: "eof"}, status: StatusEnded, ok: true},
		{name: "unknown end", event: mpvIPCEvent{Event: "end-file", Reason: "unknown"}, status: StatusFailed, ok: true},
		{name: "user quit", event: mpvIPCEvent{Event: "end-file", Reason: "quit"}, status: StatusClosed, ok: true},
		{name: "replacement stop", event: mpvIPCEvent{Event: "end-file", Reason: "stop"}},
		{name: "unrelated event", event: mpvIPCEvent{Event: "file-loaded"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, ok := mpvEventStatus(tt.event)
			if status != tt.status || ok != tt.ok {
				t.Fatalf("mpvEventStatus(%#v) = (%q, %t), want (%q, %t)", tt.event, status, ok, tt.status, tt.ok)
			}
		})
	}
}

func TestScanMPVEventsIgnoresRepliesAndMalformedMessages(t *testing.T) {
	input := strings.Join([]string{
		`{"request_id":1,"error":"success","data":false}`,
		`not json`,
		`{"event":"file-loaded"}`,
		`{"event":"property-change","name":"volume","data":85}`,
		`{"event":"end-file","reason":"error","error":"loading failed"}`,
	}, "\n") + "\n"
	var events []mpvIPCEvent
	if err := scanMPVEvents(strings.NewReader(input), func(event mpvIPCEvent) {
		events = append(events, event)
	}); err != nil {
		t.Fatalf("scanMPVEvents: %v", err)
	}
	want := []mpvIPCEvent{
		{Event: "file-loaded"},
		{Event: "property-change", Name: "volume", Data: json.RawMessage("85")},
		{Event: "end-file", Reason: "error", Error: "loading failed"},
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
}

func TestMPVObservePropertiesPayloadBatchesJSONCommands(t *testing.T) {
	payload := mpvObservePropertiesPayload([]string{"time-pos", "", "duration"})
	lines := strings.Split(strings.TrimSpace(string(payload)), "\n")
	if len(lines) != 2 {
		t.Fatalf("observe payload lines = %q, want 2 commands", lines)
	}
	var first struct {
		Command []any `json:"command"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("decode first observe command: %v", err)
	}
	want := []any{"observe_property", float64(1), "time-pos"}
	if !reflect.DeepEqual(first.Command, want) {
		t.Fatalf("first observe command = %#v, want %#v", first.Command, want)
	}
}

func TestObservedPropertiesWaitForKnownInitialValues(t *testing.T) {
	observed := newMPVObservedProperties([]string{"time-pos", "pause", ""})
	now := time.Unix(100, 0)
	if values, ready := observed.update(mpvIPCEvent{Event: "property-change", Name: "time-pos"}, now); ready || values != nil {
		t.Fatalf("first known property ready=%t values=%#v, want not ready", ready, values)
	}
	if values, ready := observed.update(mpvIPCEvent{Event: "property-change", Name: "playlist-pos", Data: json.RawMessage("1")}, now); ready || values != nil {
		t.Fatalf("unknown property ready=%t values=%#v, want ignored", ready, values)
	}
	values, ready := observed.update(mpvIPCEvent{Event: "property-change", Name: "pause", Data: json.RawMessage("false")}, now)
	if !ready {
		t.Fatalf("all initial known properties ready=%t values=%#v, want ready", ready, values)
	}
	if _, ok := values["playlist-pos"]; ok {
		t.Fatalf("unknown property leaked into values: %#v", values)
	}
	if value, ok := values["time-pos"]; !ok || value != nil {
		t.Fatalf("missing-data time-pos = %#v, present=%t; want present nil", value, ok)
	}
	if value, ok := values["pause"].(bool); !ok || value {
		t.Fatalf("pause = %#v, present=%t; want false", values["pause"], ok)
	}
}

func TestObservedPropertiesThrottleProgressButKeepLatestValue(t *testing.T) {
	observed := newMPVObservedProperties([]string{"time-pos", "pause"})
	start := time.Unix(200, 0)
	if _, ready := observed.update(mpvIPCEvent{Event: "property-change", Name: "time-pos", Data: json.RawMessage("0")}, start); ready {
		t.Fatal("observer became ready before all initial properties arrived")
	}
	if _, ready := observed.update(mpvIPCEvent{Event: "property-change", Name: "pause", Data: json.RawMessage("false")}, start); !ready {
		t.Fatal("observer did not become ready after all initial properties arrived")
	}
	if values, emit := observed.update(mpvIPCEvent{Event: "property-change", Name: "time-pos", Data: json.RawMessage("1")}, start.Add(50*time.Millisecond)); emit || values != nil {
		t.Fatalf("early progress emit=%t values=%#v, want throttled", emit, values)
	}
	values, emit := observed.update(mpvIPCEvent{Event: "property-change", Name: "time-pos", Data: json.RawMessage("2")}, start.Add(130*time.Millisecond))
	if !emit {
		t.Fatalf("later progress emit=%t values=%#v, want emitted", emit, values)
	}
	if got := values["time-pos"]; got != float64(2) {
		t.Fatalf("time-pos = %#v, want latest throttled value 2", got)
	}
}

func TestObservedPropertiesEmitControlChangesImmediately(t *testing.T) {
	observed := newMPVObservedProperties([]string{"time-pos", "pause", "track-list"})
	start := time.Unix(300, 0)
	observed.update(mpvIPCEvent{Event: "property-change", Name: "time-pos", Data: json.RawMessage("0")}, start)
	observed.update(mpvIPCEvent{Event: "property-change", Name: "pause", Data: json.RawMessage("false")}, start)
	observed.update(mpvIPCEvent{Event: "property-change", Name: "track-list", Data: json.RawMessage("[]")}, start)
	values, emit := observed.update(mpvIPCEvent{Event: "property-change", Name: "pause", Data: json.RawMessage("true")}, start.Add(time.Millisecond))
	if !emit {
		t.Fatalf("pause change emit=%t values=%#v, want immediate emit", emit, values)
	}
	if got := values["pause"]; got != true {
		t.Fatalf("pause = %#v, want true", got)
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

func TestExperimentalNativePlayerEnabledIsExplicitOptIn(t *testing.T) {
	for _, value := range []string{"", "0", "true", "yes", "2"} {
		if experimentalNativePlayerEnabled(value) {
			t.Fatalf("experimentalNativePlayerEnabled(%q) = true, want false", value)
		}
	}
	if !experimentalNativePlayerEnabled("1") {
		t.Fatal("experimentalNativePlayerEnabled(\"1\") = false, want true")
	}
}

func TestSystemMPVLookupEnabledIsExplicitOptIn(t *testing.T) {
	for _, value := range []string{"", "0", "true", "yes", "2"} {
		if systemMPVLookupEnabled(value) {
			t.Fatalf("systemMPVLookupEnabled(%q) = true, want false", value)
		}
	}
	if !systemMPVLookupEnabled("1") {
		t.Fatal("systemMPVLookupEnabled(\"1\") = false, want true")
	}
}
