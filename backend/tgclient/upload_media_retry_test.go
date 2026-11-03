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

func TestSendUploadedMediaRecoversLostReceiptWithoutDuplicate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := &Gotd{writes: newWriteCoordinator(nil, nil)}
		file := &tg.InputFileBig{ID: 87, Parts: 20, Name: "clip.mkv"}
		request := &tg.MessagesSendMediaRequest{RandomID: 53, Media: &tg.InputMediaUploadedDocument{File: file}}
		accepted := map[int64]int{}
		attempts := 0
		result, err := g.sendUploadedMedia(t.Context(), request, func(_ context.Context, actual *tg.MessagesSendMediaRequest) (tg.UpdatesClass, error) {
			attempts++
			if actual != request || actual.RandomID != 53 || actual.Media.(*tg.InputMediaUploadedDocument).File != file {
				t.Fatal("retry changed the publication identity or uploaded handle")
			}
			// Model Telegram accepting the first send, losing its receipt, and
			// deduplicating subsequent attempts by random_id after connectivity returns.
			if _, exists := accepted[actual.RandomID]; !exists {
				accepted[actual.RandomID] = 99
			}
			if attempts <= 8 {
				return nil, errors.New("connection reset by peer")
			}
			return &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateMessageID{ID: accepted[actual.RandomID], RandomID: actual.RandomID}}}, nil
		})
		if err != nil || result.MsgID != 99 || attempts != 9 || len(accepted) != 1 {
			t.Fatalf("send = %+v, %v; attempts=%d accepted=%v", result, err, attempts, accepted)
		}
	})
}

func TestSendUploadedMediaRetainsUnknownOutcomeWhenRecoveryStops(t *testing.T) {
	for _, cancelRecovery := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel=%v", cancelRecovery), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				g := &Gotd{writes: newWriteCoordinator(nil, nil)}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				attempts := 0
				transport := errors.New("connection reset by peer")
				permanent := tgerr.New(400, "FILE_PART_MISSING")
				_, err := g.sendUploadedMedia(ctx, &tg.MessagesSendMediaRequest{RandomID: 53}, func(context.Context, *tg.MessagesSendMediaRequest) (tg.UpdatesClass, error) {
					attempts++
					if attempts == 1 {
						if cancelRecovery {
							cancel()
						}
						return nil, transport
					}
					return nil, permanent
				})
				want := error(permanent)
				if cancelRecovery {
					want = context.Canceled
				}
				if !errors.Is(err, want) || !errors.Is(err, ErrSendOutcomeUnknown) || !errors.Is(err, transport) {
					t.Fatalf("send = %v, want terminal error and retained unknown outcome", err)
				}
				select {
				case g.writes.messageSlot <- struct{}{}:
					<-g.writes.messageSlot
				default:
					t.Fatal("recovery retained message slot")
				}
			})
		})
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
