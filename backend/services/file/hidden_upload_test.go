package file

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"TDrive/backend/projection"
	tdsync "TDrive/backend/sync"
	"TDrive/backend/tgclient"
)

type hiddenTestPeerResolver struct {
	peer tgclient.InputPeer
	err  error
}

func (r hiddenTestPeerResolver) ResolvePeer(context.Context, int64) (tgclient.InputPeer, error) {
	return r.peer, r.err
}

type zeroMessageIDClient struct {
	*tgclient.Fake
}

func (c *zeroMessageIDClient) SendFileWithRandomID(context.Context, tgclient.InputPeer, io.Reader, string, string, int64, func(int64, int64), int64) (tgclient.SendFileResult, error) {
	return tgclient.SendFileResult{}, nil
}

type failNthFileClient struct {
	*tgclient.Fake
	failAt int
	calls  int
	err    error
}

func (c *failNthFileClient) SendFileWithRandomID(ctx context.Context, peer tgclient.InputPeer, source io.Reader, name, caption string, size int64, progress func(int64, int64), randomID int64) (tgclient.SendFileResult, error) {
	c.calls++
	if c.calls == c.failAt {
		return tgclient.SendFileResult{}, c.err
	}
	return c.Fake.SendFileWithRandomID(ctx, peer, source, name, caption, size, progress, randomID)
}

type deleteFailureClient struct {
	*tgclient.Fake
	err error
}

func (c *deleteFailureClient) DeleteMessages(context.Context, tgclient.InputPeer, []int64) error {
	return c.err
}

type failingSeeker struct {
	seekCalls int
	failAt    int
}

func (*failingSeeker) Read([]byte) (int, error) { return 0, io.EOF }

func (s *failingSeeker) Seek(int64, int) (int64, error) {
	s.seekCalls++
	if s.seekCalls == s.failAt {
		return 0, errors.New("injected seek failure")
	}
	return 0, nil
}

func TestUploadHiddenSingleIsNotVisibleBeforeCommit(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	body := []byte("hello from the mounted drive")

	remote, err := svc.UploadHidden(context.Background(), personalChannelID, HiddenUploadRequest{
		OperationID: "op-single-1",
		Name:        "notes.txt",
		Size:        int64(len(body)),
	}, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("UploadHidden: %v", err)
	}
	if remote.UploadUUID == "" || remote.PartCount != 1 {
		t.Fatalf("remote body = %+v, want one invisible part", remote)
	}
	if remote.StoredSize != int64(len(body)) || remote.PlaintextSize != int64(len(body)) {
		t.Fatalf("remote sizes = stored %d plain %d", remote.StoredSize, remote.PlaintextSize)
	}
	if len(remote.MessageIDs) != 1 || remote.MessageIDs[0] <= 0 {
		t.Fatalf("cleanup ids = %v, want one message", remote.MessageIDs)
	}

	sent := fakeTG.SentFiles()
	if len(sent) != 1 {
		t.Fatalf("sent files = %+v, want one", sent)
	}
	op, err := projection.Parse(sent[0].Caption)
	if err != nil {
		t.Fatalf("hidden body caption: %v", err)
	}
	if op.Type != projection.OpFilePart || op.UploadUUID != remote.UploadUUID || op.PartIndex != 0 {
		t.Fatalf("hidden body op = %+v, want part 0", op)
	}
	history, err := fakeTG.GetHistory(context.Background(), tgclient.InputPeer{ChannelID: personalChannelID}, 0, 0, 10)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	parsed := tdsync.ParseHistoryPageWithOptions(history, tdsync.ParseOptions{AdoptCaptionlessMedia: true})
	if len(parsed) != 1 || parsed[0].AdoptedCaptionless || parsed[0].Op.Type != projection.OpFilePart {
		t.Fatalf("personal sync parsed hidden body as %+v, want an invisible part", parsed)
	}
	parts, err := projection.PartsForUUID(db, personalChannelID, remote.UploadUUID)
	if err != nil || len(parts) != 1 || parts[0].MsgID != remote.MessageIDs[0] {
		t.Fatalf("hidden parts = %+v, err %v", parts, err)
	}
	var visible int
	if err := db.QueryRow(`SELECT COUNT(*) FROM files WHERE channel_id = ? AND tombstoned = 0`, personalChannelID).Scan(&visible); err != nil {
		t.Fatalf("count visible files: %v", err)
	}
	if visible != 0 {
		t.Fatalf("visible files = %d, want 0 before commit", visible)
	}
}

