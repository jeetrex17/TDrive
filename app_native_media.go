package main

import (
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
	player *nativeplayer.Player
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

	if err := nativeplayer.PreflightDecode(a.ctx, opened.URL); err != nil {
		_ = a.engine.MediaService().CloseSession(opened.Token)
		if errors.Is(err, nativeplayer.ErrDecoderUnsafe) {
			return NativeMediaResult{}, fmt.Errorf("the native decoder crashed while checking this video, so playback was blocked for safety")
		}
		return NativeMediaResult{}, err
	}

	token := opened.Token
	htmlControls := nativeHTMLControlsEnabled() && nativeplayer.SupportsHTMLControls()
	opts := nativeplayer.Options{UseHTMLControls: htmlControls}
	if htmlControls {
		opts.OnState = func(state nativeplayer.State) {
			a.emitNativeMediaState(token, state)
		}
	}
	player, err := nativeplayer.Start(a.ctx, opened.URL, rect, opts)
	if err != nil {
		_ = a.engine.MediaService().CloseSession(opened.Token)
		if errors.Is(err, nativeplayer.ErrUnsupported) {
			return NativeMediaResult{}, fmt.Errorf("native playback is not available on this platform yet")
		}
		return NativeMediaResult{}, err
	}

	a.nativeMediaMu.Lock()
	if a.nativeMedia == nil {
		a.nativeMedia = make(map[string]*nativeMediaSession)
	}
	a.nativeMedia[opened.Token] = &nativeMediaSession{player: player}
	a.nativeMediaMu.Unlock()

	return NativeMediaResult{
		Token:        opened.Token,
		Name:         opened.Name,
		ThumbnailURL: opened.ThumbnailURL,
		HTMLControls: htmlControls,
		Info:         opened.Info,
	}, nil
}

func (a *App) ResizeNativeMedia(token string, rect nativeplayer.Rect) error {
	session := a.nativeMediaSession(token)
	if session == nil || session.player == nil {
		return nil
	}
	return session.player.Resize(rect)
}

func (a *App) NativeMediaCommand(token string, command []string) error {
	if err := validateNativeMediaCommand(command); err != nil {
		return err
	}
	session := a.nativeMediaSession(token)
	if session == nil || session.player == nil {
		return nil
	}
	return session.player.Command(command...)
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
		return a.engine.MediaService().CloseSession(token)
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
	runtime.EventsEmit(a.ctx, "native_media_state", map[string]any{
		"token":        token,
		"paused":       state.Paused,
		"current_time": state.CurrentTime,
		"duration":     state.Duration,
		"buffered":     buffered,
		"volume":       state.Volume,
		"muted":        state.Muted,
		"rate":         state.Rate,
		"loading":      state.Loading,
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
		default:
			return fmt.Errorf("unsupported native media set target")
		}
	default:
		return fmt.Errorf("unsupported native media command")
	}
}
