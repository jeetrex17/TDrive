package file

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

var errInjectedHiddenUploadCrash = errors.New("injected crash after hidden send")

func TestRecoverAndDiscardHiddenUploadClosesSendReceiptCrashGap(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
		request func(*testing.T, []byte) (HiddenUploadRequest, []byte)
	}{
		{
			name:    "plaintext",
			payload: []byte("plaintext body that must not be orphaned"),
			request: func(_ *testing.T, payload []byte) (HiddenUploadRequest, []byte) {
				return plaintextHiddenRequest("op-crash-gap-plaintext", "plain.txt", int64(len(payload))), payload
			},
		},
		{
			name:    "encrypted",
			payload: []byte("encrypted body that must not be orphaned"),
			request: func(t *testing.T, payload []byte) (HiddenUploadRequest, []byte) {
				stored := encryptedHiddenSource(t, payload)
				return HiddenUploadRequest{
					OperationID:   "op-crash-gap-encrypted",
					Name:          "secret.bin",
					StoredSize:    int64(len(stored)),
					PlaintextSize: int64(len(payload)),
					Encrypted:     true,
				}, stored
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc, db, fakeTG, _ := newTestService(t)
			request, stored := test.request(t, test.payload)
			svc.afterHiddenPartSend = func(partIndex int, _ int64) {
				if partIndex == 0 {
					panic(errInjectedHiddenUploadCrash)
				}
			}

			assertHiddenUploadCrashes(t, func() {
				_, _ = svc.UploadHidden(
					context.Background(), personalChannelID, request, bytes.NewReader(stored),
				)
			})

			sentBeforeRestart := fakeTG.SentFiles()
			if len(sentBeforeRestart) != 1 {
				t.Fatalf("sent files before restart = %+v, want one accepted send", sentBeforeRestart)
			}
			parts, err := projection.PartsForUUID(db, personalChannelID, hiddenUploadUUID(request.OperationID))
			if err != nil || len(parts) != 0 {
				t.Fatalf("parts at crash boundary = %+v, err=%v; receipt must not be projected yet", parts, err)
			}

			// A fresh service value models process restart. The same staged stored
			// bytes are still available from the mount-write staging journal.
			restarted := restartedHiddenUploadService(svc)
			if err := restarted.RecoverAndDiscardHiddenUpload(
				context.Background(), personalChannelID, request, bytes.NewReader(stored),
			); err != nil {
				t.Fatalf("RecoverAndDiscardHiddenUpload: %v", err)
			}

			// Telegram random_id idempotency must resolve the original message,
			// not publish a duplicate, and cleanup must target that exact receipt.
			if sentAfterRestart := fakeTG.SentFiles(); len(sentAfterRestart) != 1 {
				t.Fatalf("sent files after restart = %+v, want original send only", sentAfterRestart)
			}
			deleted := fakeTG.DeletedBatches()
			if len(deleted) != 1 || !slices.Equal(deleted[0], []int64{sentBeforeRestart[0].MsgID}) {
				t.Fatalf("deleted batches = %+v, want exact message %d", deleted, sentBeforeRestart[0].MsgID)
			}
			parts, err = projection.PartsForUUID(db, personalChannelID, hiddenUploadUUID(request.OperationID))
			if err != nil || len(parts) != 0 {
				t.Fatalf("parts after recovery = %+v, err=%v", parts, err)
			}
		})
	}
}

