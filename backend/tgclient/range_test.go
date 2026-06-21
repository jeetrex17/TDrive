package tgclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"testing"

	"github.com/gotd/td/tg"
)

func TestRoundedTelegramLimit(t *testing.T) {
	tests := []struct {
		want int
		got  int
	}{
		{want: 1, got: int(RangeReadAlignment)},
		{want: 128, got: int(RangeReadAlignment)},
		{want: int(RangeReadAlignment), got: int(RangeReadAlignment)},
		{want: int(RangeReadAlignment) + 1, got: int(RangeReadAlignment) * 2},
		{want: RangeReadMaxBytes - 1, got: RangeReadMaxBytes},
		{want: RangeReadMaxBytes, got: RangeReadMaxBytes},
	}
	for _, tt := range tests {
		if got := roundedTelegramLimit(tt.want); got != tt.got {
			t.Fatalf("roundedTelegramLimit(%d) = %d, want %d", tt.want, got, tt.got)
		}
	}
}

func TestCrossesRangeBoundary(t *testing.T) {
	if !crossesRangeBoundary(int64(RangeReadMaxBytes)-1, 2) {
		t.Fatal("expected crossing range")
	}
	if crossesRangeBoundary(int64(RangeReadMaxBytes)-2, 2) {
		t.Fatal("two bytes ending at boundary should not cross")
	}
	if crossesRangeBoundary(int64(RangeReadMaxBytes), RangeReadMaxBytes) {
		t.Fatal("full aligned block should not cross")
	}
}

func TestCopyRequestedRange(t *testing.T) {
	src := []byte("abcdefgh")
	dst := make([]byte, 3)
	n, err := copyRequestedRange(dst, src)
	if err != nil {
		t.Fatalf("copyRequestedRange: %v", err)
	}
	if n != len(dst) || !bytes.Equal(dst, []byte("abc")) {
		t.Fatalf("n=%d dst=%q", n, dst)
	}
}

func TestVerifyFileHashes(t *testing.T) {
	data := []byte("abcdefghijklmnopqrstuvwxyz")
	h1 := hashFor(10, 8, data[:8])
	h2 := hashFor(18, 8, data[8:16])
	h3 := hashFor(26, len(data[16:]), data[16:])
	if err := verifyFileHashes(10, data, []tg.FileHash{h3, h1, h2}, nil); err != nil {
		t.Fatalf("verifyFileHashes: %v", err)
	}
}

func TestVerifyFileHashesRejectsCoverageGap(t *testing.T) {
	data := []byte("abcdefghijklmnop")
	err := verifyFileHashes(0, data, []tg.FileHash{
		hashFor(0, 8, data[:8]),
		hashFor(12, 4, data[12:]),
	}, nil)
	if err == nil {
		t.Fatal("expected gap error")
	}
}

func TestVerifyFileHashesRejectsMismatch(t *testing.T) {
	data := []byte("abcdefgh")
	bad := hashFor(0, 8, []byte("xxxxxxxx"))
	if err := verifyFileHashes(0, data, []tg.FileHash{bad}, nil); err == nil {
		t.Fatal("expected hash mismatch")
	}
}

func TestVerifyFileHashesFetchesTrailingPartialHashRange(t *testing.T) {
	full := []byte("abcdefgh")
	request := full[:3]
	hash := hashFor(0, 8, full)
	calls := 0
	err := verifyFileHashes(0, request, []tg.FileHash{hash}, func(got tg.FileHash) ([]byte, error) {
		calls++
		if got.Offset != hash.Offset || got.Limit != hash.Limit {
			t.Fatalf("fetch hash = %+v, want %+v", got, hash)
		}
		return full, nil
	})
	if err != nil {
		t.Fatalf("verifyFileHashes: %v", err)
	}
	if calls != 1 {
		t.Fatalf("fetch calls = %d, want 1", calls)
	}
}

func TestVerifyFileHashesFetchesLeadingPartialHashRange(t *testing.T) {
	full := []byte("abcdefgh")
	request := full[4:]
	hash := hashFor(0, 8, full)
	calls := 0
	err := verifyFileHashes(4, request, []tg.FileHash{hash}, func(got tg.FileHash) ([]byte, error) {
		calls++
		return full, nil
	})
	if err != nil {
		t.Fatalf("verifyFileHashes: %v", err)
	}
	if calls != 1 {
		t.Fatalf("fetch calls = %d, want 1", calls)
	}
}

func TestVerifyFileHashesRejectsPartialHashRangeWithoutFetch(t *testing.T) {
	full := []byte("abcdefgh")
	hash := hashFor(0, 8, full)
	if err := verifyFileHashes(0, full[:3], []tg.FileHash{hash}, nil); err == nil {
		t.Fatal("expected partial hash range error")
	}
}

func TestFakeRangeClient(t *testing.T) {
	f := NewFake(1)
	peer := InputPeer{ChannelID: 10, AccessHash: 20}
	f.SeedHistory(HistoryMessage{
		MsgID:        5,
		HasMedia:     true,
		MediaSize:    8,
		DocumentName: "clip.bin",
	})
	f.fileBodies[5] = []byte("abcdefgh")

	ref, err := f.ResolveDocument(context.Background(), peer, 5)
	if err != nil {
		t.Fatalf("ResolveDocument: %v", err)
	}
	if ref.Size != 8 || ref.Name != "clip.bin" || ref.Peer != peer {
		t.Fatalf("ref = %+v", ref)
	}
	dst := make([]byte, 3)
	n, err := f.ReadDocumentRange(context.Background(), ref, 2, dst)
	if err != nil {
		t.Fatalf("ReadDocumentRange: %v", err)
	}
	if n != 3 || !bytes.Equal(dst, []byte("cde")) {
		t.Fatalf("n=%d dst=%q", n, dst)
	}
}

func hashFor(offset int64, limit int, data []byte) tg.FileHash {
	sum := sha256.Sum256(data)
	return tg.FileHash{
		Offset: offset,
		Limit:  limit,
		Hash:   sum[:],
	}
}