func TestUploadHiddenMultipartProjectsOnlyInvisibleParts(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	svc.MaxUploadBytes = 4
	body := []byte("0123456789")

	remote, err := svc.UploadHidden(context.Background(), personalChannelID, HiddenUploadRequest{
		OperationID: "op-multipart-1",
		Name:        "large.bin",
		Size:        int64(len(body)),
	}, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("UploadHidden: %v", err)
	}
	if remote.UploadUUID == "" || remote.PartCount != 3 {
		t.Fatalf("remote body = %+v, want three multipart messages", remote)
	}
	if len(remote.MessageIDs) != 3 {
		t.Fatalf("cleanup ids = %v, want 3", remote.MessageIDs)
	}

	parts, err := projection.PartsForUUID(db, personalChannelID, remote.UploadUUID)
	if err != nil {
		t.Fatalf("PartsForUUID: %v", err)
	}
	if len(parts) != 3 || parts[0].Size != 4 || parts[1].Size != 4 || parts[2].Size != 2 {
		t.Fatalf("parts = %+v", parts)
	}
	for i, sent := range fakeTG.SentFiles() {
		op, err := projection.Parse(sent.Caption)
		if err != nil {
			t.Fatalf("part %d caption: %v", i, err)
		}
		if op.Type != projection.OpFilePart || op.UploadUUID != remote.UploadUUID || op.PartIndex != i {
			t.Fatalf("part %d op = %+v", i, op)
		}
	}
	var visible int
	if err := db.QueryRow(`SELECT COUNT(*) FROM files WHERE channel_id = ? AND tombstoned = 0`, personalChannelID).Scan(&visible); err != nil {
		t.Fatalf("count visible files: %v", err)
	}
	if visible != 0 {
		t.Fatalf("visible files = %d, want 0 before commit", visible)
	}
}

func TestUploadHiddenRetryUsesStableTelegramMessage(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	body := []byte("retry me")
	request := HiddenUploadRequest{OperationID: "op-retry-1", Name: "retry.txt", Size: int64(len(body))}

	first, err := svc.UploadHidden(context.Background(), personalChannelID, request, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("first UploadHidden: %v", err)
	}
	second, err := svc.UploadHidden(context.Background(), personalChannelID, request, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("retry UploadHidden: %v", err)
	}
	if second.UploadUUID != first.UploadUUID || len(second.MessageIDs) != 1 || second.MessageIDs[0] != first.MessageIDs[0] {
		t.Fatalf("retry body = %+v, want %+v", second, first)
	}
	if sent := fakeTG.SentFiles(); len(sent) != 1 {
		t.Fatalf("sent files = %+v, want one idempotent message", sent)
	}
}

func TestUploadHiddenRetriesBoundedFloodWaitAndRewinds(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	fakeTG.InjectFloodWaits(2)
	var sleeps int
	svc.FloodWaitRetry = tgclient.FloodWaitRetryPolicy{
		MaxRetries:   2,
		MaxWait:      time.Second,
		MaxTotalWait: 2 * time.Second,
		Sleep: func(context.Context, time.Duration) error {
			sleeps++
			return nil
		},
	}
	body := []byte("complete body after retry")

	remote, err := svc.UploadHidden(context.Background(), personalChannelID, HiddenUploadRequest{
		OperationID: "op-flood-1",
		Name:        "flood.txt",
		Size:        int64(len(body)),
	}, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("UploadHidden: %v", err)
	}
	if sleeps != 2 {
		t.Fatalf("sleeps = %d, want 2", sleeps)
	}

	var downloaded bytes.Buffer
	peer := tgclient.InputPeer{ChannelID: personalChannelID, AccessHash: 99}
	if err := fakeTG.DownloadFile(context.Background(), peer, remote.MessageIDs[0], &downloaded, nil); err != nil {
		t.Fatalf("download hidden body: %v", err)
	}
	if !bytes.Equal(downloaded.Bytes(), body) {
		t.Fatalf("downloaded = %q, want %q", downloaded.Bytes(), body)
	}
}