func TestRecoverAndDiscardHiddenUploadResendsOnlyFirstUnprojectedPart(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	svc.MaxUploadBytes = 4
	request := plaintextHiddenRequest("op-crash-gap-multipart", "large.bin", 10)
	stored := []byte("0123456789")
	const unrelatedMsgID int64 = 777
	fakeTG.SeedHistory(tgclient.HistoryMessage{
		MsgID: unrelatedMsgID, FromID: 7, Text: "unrelated media", HasMedia: true, MediaSize: 99,
	})
	svc.afterHiddenPartSend = func(partIndex int, _ int64) {
		if partIndex == 1 {
			panic(errInjectedHiddenUploadCrash)
		}
	}

	assertHiddenUploadCrashes(t, func() {
		_, _ = svc.UploadHidden(context.Background(), personalChannelID, request, bytes.NewReader(stored))
	})

	sent := fakeTG.SentFiles()
	if len(sent) != 2 {
		t.Fatalf("sent files at crash = %+v, want parts zero and one", sent)
	}
	wantUncertainRandomID, err := tgclient.StableRandomID(request.OperationID, "part:1")
	if err != nil {
		t.Fatalf("StableRandomID: %v", err)
	}
	if sent[1].RandomID != wantUncertainRandomID {
		t.Fatalf("uncertain part random id = %d, want deterministic %d", sent[1].RandomID, wantUncertainRandomID)
	}
	parts, err := projection.PartsForUUID(db, personalChannelID, hiddenUploadUUID(request.OperationID))
	if err != nil || len(parts) != 1 || parts[0].PartIndex != 0 || parts[0].MsgID != sent[0].MsgID {
		t.Fatalf("durable prefix at crash = %+v, err=%v", parts, err)
	}

	restarted := restartedHiddenUploadService(svc)
	if err := restarted.RecoverAndDiscardHiddenUpload(
		context.Background(), personalChannelID, request, bytes.NewReader(stored),
	); err != nil {
		t.Fatalf("RecoverAndDiscardHiddenUpload: %v", err)
	}
	if got := fakeTG.SentFiles(); len(got) != 2 {
		t.Fatalf("recovery sent later/unrelated parts: %+v", got)
	}
	deleted := fakeTG.DeletedBatches()
	wantIDs := []int64{sent[0].MsgID, sent[1].MsgID}
	if len(deleted) != 1 || !slices.Equal(deleted[0], wantIDs) {
		t.Fatalf("deleted = %+v, want exact accepted prefix/current %v", deleted, wantIDs)
	}
	history, err := fakeTG.GetHistory(
		context.Background(), tgclient.InputPeer{ChannelID: personalChannelID}, 0, 0, 10,
	)
	if err != nil {
		t.Fatalf("history after cleanup: %v", err)
	}
	if !slices.ContainsFunc(history, func(message tgclient.HistoryMessage) bool {
		return message.MsgID == unrelatedMsgID
	}) {
		t.Fatalf("unrelated message %d was deleted; history=%+v", unrelatedMsgID, history)
	}
}

func TestDiscardHiddenReceiptRejectsMessageIDsWithoutProjectedOwnership(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	request := plaintextHiddenRequest("op-reject-unowned-cleanup", "owned.bin", 7)
	body, err := svc.UploadHidden(
		context.Background(),
		personalChannelID,
		request,
		bytes.NewReader([]byte("payload")),
	)
	if err != nil {
		t.Fatalf("UploadHidden: %v", err)
	}
	if len(body.MessageIDs) != 1 {
		t.Fatalf("uploaded body = %+v", body)
	}

	tampered := body
	tampered.MessageIDs = []int64{body.MessageIDs[0] + 1000}
	if err := svc.DiscardHiddenReceipt(
		context.Background(), personalChannelID, request.OperationID, tampered,
	); err == nil {
		t.Fatal("DiscardHiddenReceipt accepted an unowned message ID")
	}
	if deleted := fakeTG.DeletedBatches(); len(deleted) != 0 {
		t.Fatalf("unowned message ID reached Telegram delete: %+v", deleted)
	}
	parts, err := projection.PartsForUUID(db, personalChannelID, body.UploadUUID)
	if err != nil || len(parts) != 1 || parts[0].MsgID != body.MessageIDs[0] {
		t.Fatalf("owned projection changed after rejection: parts=%+v err=%v", parts, err)
	}
}

func TestDiscardHiddenReceiptRejectsUploadUUIDFromAnotherOperation(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	request := plaintextHiddenRequest("op-reject-wrong-upload", "owned.bin", 7)
	body, err := svc.UploadHidden(
		context.Background(),
		personalChannelID,
		request,
		bytes.NewReader([]byte("payload")),
	)
	if err != nil {
		t.Fatalf("UploadHidden: %v", err)
	}
	body.UploadUUID = hiddenUploadUUID("different-operation")
	if err := svc.DiscardHiddenReceipt(
		context.Background(), personalChannelID, request.OperationID, body,
	); err == nil {
		t.Fatal("DiscardHiddenReceipt accepted another operation's upload UUID")
	}
	if deleted := fakeTG.DeletedBatches(); len(deleted) != 0 {
		t.Fatalf("wrong upload UUID reached Telegram delete: %+v", deleted)
	}
}

