package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"TDrive/backend/media"
	"TDrive/backend/nativeplayer"
)

func TestValidateNativeMediaCommandAcceptsSupportedCommands(t *testing.T) {
	tests := [][]string{
		{"cycle", "pause"},
		{"cycle", "mute"},
		{"seek", "10", "relative"},
		{"seek", "-5.5", "relative"},
		{"seek", "125.25", "absolute"},
		{"set", "volume", "0"},
		{"set", "volume", "82.5"},
		{"set", "speed", "0.5"},
		{"set", "speed", "2"},
		{"set", "mute", "yes"},
		{"set", "mute", "no"},
		{"set", "pause", "yes"},
		{"set", "pause", "no"},
		{"set", "aid", "1"},
		{"set", "aid", "42"},
		{"set", "aid", "auto"},
		{"set", "aid", "no"},
		{"set", "sid", "2"},
		{"set", "sid", "auto"},
		{"set", "sid", "no"},
	}

	for _, command := range tests {
		if err := validateNativeMediaCommand(command); err != nil {
			t.Fatalf("validateNativeMediaCommand(%v) returned error: %v", command, err)
		}
	}
}

func TestNativeHTMLControlsEnabledHonorsFallbackEnv(t *testing.T) {
	t.Setenv("TDRIVE_NATIVE_VIDEO_FALLBACK", "")
	if !nativeHTMLControlsEnabled() {
		t.Fatal("nativeHTMLControlsEnabled returned false without fallback env")
	}

	t.Setenv("TDRIVE_NATIVE_VIDEO_FALLBACK", "1")
	if nativeHTMLControlsEnabled() {
		t.Fatal("nativeHTMLControlsEnabled returned true with fallback env")
	}
}

func TestValidateNativeMediaOpenResultRejectsNonVideo(t *testing.T) {
	sessionID := "opaque-session-id"
	valid := media.OpenResult{Token: sessionID, URL: "http://127.0.0.1/media", Kind: media.StreamKindVideo}
	if err := validateNativeMediaOpenResult(valid); err != nil {
		t.Fatalf("video result rejected: %v", err)
	}

	for _, kind := range []media.StreamKind{
		media.StreamKindAudio,
		media.StreamKindPDF,
		media.StreamKindText,
		media.StreamKindUnknown,
	} {
		opened := valid
		opened.Kind = kind
		if err := validateNativeMediaOpenResult(opened); !errors.Is(err, media.ErrUnsupportedMediaType) {
			t.Fatalf("kind %q error = %v, want ErrUnsupportedMediaType", kind, err)
		}
	}
}

func TestNativeMediaReservationRetainsSequencedStateBeforeCompletion(t *testing.T) {
	app := &App{}
	reservation, err := app.reserveNativeMediaSession("opaque-session-token", false)
	if err != nil {
		t.Fatalf("reserveNativeMediaSession: %v", err)
	}

	failed := nativeplayer.State{Status: nativeplayer.StatusFailed, Error: nativeplayer.ErrPlayerExited.Error(), Paused: true}
	snapshot, emit := app.recordNativeMediaState("opaque-session-token", reservation, failed)
	if emit {
		t.Fatal("state emitted while native attachment was still in progress")
	}
	if snapshot.Sequence != 1 || snapshot.Status != nativeplayer.StatusFailed {
		t.Fatalf("recorded state = %#v, want sequence 1 failed", snapshot)
	}
	if !app.completeNativeMediaSession("opaque-session-token", reservation, new(nativeplayer.Player)) {
		t.Fatal("reservation could not be completed")
	}

	initial, ok := app.nativeMediaStateSnapshot("opaque-session-token", reservation)
	if !ok || initial.Sequence != 1 || initial.Status != nativeplayer.StatusFailed {
		t.Fatalf("initial state = %#v, %t; want retained sequence 1 failure", initial, ok)
	}

	playing := nativeplayer.State{Status: nativeplayer.StatusPlaying, Rate: 1, Volume: 1}
	next, emit := app.recordNativeMediaState("opaque-session-token", reservation, playing)
	if emit || next.Sequence != 1 || next.Status != nativeplayer.StatusFailed {
		t.Fatalf("state after terminal failure = %#v, emit=%t; want retained sequence 1 failure", next, emit)
	}

	active, err := app.reserveNativeMediaSession("active-session-token", false)
	if err != nil {
		t.Fatalf("reserve active session: %v", err)
	}
	opening := nativeplayer.State{Status: nativeplayer.StatusOpening, Paused: true, Loading: true, Rate: 1, Volume: 1}
	if snapshot, emit := app.recordNativeMediaState("active-session-token", active, opening); emit || snapshot.Sequence != 1 {
		t.Fatalf("opening state = %#v, emit=%t; want retained sequence 1", snapshot, emit)
	}
	if !app.completeNativeMediaSession("active-session-token", active, new(nativeplayer.Player)) {
		t.Fatal("active reservation could not be completed")
	}
	next, emit = app.recordNativeMediaState("active-session-token", active, playing)
	if !emit || next.Sequence != 2 || next.Status != nativeplayer.StatusPlaying {
		t.Fatalf("active state = %#v, emit=%t; want emitted sequence 2 playing", next, emit)
	}
}

