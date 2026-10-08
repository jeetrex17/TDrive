package tgclient

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
)

func TestPhotoHistoryAndRangeUseLargestFullSize(t *testing.T) {
	t.Parallel()
	photo := &tg.Photo{ID: 700, AccessHash: 800, DCID: 4, FileReference: []byte("photo-ref"), Sizes: []tg.PhotoSizeClass{
		&tg.PhotoStrippedSize{Type: "i", Bytes: []byte{1, 2}},
		&tg.PhotoSize{Type: "m", W: 320, H: 240, Size: 2000},
		&tg.PhotoSizeProgressive{Type: "y", W: 1280, H: 960, Sizes: []int{5000, 20000}},
		&tg.PhotoSize{Type: "x", W: 800, H: 600, Size: 10000},
	}}
	media := &tg.MessageMediaPhoto{Photo: photo}
	media.SetTTLSeconds(20)
	msg := &tg.Message{ID: 42, Media: media}
	history, ok := historyMessageFromTG(msg)
	if !ok || !history.HasMedia || history.MediaSize != 20000 || history.MimeType != "image/jpeg" || history.DocumentName != "Telegram photo 42.jpg" || history.DocumentID != 700 || history.TTLSeconds != 20 {
		t.Fatalf("photo history = %+v, ok %t", history, ok)
	}
	peer := InputPeer{ChannelID: 10, AccessHash: 20}
	refs, err := documentRefsInOrder(peer, []int64{42}, map[int64]tg.MessageClass{42: msg})
	if err != nil || len(refs) != 1 {
		t.Fatalf("photo refs = %+v, err %v", refs, err)
	}
	ref := refs[0]
	if ref.PhotoSizeType != "y" || ref.Size != 20000 || ref.DocumentID != 700 || ref.AccessHash != 800 || ref.DCID != 4 || ref.Peer != peer {
		t.Fatalf("photo ref = %+v", ref)
	}
	photo.FileReference[0] = 'X'
	if string(ref.FileReference) != "photo-ref" {
		t.Fatal("photo ref must own its reference bytes")
	}
	if _, _, err := documentOf(msg); !errors.Is(err, ErrNotFile) {
		t.Fatalf("document extraction must remain document-only, got %v", err)
	}
	api := tg.NewClient(invokerFunc(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		request := input.(*tg.UploadGetFileRequest)
		location, ok := request.Location.(*tg.InputPhotoFileLocation)
		if !ok || location.ID != 700 || location.AccessHash != 800 || location.ThumbSize != "y" || string(location.FileReference) != "photo-ref" {
			t.Errorf("photo location = %+v", request.Location)
		}
		var buf bin.Buffer
		if err := (&tg.UploadFile{Type: &tg.StorageFileJpeg{}, Bytes: []byte("jpeg")}).Encode(&buf); err != nil {
			return err
		}
		return output.Decode(&buf)
	}))
	dst := make([]byte, 4)
	n, err := (&Gotd{}).readDocumentRangeVia(t.Context(), nil, api, ref, 0, dst)
	if err != nil || n != 4 || string(dst) != "jpeg" {
		t.Fatalf("photo read = %q, %d, %v", dst, n, err)
	}
}

func TestPhotoCachedFallbackOwnsBytesAndReadsWithoutTelegram(t *testing.T) {
	t.Parallel()
	payload := bytes.Repeat([]byte{42}, 5000)
	msg := &tg.Message{ID: 7, Media: &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 70, Sizes: []tg.PhotoSizeClass{
		&tg.PhotoCachedSize{Type: "m", W: 320, H: 240, Bytes: payload},
	}}}}
	refs, err := documentRefsInOrder(InputPeer{ChannelID: 1}, []int64{7}, map[int64]tg.MessageClass{7: msg})
	if err != nil {
		t.Fatal(err)
	}
	clear(payload)
	dst := make([]byte, 904)
	n, err := (&Gotd{}).ReadDocumentRange(t.Context(), refs[0], 4096, dst)
	if err != nil || n != 904 || !bytes.Equal(dst, bytes.Repeat([]byte{42}, 904)) {
		t.Fatalf("cached photo tail = %v, %d, %v", dst[:4], n, err)
	}
	var stream bytes.Buffer
	download := &documentDownload{ref: refs[0], read: func(ctx context.Context, ref DocumentRef, offset int64, dst []byte) (int, error) {
		return (&Gotd{}).readDocumentRange(ctx, nil, ref, offset, dst)
	}}
	if err := download.stream(t.Context(), &stream); err != nil || !bytes.Equal(stream.Bytes(), bytes.Repeat([]byte{42}, 5000)) {
		t.Fatalf("cached download wrote %d bytes, err %v", stream.Len(), err)
	}
}

func TestPhotoRefreshKeepsVariantAndUpdatesReference(t *testing.T) {
	t.Parallel()
	version := []byte("old-ref")
	api := tg.NewClient(invokerFunc(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		request := input.(*tg.ChannelsGetMessagesRequest)
		if request.Channel.(*tg.InputChannel).ChannelID != 101 || request.Channel.(*tg.InputChannel).AccessHash != 202 {
			t.Errorf("photo lookup changed channel: %+v", request.Channel)
		}
		photo := &tg.Photo{ID: 999, AccessHash: 888, DCID: 4, FileReference: version,
			Sizes: []tg.PhotoSizeClass{&tg.PhotoSize{Type: "x", W: 800, H: 600, Size: 20000}}}
		var buf bin.Buffer
		result := &tg.MessagesChannelMessages{Messages: []tg.MessageClass{&tg.Message{ID: 7,
			PeerID: &tg.PeerChannel{ChannelID: 101}, Media: &tg.MessageMediaPhoto{Photo: photo}}}}
		if err := result.Encode(&buf); err != nil {
			return err
		}
		return output.Decode(&buf)
	}))
	peer := InputPeer{ChannelID: 101, AccessHash: 202}
	first, err := getMediaRefByMessageID(t.Context(), api, peer, 7)
	if err != nil {
		t.Fatal(err)
	}
	version = []byte("fresh-ref")
	fresh, err := getMediaRefByMessageID(t.Context(), api, peer, 7)
	if err != nil || fresh.DocumentID != 999 || fresh.Size != 20000 || fresh.PhotoSizeType != "x" || string(fresh.FileReference) != "fresh-ref" || string(first.FileReference) != "old-ref" {
		t.Fatalf("photo refresh = %+v, first %+v, err %v", fresh, first, err)
	}
}

func TestPhotoWithoutUsableSizeIsNotAdopted(t *testing.T) {
	t.Parallel()
	for _, size := range []tg.PhotoSizeClass{
		&tg.PhotoStrippedSize{Type: "i", Bytes: []byte{1}},
		&tg.PhotoSize{Type: "x", W: 800, H: 600, Size: -1},
		&tg.PhotoSizeProgressive{Type: "y", W: 800, H: 600, Sizes: []int{20, -1}},
		&tg.PhotoCachedSize{Type: "m", W: 320, H: 240, Bytes: make([]byte, RangeReadMaxBytes+1)},
	} {
		msg := &tg.Message{ID: 9, Media: &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 90, Sizes: []tg.PhotoSizeClass{size}}}}
		history, _ := historyMessageFromTG(msg)
		if history.HasMedia {
			t.Errorf("invalid size %T became file: %+v", size, history)
		}
		if _, err := documentRefsInOrder(InputPeer{ChannelID: 1}, []int64{9}, map[int64]tg.MessageClass{9: msg}); !errors.Is(err, ErrEmptyDocument) {
			t.Errorf("size %T: err %v, want ErrEmptyDocument", size, err)
		}
	}
}
