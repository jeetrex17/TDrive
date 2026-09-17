package file

import (
	"TDrive/backend/thumbnail"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

type renditionClient struct {
	tgclient.Client
	doc       tgclient.FileDocument
	originals atomic.Int32
	thumbs    atomic.Int32
	thumbnail func(context.Context, io.Writer) error
	download  func(context.Context, io.Writer) error
}

func (c *renditionClient) GetFileDocument(context.Context, tgclient.InputPeer, int64) (tgclient.FileDocument, error) {
	return c.doc, nil
}
func (c *renditionClient) DownloadFile(ctx context.Context, _ tgclient.InputPeer, _ int64, w io.Writer, _ func(int64, int64)) error {
	c.originals.Add(1)
	if c.download != nil {
		return c.download(ctx, w)
	}
	return errors.New("original must not be downloaded")
}
func (c *renditionClient) DownloadFileThumbnail(ctx context.Context, _ tgclient.InputPeer, _ int64, _ string, w io.Writer) error {
	c.thumbs.Add(1)
	return c.thumbnail(ctx, w)
}
func renditionFixture(t *testing.T) (*Service, *renditionClient) {
	t.Helper()
	s, db, tg, _ := newTestService(t)
	project(t, db, personalChannelID, 91, 7, projection.Op{Type: projection.OpFileUpload, Name: "photo.jpg", FileSize: 100_000_000, FileUploadTime: 1})
	c := &renditionClient{Client: tg, doc: tgclient.FileDocument{MsgID: 91, Name: "photo.jpg", Size: 100_000_000}}
	s.TG = c
	return s, c
}
func TestRenditionMissingNeverDownloadsOriginal(t *testing.T) {
	s, c := renditionFixture(t)
	for _, kind := range []string{"thumbnail", "preview"} {
		_, err := s.Rendition(context.Background(), personalChannelID, 91, 1, kind)
		if !errors.Is(err, ErrRenditionMissing) {
			t.Fatalf("%s err=%v", kind, err)
		}
	}
	if c.originals.Load() != 0 {
		t.Fatal("gallery fetched original")
	}
}
func TestRenditionPlainThumbnailIgnoresOriginalSize(t *testing.T) {
	s, c := renditionFixture(t)
	raw := makePNG(t, 32, 16)
	c.doc.Thumbs = []tgclient.FileThumb{{Type: "m", Width: 32, Height: 16, Size: len(raw)}}
	c.thumbnail = func(_ context.Context, w io.Writer) error { _, err := w.Write(raw); return err }
	got, err := s.Rendition(context.Background(), personalChannelID, 91, 1, "thumbnail")
	if err != nil {
		t.Fatal(err)
	}
	if got.Width != 32 || got.Height != 16 || !bytes.Equal(got.Bytes, raw) {
		t.Fatalf("rendition=%+v", got)
	}
	if c.originals.Load() != 0 || c.thumbs.Load() != 1 {
		t.Fatal("wrong download path")
	}
}
func TestRenditionRevisionPinRejectsBeforeNetwork(t *testing.T) {
	s, c := renditionFixture(t)
	_, err := s.Rendition(context.Background(), personalChannelID, 91, 2, "thumbnail")
	if !errors.Is(err, ErrRenditionStale) {
		t.Fatalf("err=%v", err)
	}
	if c.originals.Load() != 0 || c.thumbs.Load() != 0 {
		t.Fatal("stale request performed transfer")
	}
}
func TestRenditionLastSubscriberCancelsTransfer(t *testing.T) {
	s, c := renditionFixture(t)
	started := make(chan struct{})
	stopped := make(chan struct{})
	c.doc.Thumbs = []tgclient.FileThumb{{Type: "m", Width: 32, Height: 16}}
	c.thumbnail = func(ctx context.Context, _ io.Writer) error {
		close(started)
		<-ctx.Done()
		close(stopped)
		return ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := s.Rendition(ctx, personalChannelID, 91, 1, "thumbnail"); done <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("transfer not started")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber stuck")
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("orphan transfer not canceled")
	}
}
func TestRenditionCacheIdentityIncludesContentAndAccount(t *testing.T) {
	f := projection.File{ChannelID: 1, MsgID: 2, ContentMsgID: 2, Revision: 1}
	a := renditionCacheKey("account-a", f, "thumbnail", 0)
	if a == renditionCacheKey("account-b", f, "thumbnail", 0) {
		t.Fatal("cross-account collision")
	}
	f.ContentMsgID = 3
	if a == renditionCacheKey("account-a", f, "thumbnail", 0) {
		t.Fatal("replacement collision")
	}
}

func TestRenditionSharedSubscriberSurvivesPeerCancellation(t *testing.T) {
	var group renditionFlightGroup
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan error, 2)
	ctx, cancel := context.WithCancel(context.Background())
	load := func(ctx context.Context) (Rendition, error) {
		calls.Add(1)
		close(started)
		select {
		case <-release:
			return Rendition{Width: 1}, nil
		case <-ctx.Done():
			return Rendition{}, ctx.Err()
		}
	}
	go func() { _, err := group.do(ctx, "key", load); finished <- err }()
	<-started
	go func() { _, err := group.do(context.Background(), "key", load); finished <- err }()
	deadline := time.Now().Add(time.Second)
	for {
		group.mu.Lock()
		joined := group.flights["key"].subscribers == 2
		group.mu.Unlock()
		if joined {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second subscriber failed to join")
		}
		runtime.Gosched()
	}
	cancel()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("first result=%v", err)
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatalf("remaining subscriber=%v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("transfers=%d", calls.Load())
	}
}
func TestRenditionWriterLimitCannotBeBypassedByCopy(t *testing.T) {
	dst := &renditionWriter{limit: 4}
	_, err := io.Copy(dst, io.LimitReader(bytes.NewReader([]byte("123456")), 6))
	if !errors.Is(err, errPreviewTooLarge) || dst.Len() > 4 {
		t.Fatalf("len=%d err=%v", dst.Len(), err)
	}
}
func TestRenditionRejectsDimensionsBeforeBrowserDecode(t *testing.T) {
	raw := makePNG(t, 513, 513)
	if _, err := checkedRendition(raw, "thumbnail", false); !errors.Is(err, errPreviewTooLarge) {
		t.Fatalf("err=%v", err)
	}
}
func TestRenditionReplacementDuringTransferIsNotCached(t *testing.T) {
	s, c := renditionFixture(t)
	s.Thumbs = thumbnail.NewCache(t.TempDir(), 1<<20)
	c.doc.Thumbs = []tgclient.FileThumb{{Type: "m", Width: 32, Height: 16}}
	raw := makePNG(t, 32, 16)
	c.thumbnail = func(_ context.Context, w io.Writer) error {
		if _, err := s.DB.Exec("UPDATE files SET revision=2,content_msg_id=99 WHERE channel_id=? AND msg_id=91", personalChannelID); err != nil {
			return err
		}
		_, err := w.Write(raw)
		return err
	}
	if _, err := s.Rendition(context.Background(), personalChannelID, 91, 1, "thumbnail"); !errors.Is(err, ErrRenditionStale) {
		t.Fatalf("err=%v", err)
	}
}

