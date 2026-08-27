package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"os"
	"strconv"

	"TDrive/backend/media"
	"TDrive/backend/nativeplayer"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type NativeMediaResult struct {
	Token        string            `json:"token"`
	Name         string            `json:"name"`
	ThumbnailURL string            `json:"thumbnail_url"`
	HTMLControls bool              `json:"html_controls"`
	Presentation string            `json:"presentation"`
	InitialState *NativeMediaState `json:"initial_state,omitempty"`
	Info         media.LogicalFile `json:"info"`
}

type NativeMediaRange struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

type NativeMediaState struct {
	Token       string                      `json:"token,omitempty"`
	Sequence    uint64                      `json:"sequence"`
	Status      nativeplayer.PlaybackStatus `json:"status"`
	Error       string                      `json:"error,omitempty"`
	EOF         bool                        `json:"eof"`
	Paused      bool                        `json:"paused"`
	CurrentTime float64                     `json:"current_time"`
	Duration    float64                     `json:"duration"`
	Buffered    []NativeMediaRange          `json:"buffered"`
	Volume      float64                     `json:"volume"`
	Muted       bool                        `json:"muted"`
	Rate        float64                     `json:"rate"`
	Loading     bool                        `json:"loading"`
	Tracks      []nativeplayer.Track        `json:"tracks"`
}

type nativeMediaSession struct {
	player        *nativeplayer.Player
	attaching     bool
	encrypted     bool
	stateSequence uint64
	lastState     *NativeMediaState
	terminal      bool
}

const (
	// Bridge input is untrusted. These ceilings keep token hashing, command
	// parsing, base64 allocation, and image-header parsing bounded.
	maxNativeMediaSessionTokenBytes    = 256
	maxNativeMediaCommandArguments     = 3
	maxNativeMediaCommandArgumentBytes = 64
	maxNativeSeekThumbnailDecodedBytes = 1 << 20
	maxNativeSeekThumbnailEncodedBytes = ((maxNativeSeekThumbnailDecodedBytes + 2) / 3) * 4
	maxNativeSeekThumbnailDimension    = 4096
	maxNativeSeekThumbnailPixels       = 4_194_304
)

var (
	errInvalidNativeMediaSession   = errors.New("invalid native media session")
	errInvalidNativeMediaViewport  = errors.New("invalid native media viewport")
	errNativeMediaCommandTooLarge  = errors.New("native media command exceeds size limit")
	errInvalidNativeSeekThumbnail  = errors.New("invalid native seek thumbnail")
	errNativeSeekThumbnailTooLarge = errors.New("native seek thumbnail exceeds size limit")
	errNativeSeekThumbnailDisplay  = errors.New("native seek thumbnail could not be displayed")
)

// OpenNativeMedia opens the same tokenized loopback stream as OpenMedia, then
// hands it to the native player. The returned token owns both resources.
func (a *App) OpenNativeMedia(msgID int, rect nativeplayer.Rect) (NativeMediaResult, error) {
	if a.engine == nil {
		return NativeMediaResult{}, fmt.Errorf("backend not ready")
	}
	if !rect.Valid() {
		return NativeMediaResult{}, fmt.Errorf("invalid video viewport")
	}

	opened, err := a.engine.MediaService().Open(a.ctx, a.ActiveChannelID(), int64(msgID))
	if err != nil {
		return NativeMediaResult{}, err
	}
	result, err := a.attachNativeMedia(opened, rect)
	if err != nil {
		_ = a.engine.MediaService().CloseSession(opened.Token)
		return NativeMediaResult{}, err
	}
	return result, nil
}

// AttachNativeMedia attaches the native player to an existing loopback media
// session. It is used when the embedded webview cannot decode a file, allowing
// the fallback to keep the same token and range cache. Ownership of the media
// session remains with the original opener, including on attachment failure.
func (a *App) AttachNativeMedia(token string, rect nativeplayer.Rect) (NativeMediaResult, error) {
	if token == "" {
		return NativeMediaResult{}, fmt.Errorf("media session is required")
	}
	if err := validateNativeMediaSessionToken(token); err != nil {
		return NativeMediaResult{}, err
	}
	if !rect.Valid() {
		return NativeMediaResult{}, fmt.Errorf("invalid video viewport")
	}
	if a.engine == nil {
		return NativeMediaResult{}, fmt.Errorf("backend not ready")
	}
	opened, err := a.engine.MediaService().OpenResultForToken(token)
	if err != nil {
		return NativeMediaResult{}, err
	}
	return a.attachNativeMedia(opened, rect)
}

