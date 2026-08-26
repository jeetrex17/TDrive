package nativeplayer

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
)

var ErrUnsupported = errors.New("native player is not supported on this platform")
var ErrDecoderUnsafe = errors.New("native player decoder preflight failed")
var ErrPlayerExited = errors.New("native media player exited unexpectedly")

type PlaybackStatus string

const (
	StatusOpening   PlaybackStatus = "opening"
	StatusPlaying   PlaybackStatus = "playing"
	StatusPaused    PlaybackStatus = "paused"
	StatusBuffering PlaybackStatus = "buffering"
	StatusEnded     PlaybackStatus = "ended"
	StatusFailed    PlaybackStatus = "failed"
	StatusClosed    PlaybackStatus = "closed"
)

type TrackType string

const (
	TrackTypeAudio    TrackType = "audio"
	TrackTypeSubtitle TrackType = "subtitle"
)

// Track is the small, cross-platform subset of mpv track metadata needed by
// the player UI. Video tracks are intentionally omitted: aid/sid are the only
// selectable tracks exposed at this boundary.
type Track struct {
	ID       int64     `json:"id"`
	Type     TrackType `json:"type"`
	Title    string    `json:"title"`
	Language string    `json:"language"`
	Codec    string    `json:"codec"`
	Selected bool      `json:"selected"`
	Default  bool      `json:"default"`
	Forced   bool      `json:"forced"`
}

type BufferedRange struct {
	Start float64
	End   float64
}

type State struct {
	Status      PlaybackStatus
	Error       string
	EOF         bool
	Paused      bool
	CurrentTime float64
	Duration    float64
	Buffered    []BufferedRange
	Volume      float64
	Muted       bool
	Rate        float64
	Loading     bool
	Tracks      []Track
}

type StateHandler func(State)

type Options struct {
	UseHTMLControls bool
	OnState         StateHandler
}

type Rect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

func (r Rect) Valid() bool {
	const maxCoordinate = 1_000_000
	values := [...]float64{r.X, r.Y, r.Width, r.Height}
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) > maxCoordinate {
			return false
		}
	}
	return r.Width > 0 && r.Height > 0
}

var mpvStatePropertyNames = []string{
	"time-pos",
	"duration",
	"pause",
	"mute",
	"volume",
	"speed",
	"paused-for-cache",
	"cache-buffering-state",
	"demuxer-cache-duration",
	"eof-reached",
	"idle-active",
	"track-list",
}

// stateFromMPVProperties is shared by the Windows and Linux sidecars so they
// cannot silently drift on EOF, buffering, volume, or track semantics.
func stateFromMPVProperties(values map[string]any) State {
	current := cleanMPVSeconds(mpvNumberProperty(values, "time-pos"))
	duration := cleanMPVSeconds(mpvNumberProperty(values, "duration"))
	paused := mpvBoolProperty(values, "pause")
	eof := mpvBoolProperty(values, "eof-reached")
	idle := mpvBoolProperty(values, "idle-active")
	volume := clampMPVFloat(mpvNumberProperty(values, "volume")/100, 0, 1)
	rate := clampMPVFloat(mpvNumberProperty(values, "speed"), 0.25, 4)
	if rate == 0 {
		rate = 1
	}
	bufferingPercent := mpvNumberProperty(values, "cache-buffering-state")
	loading := mpvBoolProperty(values, "paused-for-cache") || (!paused && bufferingPercent > 0 && bufferingPercent < 100)

	state := State{
		Paused:      paused,
		CurrentTime: clampMPVFloat(current, 0, maxMPVFloat(duration, current)),
		Duration:    duration,
		Volume:      volume,
		Muted:       mpvBoolProperty(values, "mute") || volume == 0,
		Rate:        rate,
		Loading:     loading,
		EOF:         eof,
		Tracks:      tracksFromProperty(values["track-list"]),
	}
	cacheDuration := cleanMPVSeconds(mpvNumberProperty(values, "demuxer-cache-duration"))
	if duration > 0 && cacheDuration > 0 {
		state.Buffered = []BufferedRange{{
			Start: clampMPVFloat(state.CurrentTime, 0, duration),
			End:   clampMPVFloat(state.CurrentTime+cacheDuration, 0, duration),
		}}
	}
	if idle && !eof {
		state.Status = StatusOpening
		state.Paused = true
		state.Loading = true
	}
	return normalizeState(state)
}

