package file

import (
	"testing"

	"TDrive/backend/projection"
	tdsync "TDrive/backend/sync"
	"TDrive/backend/tgclient"
)

func TestSourceCopyRepairsLegacyRejectionBeforeResumingCleanup(t *testing.T) {
	svc, telegram, deps := newSourceCopyFixture(t)
	const folder = "d:legacy-copy-target"
	telegram.SeedHistory(tgclient.HistoryMessage{
		ChannelID: personalChannelID, MsgID: 100, FromID: 7,
		Text: projection.Format(projection.Op{Type: projection.OpMkdir, Obj: folder, Name: "Folder"}),
	})
	var commitID int64
	history := &sourceCopyHistoryHook{sourceCopyTelegram: telegram, afterCommitCount: func(head int64) {
		commitID = head
		project(t, svc.DB, personalChannelID, head+1, 7, projection.Op{
			Type: projection.OpTrashTree, ProtocolVersion: 1, OpID: "legacy-trash", Obj: folder,
			ExpectedRevision: 1, DeletedAt: 1000, PurgeAfter: 2000,
		})
	}}
	deps.Sync = tdsync.NewEngine(svc.DB, history, svc.Peers).EnsureAuthoritative
	ctx := t.Context()
	results, err := svc.StartSourceCopies(ctx, personalChannelID, 7, folder, []SourceCopyInput{sourceCopyTestInput()}, deps, projection.NewUploadUUID())
	if err != nil || len(results) != 1 || results[0].Status != sourceCopyPending || commitID <= telegram.nextBodyID {
		t.Fatalf("interrupted copy = %+v, %v, commit %d, body %d", results, err, commitID, telegram.nextBodyID)
	}

	// Before ordered reconciliation, a scan could cross a local operation that
	// Telegram had removed and leave the stale rejection below the watermark.
	telegram.SeedHistory(tgclient.HistoryMessage{
		ChannelID: personalChannelID, MsgID: commitID + 2, FromID: 7, Text: "another message",
	})
	if _, err := svc.DB.ExecContext(ctx, `UPDATE channels SET last_synced_msg=? WHERE channel_id=?`, commitID+2, personalChannelID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DB.ExecContext(ctx, `UPDATE source_copy_jobs SET batch_id='', cleaning_up=1 WHERE id=?`, results[0].ID); err != nil {
		t.Fatal(err)
	}
	job, err := svc.loadSourceCopyJob(ctx, personalChannelID, 7, results[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := projection.ReadOperationSnapshot(ctx, svc.DB, personalChannelID, job.commitOp().OpID)
	if err != nil || !snapshot.Ordered || snapshot.Operation.Outcome != projection.OperationRejected {
		t.Fatalf("legacy operation snapshot = %+v, %v", snapshot, err)
	}

	result, err := svc.RetrySourceCopy(ctx, personalChannelID, 7, results[0].ID, deps)
	if err != nil || result.Status != sourceCopySaved || result.FileID != commitID || len(telegram.DeletedBatches()) != 0 {
		t.Fatalf("legacy cleanup retry = %+v, %v, deleted %v, want saved commit %d",
			result, err, telegram.DeletedBatches(), commitID)
	}
}