func (a *App) attachNativeMedia(opened media.OpenResult, rect nativeplayer.Rect) (NativeMediaResult, error) {
	if !rect.Valid() {
		return NativeMediaResult{}, fmt.Errorf("invalid video viewport")
	}
	if err := validateNativeMediaOpenResult(opened); err != nil {
		return NativeMediaResult{}, err
	}
	reservation, err := a.reserveNativeMediaSession(opened.Token, opened.Info.Encrypted)
	if err != nil {
		return NativeMediaResult{}, err
	}
	completed := false
	defer func() {
		if !completed {
			a.releaseNativeMediaReservation(opened.Token, reservation)
		}
	}()

	if err := nativeplayer.PreflightDecode(a.ctx, opened.URL); err != nil {
		if errors.Is(err, nativeplayer.ErrDecoderUnsafe) {
			return NativeMediaResult{}, fmt.Errorf("the native decoder crashed while checking this video, so playback was blocked for safety")
		}
		return NativeMediaResult{}, err
	}

	token := opened.Token
	htmlControls := nativeHTMLControlsEnabled() && nativeplayer.SupportsHTMLControls()
	opts := nativeplayer.Options{
		UseHTMLControls: htmlControls,
		OnState: func(state nativeplayer.State) {
			snapshot, emit := a.recordNativeMediaState(token, reservation, state)
			if emit {
				a.emitNativeMediaState(snapshot)
			}
		},
	}
	player, err := nativeplayer.Start(a.ctx, opened.URL, rect, opts)
	if err != nil {
		if errors.Is(err, nativeplayer.ErrUnsupported) {
			return NativeMediaResult{}, fmt.Errorf("native playback is not available on this platform yet")
		}
		return NativeMediaResult{}, err
	}
	if _, ok := a.nativeMediaStateSnapshot(opened.Token, reservation); !ok {
		_, _ = a.recordNativeMediaState(token, reservation, nativeplayer.State{
			Status:  nativeplayer.StatusOpening,
			Paused:  true,
			Loading: true,
			Volume:  1,
			Rate:    1,
		})
	}

	if !a.completeNativeMediaSession(opened.Token, reservation, player) {
		_ = player.Close()
		return NativeMediaResult{}, fmt.Errorf("native playback attachment was canceled")
	}
	completed = true
	initialState, _ := a.nativeMediaStateSnapshot(opened.Token, reservation)

	return NativeMediaResult{
		Token:        opened.Token,
		Name:         opened.Name,
		ThumbnailURL: opened.ThumbnailURL,
		HTMLControls: htmlControls,
		Presentation: string(player.Presentation()),
		InitialState: initialState,
		Info:         opened.Info,
	}, nil
}

func (a *App) ResizeNativeMedia(token string, rect nativeplayer.Rect) error {
	if err := validateNativeMediaSessionToken(token); err != nil {
		return err
	}
	if !rect.Valid() {
		return errInvalidNativeMediaViewport
	}
	player := a.nativeMediaPlayer(token)
	if player == nil {
		return nil
	}
	return player.Resize(rect)
}

func (a *App) NativeMediaCommand(token string, command []string) error {
	if err := validateNativeMediaSessionToken(token); err != nil {
		return err
	}
	if err := validateNativeMediaCommand(command); err != nil {
		return err
	}
	player := a.nativeMediaPlayer(token)
	if player == nil {
		return nil
	}
	return player.Command(command...)
}

func (a *App) CloseNativeMedia(token string) error {
	if token == "" {
		return nil
	}
	if err := validateNativeMediaSessionToken(token); err != nil {
		return err
	}
	session := a.takeNativeMediaSession(token)
	if session != nil && session.player != nil {
		_ = session.player.Close()
	}
	if a.engine != nil {
		err := a.engine.MediaService().CloseSession(token)
		if errors.Is(err, media.ErrSessionNotFound) {
			return nil
		}
		return err
	}
	return nil
}

