package main

import (
	"encoding/base64"
	"errors"
	"fmt"
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
	Info         media.LogicalFile `json:"info"`
}

type nativeMediaSession struct {
	player    *nativeplayer.Player
	attaching bool
	encrypted bool
}

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
	if a.engine == nil {
		return NativeMediaResult{}, fmt.Errorf("backend not ready")
	}
	if token == "" {
		return NativeMediaResult{}, fmt.Errorf("media session is required")
	}
	if !rect.Valid() {
		return NativeMediaResult{}, fmt.Errorf("invalid video viewport")
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
	if opened.Token == "" || opened.URL == "" {
		return NativeMediaResult{}, fmt.Errorf("media session is not playable")
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
			a.emitNativeMediaState(token, state)
		},
	}
	player, err := nativeplayer.Start(a.ctx, opened.URL, rect, opts)
	if err != nil {
		if errors.Is(err, nativeplayer.ErrUnsupported) {
			return NativeMediaResult{}, fmt.Errorf("native playback is not available on this platform yet")
		}
		return NativeMediaResult{}, err
	}

	if !a.completeNativeMediaSession(opened.Token, reservation, player) {
		_ = player.Close()
		return NativeMediaResult{}, fmt.Errorf("native playback attachment was canceled")
	}
	completed = true

	return NativeMediaResult{
		Token:        opened.Token,
		Name:         opened.Name,
		ThumbnailURL: opened.ThumbnailURL,
		HTMLControls: htmlControls,
		Info:         opened.Info,
	}, nil
}

func (a *App) ResizeNativeMedia(token string, rect nativeplayer.Rect) error {
	player := a.nativeMediaPlayer(token)
	if player == nil {
		return nil
	}
	return player.Resize(rect)
}

func (a *App) NativeMediaCommand(token string, command []string) error {
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

func (a *App) emitNativeMediaState(token string, state nativeplayer.State) {
	if a.ctx == nil || token == "" {
		return
	}
	buffered := make([]map[string]float64, 0, len(state.Buffered))
	for _, item := range state.Buffered {
		if item.End <= item.Start {
			continue
		}
		buffered = append(buffered, map[string]float64{
			"start": item.Start,
			"end":   item.End,
		})
	}
	tracks := make([]nativeplayer.Track, len(state.Tracks))
	copy(tracks, state.Tracks)
	runtime.EventsEmit(a.ctx, "native_media_state", map[string]any{
		"token":        token,
		"status":       state.Status,
		"error":        state.Error,
		"eof":          state.EOF,
		"paused":       state.Paused,
		"current_time": state.CurrentTime,
		"duration":     state.Duration,
		"buffered":     buffered,
		"volume":       state.Volume,
		"muted":        state.Muted,
		"rate":         state.Rate,
		"loading":      state.Loading,
		"tracks":       tracks,
	})
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
		case "mute":
			if command[2] != "yes" && command[2] != "no" {
				return fmt.Errorf("invalid native media mute value")
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

// ShowNativeSeekThumbnail paints a seek-preview thumbnail over the native video.
// imageBase64 is the raw base64 of a JPEG/PNG frame the frontend already holds;
// rect is the desired preview box in CSS pixels. It is only meaningful on the
// Windows/Linux fallback (where HTML can't draw over the video) and is a no-op
// on platforms whose player does not implement an overlay.
func (a *App) ShowNativeSeekThumbnail(token string, imageBase64 string, rect nativeplayer.Rect) error {
	player := a.nativeMediaPlayer(token)
	if os.Getenv("TDRIVE_MEDIA_THUMB_DEBUG") == "1" {
		fmt.Fprintf(os.Stderr, "[seek-overlay] App.ShowNativeSeekThumbnail b64len=%d hasPlayer=%v\n", len(imageBase64), player != nil)
	}
	if player == nil {
		return nil
	}
	data, err := base64.StdEncoding.DecodeString(imageBase64)
	if err != nil {
		return fmt.Errorf("native seek thumbnail: decode image: %w", err)
	}
	return player.ShowSeekThumbnail(data, rect)
}

// MoveNativeSeekThumbnail moves the already-painted seek-preview overlay without
// re-uploading or re-decoding the frame. Windows calls this on every scrub move;
// keeping it separate from ShowNativeSeekThumbnail avoids doing JPEG decode and
// GDI bitmap upload work just to follow the cursor.
func (a *App) MoveNativeSeekThumbnail(token string, rect nativeplayer.Rect) error {
	player := a.nativeMediaPlayer(token)
	if player == nil {
		return nil
	}
	return player.MoveSeekThumbnail(rect)
}

// HideNativeSeekThumbnail hides the seek-preview overlay for the session.
func (a *App) HideNativeSeekThumbnail(token string) error {
	player := a.nativeMediaPlayer(token)
	if player == nil {
		return nil
	}
	return player.HideSeekThumbnail()
}