func TestDiscardHiddenReceiptTreatsMissingProjectionAsCompletedCleanup(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	request := plaintextHiddenRequest("op-cleanup-already-complete", "owned.bin", 7)
	body, err := svc.UploadHidden(
		context.Background(),
		personalChannelID,
		request,
		bytes.NewReader([]byte("payload")),
	)
	if err != nil {
		t.Fatalf("UploadHidden: %v", err)
	}
	if err := projection.DeleteFileParts(db, personalChannelID, body.UploadUUID); err != nil {
		t.Fatalf("simulate completed pointer cleanup: %v", err)
	}
	if err := svc.DiscardHiddenReceipt(
		context.Background(), personalChannelID, request.OperationID, body,
	); err != nil {
		t.Fatalf("idempotent DiscardHiddenReceipt: %v", err)
	}
	if deleted := fakeTG.DeletedBatches(); len(deleted) != 0 {
		t.Fatalf("missing projection triggered a second Telegram delete: %+v", deleted)
	}
}

func restartedHiddenUploadService(previous *Service) *Service {
	return &Service{
		DB:             previous.DB,
		TG:             previous.TG,
		Peers:          previous.Peers,
		ActorID:        previous.ActorID,
		Warnf:          previous.Warnf,
		MaxUploadBytes: previous.MaxUploadBytes,
		FloodWaitRetry: previous.FloodWaitRetry,
	}
}

func TestRecoverAndDiscardHiddenUploadRejectsUntrustedPartPrefix(t *testing.T) {
	tests := []struct {
		name string
		op   projection.Op
	}{
		{
			name: "gap",
			op: projection.Op{
				Type: projection.OpFilePart, PartIndex: 1, FileSize: 4,
			},
		},
		{
			name: "wrong size",
			op: projection.Op{
				Type: projection.OpFilePart, PartIndex: 0, FileSize: 3,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc, db, fakeTG, actor := newTestService(t)
			svc.MaxUploadBytes = 4
			request := plaintextHiddenRequest("op-invalid-prefix-"+test.name, "large.bin", 10)
			test.op.UploadUUID = hiddenUploadUUID(request.OperationID)
			caption := projection.Format(test.op)
			if _, err := projection.ProjectFromOp(db, personalChannelID, 991, test.op, *actor, caption); err != nil {
				t.Fatalf("seed invalid prefix: %v", err)
			}

			err := svc.RecoverAndDiscardHiddenUpload(
				context.Background(), personalChannelID, request, bytes.NewReader([]byte("0123456789")),
			)
			if err == nil {
				t.Fatal("malformed receipt prefix was accepted")
			}
			if sent := fakeTG.SentFiles(); len(sent) != 0 {
				t.Fatalf("recovery sent with untrusted prefix: %+v", sent)
			}
			if deleted := fakeTG.DeletedBatches(); len(deleted) != 0 {
				t.Fatalf("recovery deleted with untrusted prefix: %+v", deleted)
			}
		})
	}
}

func TestRecoverHiddenUploadResolvesActorBeforeUncertainResend(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	actorErr := errors.New("actor unavailable")
	svc.ActorID = func(context.Context) (int64, error) { return 0, actorErr }
	request := plaintextHiddenRequest("op-actor-before-resend", "actor.bin", 1)

	if _, err := svc.RecoverHiddenUpload(
		context.Background(), personalChannelID, request, bytes.NewReader([]byte("x")),
	); !errors.Is(err, actorErr) {
		t.Fatalf("RecoverHiddenUpload error = %v, want %v", err, actorErr)
	}
	if sent := fakeTG.SentFiles(); len(sent) != 0 {
		t.Fatalf("recovery sent before actor resolution: %+v", sent)
	}
	if deleted := fakeTG.DeletedBatches(); len(deleted) != 0 {
		t.Fatalf("recovery deleted before actor resolution: %+v", deleted)
	}
}