func (a *App) closeAllNativeMedia() {
	a.nativeMediaMu.Lock()
	sessions := a.nativeMedia
	a.nativeMedia = nil
	a.nativeMediaMu.Unlock()

	for token, session := range sessions {
		if session != nil && session.player != nil {
			_ = session.player.Close()
		}
		if a.engine != nil {
			_ = a.engine.MediaService().CloseSession(token)
		}
	}
}

// closeEncryptedNativeMedia detaches only sessions backed by encrypted media.
// Removing entries under the lock also cancels in-flight attachments: their
// completion step will fail and close any player they managed to start.
func (a *App) closeEncryptedNativeMedia() {
	type encryptedSession struct {
		token   string
		session *nativeMediaSession
	}

	a.nativeMediaMu.Lock()
	sessions := make([]encryptedSession, 0)
	for token, session := range a.nativeMedia {
		if session == nil || !session.encrypted {
			continue
		}
		delete(a.nativeMedia, token)
		sessions = append(sessions, encryptedSession{token: token, session: session})
	}
	a.nativeMediaMu.Unlock()

	for _, item := range sessions {
		if item.session.player != nil {
			_ = item.session.player.Close()
		}
		if a.engine != nil {
			_ = a.engine.MediaService().CloseSession(item.token)
		}
	}
}

func (a *App) emitNativeMediaState(state *NativeMediaState) {
	if a.ctx == nil || state == nil || state.Token == "" {
		return
	}
	runtime.EventsEmit(a.ctx, "native_media_state", state)
}

func (a *App) nativeMediaSession(token string) *nativeMediaSession {
	if token == "" {
		return nil
	}
	a.nativeMediaMu.Lock()
	defer a.nativeMediaMu.Unlock()
	return a.nativeMedia[token]
}

func (a *App) nativeMediaPlayer(token string) *nativeplayer.Player {
	if token == "" {
		return nil
	}
	a.nativeMediaMu.Lock()
	defer a.nativeMediaMu.Unlock()
	session := a.nativeMedia[token]
	if session == nil {
		return nil
	}
	return session.player
}

func (a *App) reserveNativeMediaSession(token string, encrypted bool) (*nativeMediaSession, error) {
	if token == "" {
		return nil, fmt.Errorf("media session is required")
	}
	a.nativeMediaMu.Lock()
	defer a.nativeMediaMu.Unlock()
	if a.nativeMedia == nil {
		a.nativeMedia = make(map[string]*nativeMediaSession)
	}
	if _, exists := a.nativeMedia[token]; exists {
		return nil, fmt.Errorf("native playback is already attached or attaching")
	}
	reservation := &nativeMediaSession{attaching: true, encrypted: encrypted}
	a.nativeMedia[token] = reservation
	return reservation, nil
}

func (a *App) completeNativeMediaSession(token string, reservation *nativeMediaSession, player *nativeplayer.Player) bool {
	if token == "" || reservation == nil || player == nil {
		return false
	}
	a.nativeMediaMu.Lock()
	defer a.nativeMediaMu.Unlock()
	current, exists := a.nativeMedia[token]
	if !exists || current != reservation || !reservation.attaching {
		return false
	}
	reservation.player = player
	reservation.attaching = false
	return true
}

func (a *App) recordNativeMediaState(token string, reservation *nativeMediaSession, state nativeplayer.State) (*NativeMediaState, bool) {
	if token == "" || reservation == nil {
		return nil, false
	}
	a.nativeMediaMu.Lock()
	defer a.nativeMediaMu.Unlock()
	current := a.nativeMedia[token]
	if current == nil || current != reservation {
		return nil, false
	}
	if current.terminal {
		return cloneNativeMediaState(current.lastState), false
	}
	current.stateSequence++
	snapshot := nativeMediaStateFromPlayer(token, current.stateSequence, state)
	current.lastState = snapshot
	current.terminal = snapshot.Status == nativeplayer.StatusFailed || snapshot.Status == nativeplayer.StatusClosed
	return snapshot, !current.attaching
}

func (a *App) nativeMediaStateSnapshot(token string, reservation *nativeMediaSession) (*NativeMediaState, bool) {
	if token == "" || reservation == nil {
		return nil, false
	}
	a.nativeMediaMu.Lock()
	defer a.nativeMediaMu.Unlock()
	current := a.nativeMedia[token]
	if current == nil || current != reservation || current.lastState == nil {
		return nil, false
	}
	snapshot := cloneNativeMediaState(current.lastState)
	return snapshot, true
}