func TestValidateNativeMediaCommandRejectsUnsupportedCommands(t *testing.T) {
	tests := [][]string{
		nil,
		{},
		{"loadfile", "https://example.com/video.mkv"},
		{"screenshot-to-file", "/tmp/frame.png"},
		{"cycle", "fullscreen"},
		{"cycle", "pause", "extra"},
		{"seek", "-1", "absolute"},
		{"seek", "90000", "absolute"},
		{"seek", "4000", "relative"},
		{"seek", "soon", "relative"},
		{"seek", "10", "exact"},
		{"set", "volume", "-1"},
		{"set", "volume", "101"},
		{"set", "speed", "0.1"},
		{"set", "speed", "5"},
		{"set", "mute", "maybe"},
		{"set", "pause", "maybe"},
		{"set", "pause", "true"},
		{"set", "pause", "1"},
		{"seek", strings.Repeat("9", maxNativeMediaCommandArgumentBytes+1), "absolute"},
		{"set", "aid", strings.Repeat("9", maxNativeMediaCommandArgumentBytes+1)},
		{"set", "pause", "yes", "extra"},
		{"set", "aid", "-1"},
		{"set", "aid", "1.5"},
		{"set", "aid", "yes"},
		{"set", "sid", "0"},
		{"set", "sid", "999999999999999999999"},
		{"set", "vid", "1"},
		{"set", "playlist-pos", "1"},
	}

	for _, command := range tests {
		if err := validateNativeMediaCommand(command); err == nil {
			t.Fatalf("validateNativeMediaCommand(%v) returned nil error", command)
		}
	}
}

func TestShowNativeSeekThumbnailSkipsStaleSessionBeforeDecoding(t *testing.T) {
	app := &App{}
	invalidOversizedPayload := strings.Repeat("!", maxNativeSeekThumbnailEncodedBytes+1)

	err := app.ShowNativeSeekThumbnail("stale-session-token", invalidOversizedPayload, validSeekThumbnailRect())
	if err != nil {
		t.Fatalf("ShowNativeSeekThumbnail() stale-session error = %v, want nil", err)
	}
}