func TestUploadHiddenValidatesSourceBeforeTelegramSend(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)

	_, err := svc.UploadHidden(context.Background(), personalChannelID, HiddenUploadRequest{
		OperationID: "op-short-1",
		Name:        "short.txt",
		Size:        20,
	}, bytes.NewReader([]byte("short")))
	if err == nil {
		t.Fatal("UploadHidden accepted a source shorter than its declared size")
	}
	if sent := fakeTG.SentFiles(); len(sent) != 0 {
		t.Fatalf("sent files = %+v, want none", sent)
	}
}

func TestUploadHiddenRejectsEncryptedWritableBody(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)

	_, err := svc.UploadHidden(context.Background(), personalChannelID, HiddenUploadRequest{
		OperationID: "op-encrypted-1",
		Name:        "secret.txt",
		Size:        6,
		Encrypted:   true,
	}, bytes.NewReader([]byte("secret")))
	if !errors.Is(err, ErrHiddenEncryptionUnsupported) {
		t.Fatalf("UploadHidden error = %v, want ErrHiddenEncryptionUnsupported", err)
	}
	if sent := fakeTG.SentFiles(); len(sent) != 0 {
		t.Fatalf("sent files = %+v, want none", sent)
	}
}

func TestDiscardHiddenDeletesBodiesAndPartProjection(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	svc.MaxUploadBytes = 4
	body := []byte("0123456789")
	remote, err := svc.UploadHidden(context.Background(), personalChannelID, HiddenUploadRequest{
		OperationID: "op-discard-1",
		Name:        "discard.bin",
		Size:        int64(len(body)),
	}, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("UploadHidden: %v", err)
	}

	if err := svc.DiscardHidden(context.Background(), personalChannelID, remote); err != nil {
		t.Fatalf("DiscardHidden: %v", err)
	}
	if parts, err := projection.PartsForUUID(db, personalChannelID, remote.UploadUUID); err != nil || len(parts) != 0 {
		t.Fatalf("parts after discard = %+v, err %v", parts, err)
	}
	if batches := fakeTG.DeletedBatches(); len(batches) != 1 || len(batches[0]) != 3 {
		t.Fatalf("deleted batches = %+v, want one three-message batch", batches)
	}
}

func TestUploadHiddenRejectsInvalidRequestsBeforeSend(t *testing.T) {
	longName := strings.Repeat("a", 241)
	tests := []struct {
		name      string
		ctx       context.Context
		channelID int64
		request   HiddenUploadRequest
		source    io.ReadSeeker
	}{
		{name: "nil context", channelID: personalChannelID, request: HiddenUploadRequest{OperationID: "op", Name: "a", Size: 1}, source: bytes.NewReader([]byte("a"))},
		{name: "canceled context", ctx: canceledContext(), channelID: personalChannelID, request: HiddenUploadRequest{OperationID: "op", Name: "a", Size: 1}, source: bytes.NewReader([]byte("a"))},
		{name: "missing channel", ctx: context.Background(), request: HiddenUploadRequest{OperationID: "op", Name: "a", Size: 1}, source: bytes.NewReader([]byte("a"))},
		{name: "missing operation", ctx: context.Background(), channelID: personalChannelID, request: HiddenUploadRequest{Name: "a", Size: 1}, source: bytes.NewReader([]byte("a"))},
		{name: "negative size", ctx: context.Background(), channelID: personalChannelID, request: HiddenUploadRequest{OperationID: "op", Name: "a", Size: -1}, source: bytes.NewReader(nil)},
		{name: "empty name", ctx: context.Background(), channelID: personalChannelID, request: HiddenUploadRequest{OperationID: "op", Size: 1}, source: bytes.NewReader([]byte("a"))},
		{name: "trailing whitespace", ctx: context.Background(), channelID: personalChannelID, request: HiddenUploadRequest{OperationID: "op", Name: "a ", Size: 1}, source: bytes.NewReader([]byte("a"))},
		{name: "reserved Windows name", ctx: context.Background(), channelID: personalChannelID, request: HiddenUploadRequest{OperationID: "op", Name: "CON.txt", Size: 1}, source: bytes.NewReader([]byte("a"))},
		{name: "slash", ctx: context.Background(), channelID: personalChannelID, request: HiddenUploadRequest{OperationID: "op", Name: "a/b", Size: 1}, source: bytes.NewReader([]byte("a"))},
		{name: "backslash", ctx: context.Background(), channelID: personalChannelID, request: HiddenUploadRequest{OperationID: "op", Name: `a\b`, Size: 1}, source: bytes.NewReader([]byte("a"))},
		{name: "name too long", ctx: context.Background(), channelID: personalChannelID, request: HiddenUploadRequest{OperationID: "op", Name: longName, Size: 1}, source: bytes.NewReader([]byte("a"))},
		{name: "invalid UTF-8 name", ctx: context.Background(), channelID: personalChannelID, request: HiddenUploadRequest{OperationID: "op", Name: string([]byte{0xff}), Size: 1}, source: bytes.NewReader([]byte("a"))},
		{name: "nil source", ctx: context.Background(), channelID: personalChannelID, request: HiddenUploadRequest{OperationID: "op", Name: "a", Size: 1}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, fakeTG, _ := newTestService(t)
			if _, err := svc.UploadHidden(tc.ctx, tc.channelID, tc.request, tc.source); err == nil {
				t.Fatal("UploadHidden unexpectedly accepted invalid request")
			}
			if sent := fakeTG.SentFiles(); len(sent) != 0 {
				t.Fatalf("sent files = %+v, want none", sent)
			}
		})
	}
}

