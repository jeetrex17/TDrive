package tgclient

import (
	"context"
	"errors"
	"testing"
)

func TestFakeGetChannelDifferenceOrdersEventsAfterSeedSendDelete(t *testing.T) {
	f := NewFake(7)
	ctx := context.Background()

	startPts, err := f.GetChannelPts(ctx, testPeer)
	if err != nil {
		t.Fatalf("pts: %v", err)
	}

	f.SeedHistory(HistoryMessage{MsgID: 1, Text: "seeded"})
	id, err := f.SendControl(ctx, testPeer, "sent", false)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if err := f.DeleteMessages(ctx, testPeer, []int64{id}); err != nil {
		t.Fatalf("delete: %v", err)
	}

	diff, err := f.GetChannelDifference(ctx, testPeer, startPts, 100)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if !diff.Final {
		t.Fatal("expected Final with a large limit")
	}
	if diff.TooLong {
		t.Fatal("did not expect TooLong")
	}
	if len(diff.NewMessages) != 2 {
		t.Fatalf("new messages = %d, want 2", len(diff.NewMessages))
	}
	if diff.NewMessages[0].MsgID != 1 || diff.NewMessages[1].MsgID != id {
		t.Fatalf("new messages out of order: %+v", diff.NewMessages)
	}
	if len(diff.DeletedIDs) != 1 || diff.DeletedIDs[0] != id {
		t.Fatalf("deleted ids = %v, want [%d]", diff.DeletedIDs, id)
	}
	if diff.Pts <= startPts {
		t.Fatalf("pts did not advance: got %d, start %d", diff.Pts, startPts)
	}

	// A second call from the returned pts should find nothing left to page.
	diff2, err := f.GetChannelDifference(ctx, testPeer, diff.Pts, 100)
	if err != nil {
		t.Fatalf("diff2: %v", err)
	}
	if !diff2.Final || len(diff2.NewMessages) != 0 || len(diff2.DeletedIDs) != 0 {
		t.Fatalf("expected an empty final page, got %+v", diff2)
	}
	if diff2.Pts != diff.Pts {
		t.Fatalf("empty page pts = %d, want %d (current pts)", diff2.Pts, diff.Pts)
	}
}

func TestFakeGetChannelDifferenceLimitPages(t *testing.T) {
	f := NewFake(7)
	ctx := context.Background()

	startPts, err := f.GetChannelPts(ctx, testPeer)
	if err != nil {
		t.Fatalf("pts: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := f.SendControl(ctx, testPeer, "msg", false); err != nil {
			t.Fatalf("send: %v", err)
		}
	}

	page1, err := f.GetChannelDifference(ctx, testPeer, startPts, 2)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if page1.Final {
		t.Fatal("page1 should not be final: one event remains")
	}
	if len(page1.NewMessages) != 2 {
		t.Fatalf("page1 messages = %d, want 2", len(page1.NewMessages))
	}

	page2, err := f.GetChannelDifference(ctx, testPeer, page1.Pts, 2)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if !page2.Final {
		t.Fatal("page2 should be final")
	}
	if len(page2.NewMessages) != 1 {
		t.Fatalf("page2 messages = %d, want 1", len(page2.NewMessages))
	}
}

func TestFakeInjectDifferenceTooLong(t *testing.T) {
	f := NewFake(7)
	ctx := context.Background()
	f.InjectDifferenceTooLong(1)

	diff, err := f.GetChannelDifference(ctx, testPeer, 0, 100)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if !diff.TooLong || !diff.Final {
		t.Fatalf("expected TooLong+Final, got %+v", diff)
	}

	diff2, err := f.GetChannelDifference(ctx, testPeer, 0, 100)
	if err != nil {
		t.Fatalf("diff2: %v", err)
	}
	if diff2.TooLong {
		t.Fatal("injection should only affect one call")
	}
	if f.DifferenceCalls() != 2 {
		t.Fatalf("DifferenceCalls = %d, want 2", f.DifferenceCalls())
	}
}

func TestFakeGetChannelPts(t *testing.T) {
	f := NewFake(7)
	ctx := context.Background()

	before, err := f.GetChannelPts(ctx, testPeer)
	if err != nil {
		t.Fatalf("pts: %v", err)
	}
	if _, err := f.SendControl(ctx, testPeer, "x", false); err != nil {
		t.Fatalf("send: %v", err)
	}
	after, err := f.GetChannelPts(ctx, testPeer)
	if err != nil {
		t.Fatalf("pts: %v", err)
	}
	if after <= before {
		t.Fatalf("pts did not advance after send: before=%d after=%d", before, after)
	}
	if f.FullChannelCalls() != 2 {
		t.Fatalf("FullChannelCalls = %d, want 2", f.FullChannelCalls())
	}
}

func TestFakeGetChannelDifferenceHonoursReadFloodWaitInjection(t *testing.T) {
	f := NewFake(7)
	ctx := context.Background()
	f.InjectReadFloodWaits(1)

	if _, err := f.GetChannelDifference(ctx, testPeer, 0, 100); !errors.Is(err, ErrFloodWait) {
		t.Fatalf("err = %v, want ErrFloodWait", err)
	}
	diff, err := f.GetChannelDifference(ctx, testPeer, 0, 100)
	if err != nil {
		t.Fatalf("diff after flood wait clears: %v", err)
	}
	if !diff.Final {
		t.Fatal("expected Final on the empty diff")
	}
}
