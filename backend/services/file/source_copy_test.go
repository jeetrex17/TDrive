package file

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"TDrive/backend/channelsource"
	"TDrive/backend/projection"
	tdsync "TDrive/backend/sync"
	"TDrive/backend/tgclient"
)

func TestSourceCopyPublishesOrderedFileAndKeepsBothNames(t *testing.T) {
	svc, telegram, deps := newSourceCopyFixture(t)
	telegram.beforeCommit = func(peer tgclient.InputPeer, msgID int64) {
		// Another device takes the name between our last sync and our commit.
		telegram.SeedHistory(tgclient.HistoryMessage{
			ChannelID: peer.ChannelID, MsgID: msgID, FromID: 7, Text: projection.Format(projection.Op{
				Type: projection.OpFileCommit, ProtocolVersion: 1, OpID: "other-device", Name: "Video.MP4", ContentMsgID: 99,
			}),
		})
		telegram.beforeCommit = nil
	}
	results, err := svc.StartSourceCopies(t.Context(), personalChannelID, 7, "", []SourceCopyInput{sourceCopyTestInput()}, deps, projection.NewUploadUUID())
	if err != nil || len(results) != 1 {
		t.Fatalf("start source copy = %+v, %v", results, err)
	}
	saved := results[0]
	if saved.Status != "saved" || saved.Name != "video (2).mp4" || saved.FileID <= 0 {
		t.Fatalf("result = %+v, want saved video (2).mp4", saved)
	}
	parts, err := projection.MultipartParts(svc.DB, personalChannelID, saved.FileID)
	if err != nil || len(parts) != 1 || parts[0].Size != 12 || parts[0].MsgID == saved.FileID {
		t.Fatalf("destination parts = %+v, %v", parts, err)
	}
	if len(telegram.SentControls()) != 2 || telegram.bodyCalls != 1 {
		t.Fatalf("control sends = %d, body sends = %d", len(telegram.SentControls()), telegram.bodyCalls)
	}
	if id := projection.LookupFileName(svc.DB, personalChannelID, saved.FileID); id != saved.Name {
		t.Fatalf("projected file name = %q", id)
	}
}

func TestSourceCopyRecoversAcceptedBodyAfterSourceDisconnect(t *testing.T) {
	svc, telegram, deps := newSourceCopyFixture(t)
	telegram.loseBodyReceipt = true
	results, err := svc.StartSourceCopies(t.Context(), personalChannelID, 7, "", []SourceCopyInput{sourceCopyTestInput()}, deps, projection.NewUploadUUID())
	if err != nil || len(results) != 1 || results[0].Status != "pending" {
		t.Fatalf("uncertain body = %+v, %v", results, err)
	}
	listed, err := svc.ListSourceCopies(t.Context(), personalChannelID, 7)
	if err != nil || len(listed) != 1 || listed[0].ID != results[0].ID {
		t.Fatalf("listed operations = %+v, %v", listed, err)
	}
	if _, err := svc.DB.Exec(`ALTER TABLE source_copy_jobs DROP COLUMN body_parts`); err != nil {
		t.Fatal(err)
	}

	restarted := &Service{DB: svc.DB, TG: telegram, Peers: svc.Peers, ActorID: svc.ActorID}
	deps.Resolve = func(context.Context, channelsource.CopySource) (tgclient.DocumentRef, error) {
		t.Error("recovery should reconcile the accepted destination body without resolving the source")
		return tgclient.DocumentRef{}, tgclient.ErrChannelUnavailable
	}
	result, err := restarted.RetrySourceCopy(t.Context(), personalChannelID, 7, results[0].ID, deps)
	if err != nil || result.Status != "saved" || telegram.bodyCalls != 1 {
		t.Fatalf("recover body = %+v, %v, %d sends", result, err, telegram.bodyCalls)
	}
}

