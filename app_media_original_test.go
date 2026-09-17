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

func TestOpenOriginalImageRefreshesStaleRevisionOnce(t *testing.T) {
	opener := &staleOriginalImageOpener{currentRevision: 12}

	got, err := openOriginalImage(context.Background(), opener, 7, 42, 11)
	if err != nil {
		t.Fatalf("openOriginalImage: %v", err)
	}
	if got.Info.Revision != 12 {
		t.Fatalf("opened revision = %d, want 12", got.Info.Revision)
	}
	if len(opener.revisions) != 2 || opener.revisions[0] != 11 || opener.revisions[1] != 12 {
		t.Fatalf("OpenImage revisions = %v, want [11 12]", opener.revisions)
	}
	if opener.resolveCalls != 1 {
		t.Fatalf("Resolve calls = %d, want 1", opener.resolveCalls)
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

func (opener *recordingOriginalImageOpener) Resolve(context.Context, int64, int64) (media.LogicalFile, error) {
	return media.LogicalFile{}, errors.New("unexpected resolve")
}

type staleOriginalImageOpener struct {
	currentRevision int64
	revisions       []int64
	resolveCalls    int
}

func (opener *staleOriginalImageOpener) OpenImage(_ context.Context, _, _ int64, revision int64) (media.OpenResult, error) {
	opener.revisions = append(opener.revisions, revision)
	if revision != opener.currentRevision {
		return media.OpenResult{}, media.ErrStaleRevision
	}
	return media.OpenResult{Info: media.LogicalFile{Revision: revision}}, nil
}

func (opener *staleOriginalImageOpener) Resolve(_ context.Context, _, _ int64) (media.LogicalFile, error) {
	opener.resolveCalls++
	return media.LogicalFile{Revision: opener.currentRevision}, nil
}
