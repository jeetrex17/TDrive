package main

import (
	"errors"
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
