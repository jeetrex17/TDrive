package nativeplayer

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strings"
	"time"
)

var ErrUnsupported = errors.New("native player is not supported on this platform")
var ErrDecoderUnsafe = errors.New("native player decoder preflight failed")
var ErrPlayerExited = errors.New("native media player exited unexpectedly")
var errIPCWriteTimeout = errors.New("native player: mpv IPC write timed out")

const observedProgressEmitInterval = 125 * time.Millisecond

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

type Presentation string

const (
	PresentationEmbedded   Presentation = "embedded"
	PresentationStandalone Presentation = "standalone"
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
	Start float64 `json:"start"`
	End   float64 `json:"end"`
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
	volume := 1.0
	if value, ok := mpvNumberPropertyOK(values, "volume"); ok && isFiniteMPVFloat(value) {
		volume = clampMPVFloat(value/100, 0, 1)
	}
	rate := 1.0
	if value, ok := mpvNumberPropertyOK(values, "speed"); ok && isFiniteMPVFloat(value) && value > 0 {
		rate = clampMPVFloat(value, 0.25, 4)
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
	} else if status == StatusEnded {
		state.EOF = true
	}
	return normalizeState(state)
}

func sidecarExitStatus(closed, standalone bool, err error) PlaybackStatus {
	if closed {
		return StatusClosed
	}
	if standalone && err == nil {
		return StatusClosed
	}
	return StatusFailed
}

func endedState(previous State) State {
	next := previous
	next.Status = StatusEnded
	next.Error = ""
	next.EOF = true
	next.Paused = true
	next.Loading = false
	return normalizeState(next)
}

type mpvIPCEvent struct {
	Event  string          `json:"event"`
	Reason string          `json:"reason"`
	Error  string          `json:"error"`
	Name   string          `json:"name"`
	Data   json.RawMessage `json:"data"`
}

func mpvEventStatus(event mpvIPCEvent) (PlaybackStatus, bool) {
	if event.Event != "end-file" {
		return "", false
	}
	switch event.Reason {
	case "eof":
		return StatusEnded, true
	case "error", "unknown":
		return StatusFailed, true
	case "quit":
		return StatusClosed, true
	default:
		return "", false
	}
}

func scanMPVEvents(reader io.Reader, onEvent func(mpvIPCEvent)) error {
	if reader == nil || onEvent == nil {
		return nil
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		var event mpvIPCEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil || event.Event == "" {
			continue
		}
		onEvent(event)
	}
	return scanner.Err()
}

func mpvObservePropertiesPayload(names []string) []byte {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	for index, name := range names {
		if name == "" {
			continue
		}
		_ = encoder.Encode(struct {
			Command []any `json:"command"`
		}{Command: []any{"observe_property", index + 1, name}})
	}
	return buf.Bytes()
}

func writeMPVObserveProperties(writer io.Writer, names []string) error {
	if writer == nil {
		return errors.New("native player: mpv IPC writer is required")
	}
	payload := mpvObservePropertiesPayload(names)
	if len(payload) == 0 {
		return nil
	}
	written, err := writer.Write(payload)
	if err != nil {
		return err
	}
	if written != len(payload) {
		return io.ErrShortWrite
	}
	return nil
}

type mpvObservedProperties struct {
	allowed          map[string]struct{}
	values           map[string]any
	seen             map[string]struct{}
	required         int
	ready            bool
	lastProgressEmit time.Time
}

func newMPVObservedProperties(names []string) *mpvObservedProperties {
	observed := &mpvObservedProperties{
		allowed: make(map[string]struct{}, len(names)),
		values:  make(map[string]any, len(names)),
		seen:    make(map[string]struct{}, len(names)),
	}
	for _, name := range names {
		if name == "" {
			continue
		}
		if _, exists := observed.allowed[name]; exists {
			continue
		}
		observed.allowed[name] = struct{}{}
		observed.required++
	}
	return observed
}

func (o *mpvObservedProperties) update(event mpvIPCEvent, now time.Time) (map[string]any, bool) {
	if o == nil || event.Event != "property-change" || event.Name == "" {
		return nil, false
	}
	if _, ok := o.allowed[event.Name]; !ok {
		return nil, false
	}
	var value any
	if len(event.Data) > 0 {
		if err := json.Unmarshal(event.Data, &value); err != nil {
			return nil, false
		}
	}
	o.values[event.Name] = value
	o.seen[event.Name] = struct{}{}
	if !o.ready {
		if len(o.seen) < o.required {
			return nil, false
		}
		o.ready = true
		o.lastProgressEmit = now
		return cloneMPVPropertyValues(o.values), true
	}
	if isHighFrequencyMPVProperty(event.Name) && !o.lastProgressEmit.IsZero() && now.Sub(o.lastProgressEmit) < observedProgressEmitInterval {
		return nil, false
	}
	if isHighFrequencyMPVProperty(event.Name) {
		o.lastProgressEmit = now
	}
	return cloneMPVPropertyValues(o.values), true
}

func cloneMPVPropertyValues(values map[string]any) map[string]any {
	clone := make(map[string]any, len(values))
	for name, value := range values {
		clone[name] = value
	}
	return clone
}

func isHighFrequencyMPVProperty(name string) bool {
	switch name {
	case "time-pos", "cache-buffering-state", "demuxer-cache-duration":
		return true
	default:
		return false
	}
}

type stoppableTimer interface {
	Stop() bool
}

type closeTimerScheduler func(time.Duration, func()) stoppableTimer

func writeAndCloseWithTimeout(writer io.WriteCloser, payload []byte, timeout time.Duration) error {
	return writeAndCloseWithTimer(writer, payload, timeout, func(delay time.Duration, callback func()) stoppableTimer {
		return time.AfterFunc(delay, callback)
	})
}

func writeAndCloseWithTimer(writer io.WriteCloser, payload []byte, timeout time.Duration, schedule closeTimerScheduler) error {
	if writer == nil {
		return errors.New("native player: mpv IPC writer is required")
	}
	if schedule == nil {
		return errors.New("native player: mpv IPC timeout scheduler is required")
	}
	timeoutDone := make(chan struct{})
	timer := schedule(timeout, func() {
		_ = writer.Close()
		close(timeoutDone)
	})
	written, writeErr := writer.Write(payload)
	var closeErr error
	if timer.Stop() {
		closeErr = writer.Close()
	} else {
		// The timer fired and closed the writer. That is only a timeout when
		// the write itself did not complete; a slow but successful write stands.
		<-timeoutDone
		if writeErr != nil {
			return errIPCWriteTimeout
		}
	}
	if writeErr != nil {
		return writeErr
	}
	if written != len(payload) {
		return io.ErrShortWrite
	}
	return closeErr
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
	value, _ := mpvNumberPropertyOK(values, key)
	return value
}

// Property values arrive through json.Unmarshal into any, so numbers are
// always float64.
func mpvNumberPropertyOK(values map[string]any, key string) (float64, bool) {
	value, ok := values[key].(float64)
	return value, ok
}

func positiveInt64(value any) (int64, bool) {
	typed, ok := value.(float64)
	if !ok || math.IsNaN(typed) || math.IsInf(typed, 0) || typed < 1 || typed != math.Trunc(typed) || typed > math.MaxInt64 {
		return 0, false
	}
	return int64(typed), true
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

func isFiniteMPVFloat(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
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

// nativePlayerEnabled reads a per-OS opt-out flag: native playback is on unless
// the variable is explicitly "0".
func nativePlayerEnabled(value string) bool {
	return value != "0"
}