func TestRecoverAndDiscardHiddenUploadPersistsRecoveredReceiptBeforeDeleteRetry(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	request := plaintextHiddenRequest("op-recovered-receipt-delete-retry", "retry.bin", 7)
	stored := []byte("payload")
	svc.afterHiddenPartSend = func(partIndex int, _ int64) {
		if partIndex == 0 {
			panic(errInjectedHiddenUploadCrash)
		}
	}
	assertHiddenUploadCrashes(t, func() {
		_, _ = svc.UploadHidden(context.Background(), personalChannelID, request, bytes.NewReader(stored))
	})
	svc.afterHiddenPartSend = nil

	if _, err := db.Exec(`
		CREATE TRIGGER fail_hidden_receipt_pointer_delete
		BEFORE DELETE ON file_parts
		BEGIN
			SELECT RAISE(ABORT, 'injected pointer cleanup failure');
		END;
	`); err != nil {
		t.Fatalf("create delete trigger: %v", err)
	}
	if err := svc.RecoverAndDiscardHiddenUpload(
		context.Background(), personalChannelID, request, bytes.NewReader(stored),
	); err == nil {
		t.Fatal("first cleanup unexpectedly ignored pointer deletion failure")
	}
	sent := fakeTG.SentFiles()
	if len(sent) != 1 {
		t.Fatalf("sent after first cleanup = %+v, want original idempotent send only", sent)
	}
	parts, err := projection.PartsForUUID(db, personalChannelID, hiddenUploadUUID(request.OperationID))
	if err != nil || len(parts) != 1 || parts[0].MsgID != sent[0].MsgID {
		t.Fatalf("recovered receipt was not durable: parts=%+v err=%v", parts, err)
	}

	if _, err := db.Exec(`DROP TRIGGER fail_hidden_receipt_pointer_delete`); err != nil {
		t.Fatalf("drop delete trigger: %v", err)
	}
	if err := svc.RecoverAndDiscardHiddenUpload(
		context.Background(), personalChannelID, request, bytes.NewReader(stored),
	); err != nil {
		t.Fatalf("retry cleanup: %v", err)
	}
	if got := fakeTG.SentFiles(); len(got) != 1 {
		t.Fatalf("retry resent an already recovered part: %+v", got)
	}
	deleted := fakeTG.DeletedBatches()
	want := []int64{sent[0].MsgID}
	if len(deleted) != 2 || !slices.Equal(deleted[0], want) || !slices.Equal(deleted[1], want) {
		t.Fatalf("delete retries = %+v, want exact receipt twice", deleted)
	}
	parts, err = projection.PartsForUUID(db, personalChannelID, hiddenUploadUUID(request.OperationID))
	if err != nil || len(parts) != 0 {
		t.Fatalf("receipt pointers after retry = %+v, err=%v", parts, err)
	}
}

type acceptThenLoseReceiptClient struct {
	*tgclient.Fake
	failAt int
	calls  int
	err    error
}

type acceptThenZeroReceiptClient struct {
	*tgclient.Fake
	calls int
}

func (c *acceptThenZeroReceiptClient) SendFileWithRandomID(
	ctx context.Context,
	peer tgclient.InputPeer,
	source io.Reader,
	name string,
	caption string,
	size int64,
	progress func(int64, int64),
	randomID int64,
) (tgclient.SendFileResult, error) {
	c.calls++
	result, err := c.Fake.SendFileWithRandomID(ctx, peer, source, name, caption, size, progress, randomID)
	if err != nil {
		return tgclient.SendFileResult{}, err
	}
	if c.calls == 1 {
		return tgclient.SendFileResult{}, nil
	}
	return result, nil
}

func (c *acceptThenLoseReceiptClient) SendFileWithRandomID(
	ctx context.Context,
	peer tgclient.InputPeer,
	source io.Reader,
	name string,
	caption string,
	size int64,
	progress func(int64, int64),
	randomID int64,
) (tgclient.SendFileResult, error) {
	c.calls++
	result, err := c.Fake.SendFileWithRandomID(ctx, peer, source, name, caption, size, progress, randomID)
	if err != nil {
		return result, err
	}
	if c.calls == c.failAt {
		return tgclient.SendFileResult{}, errors.Join(tgclient.ErrSendOutcomeUnknown, c.err)
	}
	return result, nil
}