func TestRenditionEncryptedRemoteDerivativeWorksWithoutOriginal(t *testing.T) {
	s, _, telegram, _ := newTestService(t)
	s.Thumbs = thumbnail.NewCache(t.TempDir(), 1<<20)
	key := bytes.Repeat([]byte{8}, 32)
	wireEncryption(s, key)
	source := writeTempNamedFile(t, "private.png", makePNG(t, 64, 32))
	uploaded, err := s.Upload(context.Background(), personalChannelID, []string{source}, []string{""}, true)
	if err != nil || len(uploaded) != 1 {
		t.Fatalf("upload=%+v %v", uploaded, err)
	}
	id := int64(uploaded[0].MsgID)
	ref, err := projection.CurrentFileRendition(context.Background(), s.DB, personalChannelID, id, "thumbnail")
	if err != nil {
		t.Fatalf("derivative reference: %v", err)
	}
	if err := telegram.DeleteMessages(context.Background(), tgclient.InputPeer{}, []int64{id}); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var keys [][]byte
	s.RequireEncryptionKey = func(bool) ([]byte, error) {
		owned := append([]byte(nil), key...)
		mu.Lock()
		keys = append(keys, owned)
		mu.Unlock()
		return owned, nil
	}
	result, err := s.Rendition(context.Background(), personalChannelID, id, 1, "thumbnail")
	if err != nil || result.Width != 64 || result.Height != 32 || !result.Encrypted {
		t.Fatalf("rendition=%+v %v", result, err)
	}
	mu.Lock()
	ownedKeys := append([][]byte(nil), keys...)
	mu.Unlock()
	if len(ownedKeys) != 2 {
		t.Fatalf("key requests=%d", len(ownedKeys))
	}
	for _, owned := range ownedKeys {
		assertKeyZeroed(t, owned)
	}
	if err := telegram.DeleteMessages(context.Background(), tgclient.InputPeer{}, []int64{ref.MsgID}); err != nil {
		t.Fatal(err)
	}
	cached, err := s.Rendition(context.Background(), personalChannelID, id, 1, "thumbnail")
	if err != nil || !bytes.Equal(cached.Bytes, result.Bytes) {
		t.Fatalf("cache: %v", err)
	}
}