func normalizeState(state State) State {
	normalized := state
	normalized.Tracks = append([]Track(nil), state.Tracks...)
	normalized.Buffered = append([]BufferedRange(nil), state.Buffered...)

	switch {
	case normalized.Status == StatusClosed:
		normalized.Paused = true
		normalized.Loading = false
		normalized.Error = ""
	case normalized.Error != "" || normalized.Status == StatusFailed:
		normalized.Status = StatusFailed
		normalized.Paused = true
		normalized.Loading = false
		if normalized.Error == "" {
			normalized.Error = ErrPlayerExited.Error()
		}
	case normalized.EOF || normalized.Status == StatusEnded:
		normalized.Status = StatusEnded
		normalized.EOF = true
		normalized.Paused = true
		normalized.Loading = false
		normalized.Error = ""
		if normalized.Duration > 0 {
			normalized.CurrentTime = normalized.Duration
		}
	case normalized.Status == StatusOpening:
		normalized.Paused = true
		normalized.Loading = true
		normalized.Error = ""
	case normalized.Loading:
		normalized.Status = StatusBuffering
		normalized.Error = ""
	case normalized.Paused:
		normalized.Status = StatusPaused
		normalized.Error = ""
	default:
		normalized.Status = StatusPlaying
		normalized.Error = ""
	}
	return normalized
}

func terminalState(status PlaybackStatus) State {
	state := State{Status: status, Paused: true, Rate: 1}
	if status == StatusFailed {
		state.Error = ErrPlayerExited.Error()
	}
	return normalizeState(state)
}

func sidecarExitStatus(closed bool, lastState State) PlaybackStatus {
	if closed {
		return StatusClosed
	}
	if lastState.EOF || lastState.Status == StatusEnded {
		return StatusEnded
	}
	return StatusFailed
}

func mpvCommandPayload(command ...string) []byte {
	payload, _ := json.Marshal(struct {
		Command []string `json:"command"`
	}{Command: command})
	return append(payload, '\n')
}

func sidecarPreflightEnabled(enable, skip string) bool {
	return enable == "1" && skip != "1"
}

func mpvPreflightInvocation(url string) ([]string, *strings.Reader) {
	args := []string{
		"--no-config",
		"--really-quiet",
		"--terminal=no",
		"--force-window=no",
		"--vo=null",
		"--ao=null",
		"--frames=1",
		"--demuxer-readahead-secs=0.5",
		"--demuxer-max-bytes=2097152",
		"--playlist=-",
	}
	return args, strings.NewReader("#EXTM3U\n" + url + "\n")
}

func tracksFromProperty(value any) []Track {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	tracks := make([]Track, 0, len(items))
	for _, item := range items {
		fields, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id, ok := positiveInt64(fields["id"])
		if !ok {
			continue
		}
		var trackType TrackType
		switch stringProperty(fields, "type") {
		case "audio":
			trackType = TrackTypeAudio
		case "sub":
			trackType = TrackTypeSubtitle
		default:
			continue
		}
		tracks = append(tracks, Track{
			ID:       id,
			Type:     trackType,
			Title:    boundedTrackText(stringProperty(fields, "title")),
			Language: boundedTrackText(stringProperty(fields, "lang")),
			Codec:    boundedTrackText(stringProperty(fields, "codec")),
			Selected: mpvBoolProperty(fields, "selected"),
			Default:  mpvBoolProperty(fields, "default"),
			Forced:   mpvBoolProperty(fields, "forced"),
		})
	}
	return tracks
}

func mpvNumberProperty(values map[string]any, key string) float64 {
	value, ok := values[key]
	if !ok || value == nil {
		return 0
	}
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case json.Number:
		n, _ := typed.Float64()
		return n
	default:
		return 0
	}
}

func positiveInt64(value any) (int64, bool) {
	var result int64
	switch typed := value.(type) {
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || typed < 1 || typed != math.Trunc(typed) || typed > math.MaxInt64 {
			return 0, false
		}
		result = int64(typed)
	case int:
		result = int64(typed)
	case int64:
		result = typed
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return 0, false
		}
		result = parsed
	default:
		return 0, false
	}
	return result, result > 0
}

func mpvBoolProperty(values map[string]any, key string) bool {
	value, ok := values[key]
	if !ok {
		return false
	}
	result, ok := value.(bool)
	return ok && result
}

func stringProperty(values map[string]any, key string) string {
	value, ok := values[key]
	if !ok {
		return ""
	}
	result, _ := value.(string)
	return result
}

func boundedTrackText(value string) string {
	const maxTrackTextRunes = 256
	runes := []rune(strings.TrimSpace(value))
	if len(runes) > maxTrackTextRunes {
		runes = runes[:maxTrackTextRunes]
	}
	return string(runes)
}

func cleanMPVSeconds(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0
	}
	return value
}

func clampMPVFloat(value, low, high float64) float64 {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func maxMPVFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
