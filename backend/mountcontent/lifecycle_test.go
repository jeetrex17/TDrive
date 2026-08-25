package mountcontent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

func TestOpenerCloseDuringResolutionPreventsReaderPublication(t *testing.T) {
	db := newTestDB(t)
	projectFile(t, db, 10, projection.Op{
		Type:     projection.OpFileUpload,
		Name:     "overlap.bin",
		FileSize: 1,
	})

	started := make(chan struct{})
	release := make(chan struct{})
	fake := &resolveControlFake{
		rangeFake: newRangeFake(map[int64][]byte{10: []byte("x")}),
		resolve: func(_ context.Context, peer tgclient.InputPeer, msgID int64) (tgclient.DocumentRef, error) {
			close(started)
			<-release
			return tgclient.DocumentRef{Peer: peer, MsgID: msgID, Size: 1}, nil
		},
	}
	opener := newTestOpener(t, db, fake)

	result := make(chan readerOpenResult, 1)
	go func() {
		reader, err := opener.Open(context.Background(), testChannelID, 10)
		result <- readerOpenResult{reader: reader, err: err}
	}()
	waitForSignal(t, started, "document resolution")
	opener.Close()
	close(release)

	got := waitForReaderOpen(t, result)
	if got.reader != nil || !errors.Is(got.err, ErrClosed) {
		t.Fatalf("Open after overlapping Close = reader %v, err %v; want nil/ErrClosed", got.reader, got.err)
	}
}

