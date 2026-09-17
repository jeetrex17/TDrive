package media

import (
	"context"
	"testing"
	"time"

	"TDrive/backend/tgclient"
	"TDrive/backend/thumbnail"
)

// The thumbnail extractor reads the head and index blocks playback already
// holds. With one cache behind both readers, it never fetches them again.
func TestSessionThumbnailReaderSharesPlaybackCache(t *testing.T) {
	const size int64 = 4 * 1024 * 1024
	block := int64(tgclient.RangeReadMaxBytes)
	ranges := newRecordingRangeClient(size)
	ref := tgclient.DocumentRef{DocumentID: 9, MsgID: 1, Size: size}
	file := LogicalFile{ChannelID: 1, FileID: 2, Name: "clip.mkv", StoredSize: size, PlaintextSize: size}
	segments := []resolvedSegment{{start: 0, size: size, ref: ref}}
	thumbs := thumbnail.NewCache(t.TempDir(), 1<<20)

	session, err := newSession(file, segments, ranges, thumbs, &fakeVideoThumbGenerator{available: true}, SessionOptions{EnableVideoThumbnails: true})
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}
	t.Cleanup(session.Close)

	if _, err := session.ReadAt(context.Background(), make([]byte, 64), 2*block); err != nil {
		t.Fatalf("playback read: %v", err)
	}
	// Opening also warms the last block; drain both reads before the check.
	seen := map[int64]bool{ranges.awaitRead(t): true}
	seen[ranges.awaitRead(t)] = true
	if !seen[2*block] || !seen[3*block] {
		t.Fatalf("reads = %v, want the playback block and the warmed index block", seen)
	}

	if _, err := session.ReadThumbAt(context.Background(), make([]byte, 64), 2*block+100); err != nil {
		t.Fatalf("thumbnail read: %v", err)
	}
	select {
	case offset := <-ranges.reads:
		t.Fatalf("thumbnail read fetched block at %d again instead of using the shared cache", offset)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestImageSessionUsesSmallCacheWithoutReadAhead(t *testing.T) {
	const size int64 = 40 * 1024 * 1024
	ranges := newRecordingRangeClient(size)
	file := LogicalFile{ChannelID: 1, FileID: 2, Name: "photo.jpg", StoredSize: size, PlaintextSize: size}
	segments := []resolvedSegment{{start: 0, size: size, ref: tgclient.DocumentRef{DocumentID: 9, MsgID: 1, Size: size}}}

	session, err := newSession(file, segments, ranges, nil, nil, SessionOptions{})
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}
	t.Cleanup(session.Close)

	if got := session.reader.cache.maxBytes; got != imageRangeCacheBytes {
		t.Fatalf("image cache = %d, want %d", got, imageRangeCacheBytes)
	}
	if got := session.reader.readAhead; got != 0 {
		t.Fatalf("image read-ahead = %d, want 0", got)
	}
}

func TestVideoSessionKeepsPlaybackCacheAndReadAhead(t *testing.T) {
	const size int64 = 40 * 1024 * 1024
	ranges := newRecordingRangeClient(size)
	file := LogicalFile{ChannelID: 1, FileID: 2, Name: "clip.mp4", StoredSize: size, PlaintextSize: size}
	segments := []resolvedSegment{{start: 0, size: size, ref: tgclient.DocumentRef{DocumentID: 9, MsgID: 1, Size: size}}}

	session, err := newSession(file, segments, ranges, nil, nil, SessionOptions{})
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}
	t.Cleanup(session.Close)

	if got := session.reader.cache.maxBytes; got != defaultRangeCacheBytes {
		t.Fatalf("video cache = %d, want %d", got, defaultRangeCacheBytes)
	}
	if got := session.reader.readAhead; got != playbackReadAhead {
		t.Fatalf("video read-ahead = %d, want %d", got, playbackReadAhead)
	}
}
