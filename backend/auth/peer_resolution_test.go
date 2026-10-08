package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
)

type driveDialogInvoker func(context.Context, bin.Encoder, bin.Decoder) error

func (f driveDialogInvoker) Invoke(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
	return f(ctx, input, output)
}

func encodeDialogResponse(output bin.Decoder, response tg.MessagesDialogsClass) error {
	var buffer bin.Buffer
	if err := response.Encode(&buffer); err != nil {
		return err
	}
	return output.Decode(&buffer)
}

func TestResolveDriveChannelUsesDialogHashForBroadcastAndMegagroup(t *testing.T) {
	for _, test := range []struct {
		name      string
		folder    int
		megagroup bool
	}{
		{"primary broadcast", 0, false}, {"archived shared megagroup", 1, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			folders := make([]int, 0, 2)
			api := tg.NewClient(driveDialogInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
				request, ok := input.(*tg.MessagesGetDialogsRequest)
				if !ok {
					return errors.New("resolution attempted a channel RPC without its access hash")
				}
				if !request.Flags.Has(1) {
					return errors.New("folder flag missing")
				}
				folders = append(folders, request.FolderID)
				if request.FolderID != test.folder {
					return encodeDialogResponse(output, &tg.MessagesDialogs{})
				}
				channel := &tg.Channel{ID: 81, AccessHash: 991, Broadcast: !test.megagroup, Megagroup: test.megagroup, Photo: &tg.ChatPhotoEmpty{}}
				return encodeDialogResponse(output, &tg.MessagesDialogs{
					Dialogs: []tg.DialogClass{&tg.Dialog{Peer: &tg.PeerChannel{ChannelID: 81}}},
					Chats:   []tg.ChatClass{channel},
				})
			}))
			channel, peer, err := ResolveDriveChannel(t.Context(), api, 81)
			if err != nil || channel == nil || peer == nil {
				t.Fatalf("resolve = %v, %v, %v; want dialog peer", channel, peer, err)
			}
			if channel.ChannelID != 81 || channel.AccessHash != 991 || peer.ChannelID != 81 || peer.AccessHash != 991 {
				t.Fatalf("channel %+v, peer %+v; want id 81 hash 991", channel, peer)
			}
			if len(folders) != test.folder+1 {
				t.Fatalf("folders = %v, want stop at requested folder", folders)
			}
		})
	}
}

func TestResolveDriveChannelContinuesAcrossDialogPages(t *testing.T) {
	pages := 0
	api := tg.NewClient(driveDialogInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		request, ok := input.(*tg.MessagesGetDialogsRequest)
		if !ok {
			return errors.New("unexpected RPC")
		}
		pages++
		if pages == 1 {
			return encodeDialogResponse(output, &tg.MessagesDialogsSlice{Count: 2,
				Dialogs:  []tg.DialogClass{&tg.Dialog{Peer: &tg.PeerChannel{ChannelID: 80}, TopMessage: 44}},
				Chats:    []tg.ChatClass{&tg.Channel{ID: 80, AccessHash: 990, Photo: &tg.ChatPhotoEmpty{}}},
				Messages: []tg.MessageClass{&tg.Message{ID: 44, Date: 100, PeerID: &tg.PeerChannel{ChannelID: 80}}},
			})
		}
		if request.OffsetID != 44 || request.OffsetDate != 100 {
			return errors.New("dialog pagination cursor lost")
		}
		return encodeDialogResponse(output, &tg.MessagesDialogs{
			Dialogs: []tg.DialogClass{&tg.Dialog{Peer: &tg.PeerChannel{ChannelID: 81}}},
			Chats:   []tg.ChatClass{&tg.Channel{ID: 81, AccessHash: 991, Megagroup: true, Photo: &tg.ChatPhotoEmpty{}}},
		})
	}))
	_, peer, err := ResolveDriveChannel(t.Context(), api, 81)
	if err != nil || peer == nil || peer.AccessHash != 991 || pages != 2 {
		t.Fatalf("peer %v, err %v, pages %d; want second-page hash 991", peer, err, pages)
	}
}

func TestResolveDriveChannelRejectsInaccessibleEntities(t *testing.T) {
	for _, test := range []struct {
		name   string
		entity tg.ChatClass
	}{
		{"left", &tg.Channel{ID: 81, AccessHash: 991, Left: true, Photo: &tg.ChatPhotoEmpty{}}},
		{"minimal", &tg.Channel{ID: 81, AccessHash: 991, Min: true, Photo: &tg.ChatPhotoEmpty{}}},
		{"forbidden", &tg.ChannelForbidden{ID: 81, AccessHash: 991}},
		{"missing hash", &tg.Channel{ID: 81, Photo: &tg.ChatPhotoEmpty{}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			api := tg.NewClient(driveDialogInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
				if _, ok := input.(*tg.MessagesGetDialogsRequest); !ok {
					return errors.New("unexpected RPC")
				}
				return encodeDialogResponse(output, &tg.MessagesDialogs{
					Dialogs: []tg.DialogClass{&tg.Dialog{Peer: &tg.PeerChannel{ChannelID: 81}}}, Chats: []tg.ChatClass{test.entity},
				})
			}))
			channel, peer, err := ResolveDriveChannel(t.Context(), api, 81)
			if err == nil || channel != nil || peer != nil {
				t.Fatalf("resolve = %v, %v, %v; want inaccessible rejection", channel, peer, err)
			}
		})
	}
}

func TestResolveDriveChannelPreservesRPCFailureAndCancellation(t *testing.T) {
	failure := errors.New("dialog access failed")
	calls := 0
	api := tg.NewClient(driveDialogInvoker(func(context.Context, bin.Encoder, bin.Decoder) error { calls++; return failure }))
	if _, _, err := ResolveDriveChannel(t.Context(), api, 81); !errors.Is(err, failure) {
		t.Fatalf("error %v, want original RPC failure", err)
	}
	if calls != 1 {
		t.Fatalf("RPC calls %d, want stop on first failure", calls)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := ResolveDriveChannel(ctx, api, 81); !errors.Is(err, context.Canceled) {
		t.Fatalf("error %v, want context canceled", err)
	}
	if calls != 1 {
		t.Fatal("canceled resolution reached Telegram")
	}
}
