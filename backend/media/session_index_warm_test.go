package media

import (
	"context"
	"sync"
	"testing"
	"time"

	"TDrive/backend/tgclient"
)

// recordingRangeClient reports the offset of every range read it serves.
type recordingRangeClient struct {
	size    int64
	reads   chan int64
	mu      sync.Mutex
	lastLen int
}

func newRecordingRangeClient(size int64) *recordingRangeClient {
	return &recordingRangeClient{size: size, reads: make(chan int64, 8)}
}

func (c *recordingRangeClient) ResolveDocument(context.Context, tgclient.InputPeer, int64) (tgclient.DocumentRef, error) {
	return tgclient.DocumentRef{DocumentID: 9, MsgID: 1, Size: c.size}, nil
}

func (c *recordingRangeClient) ReadDocumentRange(_ context.Context, _ tgclient.DocumentRef, offset int64, dst []byte) (int, error) {
	c.mu.Lock()
	c.lastLen = len(dst)
	c.mu.Unlock()
	select {
	case c.reads <- offset:
	default:
	}
	return len(dst), nil
}

// lastLength reports the size of the most recent range read.
func (c *recordingRangeClient) lastLength() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastLen
}

func (c *recordingRangeClient) awaitRead(t *testing.T) int64 {
	t.Helper()
	select {
	case offset := <-c.reads:
		return offset
	case <-time.After(2 * time.Second):
		t.Fatal("no range read was issued")
		return 0
	}
}

var _ tgclient.RangeClient = (*recordingRangeClient)(nil)

// The container index sits at the end of MP4 and MKV files, and a player reads
// it before the first frame. The session has to start that read itself, or it
// lands after the head fetch and the viewer waits for both in series.
func TestNewSessionWarmsContainerIndex(t *testing.T) {
	const size int64 = 240 * 1024 * 1024
	ranges := newRecordingRangeClient(size)
	file := LogicalFile{ChannelID: 1, FileID: 2, Name: "movie.mkv", StoredSize: size}
	segments := []resolvedSegment{{start: 0, size: size, ref: tgclient.DocumentRef{DocumentID: 9, MsgID: 1, Size: size}}}

	session, err := newSession(file, segments, ranges, nil, nil, SessionOptions{})
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}
	t.Cleanup(session.Close)

	offset := ranges.awaitRead(t)
	if want := blockStartFor(size - 1); offset != want {
		t.Fatalf("warmed offset = %d, want the final block at %d", offset, want)
	}
}
