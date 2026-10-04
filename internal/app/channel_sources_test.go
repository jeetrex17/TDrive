package app

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"TDrive/backend"
	"TDrive/backend/core"
	"TDrive/backend/projection"
	"TDrive/backend/tgclient"

	"github.com/gotd/td/telegram"
	_ "modernc.org/sqlite"
)

type stalledChannelResolve struct {
	*tgclient.Fake
	stall   atomic.Bool
	entered chan struct{}
	release chan struct{}
}

func (c *stalledChannelResolve) ResolveDocument(ctx context.Context, peer tgclient.InputPeer, msgID int64) (tgclient.DocumentRef, error) {
	if c.stall.Load() {
		select {
		case c.entered <- struct{}{}:
		default:
		}
		select {
		case <-c.release:
		case <-ctx.Done():
			return tgclient.DocumentRef{}, ctx.Err()
		}
	}
	return c.Fake.ResolveDocument(ctx, peer, msgID)
}

func TestChannelMediaLogoutRevokesAndRejectsInFlightOpen(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	oldDB := backend.DB
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	backend.DB = db
	t.Cleanup(func() { backend.DB = oldDB; _ = db.Close() })
	if err := projection.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	fake := tgclient.NewFake(8080)
	fake.SeedJoinedBroadcastChannels(tgclient.JoinedBroadcastChannel{ID: 7331, AccessHash: 77, Title: "Film club"})
	fake.SeedHistory(tgclient.HistoryMessage{ChannelID: 7331, MsgID: 42, HasMedia: true,
		DocumentID: 4343, DocumentAccessHash: 98, DocumentName: "clip.mp4",
		MimeType: "video/mp4", Video: true, MediaSize: 128})
	fake.SeedDocumentBody(42, make([]byte, 128))
	client := &stalledChannelResolve{Fake: fake, entered: make(chan struct{}, 1), release: make(chan struct{})}
	engine, err := core.New(t.Context(), core.Config{TG: client, SkipDBInit: true,
		Connect: func() (*telegram.Client, error) { return nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(engine.Close)
	app := &App{ctx: t.Context(), engine: engine}
	app.initServices("dev")
	connected, err := app.drives.ConnectChannelSource(7331, 8080)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := app.media.OpenChannelMedia(7331, 42, connected.AccountID, connected.Generation)
	if err != nil {
		t.Fatal(err)
	}
	client.stall.Store(true)
	openDone := make(chan error, 1)
	go func() {
		_, err := app.media.OpenChannelMedia(7331, 42, connected.AccountID, connected.Generation)
		openDone <- err
	}()
	select {
	case <-client.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("second open did not reach Telegram resolve")
	}
	if err := app.runWithClosedMountForLogout(func() error {
		response, err := http.Get(opened.URL)
		if err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Errorf("old channel URL remained live during logout: %d", response.StatusCode)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	close(client.release)
	select {
	case err := <-openDone:
		if err == nil {
			t.Fatal("in-flight channel open published after logout")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight open did not finish after logout")
	}
}