func TestSourceCopyRecoversAcceptedCommitWithoutRepublishing(t *testing.T) {
	svc, telegram, deps := newSourceCopyFixture(t)
	telegram.loseCommitReceipt = true
	results, err := svc.StartSourceCopies(t.Context(), personalChannelID, 7, "", []SourceCopyInput{sourceCopyTestInput()}, deps, projection.NewUploadUUID())
	if err != nil || len(results) != 1 || results[0].Status != "pending" {
		t.Fatalf("uncertain commit = %+v, %v", results, err)
	}
	result, err := svc.RetrySourceCopy(t.Context(), personalChannelID, 7, results[0].ID, deps)
	if err != nil || result.Status != "saved" || telegram.bodyCalls != 1 || len(telegram.SentControls()) != 1 {
		t.Fatalf("recover commit = %+v, %v, %d body sends, %d commits", result, err, telegram.bodyCalls, len(telegram.SentControls()))
	}
}

func TestSourceCopyAfterUncertainWriteStillRequiresSourceAccessToResend(t *testing.T) {
	svc, telegram, deps := newSourceCopyFixture(t)
	telegram.unknownBodyWithoutPublish = true
	results, err := svc.StartSourceCopies(t.Context(), personalChannelID, 7, "", []SourceCopyInput{sourceCopyTestInput()}, deps, projection.NewUploadUUID())
	if err != nil || len(results) != 1 || results[0].Status != "pending" {
		t.Fatalf("uncertain body = %+v, %v", results, err)
	}
	deps.Resolve = func(context.Context, channelsource.CopySource) (tgclient.DocumentRef, error) {
		return tgclient.DocumentRef{}, channelsource.ErrCopyNotAllowed
	}
	result, err := svc.RetrySourceCopy(t.Context(), personalChannelID, 7, results[0].ID, deps)
	if err != nil || result.Status != "pending" || telegram.bodyCalls != 1 || len(telegram.SentControls()) != 0 {
		t.Fatalf("retry with blocked source = %+v, %v, %d body attempts, %d commits",
			result, err, telegram.bodyCalls, len(telegram.SentControls()))
	}
}

func TestSourceCopyRetriesMissingBodyWithTheOriginalRandomID(t *testing.T) {
	svc, telegram, deps := newSourceCopyFixture(t)
	telegram.unknownBodyWithoutPublish = true
	results, err := svc.StartSourceCopies(t.Context(), personalChannelID, 7, "", []SourceCopyInput{sourceCopyTestInput()}, deps, projection.NewUploadUUID())
	if err != nil || len(results) != 1 || results[0].Status != "pending" {
		t.Fatalf("uncertain body = %+v, %v", results, err)
	}
	if _, err := svc.DB.Exec(`ALTER TABLE source_copy_jobs DROP COLUMN body_parts`); err != nil {
		t.Fatal(err)
	}
	telegram.unknownBodyWithoutPublish = false
	result, err := svc.RetrySourceCopy(t.Context(), personalChannelID, 7, results[0].ID, deps)
	if err != nil || result.Status != "saved" {
		t.Fatalf("retried copy = %+v, %v", result, err)
	}
	ids := telegram.bodyRandomIDs
	if len(ids) != 2 || ids[0] <= 0 || ids[0] != ids[1] {
		t.Fatalf("body random IDs = %v, want same positive ID for both attempts", ids)
	}
}