func TestUploadHiddenNamePolicyMatchesPortableProjection(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	name := " leading-space.txt"
	if _, err := projection.CanonicalNameKey(name); err != nil {
		t.Fatalf("portable policy rejected test name: %v", err)
	}
	if _, err := svc.UploadHidden(context.Background(), personalChannelID, HiddenUploadRequest{
		OperationID: "op-leading-space", Name: name, Size: 1,
	}, bytes.NewReader([]byte("x"))); err != nil {
		t.Fatalf("UploadHidden rejected portable name: %v", err)
	}
	if len(fakeTG.SentFiles()) != 1 {
		t.Fatal("portable upload did not reach Telegram")
	}
}

func TestDiscardHiddenOperationCleansCrashUploadDeterministically(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	svc.MaxUploadBytes = 4
	operationID := "op-crash-cleanup"
	remote, err := svc.UploadHidden(context.Background(), personalChannelID, HiddenUploadRequest{
		OperationID: operationID, Name: "crash.bin", Size: 10,
	}, bytes.NewReader([]byte("0123456789")))
	if err != nil {
		t.Fatalf("UploadHidden: %v", err)
	}
	if err := svc.DiscardHiddenOperation(context.Background(), personalChannelID, operationID); err != nil {
		t.Fatalf("DiscardHiddenOperation: %v", err)
	}
	if parts, err := projection.PartsForUUIDContext(context.Background(), db, personalChannelID, remote.UploadUUID); err != nil || len(parts) != 0 {
		t.Fatalf("parts after cleanup = %+v, err=%v", parts, err)
	}
	deleted := fakeTG.DeletedBatches()
	if len(deleted) != 1 || len(deleted[0]) != remote.PartCount {
		t.Fatalf("deleted batches = %+v, want %d messages", deleted, remote.PartCount)
	}
	// Recovery may repeat cleanup after an uncertain local transition.
	if err := svc.DiscardHiddenOperation(context.Background(), personalChannelID, operationID); err != nil {
		t.Fatalf("idempotent cleanup: %v", err)
	}
}

func TestDiscardHiddenOperationValidatesBeforeLookup(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	for name, call := range map[string]func() error{
		"nil context":     func() error { return svc.DiscardHiddenOperation(nil, personalChannelID, "op") },
		"zero channel":    func() error { return svc.DiscardHiddenOperation(context.Background(), 0, "op") },
		"empty operation": func() error { return svc.DiscardHiddenOperation(context.Background(), personalChannelID, "") },
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); err == nil {
				t.Fatal("invalid cleanup accepted")
			}
		})
	}
	if len(fakeTG.DeletedBatches()) != 0 {
		t.Fatal("invalid cleanup touched Telegram")
	}
}

