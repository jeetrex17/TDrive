package crypto

import "testing"

func TestStoredOffsetSkipsHeaderAndEarlierChunkTags(t *testing.T) {
	cases := map[int64]int64{
		0:                    streamHeaderLen,
		chunkSizePlain - 1:   streamHeaderLen + chunkSizePlain - 1,
		chunkSizePlain:       streamHeaderLen + chunkSizePlain + streamTagLen,
		3*chunkSizePlain + 7: streamHeaderLen + 3*(chunkSizePlain+streamTagLen) + 7,
	}
	for plain, want := range cases {
		got, ok := StoredOffset(plain)
		if !ok || got != want {
			t.Fatalf("StoredOffset(%d) = %d, %t; want %d", plain, got, ok, want)
		}
	}
	if _, ok := StoredOffset(-1); ok {
		t.Fatal("negative offsets have no stored position")
	}
}