func TestOpenerResolutionLimitIsSharedAcrossOpensAndCloseCancelsWaiters(t *testing.T) {
	db := newTestDB(t)
	bodies := make(map[int64][]byte, 5)
	for msgID := int64(1); msgID <= 5; msgID++ {
		bodies[msgID] = []byte{byte(msgID)}
		projectFile(t, db, msgID, projection.Op{
			Type:     projection.OpFileUpload,
			Name:     "parallel.bin",
			FileSize: 1,
		})
	}

	started := make(chan int64, 5)
	var mu sync.Mutex
	active, maximum, resolveCalls := 0, 0, 0
	fake := &resolveControlFake{
		rangeFake: newRangeFake(bodies),
		resolve: func(ctx context.Context, _ tgclient.InputPeer, msgID int64) (tgclient.DocumentRef, error) {
			mu.Lock()
			active++
			resolveCalls++
			if active > maximum {
				maximum = active
			}
			mu.Unlock()
			defer func() {
				mu.Lock()
				active--
				mu.Unlock()
			}()
			started <- msgID
			<-ctx.Done()
			return tgclient.DocumentRef{}, ctx.Err()
		},
	}
	opener, err := New(Config{
		DB:     db,
		Peers:  staticPeerResolver{peer: fake.peer},
		Ranges: fake,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(opener.Close)

	results := make(chan error, 5)
	for msgID := int64(1); msgID <= 4; msgID++ {
		msgID := msgID
		go func() {
			_, openErr := opener.Open(context.Background(), testChannelID, msgID)
			results <- openErr
		}()
	}
	waitForResolveStarts(t, started, 4)
	if got := cap(opener.resolveSlots); got != maxConcurrentDocumentResolutions {
		t.Fatalf("resolution slot capacity = %d, want %d", got, maxConcurrentDocumentResolutions)
	}
	if got := len(opener.resolveSlots); got != maxConcurrentDocumentResolutions {
		t.Fatalf("occupied resolution slots = %d, want %d", got, maxConcurrentDocumentResolutions)
	}

	go func() {
		_, openErr := opener.Open(context.Background(), testChannelID, 5)
		results <- openErr
	}()
	opener.Close()

	for range 5 {
		if openErr := waitForOpenResult(t, results); !errors.Is(openErr, ErrClosed) {
			t.Fatalf("Open interrupted by Close error = %v, want ErrClosed", openErr)
		}
	}
	mu.Lock()
	gotMaximum, gotCalls := maximum, resolveCalls
	mu.Unlock()
	if gotMaximum != maxConcurrentDocumentResolutions || gotCalls != maxConcurrentDocumentResolutions {
		t.Fatalf(
			"resolution concurrency max=%d calls=%d, want %d/%d",
			gotMaximum,
			gotCalls,
			maxConcurrentDocumentResolutions,
			maxConcurrentDocumentResolutions,
		)
	}
}

func TestOpenerRejectsEqualSizeDocumentWithWrongIdentity(t *testing.T) {
	db := newTestDB(t)
	projectFile(t, db, 10, projection.Op{
		Type:     projection.OpFileUpload,
		Name:     "identity.bin",
		FileSize: 1,
	})
	expectedPeer := newRangeFake(nil).peer

	tests := []struct {
		name string
		ref  tgclient.DocumentRef
	}{
		{
			name: "message",
			ref:  tgclient.DocumentRef{Peer: expectedPeer, MsgID: 99, Size: 1},
		},
		{
			name: "peer",
			ref: tgclient.DocumentRef{
				Peer:  tgclient.InputPeer{ChannelID: expectedPeer.ChannelID + 1, AccessHash: expectedPeer.AccessHash},
				MsgID: 10,
				Size:  1,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &resolveControlFake{
				rangeFake: newRangeFake(map[int64][]byte{10: []byte("x")}),
				resolve: func(context.Context, tgclient.InputPeer, int64) (tgclient.DocumentRef, error) {
					return tt.ref, nil
				},
			}
			opener := newTestOpener(t, db, fake)
			if reader, openErr := opener.Open(context.Background(), testChannelID, 10); reader != nil || openErr == nil {
				t.Fatalf("Open wrong identity = reader %v, err %v; want nil/error", reader, openErr)
			}
		})
	}
}

func TestExportedOperationsRejectNilContext(t *testing.T) {
	db := newTestDB(t)
	projectFile(t, db, 10, projection.Op{
		Type:     projection.OpFileUpload,
		Name:     "context.bin",
		FileSize: 1,
	})
	fake := newRangeFake(map[int64][]byte{10: []byte("x")})
	opener := newTestOpener(t, db, fake)

	if reader, err := opener.Open(nil, testChannelID, 10); reader != nil || !errors.Is(err, ErrNilContext) {
		t.Fatalf("Open(nil) = reader %v, err %v; want nil/ErrNilContext", reader, err)
	}
	reader, err := opener.Open(context.Background(), testChannelID, 10)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if n, readErr := reader.ReadAt(nil, nil, 0); n != 0 || !errors.Is(readErr, ErrNilContext) {
		t.Fatalf("ReadAt(nil) = n %d, err %v; want 0/ErrNilContext", n, readErr)
	}
}

func TestReaderCloseIsIndependentAndPreventsFurtherReads(t *testing.T) {
	db := newTestDB(t)
	projectFile(t, db, 10, projection.Op{
		Type:     projection.OpFileUpload,
		Name:     "close.bin",
		FileSize: 1,
	})
	fake := newRangeFake(map[int64][]byte{10: []byte("x")})
	opener := newTestOpener(t, db, fake)
	first, err := opener.Open(context.Background(), testChannelID, 10)
	if err != nil {
		t.Fatalf("Open first: %v", err)
	}
	second, err := opener.Open(context.Background(), testChannelID, 10)
	if err != nil {
		t.Fatalf("Open second: %v", err)
	}

	if err := first.Close(); err != nil {
		t.Fatalf("Close first: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close first again: %v", err)
	}
	if _, readErr := first.ReadAt(context.Background(), make([]byte, 1), 0); !errors.Is(readErr, ErrReaderClosed) {
		t.Fatalf("closed reader error = %v, want ErrReaderClosed", readErr)
	}
	buf := make([]byte, 1)
	if n, readErr := second.ReadAt(context.Background(), buf, 0); n != 1 || readErr != nil || string(buf) != "x" {
		t.Fatalf("independent reader = %q, n %d, err %v; want x/1/nil", buf, n, readErr)
	}

	opener.Close()
	if _, readErr := second.ReadAt(context.Background(), make([]byte, 1), 0); !errors.Is(readErr, ErrClosed) {
		t.Fatalf("reader after opener Close error = %v, want ErrClosed", readErr)
	}
}

type readerOpenResult struct {
	reader *Reader
	err    error
}

func waitForReaderOpen(t *testing.T, results <-chan readerOpenResult) readerOpenResult {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-timeAfterResolverEvent():
		t.Fatal("timed out waiting for Reader Open")
		return readerOpenResult{}
	}
}

func waitForSignal(t *testing.T, signal <-chan struct{}, operation string) {
	t.Helper()
	select {
	case <-signal:
	case <-timeAfterResolverEvent():
		t.Fatalf("timed out waiting for %s", operation)
	}
}

func timeAfterResolverEvent() <-chan time.Time {
	return time.After(resolverEventTimeout)
}
