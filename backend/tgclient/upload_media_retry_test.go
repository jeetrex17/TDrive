package tgclient

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

func TestSendUploadedMediaRetainsUploadedHandleAndIdentityThroughThrottling(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := &Gotd{writes: newWriteCoordinator(nil, nil)}
		file := &tg.InputFileBig{ID: 87, Parts: 20, Name: "clip.mkv"}
		request := &tg.MessagesSendMediaRequest{
			RandomID: 53, Message: "file header",
			Media: &tg.InputMediaUploadedDocument{File: file},
		}
		waits := []time.Duration{16 * time.Second, 15 * time.Second, time.Minute, 16 * time.Second}
		attempts := 0
		lastAttempt := time.Now()
		result, err := g.sendUploadedMedia(t.Context(), request, func(_ context.Context, actual *tg.MessagesSendMediaRequest) (tg.UpdatesClass, error) {
			if actual != request || actual.RandomID != 53 || actual.Media.(*tg.InputMediaUploadedDocument).File != file {
				t.Fatalf("media request = %+v, want original uploaded handle and random id", actual)
			}
			if attempts > 0 && time.Since(lastAttempt) < waits[attempts-1] {
				t.Fatalf("attempt %d arrived before Telegram's %s cooldown", attempts+1, waits[attempts-1])
			}
			lastAttempt = time.Now()
			attempts++
			if attempts <= len(waits) {
				return nil, tgerr.New(420, fmt.Sprintf("FLOOD_WAIT_%d", int(waits[attempts-1]/time.Second)))
			}
			return &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateMessageID{ID: 99, RandomID: 53}}}, nil
		})
		if err != nil || result.MsgID != 99 || attempts != 5 {
			t.Fatalf("send = %+v, %v after %d attempts; want receipt 99 after all cooldowns", result, err, attempts)
		}
	})
}

func TestSendUploadedMediaReturnsUnknownTransportOutcomeWithoutInnerRetry(t *testing.T) {
	t.Parallel()
	g := &Gotd{writes: newWriteCoordinator(nil, nil)}
	want := errors.New("connection reset by peer")
	attempts := 0
	_, err := g.sendUploadedMedia(t.Context(), &tg.MessagesSendMediaRequest{RandomID: 53}, func(context.Context, *tg.MessagesSendMediaRequest) (tg.UpdatesClass, error) {
		attempts++
		return nil, want
	})
	if attempts != 1 || !errors.Is(err, want) || !errors.Is(err, ErrSendOutcomeUnknown) {
		t.Fatalf("send = %v after %d attempts, want original unknown outcome after one", err, attempts)
	}
}

func TestSendUploadedMediaCancellationReleasesMessageSlot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := &Gotd{writes: newWriteCoordinator(nil, nil)}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		attempts := 0
		go func() {
			_, err := g.sendUploadedMedia(ctx, &tg.MessagesSendMediaRequest{RandomID: 53}, func(context.Context, *tg.MessagesSendMediaRequest) (tg.UpdatesClass, error) {
				attempts++
				return nil, tgerr.New(420, "FLOOD_WAIT_3600")
			})
			done <- err
		}()
		synctest.Wait()
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) || attempts != 1 {
			t.Fatalf("send = %v after %d attempts, want canceled during first cooldown", err, attempts)
		}
		select {
		case g.writes.messageSlot <- struct{}{}:
			<-g.writes.messageSlot
		default:
			t.Fatal("canceled media publication retained the shared message slot")
		}
	})
}