func TestUploadHiddenReportsUnavailableDependencies(t *testing.T) {
	body := []byte("a")
	request := HiddenUploadRequest{OperationID: "op-deps", Name: "a.txt", Size: 1}
	tests := []struct {
		name   string
		mutate func(*Service)
	}{
		{name: "database", mutate: func(s *Service) { s.DB = nil }},
		{name: "telegram", mutate: func(s *Service) { s.TG = nil }},
		{name: "peer resolver", mutate: func(s *Service) { s.Peers = nil }},
		{name: "peer resolution", mutate: func(s *Service) {
			s.Peers = hiddenTestPeerResolver{err: errors.New("resolve failed")}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, _, _ := newTestService(t)
			tc.mutate(svc)
			if _, err := svc.UploadHidden(context.Background(), personalChannelID, request, bytes.NewReader(body)); err == nil {
				t.Fatal("UploadHidden unexpectedly succeeded")
			}
		})
	}
}

func TestUploadHiddenReportsPlanningAndMessageErrors(t *testing.T) {
	t.Run("too large", func(t *testing.T) {
		svc, _, fakeTG, _ := newTestService(t)
		svc.MaxUploadBytes = 1
		body := bytes.Repeat([]byte{'x'}, MaxParts+1)
		_, err := svc.UploadHidden(context.Background(), personalChannelID, HiddenUploadRequest{
			OperationID: "op-too-large", Name: "large.bin", Size: int64(len(body)),
		}, bytes.NewReader(body))
		if !errors.Is(err, ErrFileTooLarge) {
			t.Fatalf("error = %v, want ErrFileTooLarge", err)
		}
		if sent := fakeTG.SentFiles(); len(sent) != 0 {
			t.Fatalf("sent files = %+v, want none", sent)
		}
	})

	t.Run("single missing message id", func(t *testing.T) {
		svc, _, fakeTG, _ := newTestService(t)
		svc.TG = &zeroMessageIDClient{Fake: fakeTG}
		_, err := svc.UploadHidden(context.Background(), personalChannelID, HiddenUploadRequest{
			OperationID: "op-zero", Name: "zero.txt", Size: 1,
		}, bytes.NewReader([]byte("x")))
		if err == nil || !strings.Contains(err.Error(), "no message id") {
			t.Fatalf("error = %v, want missing message id", err)
		}
	})

	t.Run("multipart actor unavailable", func(t *testing.T) {
		svc, _, _, _ := newTestService(t)
		svc.MaxUploadBytes = 1
		svc.ActorID = nil
		_, err := svc.UploadHidden(context.Background(), personalChannelID, HiddenUploadRequest{
			OperationID: "op-no-actor", Name: "two.bin", Size: 2,
		}, bytes.NewReader([]byte("xx")))
		if err == nil || !strings.Contains(err.Error(), "actor resolver") {
			t.Fatalf("error = %v, want actor resolver error", err)
		}
	})
}

func TestUploadHiddenMultipartFailureCleansAlreadyUploadedParts(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	svc.MaxUploadBytes = 4
	sendErr := errors.New("second part failed")
	svc.TG = &failNthFileClient{Fake: fakeTG, failAt: 2, err: sendErr}

	_, err := svc.UploadHidden(context.Background(), personalChannelID, HiddenUploadRequest{
		OperationID: "op-part-failure", Name: "large.bin", Size: 10,
	}, bytes.NewReader([]byte("0123456789")))
	if !errors.Is(err, sendErr) {
		t.Fatalf("error = %v, want %v", err, sendErr)
	}
	uploadUUID := hiddenUploadUUID("op-part-failure")
	if parts, partErr := projection.PartsForUUID(db, personalChannelID, uploadUUID); partErr != nil || len(parts) != 0 {
		t.Fatalf("parts after failure = %+v, err %v", parts, partErr)
	}
	if deleted := fakeTG.DeletedBatches(); len(deleted) != 1 || len(deleted[0]) != 1 {
		t.Fatalf("deleted batches = %+v, want cleanup of first part", deleted)
	}
}

