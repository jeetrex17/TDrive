package tgclient

import (
	"context"
	"errors"
	"strings"
	"testing"
)

var testPeer = InputPeer{ChannelID: 12345, AccessHash: 99}

func TestFakeSendControlAssignsAscendingIDs(t *testing.T) {
	f := NewFake(7)
	ctx := context.Background()

	id1, err := f.SendControl(ctx, testPeer, "TDX1|t=mkdir|obj=d:a|p=|n=A", true)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	id2, err := f.SendControl(ctx, testPeer, "TDX1|t=mkdir|obj=d:b|p=|n=B", true)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if id2 <= id1 {
		t.Fatalf("ids not ascending: %d %d", id1, id2)
	}

	got := f.SentControls()
	if len(got) != 2 {
		t.Fatalf("controls len = %d", len(got))
	}
	if !got[0].Silent || !got[1].Silent {
		t.Fatal("silent flag not preserved")
	}
}

func TestFakeFloodWaitInjection(t *testing.T) {
	f := NewFake(7)
	ctx := context.Background()
	f.InjectFloodWaits(2)

	if _, err := f.SendControl(ctx, testPeer, "x", true); !errors.Is(err, ErrFloodWait) {
		t.Fatalf("first call err = %v want ErrFloodWait", err)
	}
	if _, err := f.SendControl(ctx, testPeer, "x", true); !errors.Is(err, ErrFloodWait) {
		t.Fatalf("second call err = %v want ErrFloodWait", err)
	}
	id, err := f.SendControl(ctx, testPeer, "x", true)
	if err != nil {
		t.Fatalf("third call: %v", err)
	}
	if id == 0 {
		t.Fatal("id should be nonzero after flood-wait clears")
	}
}

func TestFakeGetHistoryMinIDPaging(t *testing.T) {
	f := NewFake(7)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, err := f.SendControl(ctx, testPeer, "msg", false); err != nil {
			t.Fatalf("send: %v", err)
		}
	}
	all := f.SentControls()
	mid := all[2].MsgID

	out, err := f.GetHistory(ctx, testPeer, mid, 0, 10)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 newer than %d, got %d", mid, len(out))
	}
	for i := range out {
		if out[i].MsgID <= mid {
			t.Fatalf("history[%d].MsgID = %d, expected > %d", i, out[i].MsgID, mid)
		}
	}
	if out[0].MsgID <= out[1].MsgID {
		t.Fatalf("minID page should be newest-first, got %d then %d", out[0].MsgID, out[1].MsgID)
	}
}

func TestFakeGetHistoryBackwardPaging(t *testing.T) {
	f := NewFake(7)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, err := f.SendControl(ctx, testPeer, "msg", false); err != nil {
			t.Fatalf("send: %v", err)
		}
	}

	out, err := f.GetHistory(ctx, testPeer, 0, 0, 3)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("expected 3 latest, got %d", len(out))
	}
	for i := 1; i < len(out); i++ {
		if out[i].MsgID >= out[i-1].MsgID {
			t.Fatalf("backward paging not descending: %d at %d, %d at %d", out[i-1].MsgID, i-1, out[i].MsgID, i)
		}
	}
}

func TestFakeEditLastControlForTamperSimulation(t *testing.T) {
	f := NewFake(7)
	ctx := context.Background()
	if _, err := f.SendControl(ctx, testPeer, "TDX1|t=mkdir|obj=d:a|p=|n=A", true); err != nil {
		t.Fatalf("send: %v", err)
	}
	mutated, err := f.EditLastControlText("TDX1|t=mkdir|obj=d:a|p=|n=Hijack")
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	hist, err := f.GetHistory(ctx, testPeer, 0, 0, 100)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(hist) != 1 {
		t.Fatalf("history len = %d", len(hist))
	}
	if hist[0].MsgID != mutated {
		t.Fatalf("msg id changed: want %d got %d", mutated, hist[0].MsgID)
	}
	if !strings.Contains(hist[0].Text, "n=Hijack") {
		t.Fatalf("text not edited: %q", hist[0].Text)
	}
}

func TestFakeDeleteMessagesRemovesFromHistory(t *testing.T) {
	f := NewFake(7)
	ctx := context.Background()
	id, _ := f.SendControl(ctx, testPeer, "x", true)
	if err := f.DeleteMessages(ctx, testPeer, []int64{id}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	hist, _ := f.GetHistory(ctx, testPeer, 0, 0, 100)
	if len(hist) != 0 {
		t.Fatalf("expected empty history after delete, got %d", len(hist))
	}
	batches := f.DeletedBatches()
	if len(batches) != 1 || len(batches[0]) != 1 || batches[0][0] != id {
		t.Fatalf("delete batches = %v", batches)
	}
}

func TestFakeSendFileDrainsReader(t *testing.T) {
	f := NewFake(7)
	ctx := context.Background()
	r := strings.NewReader("hello")
	res, err := f.SendFile(ctx, testPeer, r, "x.txt", "TDX1|t=f|p=|n=x.txt", 5, nil)
	if err != nil {
		t.Fatalf("send file: %v", err)
	}
	if res.MsgID == 0 {
		t.Fatal("msgID zero")
	}
	files := f.SentFiles()
	if len(files) != 1 {
		t.Fatalf("sent files = %d", len(files))
	}
	if files[0].Name != "x.txt" || files[0].Caption != "TDX1|t=f|p=|n=x.txt" {
		t.Fatalf("file rec = %+v", files[0])
	}
}
