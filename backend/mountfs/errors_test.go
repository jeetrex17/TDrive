package mountfs

import (
	"context"
	"errors"
	"testing"
)

func TestOpenPropagatesAccessDeniedDistinctly(t *testing.T) {
	t.Parallel()

	source := newFakeDirectorySource(map[string][]SourceEntry{
		RootID: {
			{ID: "f:locked", ParentID: RootID, Name: "locked.txt", Kind: KindFile},
		},
	})
	fs := mustNewFS(t, 42, source, &fakeContentOpener{err: ErrAccessDenied})

	_, err := fs.Open(context.Background(), "/locked.txt")
	if !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("Open() error = %v, want ErrAccessDenied", err)
	}
	if errors.Is(err, ErrContentUnavailable) {
		t.Fatalf("Open() error = %v, must remain distinct from ErrContentUnavailable", err)
	}
}

func TestOpenRejectsNilContentAsUnavailable(t *testing.T) {
	t.Parallel()

	source := newFakeDirectorySource(map[string][]SourceEntry{
		RootID: {
			{ID: "f:missing", ParentID: RootID, Name: "missing.txt", Kind: KindFile},
		},
	})
	fs := mustNewFS(t, 42, source, nilContentOpener{})

	if _, err := fs.Open(context.Background(), "/missing.txt"); !errors.Is(err, ErrContentUnavailable) {
		t.Fatalf("Open() error = %v, want ErrContentUnavailable", err)
	}
}

type nilContentOpener struct{}

func (nilContentOpener) OpenContent(context.Context, int64, SourceEntry) (RandomAccessContent, error) {
	return nil, nil
}