func TestSourceCopyReplaysInFreshMyDriveWithoutItsSourceOrJournal(t *testing.T) {
	svc, telegram, deps := newSourceCopyFixture(t)
	results, err := svc.StartSourceCopies(t.Context(), personalChannelID, 7, "", []SourceCopyInput{sourceCopyTestInput()}, deps, projection.NewUploadUUID())
	if err != nil || len(results) != 1 || results[0].Status != "saved" {
		t.Fatalf("copy = %+v, %v", results, err)
	}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := projection.MigratePersonalChannel(db, personalChannelID); err != nil {
		t.Fatal(err)
	}
	if err := tdsync.NewEngine(db, telegram, svc.Peers).EnsureAuthoritative(t.Context(), personalChannelID); err != nil {
		t.Fatal(err)
	}
	fileID := results[0].FileID
	if name := projection.LookupFileName(db, personalChannelID, fileID); name != "video.mp4" {
		t.Fatalf("replayed file name = %q", name)
	}
	parts, err := projection.MultipartParts(db, personalChannelID, fileID)
	if err != nil || len(parts) != 1 {
		t.Fatalf("replayed file parts = %v, %v", parts, err)
	}
	var downloaded bytes.Buffer
	peer := tgclient.InputPeer{ChannelID: personalChannelID}
	if err := telegram.DownloadFile(t.Context(), peer, parts[0].MsgID, &downloaded, nil); err != nil {
		t.Fatal(err)
	}
	if downloaded.String() != "test payload" {
		t.Fatalf("replayed destination bytes = %q", downloaded.String())
	}
}

func TestSourceCopyCancellationStopsNewFilesAndKeepsUncertainReceipt(t *testing.T) {
	svc, telegram, deps := newSourceCopyFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	telegram.afterBody = cancel
	telegram.loseBodyReceipt = true
	second := sourceCopyTestInput()
	second.Source.MsgID = 32
	results, err := svc.StartSourceCopies(ctx, personalChannelID, 7, "", []SourceCopyInput{sourceCopyTestInput(), second}, deps, projection.NewUploadUUID())
	if err != nil || len(results) != 2 || results[0].Status != "pending" || results[1].Status != "canceled" {
		t.Fatalf("canceled batch = %+v, %v", results, err)
	}
	if telegram.bodyCalls != 1 || len(telegram.SentControls()) != 0 {
		t.Fatalf("canceled batch made %d body attempts, %d commits", telegram.bodyCalls, len(telegram.SentControls()))
	}
}

func TestSourceCopyDeletedFolderCleansOnlyOwnedDestinationBody(t *testing.T) {
	svc, telegram, deps := newSourceCopyFixture(t)
	const folder = "d:target"
	telegram.SeedHistory(tgclient.HistoryMessage{
		ChannelID: personalChannelID, MsgID: 100, FromID: 7,
		Text: projection.Format(projection.Op{Type: projection.OpMkdir, Obj: folder, Name: "Folder"}),
	})
	telegram.beforeCommit = func(peer tgclient.InputPeer, msgID int64) {
		telegram.SeedHistory(tgclient.HistoryMessage{
			ChannelID: peer.ChannelID, MsgID: msgID, FromID: 7,
			Text: projection.Format(projection.Op{Type: projection.OpRmdir, Obj: folder}),
		})
		telegram.beforeCommit = nil
	}
	results, err := svc.StartSourceCopies(t.Context(), personalChannelID, 7, folder, []SourceCopyInput{sourceCopyTestInput()}, deps, projection.NewUploadUUID())
	if err != nil || len(results) != 1 || results[0].Status != "failed" {
		t.Fatalf("deleted folder result = %+v, %v", results, err)
	}
	deleted := telegram.DeletedBatches()
	if len(deleted) != 1 || len(deleted[0]) != 1 || deleted[0][0] != telegram.nextBodyID {
		t.Fatalf("deleted batches = %v, want only destination body %d", deleted, telegram.nextBodyID)
	}
}

func TestSourceCopyRejectsWrongAccountAndNonpersonalTarget(t *testing.T) {
	svc, telegram, deps := newSourceCopyFixture(t)
	items := []SourceCopyInput{sourceCopyTestInput()}
	if _, err := svc.StartSourceCopies(t.Context(), personalChannelID, 8, "", items, deps, projection.NewUploadUUID()); err == nil {
		t.Fatal("wrong account should fail")
	}
	if _, err := svc.StartSourceCopies(t.Context(), sharedChannelID, 7, "", items, deps, projection.NewUploadUUID()); err == nil {
		t.Fatal("shared target should fail")
	}
	if telegram.bodyCalls != 0 || len(telegram.SentControls()) != 0 {
		t.Fatal("invalid request wrote a message")
	}
}

