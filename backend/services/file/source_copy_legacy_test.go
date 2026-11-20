package file

import (
	"context"
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

func TestSourceCopyRecoversEarlierLegacyCommit(t *testing.T) {
	for _, test := range []struct {
		name         string
		cleaningUp   bool
		invalidReply bool
	}{
		{name: "retry later attempt"},
		{name: "resume cleanup", cleaningUp: true},
		{name: "cannot verify earlier receipt", cleaningUp: true, invalidReply: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, telegram, deps, job, firstCommit := earlierLegacyCopyFixture(t)
			job.CleaningUp = test.cleaningUp
			if err := svc.persistSourceCopyJob(t.Context(), job); err != nil {
				t.Fatal(err)
			}
			if test.invalidReply {
				svc.TG = &sourceCopyInvalidCommitReply{sourceCopyTelegram: telegram, msgID: firstCommit}
			}

			result, err := svc.RetrySourceCopy(t.Context(), personalChannelID, 7, job.Result.ID, deps)
			earlier, readErr := projection.ReadOperationSnapshot(t.Context(), svc.DB, personalChannelID, job.Result.ID+":1")
			if readErr != nil || earlier.Operation.Outcome != projection.OperationApplied {
				t.Fatalf("ordered original commit = %+v, %v", earlier, readErr)
			}
			if err != nil || len(telegram.DeletedBatches()) != 0 || len(telegram.SentControls()) != 1 {
				t.Fatalf("retry = %+v, %v, deletions %v, additional sends %d",
					result, err, telegram.DeletedBatches(), len(telegram.SentControls())-1)
			}
			if test.invalidReply {
				if result.Status != sourceCopyPending {
					t.Fatalf("unverified commit = %+v, want pending", result)
				}
				return
			}
			if result.Status != sourceCopySaved || result.FileID != firstCommit || result.Name != job.Result.Name {
				t.Fatalf("retry = %+v, want saved original %d with name %q", result, firstCommit, job.Result.Name)
			}
		})
	}
}

func earlierLegacyCopyFixture(t *testing.T) (*Service, *sourceCopyTelegram, SourceCopyDependencies, sourceCopyJob, int64) {
	t.Helper()
	svc, telegram, deps := newSourceCopyFixture(t)
	ctx := t.Context()
	var firstCommit int64
	history := &sourceCopyHistoryHook{sourceCopyTelegram: telegram, afterCommitCount: func(head int64) {
		firstCommit = head
		conflict := projection.Op{
			Type: projection.OpFolderCommit, ProtocolVersion: 1, OpID: "legacy-conflict",
			Obj: "d:legacy-name-conflict", Name: "video.mp4",
		}
		project(t, svc.DB, personalChannelID, head+1, 7, conflict)
		telegram.SeedHistory(tgclient.HistoryMessage{
			ChannelID: personalChannelID, MsgID: head + 1, FromID: 7, Text: projection.Format(conflict),
		})
	}}
	deps.Sync = tdsync.NewEngine(svc.DB, history, svc.Peers).EnsureAuthoritative
	results, err := svc.StartSourceCopies(ctx, personalChannelID, 7, "", []SourceCopyInput{sourceCopyTestInput()}, deps, projection.NewUploadUUID())
	if err != nil || len(results) != 1 || results[0].Status != sourceCopyPending || firstCommit <= telegram.nextBodyID {
		t.Fatalf("first attempt = %+v, %v, commit %d", results, err, firstCommit)
	}
	job, err := svc.loadSourceCopyJob(ctx, personalChannelID, 7, results[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	// The old worker could send another commit using the next name candidate
	// because it processed the later folder before the first commit.
	job.Attempt, job.CommitID = 2, firstCommit+2
	job.Candidate = projection.PortableFileName(job.Result.Name, 2)
	if _, err := svc.DB.ExecContext(ctx, "UPDATE source_copy_jobs SET batch_id='' WHERE id=?", job.Result.ID); err != nil {
		t.Fatal(err)
	}
	telegram.SeedHistory(tgclient.HistoryMessage{
		ChannelID: personalChannelID, MsgID: job.CommitID, FromID: 7, Text: projection.Format(job.commitOp()),
	})
	return svc, telegram, deps, job, firstCommit
}

type sourceCopyInvalidCommitReply struct {
	*sourceCopyTelegram
	msgID int64
}

func (f *sourceCopyInvalidCommitReply) GetChannelMessage(ctx context.Context, peer tgclient.InputPeer, id int64) (tgclient.HistoryMessage, error) {
	msg, err := f.sourceCopyTelegram.GetChannelMessage(ctx, peer, id)
	if peer.ChannelID == personalChannelID && id == f.msgID {
		msg.Text = "Message no longer contains the original copy receipt"
	}
	return msg, err
}
