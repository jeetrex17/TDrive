package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"TDrive/backend/tgclient"
)

func TestRangeReaderSplitsAcrossUploadBoundaries(t *testing.T) {
	data := testBytes(tgclient.RangeReadMaxBytes*2 + 257)
	fake := newStrictRangeFake(data)
	reader := NewRangeReader(RangeReaderConfig{Client: fake})
	ref := fake.ref()

	off := int64(tgclient.RangeReadMaxBytes - 20)
	buf := make([]byte, 80)
	n, err := reader.ReadStoredAt(context.Background(), ref, buf, off)
	if err != nil {
		t.Fatalf("ReadStoredAt: %v", err)
	}
	if n != len(buf) {
		t.Fatalf("n = %d, want %d", n, len(buf))
	}
	if !bytes.Equal(buf, data[off:off+int64(len(buf))]) {
		t.Fatal("read bytes mismatch")
	}

	calls := fake.calls()
	if len(calls) != 2 {
		t.Fatalf("calls = %+v, want 2 block reads", calls)
	}
	if calls[0].offset != 0 || calls[0].length != tgclient.RangeReadMaxBytes {
		t.Fatalf("first call = %+v, want first 1 MiB block", calls[0])
	}
	if calls[1].offset != int64(tgclient.RangeReadMaxBytes) || calls[1].length != tgclient.RangeReadMaxBytes {
		t.Fatalf("second call = %+v, want second 1 MiB block", calls[1])
	}
}

func TestRangeReaderEOFReaderAtSemantics(t *testing.T) {
	data := testBytes(128)
	fake := newStrictRangeFake(data)
	reader := NewRangeReader(RangeReaderConfig{Client: fake})
	defer reader.Close()
	ref := fake.ref()

	buf := make([]byte, 20)
	n, err := reader.ReadStoredAt(context.Background(), ref, buf, 120)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want io.EOF", err)
	}
	if n != 8 {
		t.Fatalf("n = %d, want 8", n)
	}
	if !bytes.Equal(buf[:n], data[120:]) {
		t.Fatal("tail bytes mismatch")
	}

	n, err = reader.ReadStoredAt(context.Background(), ref, buf, int64(len(data)))
	if !errors.Is(err, io.EOF) || n != 0 {
		t.Fatalf("at EOF n=%d err=%v, want 0/io.EOF", n, err)
	}
	if calls := fake.calls(); len(calls) != 1 || calls[0].length != 128 {
		t.Fatalf("final block call = %+v, want one 128-byte non-4KiB tail read", calls)
	}
}