func TestSourceCopyPortableName(t *testing.T) {
	for name, expected := range map[string]string{
		"CON.txt": "_CON.txt", "trail. ": "trail", "bad:name?.zip": "bad_name_.zip",
	} {
		if actual := NormalizeSourceCopyName(name); actual != expected {
			t.Errorf("NormalizeSourceCopyName(%q) = %q, want %q", name, actual, expected)
		}
	}
}

func TestSourceCopySharedMultipartReconcilesAcceptedPartsAfterRestart(t *testing.T) {
	svc, telegram, deps, input := newSharedMultipartSourceCopyFixture(t)
	telegram.afterBody = func() {
		if telegram.bodyCalls == 2 {
			telegram.loseBodyReceipt = true
		}
	}
	results, err := svc.StartSourceCopies(t.Context(), personalChannelID, 7, "", []SourceCopyInput{input}, deps, projection.NewUploadUUID())
	if err != nil || len(results) != 1 || results[0].Status != "pending" || telegram.bodyCalls != 2 {
		t.Fatalf("lost second receipt = %+v, %v, %d sends", results, err, telegram.bodyCalls)
	}
	if len(telegram.SentControls()) != 0 {
		t.Fatal("published before both receipts were reconciled")
	}
	telegram.SeedMediaSourcePeers()
	restarted := &Service{DB: svc.DB, TG: telegram, Peers: svc.Peers, ActorID: svc.ActorID}
	deps.SyncShared = func(context.Context, int64) error {
		t.Error("destination receipt recovery must not depend on source access")
		return tgclient.ErrChannelUnavailable
	}
	result, err := restarted.RetrySourceCopy(t.Context(), personalChannelID, 7, results[0].ID, deps)
	if err != nil || result.Status != "saved" || result.Name != "Shared recording.mp4" || telegram.bodyCalls != 2 {
		t.Fatalf("reconciled copy = %+v, %v, %d sends", result, err, telegram.bodyCalls)
	}
	parts, err := projection.MultipartParts(svc.DB, personalChannelID, result.FileID)
	if err != nil || len(parts) != 2 {
		t.Fatalf("projected parts = %v, %v", parts, err)
	}
	for index, part := range parts {
		body, err := telegram.GetChannelMessage(t.Context(), svc.Peers.(sourceCopyTestPeers).personal, part.MsgID)
		if err != nil || part.PartIndex != index || part.Size != 12 || body.DocumentID != int64(141+index) {
			t.Fatalf("destination part %d = %+v, %v", index, part, err)
		}
	}
	if len(telegram.bodyRandomIDs) != 2 || telegram.bodyRandomIDs[0] == telegram.bodyRandomIDs[1] {
		t.Fatalf("multipart send IDs = %v, want one distinct value per part", telegram.bodyRandomIDs)
	}
	if !projection.FileExists(svc.DB, sharedChannelID, input.Source.MsgID) || len(telegram.DeletedBatches()) != 0 {
		t.Fatal("copy mutated the original")
	}
}

func TestSourceCopySharedMultipartRetriesOnlyMissingPart(t *testing.T) {
	svc, telegram, deps, input := newSharedMultipartSourceCopyFixture(t)
	telegram.afterBody = func() {
		if telegram.bodyCalls == 1 {
			telegram.unknownBodyWithoutPublish = true
		}
	}
	results, err := svc.StartSourceCopies(t.Context(), personalChannelID, 7, "", []SourceCopyInput{input}, deps, projection.NewUploadUUID())
	if err != nil || len(results) != 1 || results[0].Status != sourceCopyPending || telegram.bodyCalls != 2 {
		t.Fatalf("uncertain second body = %+v, %v, %d sends", results, err, telegram.bodyCalls)
	}
	telegram.unknownBodyWithoutPublish = false
	result, err := svc.RetrySourceCopy(t.Context(), personalChannelID, 7, results[0].ID, deps)
	if err != nil || result.Status != sourceCopySaved || telegram.bodyCalls != 3 {
		t.Fatalf("resumed multipart copy = %+v, %v, %d sends", result, err, telegram.bodyCalls)
	}
	ids := telegram.bodyRandomIDs
	if ids[0] == ids[1] || ids[1] != ids[2] {
		t.Fatalf("send IDs = %v, want only the missing part retried with its original ID", ids)
	}
}

