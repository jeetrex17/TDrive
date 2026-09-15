package media

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"TDrive/backend/tgclient"
)

// gatedRangeFake holds every request open until the test releases its offset
// and records how each request ended, so a test can see which fetches a seek
// abandoned and which it let finish.
type gatedRangeFake struct {
	mu       sync.Mutex
	data     []byte
	gates    map[int64]chan struct{}
	outcomes map[int64]error
	entered  chan int64
}

func newGatedRangeFake(data []byte) *gatedRangeFake {
	return &gatedRangeFake{
		data:     data,
		gates:    make(map[int64]chan struct{}),
		outcomes: make(map[int64]error),
		entered:  make(chan int64, 64),
	}
}

func (f *gatedRangeFake) ref() tgclient.DocumentRef {
	return tgclient.DocumentRef{
		Peer:  tgclient.InputPeer{ChannelID: 42},
		MsgID: 99,
		Size:  int64(len(f.data)),
		Name:  "gated-video.bin",
	}
}

func (f *gatedRangeFake) ResolveDocument(context.Context, tgclient.InputPeer, int64) (tgclient.DocumentRef, error) {
	return f.ref(), nil
}

func (f *gatedRangeFake) gateLocked(offset int64) chan struct{} {
	gate, ok := f.gates[offset]
	if !ok {
		gate = make(chan struct{})
		f.gates[offset] = gate
	}
	return gate
}

// release lets the request at offset complete.
func (f *gatedRangeFake) release(offset int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	gate := f.gateLocked(offset)
	select {
	case <-gate:
	default:
		close(gate)
	}
}

func (f *gatedRangeFake) ReadDocumentRange(ctx context.Context, ref tgclient.DocumentRef, offset int64, dst []byte) (int, error) {
	if err := validateNormalizedRange(offset, len(dst)); err != nil {
		return 0, err
	}
	f.mu.Lock()
	gate := f.gateLocked(offset)
	f.mu.Unlock()
	f.entered <- offset

	var err error
	select {
	case <-gate:
	case <-ctx.Done():
		err = ctx.Err()
	}
	f.mu.Lock()
	f.outcomes[offset] = err
	f.mu.Unlock()
	if err != nil {
		return 0, err
	}
	if offset+int64(len(dst)) > int64(len(f.data)) {
		return 0, io.ErrUnexpectedEOF
	}
	return copy(dst, f.data[offset:offset+int64(len(dst))]), nil
}

// awaitEntered waits until requests for every offset in want have started.
func (f *gatedRangeFake) awaitEntered(t *testing.T, want ...int64) {
	t.Helper()
	pending := make(map[int64]struct{}, len(want))
	for _, offset := range want {
		pending[offset] = struct{}{}
	}
	deadline := time.After(2 * time.Second)
	for len(pending) > 0 {
		select {
		case offset := <-f.entered:
			delete(pending, offset)
		case <-deadline:
			t.Fatalf("requests never started for offsets %v", pending)
		}
	}
}