func nativeMediaStateFromPlayer(token string, sequence uint64, state nativeplayer.State) *NativeMediaState {
	buffered := make([]NativeMediaRange, 0, len(state.Buffered))
	for _, item := range state.Buffered {
		if item.End <= item.Start {
			continue
		}
		buffered = append(buffered, NativeMediaRange{Start: item.Start, End: item.End})
	}
	tracks := make([]nativeplayer.Track, len(state.Tracks))
	copy(tracks, state.Tracks)
	return &NativeMediaState{
		Token:       token,
		Sequence:    sequence,
		Status:      state.Status,
		Error:       state.Error,
		EOF:         state.EOF,
		Paused:      state.Paused,
		CurrentTime: state.CurrentTime,
		Duration:    state.Duration,
		Buffered:    buffered,
		Volume:      state.Volume,
		Muted:       state.Muted,
		Rate:        state.Rate,
		Loading:     state.Loading,
		Tracks:      tracks,
	}
}

func cloneNativeMediaState(state *NativeMediaState) *NativeMediaState {
	if state == nil {
		return nil
	}
	clone := *state
	clone.Buffered = append([]NativeMediaRange(nil), state.Buffered...)
	clone.Tracks = append([]nativeplayer.Track(nil), state.Tracks...)
	return &clone
}

func (a *App) releaseNativeMediaReservation(token string, reservation *nativeMediaSession) {
	if token == "" || reservation == nil {
		return
	}
	a.nativeMediaMu.Lock()
	if a.nativeMedia[token] == reservation && reservation.attaching {
		delete(a.nativeMedia, token)
	}
	a.nativeMediaMu.Unlock()
}

func nativeHTMLControlsEnabled() bool {
	return os.Getenv("TDRIVE_NATIVE_VIDEO_FALLBACK") != "1"
}

func (a *App) takeNativeMediaSession(token string) *nativeMediaSession {
	if token == "" {
		return nil
	}
	a.nativeMediaMu.Lock()
	defer a.nativeMediaMu.Unlock()
	session := a.nativeMedia[token]
	delete(a.nativeMedia, token)
	return session
}

func validateNativeMediaCommand(command []string) error {
	if len(command) == 0 {
		return fmt.Errorf("native media command is empty")
	}
	if len(command) > maxNativeMediaCommandArguments {
		return errNativeMediaCommandTooLarge
	}
	for _, argument := range command {
		if len(argument) > maxNativeMediaCommandArgumentBytes {
			return errNativeMediaCommandTooLarge
		}
	}

	switch command[0] {
	case "cycle":
		if len(command) != 2 {
			return fmt.Errorf("invalid native media cycle command")
		}
		switch command[1] {
		case "pause", "mute":
			return nil
		default:
			return fmt.Errorf("unsupported native media cycle target")
		}
	case "seek":
		if len(command) != 3 {
			return fmt.Errorf("invalid native media seek command")
		}
		value, err := strconv.ParseFloat(command[1], 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("invalid native media seek offset")
		}
		switch command[2] {
		case "relative":
			if math.Abs(value) > 3600 {
				return fmt.Errorf("native media relative seek is too large")
			}
		case "absolute":
			if value < 0 || value > 24*3600 {
				return fmt.Errorf("native media absolute seek is out of range")
			}
		default:
			return fmt.Errorf("unsupported native media seek mode")
		}
		return nil
	case "set":
		if len(command) != 3 {
			return fmt.Errorf("invalid native media set command")
		}
		switch command[1] {
		case "volume":
			value, err := strconv.ParseFloat(command[2], 64)
			if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 100 {
				return fmt.Errorf("invalid native media volume")
			}
			return nil
		case "speed":
			value, err := strconv.ParseFloat(command[2], 64)
			if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0.25 || value > 4 {
				return fmt.Errorf("invalid native media speed")
			}
			return nil
		case "mute", "pause":
			if command[2] != "yes" && command[2] != "no" {
				return fmt.Errorf("invalid native media boolean value")
			}
			return nil
		case "aid", "sid":
			if command[2] == "auto" || command[2] == "no" {
				return nil
			}
			trackID, err := strconv.ParseInt(command[2], 10, 32)
			if err != nil || trackID <= 0 {
				return fmt.Errorf("invalid native media track selection")
			}
			return nil
		default:
			return fmt.Errorf("unsupported native media set target")
		}
	default:
		return fmt.Errorf("unsupported native media command")
	}
}