func TestShowNativeSeekThumbnailValidatesTokenAndRectBeforeDecoding(t *testing.T) {
	const activeToken = "active-session-token"
	app := appWithNativeThumbnailPlayer(activeToken)
	validPayload := encodeSeekThumbnailPNG(t, 8, 8)

	tests := []struct {
		name    string
		token   string
		rect    nativeplayer.Rect
		wantErr error
	}{
		{name: "empty token", token: "", rect: validSeekThumbnailRect(), wantErr: errInvalidNativeMediaSession},
		{name: "oversized token", token: strings.Repeat("t", maxNativeMediaSessionTokenBytes+1), rect: validSeekThumbnailRect(), wantErr: errInvalidNativeMediaSession},
		{name: "zero width", token: activeToken, rect: nativeplayer.Rect{X: 10, Y: 10, Height: 81}, wantErr: errInvalidNativeMediaViewport},
		{name: "non-finite coordinate", token: activeToken, rect: nativeplayer.Rect{X: math.NaN(), Y: 10, Width: 144, Height: 81}, wantErr: errInvalidNativeMediaViewport},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := app.ShowNativeSeekThumbnail(test.token, validPayload, test.rect)
			assertSafeSeekThumbnailError(t, err, test.token, validPayload)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("ShowNativeSeekThumbnail() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestNativeMediaBridgeValidationBoundsRequestsBeforeLookup(t *testing.T) {
	const staleToken = "stale-session-token"
	app := &App{}
	oversizedToken := strings.Repeat("t", maxNativeMediaSessionTokenBytes+1)
	validRect := validSeekThumbnailRect()
	invalidRect := nativeplayer.Rect{X: math.Inf(1), Y: 10, Width: 144, Height: 81}

	if err := app.NativeMediaCommand(staleToken, []string{"set", "pause", "yes"}); err != nil {
		t.Fatalf("NativeMediaCommand() stale-session error = %v, want nil", err)
	}
	if err := app.ResizeNativeMedia(staleToken, validRect); err != nil {
		t.Fatalf("ResizeNativeMedia() stale-session error = %v, want nil", err)
	}
	if err := app.MoveNativeSeekThumbnail(staleToken, validRect); err != nil {
		t.Fatalf("MoveNativeSeekThumbnail() stale-session error = %v, want nil", err)
	}
	if err := app.HideNativeSeekThumbnail(staleToken); err != nil {
		t.Fatalf("HideNativeSeekThumbnail() stale-session error = %v, want nil", err)
	}

	if err := app.NativeMediaCommand(oversizedToken, []string{"set", "pause", "yes"}); !errors.Is(err, errInvalidNativeMediaSession) {
		t.Fatalf("NativeMediaCommand() oversized-token error = %v", err)
	}
	if err := app.ResizeNativeMedia(oversizedToken, validRect); !errors.Is(err, errInvalidNativeMediaSession) {
		t.Fatalf("ResizeNativeMedia() oversized-token error = %v", err)
	}
	if err := app.MoveNativeSeekThumbnail(oversizedToken, validRect); !errors.Is(err, errInvalidNativeMediaSession) {
		t.Fatalf("MoveNativeSeekThumbnail() oversized-token error = %v", err)
	}
	if err := app.HideNativeSeekThumbnail(oversizedToken); !errors.Is(err, errInvalidNativeMediaSession) {
		t.Fatalf("HideNativeSeekThumbnail() oversized-token error = %v", err)
	}
	if _, err := app.AttachNativeMedia(oversizedToken, validRect); !errors.Is(err, errInvalidNativeMediaSession) {
		t.Fatalf("AttachNativeMedia() oversized-token error = %v", err)
	}
	if err := app.CloseNativeMedia(oversizedToken); !errors.Is(err, errInvalidNativeMediaSession) {
		t.Fatalf("CloseNativeMedia() oversized-token error = %v", err)
	}
	if err := app.CloseNativeMedia(""); err != nil {
		t.Fatalf("CloseNativeMedia() empty-token error = %v, want nil", err)
	}
	if err := app.CloseNativeMedia(staleToken); err != nil {
		t.Fatalf("CloseNativeMedia() stale-session error = %v, want nil", err)
	}
	oversizedCommand := []string{"seek", strings.Repeat("9", maxNativeMediaCommandArgumentBytes+1), "absolute"}
	if err := app.NativeMediaCommand(staleToken, oversizedCommand); !errors.Is(err, errNativeMediaCommandTooLarge) {
		t.Fatalf("NativeMediaCommand() oversized-command error = %v", err)
	}
	if err := app.ResizeNativeMedia(staleToken, invalidRect); !errors.Is(err, errInvalidNativeMediaViewport) {
		t.Fatalf("ResizeNativeMedia() invalid-rect error = %v", err)
	}
	if err := app.MoveNativeSeekThumbnail(staleToken, invalidRect); !errors.Is(err, errInvalidNativeMediaViewport) {
		t.Fatalf("MoveNativeSeekThumbnail() invalid-rect error = %v", err)
	}
}

func TestShowNativeSeekThumbnailRejectsUnsafePayloads(t *testing.T) {
	const activeToken = "active-session-token"
	app := appWithNativeThumbnailPlayer(activeToken)

	oversizedDecoded := base64.StdEncoding.EncodeToString(make([]byte, maxNativeSeekThumbnailDecodedBytes+1))
	if len(oversizedDecoded) > maxNativeSeekThumbnailEncodedBytes {
		t.Fatalf("decoded-size fixture also exceeds encoded limit: %d > %d", len(oversizedDecoded), maxNativeSeekThumbnailEncodedBytes)
	}

	tests := []struct {
		name    string
		payload string
	}{
		{name: "oversized encoded input", payload: strings.Repeat("A", maxNativeSeekThumbnailEncodedBytes+1)},
		{name: "oversized decoded input", payload: oversizedDecoded},
		{name: "malformed base64", payload: "%%%secret-invalid-base64%%%"},
		{name: "malformed image", payload: base64.StdEncoding.EncodeToString([]byte("secret-not-an-image"))},
		{name: "unsupported format", payload: encodeSeekThumbnailGIF(t)},
		{name: "oversized width", payload: encodeSeekThumbnailPNG(t, maxNativeSeekThumbnailDimension+1, 1)},
		{name: "oversized pixel count", payload: encodeSeekThumbnailPNG(t, maxNativeSeekThumbnailDimension, maxNativeSeekThumbnailPixels/maxNativeSeekThumbnailDimension+1)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := app.ShowNativeSeekThumbnail(activeToken, test.payload, validSeekThumbnailRect())
			assertSafeSeekThumbnailError(t, err, activeToken, test.payload)
		})
	}
}

func TestShowNativeSeekThumbnailAcceptsJPEGAndPNG(t *testing.T) {
	const activeToken = "active-session-token"
	app := appWithNativeThumbnailPlayer(activeToken)

	tests := []struct {
		name    string
		payload string
	}{
		{name: "jpeg", payload: encodeSeekThumbnailJPEG(t)},
		{name: "png", payload: encodeSeekThumbnailPNG(t, 8, 8)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := app.ShowNativeSeekThumbnail(activeToken, test.payload, validSeekThumbnailRect()); err != nil {
				t.Fatalf("ShowNativeSeekThumbnail() error = %v", err)
			}
		})
	}
}