// awaitOutcome waits for the request at offset to end and reports its error.
func (f *gatedRangeFake) awaitOutcome(t *testing.T, offset int64) error {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		f.mu.Lock()
		err, ok := f.outcomes[offset]
		f.mu.Unlock()
		if ok {
			return err
		}
		if time.Now().After(deadline) {
			t.Fatalf("request at %d never ended", offset)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Read-ahead keeps a whole window of blocks in flight, not just the next one.
func TestRangeReaderKeepsWindowOfBlocksInFlight(t *testing.T) {
	data := testBytes(tgclient.RangeReadMaxBytes*5 + 1)
	fake := newStrictRangeFake(data)
	reader := NewRangeReader(RangeReaderConfig{Client: fake, ReadAhead: 3})
	defer reader.Close()

	if _, err := reader.ReadStoredAt(context.Background(), fake.ref(), make([]byte, 64), openingChunkBytes+128); err != nil {
		t.Fatalf("ReadStoredAt: %v", err)
	}

	block := int64(tgclient.RangeReadMaxBytes)
	deadline := time.Now().Add(2 * time.Second)
	for {
		offsets := make(map[int64]bool)
		for _, call := range fake.calls() {
			offsets[call.offset] = true
		}
		if offsets[block] && offsets[2*block] && offsets[3*block] {
			if len(offsets) != 4 || offsets[4*block] {
				t.Fatalf("fetched offsets = %v, want block 0 and a window of exactly three blocks", offsets)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("calls = %+v, want three blocks of read-ahead", fake.calls())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A seek that needs the lane slots stale read-ahead is holding cancels those
// fetches, so the new position is not queued behind a position the player
// has left.
func TestRangeReaderSeekCancelsStaleReadAhead(t *testing.T) {
	const window = 4
	block := int64(tgclient.RangeReadMaxBytes)
	data := testBytes(int(block) * 12)
	fake := newGatedRangeFake(data)
	reader := NewRangeReader(RangeReaderConfig{Client: fake, ReadAhead: window})
	defer reader.Close()
	ref := fake.ref()

	// Reading block 0 opens the window: blocks 1 to 4 start behind it and
	// fill every lane slot.
	fake.release(0)
	if _, err := reader.ReadStoredAt(context.Background(), ref, make([]byte, 64), openingChunkBytes); err != nil {
		t.Fatalf("read block 0: %v", err)
	}
	fake.awaitEntered(t, block, 2*block, 3*block, 4*block)

	// Jumping to block 6 wants a fresh window of four, which the stale
	// fetches must give up.
	fake.release(6 * block)
	if _, err := reader.ReadStoredAt(context.Background(), ref, make([]byte, 64), 6*block+64); err != nil {
		t.Fatalf("read block 6: %v", err)
	}
	for _, offset := range []int64{block, 2 * block, 3 * block, 4 * block} {
		if err := fake.awaitOutcome(t, offset); !errors.Is(err, context.Canceled) {
			t.Fatalf("stale fetch at %d ended with %v, want cancellation", offset, err)
		}
	}
	fake.awaitEntered(t, 7*block, 8*block, 9*block, 10*block)
}

// Read-ahead the player has left behind is kept when nothing needs its
// slots: an index read at the tail must not discard the head just warmed.
func TestRangeReaderTailReadKeepsHeadReadAhead(t *testing.T) {
	block := int64(tgclient.RangeReadMaxBytes)
	data := testBytes(int(block) * 12)
	fake := newGatedRangeFake(data)
	reader := NewRangeReader(RangeReaderConfig{Client: fake, ReadAhead: 2})
	defer reader.Close()
	ref := fake.ref()

	fake.release(0)
	if _, err := reader.ReadStoredAt(context.Background(), ref, make([]byte, 64), openingChunkBytes); err != nil {
		t.Fatalf("read block 0: %v", err)
	}
	fake.awaitEntered(t, block, 2*block)

	fake.release(11 * block)
	if _, err := reader.ReadStoredAt(context.Background(), ref, make([]byte, 64), 11*block); err != nil {
		t.Fatalf("read last block: %v", err)
	}
	fake.release(block)
	fake.release(2 * block)
	for _, offset := range []int64{block, 2 * block} {
		if err := fake.awaitOutcome(t, offset); err != nil {
			t.Fatalf("head read-ahead at %d ended with %v, want completion", offset, err)
		}
	}
}

// When the background pool is exhausted, a player read for a block that
// read-ahead has queued promotes that flight to the playback reserve instead
// of waiting behind the queue or fetching the block twice.
func TestRangeReaderForegroundPromotesQueuedBackgroundFlight(t *testing.T) {
	data := testBytes(tgclient.RangeReadMaxBytes)
	fake := newStrictRangeFake(data)
	reader := NewRangeReader(RangeReaderConfig{Client: fake})
	defer reader.Close()
	ref := fake.ref()

	release, err := tgclient.AcquireBackgroundGetFileSlots(context.Background(), tgclient.MaxConcurrentBackgroundGetFile)
	if err != nil {
		t.Fatalf("exhaust background pool: %v", err)
	}
	defer release()

	reader.prefetchBlock(ref, 0)
	if calls := fake.calls(); len(calls) != 0 {
		t.Fatalf("background fetch ran without a slot: %+v", calls)
	}

	buf := make([]byte, 64)
	done := make(chan error, 1)
	go func() {
		_, err := reader.ReadStoredAt(context.Background(), ref, buf, openingChunkBytes)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("foreground read: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("foreground read waited behind the background queue")
	}
	if calls := fake.calls(); len(calls) != 1 {
		t.Fatalf("calls = %+v, want the promoted flight to fetch once", calls)
	}
	if !bytes.Equal(buf, data[openingChunkBytes:openingChunkBytes+64]) {
		t.Fatal("promoted block bytes mismatch")
	}
}