func validateNativeMediaOpenResult(opened media.OpenResult) error {
	if opened.Kind != media.StreamKindVideo {
		return media.ErrUnsupportedMediaType
	}
	if opened.Token == "" || opened.URL == "" {
		return fmt.Errorf("media session is not playable")
	}
	return nil
}

func validateNativeMediaSessionToken(token string) error {
	if len(token) == 0 || len(token) > maxNativeMediaSessionTokenBytes {
		return errInvalidNativeMediaSession
	}
	return nil
}

// ShowNativeSeekThumbnail paints a seek-preview thumbnail over the native video.
// imageBase64 is the raw base64 of a JPEG/PNG frame the frontend already holds;
// rect is the desired preview box in CSS pixels. It is only meaningful on the
// Windows fallback (where HTML can't draw over the video) and is a no-op on
// platforms whose player does not implement an overlay.
func (a *App) ShowNativeSeekThumbnail(token string, imageBase64 string, rect nativeplayer.Rect) error {
	if a == nil {
		return errInvalidNativeMediaSession
	}
	if err := validateNativeMediaSessionToken(token); err != nil {
		return err
	}
	if !rect.Valid() {
		return errInvalidNativeMediaViewport
	}
	player := a.nativeMediaPlayer(token)
	if player == nil {
		return nil
	}
	data, err := decodeNativeSeekThumbnail(imageBase64)
	if err != nil {
		return err
	}
	if err := player.ShowSeekThumbnail(data, rect); err != nil {
		return errNativeSeekThumbnailDisplay
	}
	return nil
}

func decodeNativeSeekThumbnail(encoded string) ([]byte, error) {
	if encoded == "" {
		return nil, errInvalidNativeSeekThumbnail
	}
	if len(encoded) > maxNativeSeekThumbnailEncodedBytes {
		return nil, errNativeSeekThumbnailTooLarge
	}

	data, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return nil, errInvalidNativeSeekThumbnail
	}
	if len(data) == 0 {
		return nil, errInvalidNativeSeekThumbnail
	}
	if len(data) > maxNativeSeekThumbnailDecodedBytes {
		return nil, errNativeSeekThumbnailTooLarge
	}

	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "jpeg" && format != "png") {
		return nil, errInvalidNativeSeekThumbnail
	}
	if config.Width <= 0 || config.Height <= 0 {
		return nil, errInvalidNativeSeekThumbnail
	}
	if config.Width > maxNativeSeekThumbnailDimension || config.Height > maxNativeSeekThumbnailDimension {
		return nil, errNativeSeekThumbnailTooLarge
	}
	if uint64(config.Width)*uint64(config.Height) > maxNativeSeekThumbnailPixels {
		return nil, errNativeSeekThumbnailTooLarge
	}
	return data, nil
}

// MoveNativeSeekThumbnail moves the already-painted seek-preview overlay without
// re-uploading or re-decoding the frame. Windows calls this on every scrub move;
// keeping it separate from ShowNativeSeekThumbnail avoids doing JPEG decode and
// GDI bitmap upload work just to follow the cursor.
func (a *App) MoveNativeSeekThumbnail(token string, rect nativeplayer.Rect) error {
	if err := validateNativeMediaSessionToken(token); err != nil {
		return err
	}
	if !rect.Valid() {
		return errInvalidNativeMediaViewport
	}
	player := a.nativeMediaPlayer(token)
	if player == nil {
		return nil
	}
	return player.MoveSeekThumbnail(rect)
}

// HideNativeSeekThumbnail hides the seek-preview overlay for the session.
func (a *App) HideNativeSeekThumbnail(token string) error {
	if err := validateNativeMediaSessionToken(token); err != nil {
		return err
	}
	player := a.nativeMediaPlayer(token)
	if player == nil {
		return nil
	}
	return player.HideSeekThumbnail()
}