func TestSourceCopySharedMultipartRevokedSourceCleansKnownPrefix(t *testing.T) {
	svc, telegram, deps, input := newSharedMultipartSourceCopyFixture(t)
	telegram.afterBody = func() {
		telegram.SeedMediaSourcePeers(tgclient.SourcePeer{
			Kind: tgclient.PeerSupergroup, ID: sharedChannelID, AccessHash: 88, Title: "Shared", Protected: true,
		})
	}
	results, err := svc.StartSourceCopies(t.Context(), personalChannelID, 7, "", []SourceCopyInput{input}, deps, projection.NewUploadUUID())
	if err != nil || len(results) != 1 || results[0].Status != sourceCopyFailed || telegram.bodyCalls != 1 {
		t.Fatalf("revoked source result = %+v, %v, %d sends", results, err, telegram.bodyCalls)
	}
	batches := telegram.DeletedBatches()
	if len(batches) != 1 || len(batches[0]) != 1 || batches[0][0] != telegram.nextBodyID {
		t.Fatalf("deleted batches = %v, want only the known destination part", batches)
	}
	if !projection.FileExists(svc.DB, sharedChannelID, input.Source.MsgID) || len(telegram.SentControls()) != 0 {
		t.Fatal("cleanup mutated or published the original")
	}
}

func TestSourceCopySharedMultipartDeletedFolderCleansOnlyDestinationParts(t *testing.T) {
	svc, telegram, deps, input := newSharedMultipartSourceCopyFixture(t)
	const folder = "d:target"
	telegram.SeedHistory(tgclient.HistoryMessage{
		ChannelID: personalChannelID, MsgID: 100, FromID: 7,
		Text: projection.Format(projection.Op{Type: projection.OpMkdir, Obj: folder, Name: "Folder"}),
	})
	telegram.afterBody = func() {
		if telegram.bodyCalls == 2 {
			telegram.SeedHistory(tgclient.HistoryMessage{
				ChannelID: personalChannelID, MsgID: telegram.nextBodyID + 1, FromID: 7,
				Text: projection.Format(projection.Op{Type: projection.OpRmdir, Obj: folder}),
			})
		}
	}
	results, err := svc.StartSourceCopies(t.Context(), personalChannelID, 7, folder, []SourceCopyInput{input}, deps, projection.NewUploadUUID())
	if err != nil || len(results) != 1 || results[0].Status != sourceCopyFailed {
		t.Fatalf("deleted folder result = %+v, %v", results, err)
	}
	batches := telegram.DeletedBatches()
	if len(batches) != 1 || len(batches[0]) != 2 || batches[0][0] != telegram.nextBodyID-10 || batches[0][1] != telegram.nextBodyID {
		t.Fatalf("deleted batches = %v, want the two destination parts", batches)
	}
	if !projection.FileExists(svc.DB, sharedChannelID, input.Source.MsgID) {
		t.Fatal("cleanup mutated the original")
	}
}

