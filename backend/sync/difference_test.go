package sync

import (
	"context"
	"testing"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

func uploadOp(name string) projection.Op {
	return projection.Op{Type: projection.OpFileUpload, Parent: projection.RootParent, Name: name, FileSize: 1}
}

func channelPts(t *testing.T, eng *Engine) int64 {
	t.Helper()
	ch, err := projection.GetChannel(eng.db, testChan)
	if err != nil {
		t.Fatalf("get channel: %v", err)
	}
	return ch.Pts
}

func TestIncrementalBootstrapsPtsThenUsesDifference(t *testing.T) {
	db, tg, eng := newSyncEnv(t)
	ctx := context.Background()

	first := sendOp(t, tg, uploadOp("a.png"))
	if err := eng.Incremental(ctx, testChan); err != nil {
		t.Fatalf("initial incremental: %v", err)
	}
	if tg.FullChannelCalls() != 1 || tg.DifferenceCalls() != 0 {
		t.Fatalf("scan pass: full=%d diff=%d, want 1/0", tg.FullChannelCalls(), tg.DifferenceCalls())
	}
	if pts := channelPts(t, eng); pts <= 0 {
		t.Fatalf("pts not bootstrapped: %d", pts)
	}

	second := sendOp(t, tg, uploadOp("b.png"))
	if err := eng.Incremental(ctx, testChan); err != nil {
		t.Fatalf("difference incremental: %v", err)
	}
	if tg.DifferenceCalls() == 0 || tg.FullChannelCalls() != 1 {
		t.Fatalf("difference pass: full=%d diff=%d, want 1/>0", tg.FullChannelCalls(), tg.DifferenceCalls())
	}
	for _, id := range []int64{first, second} {
		if !projection.FileExists(db, testChan, id) {
			t.Fatalf("file %d missing after difference", id)
		}
	}
	want, _ := tg.GetChannelPts(ctx, tgclient.InputPeer{ChannelID: testChan, AccessHash: 1})
	if got := channelPts(t, eng); got != want {
		t.Fatalf("pts = %d, want %d", got, want)
	}
	if wm, _ := readWatermark(db, testChan); wm != second {
		t.Fatalf("watermark = %d, want %d", wm, second)
	}
}

func TestDifferenceAppliesDeletionsAndSkipsExistenceCheck(t *testing.T) {
	db, tg, eng := newSyncEnv(t)
	calls := wireEmitTomb(t, db, tg, eng)
	ctx := context.Background()
	peer := tgclient.InputPeer{ChannelID: testChan, AccessHash: 1}

	fileMsgID := sendOp(t, tg, uploadOp("x.png"))
	if err := eng.Incremental(ctx, testChan); err != nil {
		t.Fatalf("initial incremental: %v", err)
	}
	if err := tg.DeleteMessages(ctx, peer, []int64{fileMsgID}); err != nil {
		t.Fatalf("simulate external delete: %v", err)
	}

	if err := eng.Incremental(ctx, testChan); err != nil {
		t.Fatalf("difference incremental: %v", err)
	}
	if len(*calls) != 1 || (*calls)[0] != fileMsgID {
		t.Fatalf("EmitTomb calls = %v, want [%d]", *calls, fileMsgID)
	}
	if projection.FileExists(db, testChan, fileMsgID) {
		t.Fatal("file still live after difference reported its deletion")
	}

	n, err := eng.ReconcileDeletions(ctx, testChan)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if n != 0 || tg.MissingMessagesCalls() != 0 {
		t.Fatalf("reconcile ran existence check: n=%d calls=%d", n, tg.MissingMessagesCalls())
	}
}

func TestDifferenceTooLongFallsBackToScan(t *testing.T) {
	db, tg, eng := newSyncEnv(t)
	wireEmitTomb(t, db, tg, eng)
	ctx := context.Background()

	sendOp(t, tg, uploadOp("a.png"))
	if err := eng.Incremental(ctx, testChan); err != nil {
		t.Fatalf("initial incremental: %v", err)
	}

	tg.InjectDifferenceTooLong(1)
	second := sendOp(t, tg, uploadOp("b.png"))
	if err := eng.Incremental(ctx, testChan); err != nil {
		t.Fatalf("fallback incremental: %v", err)
	}
	if !projection.FileExists(db, testChan, second) {
		t.Fatal("file missing after scan fallback")
	}
	if tg.FullChannelCalls() != 2 {
		t.Fatalf("pts not re-bootstrapped after fallback: full=%d", tg.FullChannelCalls())
	}

	// The scan cannot see deletions, so the existence check must still run.
	if _, err := eng.ReconcileDeletions(ctx, testChan); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if tg.MissingMessagesCalls() != 1 {
		t.Fatalf("existence check calls = %d, want 1", tg.MissingMessagesCalls())
	}
}

func TestDifferenceIgnoresMessagesAlreadyScanned(t *testing.T) {
	db, tg, eng := newSyncEnv(t)
	ctx := context.Background()

	sendOp(t, tg, uploadOp("a.png"))
	sendOp(t, tg, uploadOp("b.png"))
	if err := eng.Incremental(ctx, testChan); err != nil {
		t.Fatalf("initial incremental: %v", err)
	}
	// Pretend the pts was captured before those messages arrived: the
	// difference replays them, and the watermark must swallow them without
	// flagging a rebuild.
	if err := projection.SetChannelPts(db, testChan, 1); err != nil {
		t.Fatalf("set pts: %v", err)
	}
	if err := eng.Incremental(ctx, testChan); err != nil {
		t.Fatalf("difference incremental: %v", err)
	}
	ch, err := projection.GetChannel(db, testChan)
	if err != nil {
		t.Fatalf("get channel: %v", err)
	}
	if ch.NeedsProjectionRebuild {
		t.Fatal("already-scanned messages flagged a rebuild")
	}
	if ch.Pts <= 1 {
		t.Fatalf("pts not advanced: %d", ch.Pts)
	}
}
