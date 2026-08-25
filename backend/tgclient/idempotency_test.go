package tgclient

import (
	"context"
	"strings"
	"testing"
)

func TestStableRandomIDIsDeterministicAndNamespaced(t *testing.T) {
	t.Parallel()

	first, err := StableRandomID("7f136fd2-dfbd-4e91-9421-181298cc1e20", "body")
	if err != nil {
		t.Fatalf("StableRandomID: %v", err)
	}
	again, err := StableRandomID("7f136fd2-dfbd-4e91-9421-181298cc1e20", "body")
	if err != nil {
		t.Fatalf("StableRandomID again: %v", err)
	}
	part, err := StableRandomID("7f136fd2-dfbd-4e91-9421-181298cc1e20", "part:0")
	if err != nil {
		t.Fatalf("StableRandomID part: %v", err)
	}

	if first <= 0 {
		t.Fatalf("random id = %d, want positive", first)
	}
	if first != again {
		t.Fatalf("same operation produced %d and %d", first, again)
	}
	if first == part {
		t.Fatalf("different steps produced the same id %d", first)
	}
}

func TestStableRandomIDRejectsAmbiguousKeys(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		operation string
		step      string
	}{
		{name: "empty operation", step: "body"},
		{name: "trimmed operation", operation: " op-1 ", step: "body"},
		{name: "empty step", operation: "op-1"},
		{name: "trimmed step", operation: "op-1", step: " body "},
		{name: "operation NUL", operation: "op\x00one", step: "body"},
		{name: "invalid UTF-8", operation: string([]byte{0xff}), step: "body"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := StableRandomID(tc.operation, tc.step); err == nil {
				t.Fatal("StableRandomID unexpectedly accepted invalid key")
			}
		})
	}
}

func TestFakeIdempotentControlSendReturnsOriginalMessage(t *testing.T) {
	fake := NewFake(7)
	randomID, err := StableRandomID("op-control", "commit")
	if err != nil {
		t.Fatalf("StableRandomID: %v", err)
	}

	first, err := fake.SendControlWithRandomID(context.Background(), testPeer, "one", true, randomID)
	if err != nil {
		t.Fatalf("first send: %v", err)
	}
	second, err := fake.SendControlWithRandomID(context.Background(), testPeer, "different retry payload", true, randomID)
	if err != nil {
		t.Fatalf("retry send: %v", err)
	}

	if second != first {
		t.Fatalf("retry message id = %d, want original %d", second, first)
	}
	if sent := fake.SentControls(); len(sent) != 1 || sent[0].RandomID != randomID {
		t.Fatalf("sent controls = %+v, want one idempotent send", sent)
	}
}

func TestFakeIdempotentFileSendDoesNotCreateDuplicate(t *testing.T) {
	fake := NewFake(7)
	randomID, err := StableRandomID("op-file", "body")
	if err != nil {
		t.Fatalf("StableRandomID: %v", err)
	}

	first, err := fake.SendFileWithRandomID(
		context.Background(), testPeer, strings.NewReader("hello"), "a.txt", "staged", 5, nil, randomID,
	)
	if err != nil {
		t.Fatalf("first send: %v", err)
	}
	second, err := fake.SendFileWithRandomID(
		context.Background(), testPeer, strings.NewReader("other"), "b.txt", "different", 5, nil, randomID,
	)
	if err != nil {
		t.Fatalf("retry send: %v", err)
	}

	if second.MsgID != first.MsgID {
		t.Fatalf("retry message id = %d, want original %d", second.MsgID, first.MsgID)
	}
	if sent := fake.SentFiles(); len(sent) != 1 || sent[0].RandomID != randomID {
		t.Fatalf("sent files = %+v, want one idempotent send", sent)
	}
}

func TestIdempotentSendHelpersUseExtension(t *testing.T) {
	fake := NewFake(7)
	controlID, err := SendControlIdempotent(context.Background(), fake, testPeer, "control", true, 11)
	if err != nil || controlID == 0 {
		t.Fatalf("SendControlIdempotent = (%d, %v)", controlID, err)
	}
	fileResult, err := SendFileIdempotent(context.Background(), fake, testPeer, strings.NewReader("x"), "x", "hidden", 1, nil, 12)
	if err != nil || fileResult.MsgID == 0 {
		t.Fatalf("SendFileIdempotent = (%+v, %v)", fileResult, err)
	}
}

func TestIdempotentSendHelpersRejectInvalidOrUnsupportedClient(t *testing.T) {
	fake := NewFake(7)
	if _, err := SendControlIdempotent(context.Background(), fake, testPeer, "x", true, 0); err == nil {
		t.Fatal("SendControlIdempotent accepted zero random id")
	}
	if _, err := SendFileIdempotent(context.Background(), fake, testPeer, strings.NewReader("x"), "x", "hidden", 1, nil, 0); err == nil {
		t.Fatal("SendFileIdempotent accepted zero random id")
	}

	// Embedding the base interface deliberately hides the optional extension.
	legacy := struct{ Client }{Client: fake}
	if _, err := SendControlIdempotent(context.Background(), legacy, testPeer, "x", true, 1); err == nil {
		t.Fatal("SendControlIdempotent accepted a legacy client")
	}
	if _, err := SendFileIdempotent(context.Background(), legacy, testPeer, strings.NewReader("x"), "x", "hidden", 1, nil, 2); err == nil {
		t.Fatal("SendFileIdempotent accepted a legacy client")
	}
}

func TestIdempotentGotdMethodsRejectZeroRandomIDBeforeConnecting(t *testing.T) {
	gotd := &Gotd{}
	if _, err := gotd.SendControlWithRandomID(context.Background(), testPeer, "x", true, 0); err == nil {
		t.Fatal("SendControlWithRandomID accepted zero random id")
	}
	if _, err := gotd.SendFileWithRandomID(context.Background(), testPeer, strings.NewReader("x"), "x", "hidden", 1, nil, 0); err == nil {
		t.Fatal("SendFileWithRandomID accepted zero random id")
	}
}