func TestRangeReaderCachesBlocks(t *testing.T) {
	data := testBytes(tgclient.RangeReadMaxBytes + 1)
	fake := newStrictRangeFake(data)
	reader := NewRangeReader(RangeReaderConfig{Client: fake})
	defer reader.Close()
	ref := fake.ref()

	for i := 0; i < 2; i++ {
		buf := make([]byte, 64)
		if _, err := reader.ReadStoredAt(context.Background(), ref, buf, 100); err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
	}
	if calls := fake.calls(); len(calls) != 1 {
		t.Fatalf("calls = %+v, want one cached block fetch", calls)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := reader.ReadStoredAt(ctx, ref, make([]byte, 64), 100); !errors.Is(err, context.Canceled) {
		t.Fatalf("cached canceled read err = %v, want context.Canceled", err)
	}
	if calls := fake.calls(); len(calls) != 1 {
		t.Fatalf("canceled cached read made new calls = %+v", calls)
	}
}

func TestRangeReaderCoalescesConcurrentBlockReads(t *testing.T) {
	data := testBytes(tgclient.RangeReadMaxBytes)
	fake := newStrictRangeFake(data)
	fake.delay = 20 * time.Millisecond
	reader := NewRangeReader(RangeReaderConfig{
		Client:         fake,
		MaxConcurrency: 8,
	})
	defer reader.Close()
	ref := fake.ref()

	const readers = 12
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, readers)
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			buf := make([]byte, 32)
			_, err := reader.ReadStoredAt(context.Background(), ref, buf, 512)
			if err != nil {
				errs <- err
				return
			}
			if !bytes.Equal(buf, data[512:544]) {
				errs <- fmt.Errorf("bytes mismatch")
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls := fake.calls(); len(calls) != 1 {
		t.Fatalf("calls = %+v, want one coalesced block fetch", calls)
	}
}

func TestRangeReaderCallerCancellationDoesNotPoisonCoalescedWaiter(t *testing.T) {
	data := testBytes(tgclient.RangeReadMaxBytes)
	fake := newStrictRangeFake(data)
	fake.entered = make(chan struct{})
	fake.release = make(chan struct{})
	reader := NewRangeReader(RangeReaderConfig{Client: fake})
	defer reader.Close()
	ref := fake.ref()

	ctx1, cancel1 := context.WithCancel(context.Background())
	err1 := make(chan error, 1)
	go func() {
		_, err := reader.ReadStoredAt(ctx1, ref, make([]byte, 32), 0)
		err1 <- err
	}()

	select {
	case <-fake.entered:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first block fetch")
	}

	buf2 := make([]byte, 32)
	err2 := make(chan error, 1)
	go func() {
		_, err := reader.ReadStoredAt(context.Background(), ref, buf2, 0)
		err2 <- err
	}()

	cancel1()
	select {
	case err := <-err1:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("first err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for canceled first caller")
	}

	close(fake.release)
	select {
	case err := <-err2:
		if err != nil {
			t.Fatalf("second read: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for coalesced second caller")
	}
	if !bytes.Equal(buf2, data[:32]) {
		t.Fatal("second caller bytes mismatch")
	}
	if calls := fake.calls(); len(calls) != 1 {
		t.Fatalf("calls = %+v, want one shared block fetch", calls)
	}
}

func TestRangeReaderRetriesFloodWait(t *testing.T) {
	data := testBytes(1024)
	fake := newStrictRangeFake(data)
	fake.floodWaits = 2
	fake.floodWait = time.Millisecond
	var waits int
	reader := NewRangeReader(RangeReaderConfig{
		Client: fake,
		OnFloodWait: func(time.Duration) {
			waits++
		},
	})
	defer reader.Close()
	ref := fake.ref()

	buf := make([]byte, 64)
	if _, err := reader.ReadStoredAt(context.Background(), ref, buf, 0); err != nil {
		t.Fatalf("ReadStoredAt: %v", err)
	}
	if calls := fake.calls(); len(calls) != 3 {
		t.Fatalf("calls = %+v, want 3 attempts", calls)
	}
	if waits != 2 {
		t.Fatalf("wait hooks = %d, want 2", waits)
	}
}

func TestRangeReaderCancellationDuringFloodWait(t *testing.T) {
	data := testBytes(1024)
	fake := newStrictRangeFake(data)
	fake.floodWaits = 10
	fake.floodWait = time.Minute

	ctx, cancel := context.WithCancel(context.Background())
	reader := NewRangeReader(RangeReaderConfig{
		Client: fake,
		OnFloodWait: func(time.Duration) {
			cancel()
		},
	})
	defer reader.Close()
	ref := fake.ref()

	_, err := reader.ReadStoredAt(ctx, ref, make([]byte, 64), 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestRangeReaderEvictsLeastRecentlyUsedBlock(t *testing.T) {
	data := testBytes(tgclient.RangeReadMaxBytes * 2)
	fake := newStrictRangeFake(data)
	reader := NewRangeReader(RangeReaderConfig{
		Client:        fake,
		MaxCacheBytes: int64(tgclient.RangeReadMaxBytes),
	})
	defer reader.Close()
	ref := fake.ref()

	for _, off := range []int64{0, int64(tgclient.RangeReadMaxBytes), 0} {
		if _, err := reader.ReadStoredAt(context.Background(), ref, make([]byte, 16), off); err != nil {
			t.Fatalf("read at %d: %v", off, err)
		}
	}
	calls := fake.calls()
	if len(calls) != 3 {
		t.Fatalf("calls = %+v, want block0, block1, block0 after eviction", calls)
	}
	if calls[0].offset != 0 || calls[1].offset != int64(tgclient.RangeReadMaxBytes) || calls[2].offset != 0 {
		t.Fatalf("call order = %+v", calls)
	}
}

type rangeCall struct {
	offset int64
	length int
}

type strictRangeFake struct {
	mu         sync.Mutex
	data       []byte
	callLog    []rangeCall
	delay      time.Duration
	floodWaits int
	floodWait  time.Duration
	entered    chan struct{}
	release    chan struct{}
	enterOnce  sync.Once
}

func newStrictRangeFake(data []byte) *strictRangeFake {
	return &strictRangeFake{data: data}
}

func (f *strictRangeFake) ref() tgclient.DocumentRef {
	return tgclient.DocumentRef{
		Peer:  tgclient.InputPeer{ChannelID: 42},
		MsgID: 99,
		Size:  int64(len(f.data)),
		Name:  "video.bin",
	}
}

func (f *strictRangeFake) ResolveDocument(context.Context, tgclient.InputPeer, int64) (tgclient.DocumentRef, error) {
	return f.ref(), nil
}

func (f *strictRangeFake) ReadDocumentRange(ctx context.Context, ref tgclient.DocumentRef, offset int64, dst []byte) (int, error) {
	if err := validateNormalizedRange(offset, len(dst)); err != nil {
		return 0, err
	}

	f.mu.Lock()
	f.callLog = append(f.callLog, rangeCall{offset: offset, length: len(dst)})
	if f.entered != nil {
		f.enterOnce.Do(func() { close(f.entered) })
	}
	if f.floodWaits > 0 {
		f.floodWaits--
		wait := f.floodWait
		f.mu.Unlock()
		return 0, tgclient.NewFloodWaitError(wait)
	}
	delay := f.delay
	f.mu.Unlock()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	if f.release != nil {
		select {
		case <-f.release:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	if ref.MsgID != 99 {
		return 0, tgclient.ErrMessageNotFound
	}
	if offset < 0 || offset+int64(len(dst)) > int64(len(f.data)) {
		return 0, io.ErrUnexpectedEOF
	}
	return copy(dst, f.data[offset:offset+int64(len(dst))]), nil
}

func (f *strictRangeFake) calls() []rangeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]rangeCall(nil), f.callLog...)
}

func validateNormalizedRange(offset int64, length int) error {
	if length <= 0 {
		return fmt.Errorf("empty range request")
	}
	if offset%tgclient.RangeReadAlignment != 0 {
		return fmt.Errorf("offset %d is not %d-aligned", offset, tgclient.RangeReadAlignment)
	}
	if length > tgclient.RangeReadMaxBytes {
		return fmt.Errorf("range length %d exceeds max %d", length, tgclient.RangeReadMaxBytes)
	}
	startBucket := offset / int64(tgclient.RangeReadMaxBytes)
	endBucket := (offset + int64(length) - 1) / int64(tgclient.RangeReadMaxBytes)
	if startBucket != endBucket {
		return fmt.Errorf("range crosses 1 MiB boundary")
	}
	return nil
}

func testBytes(n int) []byte {
	data := make([]byte, n)
	for i := range data {
		data[i] = byte((i * 31) % 251)
	}
	return data
}