func validSeekThumbnailRect() nativeplayer.Rect {
	return nativeplayer.Rect{X: 10, Y: 10, Width: 144, Height: 81}
}

func appWithNativeThumbnailPlayer(token string) *App {
	return &App{nativeMedia: map[string]*nativeMediaSession{
		token: {player: new(nativeplayer.Player)},
	}}
}

func assertSafeSeekThumbnailError(t *testing.T, err error, secrets ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("ShowNativeSeekThumbnail() error = nil, want rejection")
	}
	for _, secret := range secrets {
		if secret != "" && len(secret) <= len(err.Error()) && strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaked request data: %q", err)
		}
	}
}

func encodeSeekThumbnailPNG(t *testing.T, width, height int) string {
	t.Helper()
	return encodeSeekThumbnailImage(t, image.NewGray(image.Rect(0, 0, width, height)), func(buffer *bytes.Buffer, source image.Image) error {
		return png.Encode(buffer, source)
	})
}

func encodeSeekThumbnailJPEG(t *testing.T) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	return encodeSeekThumbnailImage(t, img, func(buffer *bytes.Buffer, source image.Image) error {
		return jpeg.Encode(buffer, source, nil)
	})
}

func encodeSeekThumbnailGIF(t *testing.T) string {
	t.Helper()
	img := image.NewPaletted(image.Rect(0, 0, 1, 1), color.Palette{color.Black})
	return encodeSeekThumbnailImage(t, img, func(buffer *bytes.Buffer, source image.Image) error {
		return gif.Encode(buffer, source, nil)
	})
}

