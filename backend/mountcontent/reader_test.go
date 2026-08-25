package mountcontent

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"math"
	"sync"
	"testing"
	"time"

	"TDrive/backend/media"
	"TDrive/backend/mountfs"
	"TDrive/backend/projection"
	"TDrive/backend/tgclient"

	_ "modernc.org/sqlite"
)

const (
	testChannelID        int64 = 424242
	resolverEventTimeout       = 10 * time.Second
)

func TestOpenerReadsSingleFileRanges(t *testing.T) {
	db := newTestDB(t)
	body := []byte("0123456789abcdefghijklmnopqrstuvwxyz")
	projectFile(t, db, 10, projection.Op{
		Type:     projection.OpFileUpload,
		Name:     "notes.txt",
		FileSize: int64(len(body)),
	})

	ranges := newRangeFake(map[int64][]byte{10: body})
	opener, err := New(Config{
		DB:     db,
		Peers:  staticPeerResolver{peer: ranges.peer},
		Ranges: ranges,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(opener.Close)

	reader, err := opener.Open(context.Background(), testChannelID, 10)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if reader.Size() != int64(len(body)) {
		t.Fatalf("Size = %d, want %d", reader.Size(), len(body))
	}

	got := make([]byte, 8)
	n, err := reader.ReadAt(context.Background(), got, 7)
	if err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	if n != len(got) || !bytes.Equal(got, body[7:15]) {
		t.Fatalf("ReadAt = %q (%d), want %q", got, n, body[7:15])
	}
}

func TestOpenerReadsAcrossMultipartBoundaries(t *testing.T) {
	db := newTestDB(t)
	parts := map[int64][]byte{
		100: []byte("abcd"),
		101: []byte("efghij"),
		102: []byte("klm"),
	}
	for index, msgID := range []int64{100, 101, 102} {
		projectFile(t, db, msgID, projection.Op{
			Type:       projection.OpFilePart,
			UploadUUID: "upload-one",
			PartIndex:  index,
			FileSize:   int64(len(parts[msgID])),
		})
	}
	projectFile(t, db, 200, projection.Op{
		Type:       projection.OpFileManifest,
		UploadUUID: "upload-one",
		Name:       "archive.bin",
		FileSize:   13,
		PartCount:  3,
	})

	ranges := newRangeFake(parts)
	opener, err := New(Config{
		DB:     db,
		Peers:  staticPeerResolver{peer: ranges.peer},
		Ranges: ranges,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(opener.Close)

	reader, err := opener.Open(context.Background(), testChannelID, 200)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got := make([]byte, 9)
	n, err := reader.ReadAt(context.Background(), got, 2)
	if err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	if n != len(got) || string(got) != "cdefghijk" {
		t.Fatalf("ReadAt = %q (%d), want %q", got, n, "cdefghijk")
	}
}

func TestOpenerResolvesMultipartSegmentsWithBoundedConcurrency(t *testing.T) {
	db := newTestDB(t)
	msgIDs := []int64{101, 102, 103, 104, 105, 106, 107, 108}
	bodies := projectMultipart(t, db, 200, msgIDs)

	started := make(chan int64, len(msgIDs))
	release := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	var mu sync.Mutex
	active := 0
	maximum := 0
	fake := &resolveControlFake{
		rangeFake: newRangeFake(bodies),
		resolve: func(ctx context.Context, peer tgclient.InputPeer, msgID int64) (tgclient.DocumentRef, error) {
			mu.Lock()
			active++
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

			select {
			case <-release:
			case <-ctx.Done():
				return tgclient.DocumentRef{}, ctx.Err()
			}

			body := bodies[msgID]
			return tgclient.DocumentRef{Peer: peer, MsgID: msgID, Size: int64(len(body))}, nil
		},
	}
	opener := newTestOpener(t, db, fake)

	result := make(chan error, 1)
	go func() {
		_, err := opener.Open(ctx, testChannelID, 200)
		result <- err
	}()

	waitForResolveStarts(t, started, 4)
	mu.Lock()
	gotActive, gotMaximum := active, maximum
	mu.Unlock()
	if gotActive != 4 || gotMaximum != 4 {
		t.Fatalf("resolution concurrency active=%d max=%d, want 4/4", gotActive, gotMaximum)
	}
	select {
	case msgID := <-started:
		t.Fatalf("segment %d started above the concurrency limit", msgID)
	default:
	}

	close(release)
	if err := waitForOpenResult(t, result); err != nil {
		t.Fatalf("Open: %v", err)
	}
	mu.Lock()
	gotMaximum = maximum
	mu.Unlock()
	if gotMaximum <= 1 || gotMaximum > 4 {
		t.Fatalf("resolution max concurrency = %d, want >1 and <=4", gotMaximum)
	}
}

func TestOpenerPreservesSegmentOrderWhenResolutionCompletesOutOfOrder(t *testing.T) {
	db := newTestDB(t)
	msgIDs := []int64{301, 302, 303, 304}
	bodies := projectMultipart(t, db, 400, msgIDs)

	started := make(chan int64, len(msgIDs))
	completed := make(chan int64, len(msgIDs))
	releases := make(map[int64]chan struct{}, len(msgIDs))
	for _, msgID := range msgIDs {
		releases[msgID] = make(chan struct{})
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	fake := &resolveControlFake{
		rangeFake: newRangeFake(bodies),
		resolve: func(ctx context.Context, peer tgclient.InputPeer, msgID int64) (tgclient.DocumentRef, error) {
			started <- msgID
			select {
			case <-releases[msgID]:
			case <-ctx.Done():
				return tgclient.DocumentRef{}, ctx.Err()
			}
			completed <- msgID
			body := bodies[msgID]
			return tgclient.DocumentRef{Peer: peer, MsgID: msgID, Size: int64(len(body))}, nil
		},
	}
	opener := newTestOpener(t, db, fake)

	type openResult struct {
		reader *Reader
		err    error
	}
	result := make(chan openResult, 1)
	go func() {
		reader, err := opener.Open(ctx, testChannelID, 400)
		result <- openResult{reader: reader, err: err}
	}()
	waitForResolveStarts(t, started, len(msgIDs))

	for index := len(msgIDs) - 1; index >= 0; index-- {
		msgID := msgIDs[index]
		close(releases[msgID])
		if got := waitForResolveEvent(t, completed); got != msgID {
			t.Fatalf("completion order got %d, want %d", got, msgID)
		}
	}

	select {
	case got := <-result:
		if got.err != nil {
			t.Fatalf("Open: %v", got.err)
		}
		for index, part := range got.reader.segments {
			if part.ref.MsgID != msgIDs[index] {
				t.Fatalf("segment[%d].MsgID = %d, want %d", index, part.ref.MsgID, msgIDs[index])
			}
			if part.start != int64(index) {
				t.Fatalf("segment[%d].start = %d, want %d", index, part.start, index)
			}
		}
	case <-time.After(resolverEventTimeout):
		t.Fatal("Open did not finish after all resolutions completed")
	}
}

func TestOpenerCancelsSiblingResolutionAfterError(t *testing.T) {
	db := newTestDB(t)
	msgIDs := []int64{501, 502, 503, 504, 505, 506}
	bodies := projectMultipart(t, db, 600, msgIDs)

	sentinel := errors.New("resolve failed")
	started := make(chan int64, len(msgIDs))
	canceled := make(chan int64, len(msgIDs))
	failNow := make(chan struct{})
	var mu sync.Mutex
	resolveCalls := 0
	fake := &resolveControlFake{
		rangeFake: newRangeFake(bodies),
		resolve: func(ctx context.Context, peer tgclient.InputPeer, msgID int64) (tgclient.DocumentRef, error) {
			mu.Lock()
			resolveCalls++
			mu.Unlock()
			started <- msgID
			if msgID == msgIDs[0] {
				select {
				case <-failNow:
					return tgclient.DocumentRef{}, sentinel
				case <-ctx.Done():
					return tgclient.DocumentRef{}, ctx.Err()
				}
			}
			<-ctx.Done()
			canceled <- msgID
			return tgclient.DocumentRef{}, ctx.Err()
		},
	}
	opener := newTestOpener(t, db, fake)

	result := make(chan error, 1)
	go func() {
		_, err := opener.Open(context.Background(), testChannelID, 600)
		result <- err
	}()
	waitForResolveStarts(t, started, 4)
	close(failNow)

	if err := waitForOpenResult(t, result); !errors.Is(err, sentinel) {
		t.Fatalf("Open error = %v, want wrapped sentinel", err)
	}
	for range 3 {
		_ = waitForResolveEvent(t, canceled)
	}
	mu.Lock()
	gotCalls := resolveCalls
	mu.Unlock()
	if gotCalls != 4 {
		t.Fatalf("ResolveDocument calls = %d, want only the four in-flight calls", gotCalls)
	}
}

func TestBuildSegmentsRejectsInvalidAndOverflowingMetadata(t *testing.T) {
	tests := []struct {
		name       string
		projected  []media.Segment
		resolved   []tgclient.DocumentRef
		storedSize int64
	}{
		{
			name:       "negative stored size",
			projected:  []media.Segment{{MsgID: 1, Size: 0}},
			resolved:   []tgclient.DocumentRef{{MsgID: 1, Size: 0}},
			storedSize: -1,
		},
		{
			name:       "negative projected size",
			projected:  []media.Segment{{MsgID: 1, Size: -1}},
			resolved:   []tgclient.DocumentRef{{MsgID: 1, Size: -1}},
			storedSize: 0,
		},
		{
			name:       "negative telegram size",
			projected:  []media.Segment{{MsgID: 1, Size: 1}},
			resolved:   []tgclient.DocumentRef{{MsgID: 1, Size: -1}},
			storedSize: 1,
		},
		{
			name: "projected cumulative overflow",
			projected: []media.Segment{
				{MsgID: 1, Size: math.MaxInt64},
				{MsgID: 2, Size: 1},
			},
			resolved: []tgclient.DocumentRef{
				{MsgID: 1, Size: math.MaxInt64},
				{MsgID: 2, Size: 1},
			},
			storedSize: math.MaxInt64,
		},
		{
			name: "telegram cumulative overflow",
			projected: []media.Segment{
				{MsgID: 1, Size: math.MaxInt64},
				{MsgID: 2, Size: 0},
			},
			resolved: []tgclient.DocumentRef{
				{MsgID: 1, Size: math.MaxInt64},
				{MsgID: 2, Size: 1},
			},
			storedSize: math.MaxInt64,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := buildSegments(tt.projected, tt.resolved, tt.storedSize); err == nil {
				t.Fatal("buildSegments error = nil")
			}
		})
	}
}

func TestReaderUsesReaderAtEOFContract(t *testing.T) {
	db := newTestDB(t)
	body := []byte("tail")
	projectFile(t, db, 10, projection.Op{
		Type:     projection.OpFileUpload,
		Name:     "tail.txt",
		FileSize: int64(len(body)),
	})

	ranges := newRangeFake(map[int64][]byte{10: body})
	opener, err := New(Config{DB: db, Peers: staticPeerResolver{peer: ranges.peer}, Ranges: ranges})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(opener.Close)
	reader, err := opener.Open(context.Background(), testChannelID, 10)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	got := make([]byte, 8)
	n, err := reader.ReadAt(context.Background(), got, 2)
	if n != 2 || !errors.Is(err, io.EOF) || string(got[:n]) != "il" {
		t.Fatalf("tail read = %q n=%d err=%v, want il/2/EOF", got[:n], n, err)
	}

	n, err = reader.ReadAt(context.Background(), got, int64(len(body)))
	if n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("EOF read n=%d err=%v, want 0/EOF", n, err)
	}
}

func TestOpenerRejectsEncryptedFilesWithoutKeyProvider(t *testing.T) {
	db := newTestDB(t)
	projectFile(t, db, 10, projection.Op{
		Type:              projection.OpFileUpload,
		Name:              "private.txt",
		FileSize:          98,
		Encrypted:         true,
		PlaintextSize:     32,
		EncryptionVersion: 1,
	})

	ranges := newRangeFake(map[int64][]byte{10: make([]byte, 98)})
	opener, err := New(Config{DB: db, Peers: staticPeerResolver{peer: ranges.peer}, Ranges: ranges})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(opener.Close)

	_, err = opener.Open(context.Background(), testChannelID, 10)
	if !errors.Is(err, ErrKeyUnavailable) {
		t.Fatalf("Open encrypted err = %v, want ErrKeyUnavailable", err)
	}
}

func TestNewValidatesDependencies(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{name: "database", cfg: Config{Peers: staticPeerResolver{}, Ranges: newRangeFake(nil)}},
		{name: "peer resolver", cfg: Config{DB: &sql.DB{}, Ranges: newRangeFake(nil)}},
		{name: "range client", cfg: Config{DB: &sql.DB{}, Peers: staticPeerResolver{}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := New(tt.cfg); err == nil {
				t.Fatal("New error = nil, want dependency error")
			}
		})
	}
}

func TestOpenerRejectsClosedAndCanceledOperations(t *testing.T) {
	db := newTestDB(t)
	projectFile(t, db, 10, projection.Op{
		Type:     projection.OpFileUpload,
		Name:     "notes.txt",
		FileSize: 1,
	})
	ranges := newRangeFake(map[int64][]byte{10: []byte("x")})
	opener, err := New(Config{DB: db, Peers: staticPeerResolver{peer: ranges.peer}, Ranges: ranges})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := opener.Open(canceled, testChannelID, 10); !errors.Is(err, context.Canceled) {
		t.Fatalf("Open canceled err = %v, want context.Canceled", err)
	}

	opener.Close()
	opener.Close()
	if _, err := opener.Open(context.Background(), testChannelID, 10); !errors.Is(err, ErrClosed) {
		t.Fatalf("Open closed err = %v, want ErrClosed", err)
	}
	var nilOpener *Opener
	if _, err := nilOpener.Open(context.Background(), testChannelID, 10); !errors.Is(err, ErrClosed) {
		t.Fatalf("nil Open err = %v, want ErrClosed", err)
	}
}

func TestOpenerValidatesResolvedTelegramMetadata(t *testing.T) {
	db := newTestDB(t)
	projectFile(t, db, 10, projection.Op{
		Type:     projection.OpFileUpload,
		Name:     "mismatch.txt",
		FileSize: 4,
	})
	ranges := newRangeFake(map[int64][]byte{10: []byte("five!")})
	opener, err := New(Config{DB: db, Peers: staticPeerResolver{peer: ranges.peer}, Ranges: ranges})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(opener.Close)

	if _, err := opener.Open(context.Background(), testChannelID, 10); err == nil {
		t.Fatal("Open size mismatch error = nil")
	}
	if _, err := opener.Open(context.Background(), testChannelID, 999); !errors.Is(err, media.ErrFileNotFound) {
		t.Fatalf("Open missing err = %v, want media.ErrFileNotFound", err)
	}
}

func TestReaderValidatesOffsetsAndContext(t *testing.T) {
	db := newTestDB(t)
	projectFile(t, db, 10, projection.Op{
		Type:     projection.OpFileUpload,
		Name:     "notes.txt",
		FileSize: 3,
	})
	ranges := newRangeFake(map[int64][]byte{10: []byte("abc")})
	opener, err := New(Config{DB: db, Peers: staticPeerResolver{peer: ranges.peer}, Ranges: ranges})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(opener.Close)
	reader, err := opener.Open(context.Background(), testChannelID, 10)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if n, err := reader.ReadAt(context.Background(), nil, 0); n != 0 || err != nil {
		t.Fatalf("empty ReadAt n=%d err=%v, want 0/nil", n, err)
	}
	if _, err := reader.ReadAt(context.Background(), make([]byte, 1), -1); err == nil {
		t.Fatal("negative ReadAt error = nil")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := reader.ReadAt(canceled, make([]byte, 1), 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled ReadAt err = %v, want context.Canceled", err)
	}
	if size := (*Reader)(nil).Size(); size != 0 {
		t.Fatalf("nil Reader Size = %d, want 0", size)
	}
}

type staticPeerResolver struct {
	peer tgclient.InputPeer
}

func (r staticPeerResolver) ResolvePeer(context.Context, int64) (tgclient.InputPeer, error) {
	return r.peer, nil
}

type rangeFake struct {
	peer   tgclient.InputPeer
	bodies map[int64][]byte
}

type resolveControlFake struct {
	*rangeFake
	resolve func(context.Context, tgclient.InputPeer, int64) (tgclient.DocumentRef, error)
}

func (f *resolveControlFake) ResolveDocument(ctx context.Context, peer tgclient.InputPeer, msgID int64) (tgclient.DocumentRef, error) {
	return f.resolve(ctx, peer, msgID)
}

func newRangeFake(bodies map[int64][]byte) *rangeFake {
	return &rangeFake{
		peer:   tgclient.InputPeer{ChannelID: testChannelID, AccessHash: 99},
		bodies: bodies,
	}
}

func (f *rangeFake) ResolveDocument(_ context.Context, peer tgclient.InputPeer, msgID int64) (tgclient.DocumentRef, error) {
	body, ok := f.bodies[msgID]
	if !ok {
		return tgclient.DocumentRef{}, tgclient.ErrMessageNotFound
	}
	return tgclient.DocumentRef{Peer: peer, MsgID: msgID, Size: int64(len(body)), Name: "test.bin"}, nil
}

func (f *rangeFake) ReadDocumentRange(_ context.Context, ref tgclient.DocumentRef, offset int64, dst []byte) (int, error) {
	body, ok := f.bodies[ref.MsgID]
	if !ok {
		return 0, tgclient.ErrMessageNotFound
	}
	if offset < 0 || offset+int64(len(dst)) > int64(len(body)) {
		return 0, io.ErrUnexpectedEOF
	}
	return copy(dst, body[offset:offset+int64(len(dst))]), nil
}

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err := projection.MigratePersonalChannel(db, testChannelID); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func projectFile(t *testing.T, db *sql.DB, msgID int64, op projection.Op) {
	t.Helper()
	header := projection.Format(op)
	if _, err := projection.ProjectFromOp(db, testChannelID, msgID, op, 7, header); err != nil {
		t.Fatalf("project %s msg=%d: %v", op.Type, msgID, err)
	}
}

func projectMultipart(t *testing.T, db *sql.DB, manifestID int64, msgIDs []int64) map[int64][]byte {
	t.Helper()
	bodies := make(map[int64][]byte, len(msgIDs))
	for index, msgID := range msgIDs {
		bodies[msgID] = []byte{byte(index)}
		projectFile(t, db, msgID, projection.Op{
			Type:       projection.OpFilePart,
			UploadUUID: "controlled-upload",
			PartIndex:  index,
			FileSize:   1,
		})
	}
	projectFile(t, db, manifestID, projection.Op{
		Type:       projection.OpFileManifest,
		UploadUUID: "controlled-upload",
		Name:       "multipart.bin",
		FileSize:   int64(len(msgIDs)),
		PartCount:  len(msgIDs),
	})
	return bodies
}

func newTestOpener(t *testing.T, db *sql.DB, ranges tgclient.RangeClient) *Opener {
	t.Helper()
	opener, err := New(Config{DB: db, Peers: staticPeerResolver{peer: newRangeFake(nil).peer}, Ranges: ranges})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(opener.Close)
	return opener
}

func waitForResolveStarts(t *testing.T, started <-chan int64, count int) {
	t.Helper()
	for range count {
		_ = waitForResolveEvent(t, started)
	}
}

func waitForResolveEvent(t *testing.T, events <-chan int64) int64 {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(resolverEventTimeout):
		t.Fatal("timed out waiting for resolver event")
		return 0
	}
}

func waitForOpenResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(resolverEventTimeout):
		t.Fatal("timed out waiting for Open")
		return nil
	}
}

var _ media.PeerResolver = staticPeerResolver{}
var _ mountfs.RandomAccessContent = (*Reader)(nil)
var _ tgclient.RangeClient = (*rangeFake)(nil)
var _ tgclient.RangeClient = (*resolveControlFake)(nil)
