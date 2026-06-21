package main

import (
	"errors"
	"fmt"
	"strconv"

	"TDrive/backend/media"
	"TDrive/backend/nativeplayer"
)

type NativeMediaResult struct {
	Token string            `json:"token"`
	Name  string            `json:"name"`
	Info  media.LogicalFile `json:"info"`
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
			return NativeMediaResult{}, fmt.Errorf("this file crashes the local native decoder; bundled all-format playback is required for this format")
		}
		return NativeMediaResult{}, err
	}

	player, err := nativeplayer.Start(a.ctx, opened.URL, rect)
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
		Token: opened.Token,
		Name:  opened.Name,
		Info:  opened.Info,
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

func (a *App) nativeMediaSession(token string) *nativeMediaSession {
	if token == "" {
		return nil
	}
	a.nativeMediaMu.Lock()
	defer a.nativeMediaMu.Unlock()
	return a.nativeMedia[token]
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
		if len(command) != 3 || command[2] != "relative" {
			return fmt.Errorf("invalid native media seek command")
		}
		if _, err := strconv.ParseFloat(command[1], 64); err != nil {
			return fmt.Errorf("invalid native media seek offset")
		}
		return nil
	default:
		return fmt.Errorf("unsupported native media command")
	}
}