func encodeSeekThumbnailImage(t *testing.T, source image.Image, encode func(*bytes.Buffer, image.Image) error) string {
	t.Helper()
	var buffer bytes.Buffer
	if err := encode(&buffer, source); err != nil {
		t.Fatalf("encode seek-thumbnail fixture: %v", err)
	}
	return base64.StdEncoding.EncodeToString(buffer.Bytes())
}

func TestNativeMediaReservationRejectsConcurrentDuplicateAttach(t *testing.T) {
	app := &App{}
	const callers = 32
	start := make(chan struct{})
	var successes atomic.Int32
	var winner *nativeMediaSession
	var winnerMu sync.Mutex
	var wg sync.WaitGroup

	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			reservation, err := app.reserveNativeMediaSession("opaque-session-token", false)
			if err != nil {
				if strings.Contains(err.Error(), "opaque-session-token") {
					t.Errorf("duplicate-attach error leaked the media token: %v", err)
				}
				return
			}
			successes.Add(1)
			winnerMu.Lock()
			winner = reservation
			winnerMu.Unlock()
		}()
	}
	close(start)
	wg.Wait()

	if got := successes.Load(); got != 1 {
		t.Fatalf("successful reservations = %d, want 1", got)
	}
	winnerMu.Lock()
	reservation := winner
	winnerMu.Unlock()
	if reservation == nil {
		t.Fatal("winning reservation is nil")
	}
	if !app.completeNativeMediaSession("opaque-session-token", reservation, new(nativeplayer.Player)) {
		t.Fatal("winning reservation could not be completed")
	}
	if got := app.nativeMediaSession("opaque-session-token"); got != reservation || got.player == nil || got.attaching {
		t.Fatalf("completed session = %#v, want active player", got)
	}
}

func TestNativeMediaReservationCannotCompleteAfterClose(t *testing.T) {
	app := &App{}
	reservation, err := app.reserveNativeMediaSession("opaque-session-token", false)
	if err != nil {
		t.Fatalf("reserveNativeMediaSession: %v", err)
	}
	if err := app.CloseNativeMedia("opaque-session-token"); err != nil {
		t.Fatalf("CloseNativeMedia: %v", err)
	}
	if app.completeNativeMediaSession("opaque-session-token", reservation, new(nativeplayer.Player)) {
		t.Fatal("reservation completed after it was closed")
	}
	if got := app.nativeMediaSession("opaque-session-token"); got != nil {
		t.Fatalf("native media session survived close: %#v", got)
	}
}

func TestCloseEncryptedNativeMediaRemovesOnlyEncryptedEntries(t *testing.T) {
	app := &App{nativeMedia: map[string]*nativeMediaSession{
		"encrypted-active":    {player: new(nativeplayer.Player), encrypted: true},
		"encrypted-attaching": {attaching: true, encrypted: true},
		"clear-active":        {player: new(nativeplayer.Player)},
		"clear-attaching":     {attaching: true},
	}}

	app.closeEncryptedNativeMedia()
	app.closeEncryptedNativeMedia()

	if app.nativeMediaSession("encrypted-active") != nil || app.nativeMediaSession("encrypted-attaching") != nil {
		t.Fatal("encrypted native sessions survived vault-lock cleanup")
	}
	if app.nativeMediaSession("clear-active") == nil || app.nativeMediaSession("clear-attaching") == nil {
		t.Fatal("clear native sessions were removed by encrypted-only cleanup")
	}
}