func newSharedMultipartSourceCopyFixture(t *testing.T) (*Service, *sourceCopyTelegram, SourceCopyDependencies, SourceCopyInput) {
	t.Helper()
	svc, telegram, deps := newSourceCopyFixture(t)
	sharedPeer := tgclient.InputPeer{ChannelID: sharedChannelID, Kind: tgclient.PeerSupergroup, AccessHash: 88}
	svc.Peers = sourceCopyTestPeers{personal: tgclient.InputPeer{ChannelID: personalChannelID, AccessHash: 99}, shared: sharedPeer}
	telegram.SeedMediaSourcePeers(tgclient.SourcePeer{Kind: sharedPeer.Kind, ID: sharedPeer.ChannelID, AccessHash: sharedPeer.AccessHash, Title: "Shared"})

	const (
		uploadID   = "shared-original"
		logicalID  = 77
		sourceSize = 24
	)
	for index, msgID := range []int64{41, 42} {
		op := projection.Op{Type: projection.OpFilePart, UploadUUID: uploadID, PartIndex: index, FileSize: 12}
		telegram.SeedHistory(tgclient.HistoryMessage{
			ChannelID: sharedChannelID, PeerKind: sharedPeer.Kind, MsgID: msgID, FromID: 8, Text: projection.Format(op), HasMedia: true,
			DocumentID: msgID + 100, DocumentName: "clip.part", MediaSize: 12,
		})
		project(t, svc.DB, sharedChannelID, msgID, 8, op)
	}
	project(t, svc.DB, sharedChannelID, logicalID, 8, projection.Op{
		Type: projection.OpFileCommit, ProtocolVersion: 1, OpID: "shared-file-op",
		Name: "Shared recording.mp4", UploadUUID: uploadID, PartCount: 2, FileSize: sourceSize,
	})
	input := SourceCopyInput{Source: channelsource.CopySource{
		AccountID: 7, PeerKind: SharedCopyPeerKind, PeerID: sharedChannelID, MsgID: logicalID,
		Generation: "1", Identity: "managed",
	}}
	return svc, telegram, deps, input
}

func TestSourceCopySelectionAllowsDifferentRevisionsInTheSameSharedDrive(t *testing.T) {
	common := channelsource.CopySource{AccountID: 7, PeerKind: SharedCopyPeerKind, PeerID: sharedChannelID}
	first := common
	first.MsgID, first.Generation = 42, "0"
	first.Identity = tgclient.SharedRawCopyIdentity(tgclient.HistoryMessage{
		ChannelID: sharedChannelID, PeerKind: tgclient.PeerSupergroup, MsgID: first.MsgID,
		HasMedia: true, DocumentID: 142, MediaSize: 12,
	})
	second := common
	second.MsgID, second.Generation, second.Identity = 43, "2", "managed"
	if err := validateSourceCopySelection(7, []SourceCopyInput{{Source: first}, {Source: second}}); err != nil {
		t.Fatalf("mixed shared file revisions failed validation: %v", err)
	}
	second.PeerID = personalChannelID
	if err := validateSourceCopySelection(7, []SourceCopyInput{{Source: first}, {Source: second}}); err == nil {
		t.Fatal("mixed source drives passed validation")
	}
}

func TestSourceCopyRejectsPersonalDriveAsSharedSourceBeforeSync(t *testing.T) {
	svc, telegram, deps := newSourceCopyFixture(t)
	deps.SyncShared = func(context.Context, int64) error {
		t.Error("attempted to sync an invalid shared source")
		return nil
	}
	input := SourceCopyInput{Source: channelsource.CopySource{
		AccountID: 7, PeerKind: SharedCopyPeerKind, PeerID: personalChannelID, MsgID: 15,
		Generation: "1", Identity: "managed",
	}}
	results, err := svc.StartSourceCopies(t.Context(), personalChannelID, 7, "", []SourceCopyInput{input}, deps, projection.NewUploadUUID())
	if err != nil || len(results) != 1 || results[0].Status != sourceCopyFailed || telegram.bodyCalls != 0 {
		t.Fatalf("invalid shared source = %+v, %v, %d sends", results, err, telegram.bodyCalls)
	}
}

type sourceCopyTestPeers struct {
	personal tgclient.InputPeer
	shared   tgclient.InputPeer
}

