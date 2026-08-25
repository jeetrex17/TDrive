package sync

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

// wireEmitTomb gives eng a real EmitTomb that mirrors production's
// EmitAndProject: send the tomb op as a control message, then apply it
// through the real projection path. Lets these tests assert on actual
// projection state instead of just "the callback was invoked".
func wireEmitTomb(t *testing.T, db *sql.DB, tg *tgclient.Fake, eng *Engine) *[]int64 {
	t.Helper()
	var calls []int64
	eng.EmitTomb = func(channelID int64, fileMsgID int64) error {
		calls = append(calls, fileMsgID)
		op := projection.Op{Type: projection.OpTomb, Obj: fmt.Sprintf("%s%d", projection.FileIDPrefix, fileMsgID)}
		header := projection.Format(op)
		peer := tgclient.InputPeer{ChannelID: channelID, AccessHash: 1}
		msgID, err := tg.SendControl(context.Background(), peer, header, true)
		if err != nil {
			return err
		}
		_, err = projection.ProjectFromOp(db, channelID, msgID, op, 7, header)
		return err
	}
	return &calls
}

func TestReconcileDeletionsTombstonesFileWithDeletedMessage(t *testing.T) {
	db, tg, eng := newSyncEnv(t)
	calls := wireEmitTomb(t, db, tg, eng)

	fileMsgID := sendOp(t, tg, projection.Op{Type: projection.OpFileUpload, Parent: projection.RootParent, Name: "x.png", FileSize: 1})
	if err := eng.Incremental(context.Background(), testChan); err != nil {
		t.Fatalf("incremental: %v", err)
	}
	if !projection.FileExists(db, testChan, fileMsgID) {
		t.Fatal("file missing after initial sync")
	}

	// Simulate someone deleting the message directly in Telegram: bypass
	// TDrive's own delete path entirely.
	peer := tgclient.InputPeer{ChannelID: testChan, AccessHash: 1}
	if err := tg.DeleteMessages(context.Background(), peer, []int64{fileMsgID}); err != nil {
		t.Fatalf("simulate external delete: %v", err)
	}

	n, err := eng.ReconcileDeletions(context.Background(), testChan)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if n != 1 {
		t.Fatalf("tombstoned = %d, want 1", n)
	}
	if len(*calls) != 1 || (*calls)[0] != fileMsgID {
		t.Fatalf("EmitTomb calls = %v, want [%d]", *calls, fileMsgID)
	}
	if projection.FileExists(db, testChan, fileMsgID) {
		t.Fatal("file still live after reconcile")
	}
}

func TestReconcileDeletionsTombstonesMultipartFileMissingOnePart(t *testing.T) {
	db, tg, eng := newSyncEnv(t)
	calls := wireEmitTomb(t, db, tg, eng)

	uuid := "reconcile-uuid"
	part0 := sendOp(t, tg, projection.Op{Type: projection.OpFilePart, UploadUUID: uuid, PartIndex: 0, FileSize: 100})
	part1 := sendOp(t, tg, projection.Op{Type: projection.OpFilePart, UploadUUID: uuid, PartIndex: 1, FileSize: 100})
	manifestMsgID := sendOp(t, tg, projection.Op{
		Type: projection.OpFileManifest, UploadUUID: uuid, Parent: projection.RootParent,
		Name: "movie.bin", FileSize: 200, PartCount: 2,
	})
	if err := eng.Incremental(context.Background(), testChan); err != nil {
		t.Fatalf("incremental: %v", err)
	}
	if !projection.FileExists(db, testChan, manifestMsgID) {
		t.Fatal("manifest file missing after initial sync")
	}

	// Only one part vanishes; the manifest message and the other part are
	// untouched. The file's content is unrecoverable either way.
	peer := tgclient.InputPeer{ChannelID: testChan, AccessHash: 1}
	if err := tg.DeleteMessages(context.Background(), peer, []int64{part1}); err != nil {
		t.Fatalf("simulate external delete: %v", err)
	}

	n, err := eng.ReconcileDeletions(context.Background(), testChan)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if n != 1 {
		t.Fatalf("tombstoned = %d, want 1", n)
	}
	if len(*calls) != 1 || (*calls)[0] != manifestMsgID {
		t.Fatalf("EmitTomb calls = %v, want [%d]", *calls, manifestMsgID)
	}
	if projection.FileExists(db, testChan, manifestMsgID) {
		t.Fatal("multipart file still live after reconcile")
	}
	_ = part0 // untouched part; kept only for readability of the scenario
}

func TestReconcileDeletionsLeavesLiveFilesAlone(t *testing.T) {
	db, tg, eng := newSyncEnv(t)
	calls := wireEmitTomb(t, db, tg, eng)

	fileMsgID := sendOp(t, tg, projection.Op{Type: projection.OpFileUpload, Parent: projection.RootParent, Name: "x.png", FileSize: 1})
	if err := eng.Incremental(context.Background(), testChan); err != nil {
		t.Fatalf("incremental: %v", err)
	}

	n, err := eng.ReconcileDeletions(context.Background(), testChan)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if n != 0 {
		t.Fatalf("tombstoned = %d, want 0", n)
	}
	if len(*calls) != 0 {
		t.Fatalf("EmitTomb calls = %v, want none", *calls)
	}
	if !projection.FileExists(db, testChan, fileMsgID) {
		t.Fatal("live file was incorrectly tombstoned")
	}
}

func TestReconcileDeletionsNoopWithoutEmitTomb(t *testing.T) {
	_, tg, eng := newSyncEnv(t)

	fileMsgID := sendOp(t, tg, projection.Op{Type: projection.OpFileUpload, Parent: projection.RootParent, Name: "x.png", FileSize: 1})
	if err := eng.Incremental(context.Background(), testChan); err != nil {
		t.Fatalf("incremental: %v", err)
	}
	peer := tgclient.InputPeer{ChannelID: testChan, AccessHash: 1}
	if err := tg.DeleteMessages(context.Background(), peer, []int64{fileMsgID}); err != nil {
		t.Fatalf("simulate external delete: %v", err)
	}

	// eng.EmitTomb is left nil on purpose.
	n, err := eng.ReconcileDeletions(context.Background(), testChan)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if n != 0 {
		t.Fatalf("tombstoned = %d, want 0 (EmitTomb unset)", n)
	}
}

func TestReconcileDeletionsEmptyChannelNoop(t *testing.T) {
	db, tg, eng := newSyncEnv(t)
	wireEmitTomb(t, db, tg, eng)

	n, err := eng.ReconcileDeletions(context.Background(), testChan)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if n != 0 {
		t.Fatalf("tombstoned = %d, want 0", n)
	}
}