func TestRenditionRejectsLongEdgeDespiteSmallPixelCount(t *testing.T) {
	if _, err := checkedRendition(makePNG(t, 640, 100), "thumbnail", false); !errors.Is(err, errPreviewTooLarge) {
		t.Fatalf("err=%v", err)
	}
	selected := remoteRenditionType([]tgclient.FileThumb{{Type: "large", Width: 640, Height: 100}, {Type: "small", Width: 320, Height: 100}}, "thumbnail")
	if selected != "small" {
		t.Fatalf("selected=%s", selected)
	}
}

func TestRenditionCacheDoesNotCrossActorsSharingDatabase(t *testing.T) {
	s, c := renditionFixture(t)
	s.Thumbs = thumbnail.NewCache(t.TempDir(), 1<<20)
	s.CacheNamespace = "same-db"
	raw := makePNG(t, 32, 16)
	c.doc.Thumbs = []tgclient.FileThumb{{Bytes: raw}}
	if _, err := s.Rendition(context.Background(), personalChannelID, 91, 1, "thumbnail"); err != nil {
		t.Fatal(err)
	}
	c.doc.Thumbs = nil
	s.ActorID = func(context.Context) (int64, error) { return 8, nil }
	if _, err := s.Rendition(context.Background(), personalChannelID, 91, 1, "thumbnail"); !errors.Is(err, ErrRenditionMissing) {
		t.Fatalf("second account reused first account's cache: %v", err)
	}
	s.ActorID = func(context.Context) (int64, error) { return 0, errors.New("logged out") }
	if _, err := s.Rendition(context.Background(), personalChannelID, 91, 1, "thumbnail"); err == nil {
		t.Fatal("logged-out cache read succeeded")
	}
}

func TestOriginalRenditionRequiresExplicitClassAndSizeAdmission(t *testing.T) {
	s, c := renditionFixture(t)
	if _, err := s.Rendition(context.Background(), personalChannelID, 91, 1, "original"); !errors.Is(err, errPreviewTooLarge) {
		t.Fatalf("unadmitted original=%v", err)
	}
	if c.originals.Load() != 0 {
		t.Fatal("oversized original started a transfer")
	}
	raw := makePNG(t, 64, 32)
	if _, err := s.DB.Exec("UPDATE files SET size=? WHERE channel_id=? AND msg_id=91", len(raw), personalChannelID); err != nil {
		t.Fatal(err)
	}
	c.download = func(_ context.Context, w io.Writer) error { _, err := w.Write(raw); return err }
	got, err := s.Rendition(context.Background(), personalChannelID, 91, 1, "original")
	if err != nil || !bytes.Equal(got.Bytes, raw) || c.originals.Load() != 1 {
		t.Fatalf("explicit original=%+v %v", got, err)
	}
}
func TestRenditionQueueHasHardBound(t *testing.T) {
	group := renditionFlightGroup{flights: make(map[string]*renditionFlight)}
	for i := 0; i < maxRenditionFlights; i++ {
		group.flights[strconv.Itoa(i)] = &renditionFlight{}
	}
	_, err := group.do(context.Background(), "overflow", func(context.Context) (Rendition, error) {
		t.Fatal("overfull queue started work")
		return Rendition{}, nil
	})
	if !errors.Is(err, ErrRenditionBusy) {
		t.Fatalf("queue error=%v", err)
	}
}

func TestRenditionMissingReceiptIsQuarantinedButCancellationIsNot(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(strconv.FormatBool(missing), func(t *testing.T) {
			s, _, telegram, _ := newTestService(t)
			s.FloodWaitRetry = tgclient.FloodWaitRetryPolicy{MaxRetries: 1, MaxWait: time.Second, MaxTotalWait: time.Second}
			path := writeTempNamedFile(t, "photo.png", makePNG(t, 32, 16))
			files, err := s.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, false)
			if err != nil {
				t.Fatal(err)
			}
			id := int64(files[0].MsgID)
			ref, err := projection.CurrentFileRendition(context.Background(), s.DB, personalChannelID, id, "thumbnail")
			if err != nil {
				t.Fatal(err)
			}
			if missing {
				if err := telegram.DeleteMessages(context.Background(), tgclient.InputPeer{}, []int64{ref.MsgID}); err != nil {
					t.Fatal(err)
				}
			} else {
				s.TG = &renditionClient{Client: telegram, download: func(context.Context, io.Writer) error { return context.Canceled }}
			}
			_, err = s.Rendition(context.Background(), personalChannelID, id, 1, "thumbnail")
			_, lookupErr := projection.CurrentFileRendition(context.Background(), s.DB, personalChannelID, id, "thumbnail")
			if missing {
				if !errors.Is(err, ErrRenditionMissing) || !errors.Is(lookupErr, sql.ErrNoRows) {
					t.Fatalf("missing: %v lookup: %v", err, lookupErr)
				}
			} else if !errors.Is(err, context.Canceled) || lookupErr != nil {
				t.Fatalf("canceled: %v lookup: %v", err, lookupErr)
			}
		})
	}
}
