package file

import (
	"bytes"
	"context"
	"testing"

	"TDrive/backend/projection"
	tdsync "TDrive/backend/sync"
	"TDrive/backend/tgclient"
)

func TestSourceCopyKeepsBodyWhileLaterTrashIsAheadOfSync(t *testing.T) {
	for _, tc := range []struct {
		name          string
		deletedRemote bool
		legacyCleanup bool
	}{
		{name: "later trash still in history"},
		{name: "later trash only in local replay", deletedRemote: true},
		{name: "recheck cleanup intent from an earlier version", legacyCleanup: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, telegram, deps := newSourceCopyFixture(t)
			const folder = "d:copy-target"
			peer := tgclient.InputPeer{ChannelID: personalChannelID}
			mkdir := projection.Op{Type: projection.OpMkdir, Obj: folder, Name: "Folder"}
			telegram.SeedHistory(tgclient.HistoryMessage{
				ChannelID: personalChannelID, MsgID: 100, FromID: 7, Text: projection.Format(mkdir),
			})

			var commitID int64
			history := &sourceCopyHistoryHook{sourceCopyTelegram: telegram}
			history.afterCommitCount = func(head int64) {
				commitID = head
				trash := projection.Op{
					Type: projection.OpTrashTree, ProtocolVersion: 1, OpID: "concurrent-trash", Obj: folder,
					ExpectedRevision: 1, DeletedAt: 1000, PurgeAfter: 2000,
				}
				project(t, svc.DB, personalChannelID, head+1, 7, trash)
				if !tc.deletedRemote {
					telegram.SeedHistory(tgclient.HistoryMessage{
						ChannelID: personalChannelID, MsgID: head + 1, FromID: 7, Text: projection.Format(trash),
					})
				}
			}
			deps.Sync = tdsync.NewEngine(svc.DB, history, svc.Peers).EnsureAuthoritative

			results, err := svc.StartSourceCopies(t.Context(), personalChannelID, 7, folder, []SourceCopyInput{sourceCopyTestInput()}, deps, projection.NewUploadUUID())
			if err != nil || len(results) != 1 || results[0].Status != sourceCopyPending || len(telegram.DeletedBatches()) != 0 {
				t.Fatalf("copy before ordered replay = %+v, %v, deleted %v", results, err, telegram.DeletedBatches())
			}
			if commitID <= telegram.nextBodyID {
				t.Fatalf("counted commit %d, want after body %d", commitID, telegram.nextBodyID)
			}
			if tc.legacyCleanup {
				job, err := svc.loadSourceCopyJob(t.Context(), personalChannelID, 7, results[0].ID)
				if err != nil {
					t.Fatal(err)
				}
				job.CleaningUp = true
				if err := svc.persistSourceCopyJob(t.Context(), job); err != nil {
					t.Fatal(err)
				}
			}
			if tc.deletedRemote {
				// A subsequent history scan crosses the replayed trash ID even
				// though Telegram no longer returns that control message.
				telegram.SeedHistory(tgclient.HistoryMessage{
					ChannelID: personalChannelID, MsgID: commitID + 2, FromID: 7, Text: "another message",
				})
			}
			retried, err := svc.RetrySourceCopy(t.Context(), personalChannelID, 7, results[0].ID, deps)
			if err != nil || retried.Status != sourceCopySaved || retried.FileID != commitID {
				t.Fatalf("copy after ordered replay = %+v, %v, want saved commit %d", retried, err, commitID)
			}
			var downloaded bytes.Buffer
			if err := telegram.DownloadFile(t.Context(), peer, telegram.nextBodyID, &downloaded, nil); err != nil ||
				downloaded.String() != "test payload" || len(telegram.DeletedBatches()) != 0 {
				t.Fatalf("copy body = %q, %v, deleted %v", downloaded.Bytes(), err, telegram.DeletedBatches())
			}
		})
	}
}

func TestSourceCopyWaitsForOrderedReplayBeforeResendingUnknownCommit(t *testing.T) {
	svc, telegram, deps := newSourceCopyFixture(t)
	unknown := &sourceCopyUnknownCommit{sourceCopyTelegram: telegram}
	svc.TG = unknown

	results, err := svc.StartSourceCopies(t.Context(), personalChannelID, 7, "", []SourceCopyInput{sourceCopyTestInput()}, deps, projection.NewUploadUUID())
	if err != nil || len(results) != 1 || results[0].Status != sourceCopyPending || unknown.commitCalls != 1 {
		t.Fatalf("original copy = %+v, %v, commit calls %d", results, err, unknown.commitCalls)
	}
	project(t, svc.DB, personalChannelID, telegram.nextBodyID+2, 7, projection.Op{
		Type: projection.OpMkdir, Obj: "d:ahead-of-history", Name: "Another folder",
	})

	retried, err := svc.RetrySourceCopy(t.Context(), personalChannelID, 7, results[0].ID, deps)
	if err != nil || retried.Status != sourceCopyPending || unknown.commitCalls != 1 || len(telegram.DeletedBatches()) != 0 {
		t.Fatalf("copy with unknown commit and replay ahead = %+v, %v, commit calls %d, deleted %v",
			retried, err, unknown.commitCalls, telegram.DeletedBatches())
	}
}

type sourceCopyUnknownCommit struct {
	*sourceCopyTelegram
	commitCalls int
}

func (f *sourceCopyUnknownCommit) SendControlWithRandomID(context.Context, tgclient.InputPeer, string, bool, int64) (int64, error) {
	f.commitCalls++
	return 0, tgclient.ErrSendOutcomeUnknown
}

type sourceCopyHistoryHook struct {
	*sourceCopyTelegram
	afterCommitCount func(int64)
}

func (*sourceCopyHistoryHook) GetChannelPts(context.Context, tgclient.InputPeer) (int64, error) {
	return 0, nil
}

func (f *sourceCopyHistoryHook) GetHistory(ctx context.Context, peer tgclient.InputPeer, minID, offsetID int64, limit int) ([]tgclient.HistoryMessage, error) {
	page, err := f.sourceCopyTelegram.GetHistory(ctx, peer, minID, offsetID, limit)
	if err == nil && len(page) > 0 && offsetID == 0 && f.afterCommitCount != nil && len(f.SentControls()) > 0 {
		callback := f.afterCommitCount
		f.afterCommitCount = nil
		callback(page[0].MsgID)
	}
	return page, err
}
