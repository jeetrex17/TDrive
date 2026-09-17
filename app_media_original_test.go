package main

import (
	"context"
	"errors"
	"testing"

	"TDrive/backend/media"
)

func TestOpenOriginalImageDelegatesExactIdentity(t *testing.T) {
	want := media.OpenResult{Token: "image-token", Kind: media.StreamKindImage}
	opener := &recordingOriginalImageOpener{result: want}
	ctx := context.WithValue(context.Background(), originalImageContextKey{}, "expected")

	got, err := openOriginalImage(ctx, opener, 51, 72, 9)
	if err != nil {
		t.Fatalf("openOriginalImage: %v", err)
	}
	if got.Token != want.Token || got.Kind != media.StreamKindImage {
		t.Fatalf("openOriginalImage result = %+v, want %+v", got, want)
	}
	if opener.ctx != ctx || opener.channelID != 51 || opener.msgID != 72 || opener.revision != 9 {
		t.Fatalf("delegated call = ctx:%v channel:%d msg:%d revision:%d", opener.ctx, opener.channelID, opener.msgID, opener.revision)
	}
}

func TestOpenOriginalImagePropagatesServiceError(t *testing.T) {
	wantErr := errors.New("open failed")
	_, err := openOriginalImage(context.Background(), &recordingOriginalImageOpener{err: wantErr}, 1, 2, 3)
	if !errors.Is(err, wantErr) {
		t.Fatalf("openOriginalImage error = %v, want %v", err, wantErr)
	}
}

func TestAppOpenOriginalImageRequiresBackend(t *testing.T) {
	if _, err := (&App{}).OpenOriginalImage(2, 1); err == nil {
		t.Fatal("OpenOriginalImage without backend succeeded")
	}
}

type originalImageContextKey struct{}

type recordingOriginalImageOpener struct {
	ctx       context.Context
	channelID int64
	msgID     int64
	revision  int64
	result    media.OpenResult
	err       error
}

func (opener *recordingOriginalImageOpener) OpenImage(ctx context.Context, channelID, msgID, revision int64) (media.OpenResult, error) {
	opener.ctx = ctx
	opener.channelID = channelID
	opener.msgID = msgID
	opener.revision = revision
	return opener.result, opener.err
}