func TestUploadHiddenUnknownSendOutcomeReconcilesCurrentPartBeforeCleanup(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	svc.MaxUploadBytes = 4
	lostReceipt := errors.New("connection lost after Telegram accepted part")
	svc.TG = &acceptThenLoseReceiptClient{Fake: fakeTG, failAt: 2, err: lostReceipt}
	request := plaintextHiddenRequest("op-lost-part-receipt", "large.bin", 10)

	_, err := svc.UploadHidden(
		context.Background(), personalChannelID, request, bytes.NewReader([]byte("0123456789")),
	)
	if !errors.Is(err, lostReceipt) {
		t.Fatalf("UploadHidden error = %v, want %v", err, lostReceipt)
	}
	sent := fakeTG.SentFiles()
	if len(sent) != 2 {
		t.Fatalf("accepted sends = %+v, want exactly first and uncertain second parts", sent)
	}
	if deleted := fakeTG.DeletedBatches(); len(deleted) != 0 {
		t.Fatalf("UploadHidden performed pre-journal uncertain cleanup: %+v", deleted)
	}
	prefix, prefixErr := projection.PartsForUUID(db, personalChannelID, hiddenUploadUUID(request.OperationID))
	if prefixErr != nil || len(prefix) != 1 || prefix[0].MsgID != sent[0].MsgID {
		t.Fatalf("durable prefix before coordinator recovery = %+v, err=%v", prefix, prefixErr)
	}
	if err := svc.RecoverAndDiscardHiddenUpload(
		context.Background(), personalChannelID, request, bytes.NewReader([]byte("0123456789")),
	); err != nil {
		t.Fatalf("coordinator-style recover/discard: %v", err)
	}
	if got := fakeTG.SentFiles(); len(got) != 2 {
		t.Fatalf("receipt recovery duplicated an accepted send: %+v", got)
	}
	deleted := fakeTG.DeletedBatches()
	wantIDs := []int64{sent[0].MsgID, sent[1].MsgID}
	if len(deleted) != 1 || !slices.Equal(deleted[0], wantIDs) {
		t.Fatalf("deleted = %+v, want exact accepted messages %v", deleted, wantIDs)
	}
	parts, partErr := projection.PartsForUUID(db, personalChannelID, hiddenUploadUUID(request.OperationID))
	if partErr != nil || len(parts) != 0 {
		t.Fatalf("parts after unknown-outcome cleanup = %+v, err=%v", parts, partErr)
	}
}

func TestUploadHiddenAcceptedSendWithoutReceiptIsRecoveredBeforeCleanup(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	svc.TG = &acceptThenZeroReceiptClient{Fake: fakeTG}
	request := plaintextHiddenRequest("op-zero-accepted-receipt", "zero.bin", 1)

	_, err := svc.UploadHidden(
		context.Background(), personalChannelID, request, bytes.NewReader([]byte("x")),
	)
	if err == nil || !strings.Contains(err.Error(), "no message id") {
		t.Fatalf("UploadHidden error = %v, want missing receipt", err)
	}
	sent := fakeTG.SentFiles()
	if len(sent) != 1 {
		t.Fatalf("accepted sends = %+v, want one", sent)
	}
	if deleted := fakeTG.DeletedBatches(); len(deleted) != 0 {
		t.Fatalf("UploadHidden performed pre-journal zero-receipt cleanup: %+v", deleted)
	}
	if err := svc.RecoverAndDiscardHiddenUpload(
		context.Background(), personalChannelID, request, bytes.NewReader([]byte("x")),
	); err != nil {
		t.Fatalf("coordinator-style recover/discard: %v", err)
	}
	deleted := fakeTG.DeletedBatches()
	if len(deleted) != 1 || !slices.Equal(deleted[0], []int64{sent[0].MsgID}) {
		t.Fatalf("deleted = %+v, want accepted message %d", deleted, sent[0].MsgID)
	}
	parts, partErr := projection.PartsForUUID(db, personalChannelID, hiddenUploadUUID(request.OperationID))
	if partErr != nil || len(parts) != 0 {
		t.Fatalf("parts after zero-receipt recovery = %+v, err=%v", parts, partErr)
	}
}

