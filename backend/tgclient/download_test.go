package tgclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/rpc"
	"github.com/gotd/td/tgerr"
)

// blockSource serves a document from memory and fails chosen blocks with the
// errors queued for them, in order, one per read.
type blockSource struct {
	mu       sync.Mutex
	data     []byte
	ref      DocumentRef
	reads    map[int64]int
	failures map[int64][]error
	resolves int
}

func newBlockSource(size int64) *blockSource {
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i*7 + i/int(downloadBlockBytes))
	}
	return &blockSource{
		data:     data,
		ref:      DocumentRef{MsgID: 7, Size: size, FileReference: []byte("v1")},
		reads:    map[int64]int{},
		failures: map[int64][]error{},
	}
}

func (s *blockSource) read(_ context.Context, ref DocumentRef, offset int64, dst []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads[offset]++
	if queued := s.failures[offset]; len(queued) > 0 {
		s.failures[offset] = queued[1:]
		return 0, queued[0]
	}
	if !bytes.Equal(ref.FileReference, s.ref.FileReference) {
		return 0, tgerr.New(400, "FILE_REFERENCE_EXPIRED")
	}
	return copy(dst, s.data[offset:]), nil
}

func (s *blockSource) resolve(context.Context) (DocumentRef, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resolves++
	return s.ref, nil
}

// expireReference invalidates the reference readers hold; the next resolve
// hands out the new one.
func (s *blockSource) expireReference() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ref.FileReference = []byte("v2")
}

func (s *blockSource) download(t *testing.T) (*documentDownload, *[]int) {
	t.Helper()
	var sleeps []int
	policy := downloadBlockRetry()
	policy.Sleep = func(context.Context, time.Duration) error {
		sleeps = append(sleeps, 1)
		return nil
	}
	return &documentDownload{ref: s.ref, retry: policy, read: s.read, resolve: s.resolve}, &sleeps
}

func TestDocumentDownloadWritesEveryBlockOnce(t *testing.T) {
	size := 2*downloadBlockBytes + downloadBlockBytes/2
	src := newBlockSource(size)
	d, _ := src.download(t)
	out := make([]byte, size)

	if err := d.parallel(context.Background(), writerAtBuf(out), 3); err != nil {
		t.Fatalf("parallel: %v", err)
	}
	if !bytes.Equal(out, src.data) {
		t.Fatal("downloaded bytes differ from the document")
	}
	for offset, n := range src.reads {
		if n != 1 {
			t.Fatalf("block %d read %d times, want 1", offset, n)
		}
	}
	if len(src.reads) != 3 {
		t.Fatalf("read %d blocks, want 3", len(src.reads))
	}
}

func TestDocumentDownloadRetriesOnlyTheFailedBlock(t *testing.T) {
	size := 3 * downloadBlockBytes
	src := newBlockSource(size)
	dropped := fmt.Errorf("get next chunk: invoke pool: rpcDoRequest: %w", rpc.ErrEngineClosed)
	src.failures[downloadBlockBytes] = []error{dropped, dropped}
	d, sleeps := src.download(t)
	out := make([]byte, size)

	if err := d.parallel(context.Background(), writerAtBuf(out), 2); err != nil {
		t.Fatalf("parallel: %v", err)
	}
	if !bytes.Equal(out, src.data) {
		t.Fatal("downloaded bytes differ from the document")
	}
	if got := src.reads[downloadBlockBytes]; got != 3 {
		t.Fatalf("failed block read %d times, want 3", got)
	}
	if src.reads[0] != 1 || src.reads[2*downloadBlockBytes] != 1 {
		t.Fatalf("healthy blocks were re-read: %v", src.reads)
	}
	if got := d.retries.Load(); got != 2 {
		t.Fatalf("retries = %d, want 2", got)
	}
	if len(*sleeps) != 2 {
		t.Fatalf("backed off %d times, want 2", len(*sleeps))
	}
}

func TestDocumentDownloadRefreshesAnExpiredReferenceOnce(t *testing.T) {
	size := 2 * downloadBlockBytes
	src := newBlockSource(size)
	d, sleeps := src.download(t)
	src.expireReference()
	out := make([]byte, size)

	if err := d.parallel(context.Background(), writerAtBuf(out), 1); err != nil {
		t.Fatalf("parallel: %v", err)
	}
	if !bytes.Equal(out, src.data) {
		t.Fatal("downloaded bytes differ from the document")
	}
	if src.resolves != 1 {
		t.Fatalf("resolved %d times, want 1", src.resolves)
	}
	if src.reads[0] != 2 || src.reads[downloadBlockBytes] != 1 {
		t.Fatalf("reads = %v, want the first block twice and the second once", src.reads)
	}
	if len(*sleeps) != 0 || d.retries.Load() != 0 {
		t.Fatal("a refresh must not count as a retry")
	}
}

func TestDocumentDownloadFailsFastOnADefiniteRejection(t *testing.T) {
	src := newBlockSource(downloadBlockBytes)
	src.failures[0] = []error{tgerr.New(400, "FILE_ID_INVALID")}
	d, sleeps := src.download(t)

	err := d.parallel(context.Background(), writerAtBuf(make([]byte, downloadBlockBytes)), 2)
	if !tgerr.Is(err, "FILE_ID_INVALID") {
		t.Fatalf("err = %v, want FILE_ID_INVALID", err)
	}
	if src.reads[0] != 1 || len(*sleeps) != 0 {
		t.Fatalf("definite rejection was retried: reads=%d sleeps=%d", src.reads[0], len(*sleeps))
	}
}

func TestDocumentDownloadGivesUpAfterTheBlockBudget(t *testing.T) {
	src := newBlockSource(downloadBlockBytes)
	dropped := errors.New("rpcDoRequest: retryUntilAck: engine forcibly closed: context canceled")
	for range 10 {
		src.failures[0] = append(src.failures[0], dropped)
	}
	d, _ := src.download(t)

	err := d.parallel(context.Background(), writerAtBuf(make([]byte, downloadBlockBytes)), 1)
	if err == nil || !errors.Is(err, dropped) {
		t.Fatalf("err = %v, want the transport error", err)
	}
	if got, want := src.reads[0], d.retry.MaxTransientRetries+1; got != want {
		t.Fatalf("block attempted %d times, want %d", got, want)
	}
}

func TestDocumentDownloadStreamsInOrder(t *testing.T) {
	size := downloadBlockBytes + 12345
	src := newBlockSource(size)
	src.failures[downloadBlockBytes] = []error{fmt.Errorf("acquire connection: DC closed: %w", context.Canceled)}
	d, _ := src.download(t)
	var out bytes.Buffer

	if err := d.stream(context.Background(), &out); err != nil {
		t.Fatalf("stream: %v", err)
	}
	if !bytes.Equal(out.Bytes(), src.data) {
		t.Fatal("streamed bytes differ from the document")
	}
	if d.retries.Load() != 1 {
		t.Fatalf("retries = %d, want 1", d.retries.Load())
	}
}

// writerAtBuf is an in-memory io.WriterAt over a fixed buffer.
type writerAtBuf []byte

func (b writerAtBuf) WriteAt(p []byte, off int64) (int, error) {
	if off < 0 || off+int64(len(p)) > int64(len(b)) {
		return 0, fmt.Errorf("write of %d bytes at %d exceeds %d", len(p), off, len(b))
	}
	return copy(b[off:], p), nil
}