func TestUploadHiddenStopsAfterFloodWaitRetryBudget(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	fakeTG.InjectFloodWaits(3)
	svc.FloodWaitRetry = tgclient.FloodWaitRetryPolicy{
		MaxRetries: 1, MaxWait: time.Second, MaxTotalWait: time.Second,
		Sleep: func(context.Context, time.Duration) error { return nil },
	}

	_, err := svc.UploadHidden(context.Background(), personalChannelID, HiddenUploadRequest{
		OperationID: "op-flood-stop", Name: "wait.txt", Size: 1,
	}, bytes.NewReader([]byte("x")))
	if !errors.Is(err, tgclient.ErrFloodWait) {
		t.Fatalf("error = %v, want flood wait", err)
	}
	if sent := fakeTG.SentFiles(); len(sent) != 0 {
		t.Fatalf("sent files = %+v, want none", sent)
	}
}

func TestDiscardHiddenValidationAndCleanupFailure(t *testing.T) {
	t.Run("validation", func(t *testing.T) {
		svc, _, _, _ := newTestService(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		for name, call := range map[string]func() error{
			"nil context": func() error { return svc.DiscardHidden(nil, personalChannelID, HiddenBody{MessageIDs: []int64{1}}) },
			"canceled":    func() error { return svc.DiscardHidden(ctx, personalChannelID, HiddenBody{MessageIDs: []int64{1}}) },
			"channel":     func() error { return svc.DiscardHidden(context.Background(), 0, HiddenBody{MessageIDs: []int64{1}}) },
		} {
			t.Run(name, func(t *testing.T) {
				if err := call(); err == nil {
					t.Fatal("DiscardHidden unexpectedly succeeded")
				}
			})
		}
		if err := svc.DiscardHidden(context.Background(), personalChannelID, HiddenBody{}); err != nil {
			t.Fatalf("empty DiscardHidden: %v", err)
		}
	})

	t.Run("dependencies", func(t *testing.T) {
		svc, _, _, _ := newTestService(t)
		svc.TG = nil
		if err := svc.DiscardHidden(context.Background(), personalChannelID, HiddenBody{MessageIDs: []int64{1}}); err == nil {
			t.Fatal("DiscardHidden succeeded without Telegram")
		}
		svc, _, _, _ = newTestService(t)
		svc.Peers = hiddenTestPeerResolver{err: errors.New("resolve failed")}
		if err := svc.DiscardHidden(context.Background(), personalChannelID, HiddenBody{MessageIDs: []int64{1}}); err == nil {
			t.Fatal("DiscardHidden succeeded despite peer failure")
		}
	})

	t.Run("delete failure queues cleanup", func(t *testing.T) {
		svc, db, fakeTG, _ := newTestService(t)
		deleteErr := errors.New("delete failed")
		svc.TG = &deleteFailureClient{Fake: fakeTG, err: deleteErr}
		body := HiddenBody{UploadUUID: "hu-test", PartCount: 1, MessageIDs: []int64{77}}
		if err := svc.DiscardHidden(context.Background(), personalChannelID, body); !errors.Is(err, deleteErr) {
			t.Fatalf("DiscardHidden error = %v, want %v", err, deleteErr)
		}
		pending, err := projection.PendingPartCleanup(db, personalChannelID)
		if err != nil {
			t.Fatalf("PendingPartCleanup: %v", err)
		}
		if len(pending) != 1 || pending[0] != 77 {
			t.Fatalf("pending cleanup = %v, want [77]", pending)
		}
	})
}

func TestValidateSeekableSizeReportsSeekFailures(t *testing.T) {
	if err := validateSeekableSize(&failingSeeker{failAt: 1}, 0); err == nil || !strings.Contains(err.Error(), "measure") {
		t.Fatalf("first seek error = %v", err)
	}
	if err := validateSeekableSize(&failingSeeker{failAt: 2}, 0); err == nil || !strings.Contains(err.Error(), "rewind") {
		t.Fatalf("second seek error = %v", err)
	}
}

func canceledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}