func TestUploadHiddenCancellationAfterProjectedPrefixDefersExactCleanup(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	svc.MaxUploadBytes = 4
	ctx, cancel := context.WithCancel(context.Background())
	svc.afterHiddenPartSend = func(partIndex int, _ int64) {
		if partIndex == 0 {
			cancel()
		}
	}
	request := plaintextHiddenRequest("op-cancel-after-prefix", "cancel.bin", 10)

	partial, err := svc.UploadHidden(ctx, personalChannelID, request, bytes.NewReader([]byte("0123456789")))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("UploadHidden error = %v, want canceled", err)
	}
	if len(partial.MessageIDs) != 1 || len(fakeTG.SentFiles()) != 1 {
		t.Fatalf("partial=%+v sent=%+v, want exact first part", partial, fakeTG.SentFiles())
	}
	parts, partErr := projection.PartsForUUID(db, personalChannelID, partial.UploadUUID)
	if partErr != nil || len(parts) != 1 || parts[0].MsgID != partial.MessageIDs[0] {
		t.Fatalf("projected prefix=%+v err=%v", parts, partErr)
	}
	if deleted := fakeTG.DeletedBatches(); len(deleted) != 0 {
		t.Fatalf("UploadHidden deleted before journal durability: %+v", deleted)
	}
	if err := svc.discardHiddenBody(context.Background(), personalChannelID, partial); err != nil {
		t.Fatalf("discardHiddenBody: %v", err)
	}
	if got := fakeTG.SentFiles(); len(got) != 1 {
		t.Fatalf("cleanup resent after cancellation: %+v", got)
	}
	if deleted := fakeTG.DeletedBatches(); len(deleted) != 1 ||
		!slices.Equal(deleted[0], partial.MessageIDs) {
		t.Fatalf("deleted=%+v, want exact partial %v", deleted, partial.MessageIDs)
	}
}

func TestUploadHiddenProjectionFailureReturnsPositiveReceiptWithoutDeleting(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	request := plaintextHiddenRequest("op-project-failure-receipt", "project.bin", 1)
	if _, err := db.Exec(`
		CREATE TRIGGER fail_hidden_part_projection
		BEFORE INSERT ON file_parts
		BEGIN
			SELECT RAISE(ABORT, 'injected projection failure');
		END;
	`); err != nil {
		t.Fatalf("create projection trigger: %v", err)
	}

	partial, err := svc.UploadHidden(
		context.Background(), personalChannelID, request, bytes.NewReader([]byte("x")),
	)
	if err == nil || !errors.Is(err, ErrHiddenReceiptRecoveryRequired) ||
		!strings.Contains(err.Error(), "project hidden upload part") {
		t.Fatalf("UploadHidden error = %v, want projection failure", err)
	}
	if len(partial.MessageIDs) != 1 || len(fakeTG.SentFiles()) != 1 {
		t.Fatalf("partial=%+v sent=%+v, want accepted message receipt", partial, fakeTG.SentFiles())
	}
	if deleted := fakeTG.DeletedBatches(); len(deleted) != 0 {
		t.Fatalf("projection failure deleted before receipt journaling: %+v", deleted)
	}
	if _, err := db.Exec(`DROP TRIGGER fail_hidden_part_projection`); err != nil {
		t.Fatalf("drop projection trigger: %v", err)
	}
	if err := svc.RecoverAndDiscardHiddenUpload(
		context.Background(), personalChannelID, request, bytes.NewReader([]byte("x")),
	); err != nil {
		t.Fatalf("RecoverAndDiscardHiddenUpload: %v", err)
	}
	if got := fakeTG.SentFiles(); len(got) != 1 {
		t.Fatalf("cleanup resent known positive receipt: %+v", got)
	}
	if deleted := fakeTG.DeletedBatches(); len(deleted) != 1 ||
		!slices.Equal(deleted[0], partial.MessageIDs) {
		t.Fatalf("deleted=%+v, want exact receipt %v", deleted, partial.MessageIDs)
	}
}

func assertHiddenUploadCrashes(t *testing.T, run func()) {
	t.Helper()
	deferred := false
	func() {
		defer func() {
			if recovered := recover(); !errors.Is(recoveredError(recovered), errInjectedHiddenUploadCrash) {
				t.Fatalf("panic = %v, want injected crash", recovered)
			}
			deferred = true
		}()
		run()
	}()
	if !deferred {
		t.Fatal("hidden upload did not reach crash injection point")
	}
}

func recoveredError(value any) error {
	err, _ := value.(error)
	return err
}