func (r sourceCopyTestPeers) ResolvePeer(_ context.Context, channelID int64) (tgclient.InputPeer, error) {
	switch channelID {
	case r.personal.ChannelID:
		return r.personal, nil
	case r.shared.ChannelID:
		return r.shared, nil
	default:
		return tgclient.InputPeer{}, tgclient.ErrChannelUnavailable
	}
}

type sourceCopyTelegram struct {
	*tgclient.Fake
	nextBodyID                int64
	bodyCalls                 int
	bodyRandomIDs             []int64
	unknownBodyWithoutPublish bool
	loseBodyReceipt           bool
	loseCommitReceipt         bool
	afterBody                 func()
	beforeCommit              func(tgclient.InputPeer, int64)
}

func (f *sourceCopyTelegram) ResendMedia(ctx context.Context, peer tgclient.InputPeer, caption string, randomID int64, resolve func(context.Context) (tgclient.DocumentRef, error)) (tgclient.SendFileResult, error) {
	f.bodyCalls++
	f.bodyRandomIDs = append(f.bodyRandomIDs, randomID)
	ref, err := resolve(ctx)
	if err != nil {
		return tgclient.SendFileResult{}, err
	}
	if f.unknownBodyWithoutPublish {
		return tgclient.SendFileResult{}, tgclient.ErrSendOutcomeUnknown
	}
	f.nextBodyID += 10
	msgID := f.nextBodyID
	f.SeedHistory(tgclient.HistoryMessage{
		ChannelID: peer.ChannelID, MsgID: msgID, FromID: 7, Outgoing: true, Text: caption, HasMedia: true,
		DocumentID: ref.DocumentID, DocumentName: ref.Name, MediaSize: ref.Size,
	})
	f.SeedDocumentBody(msgID, []byte("test payload"))
	if f.afterBody != nil {
		f.afterBody()
	}
	if f.loseBodyReceipt {
		f.loseBodyReceipt = false
		return tgclient.SendFileResult{}, tgclient.ErrSendOutcomeUnknown
	}
	return tgclient.SendFileResult{MsgID: msgID}, nil
}

func (f *sourceCopyTelegram) SendControlWithRandomID(ctx context.Context, peer tgclient.InputPeer, caption string, silent bool, randomID int64) (int64, error) {
	if f.beforeCommit != nil {
		f.beforeCommit(peer, f.nextBodyID+1)
	}
	msgID, err := f.Fake.SendControlWithRandomID(ctx, peer, caption, silent, randomID)
	if err == nil && f.loseCommitReceipt {
		f.loseCommitReceipt = false
		return 0, tgclient.ErrSendOutcomeUnknown
	}
	return msgID, err
}

func newSourceCopyFixture(t *testing.T) (*Service, *sourceCopyTelegram, SourceCopyDependencies) {
	t.Helper()
	svc, db, fake, _ := newTestService(t)
	telegram := &sourceCopyTelegram{Fake: fake, nextBodyID: 200}
	svc.TG = telegram
	engine := tdsync.NewEngine(db, telegram, svc.Peers)
	deps := SourceCopyDependencies{
		Sync: engine.EnsureAuthoritative,
		Resolve: func(_ context.Context, source channelsource.CopySource) (tgclient.DocumentRef, error) {
			if source.MsgID != 8 {
				return tgclient.DocumentRef{}, fmt.Errorf("unexpected source: %w", errors.ErrUnsupported)
			}
			return tgclient.DocumentRef{MsgID: source.MsgID, DocumentID: 9, Size: 12, Name: "video.mp4"}, nil
		},
	}
	return svc, telegram, deps
}

func sourceCopyTestInput() SourceCopyInput {
	return SourceCopyInput{Source: channelsource.CopySource{
		AccountID: 7, PeerKind: "group", PeerID: 14, Generation: "generation", MsgID: 8, Identity: "fingerprint",
	}}
}
