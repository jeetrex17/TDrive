package mountdav

import (
	"context"
	"errors"
	"testing"
	"time"

	"TDrive/backend/mountfs"
)

func TestResourceETagUsesCanonicalProjectionIdentity(t *testing.T) {
	ctx := context.Background()
	want, err := ResourceETag(ctx, 42, "f:9", 7, "sha256:content")
	if err != nil {
		t.Fatalf("ResourceETag: %v", err)
	}
	if !validStrongETag(want) {
		t.Fatalf("ResourceETag = %q, want a strong entity tag", want)
	}

	entry := mountfs.Entry{
		ChannelID:   42,
		ID:          "f:9",
		Revision:    7,
		ContentHash: "sha256:content",
		Name:        "display-name.txt",
		Size:        123,
		ContentRef:  "telegram:private-reference",
		UploadUUID:  "private-upload-id",
		PartCount:   4,
		ModTime:     time.Unix(123, 456),
	}
	got, err := EntryETag(ctx, entry)
	if err != nil {
		t.Fatalf("EntryETag: %v", err)
	}
	if got != want {
		t.Fatalf("EntryETag = %q, ResourceETag = %q", got, want)
	}

	entry.Name = "renamed-locally.txt"
	entry.Size++
	entry.ContentRef = "telegram:other-private-reference"
	entry.UploadUUID = "other-private-upload-id"
	entry.PartCount++
	entry.ModTime = entry.ModTime.Add(time.Hour)
	if irrelevant, err := EntryETag(ctx, entry); err != nil || irrelevant != want {
		t.Fatalf("non-identity metadata changed ETag: (%q, %v), want %q", irrelevant, err, want)
	}
}

func TestResourceETagChangesWithEveryIdentityField(t *testing.T) {
	ctx := context.Background()
	base, err := ResourceETag(ctx, 42, "f:9", 7, "sha256:content")
	if err != nil {
		t.Fatalf("base ResourceETag: %v", err)
	}
	variants := []struct {
		name        string
		channelID   int64
		objectID    string
		revision    int64
		contentHash string
	}{
		{name: "channel", channelID: 43, objectID: "f:9", revision: 7, contentHash: "sha256:content"},
		{name: "object", channelID: 42, objectID: "f:10", revision: 7, contentHash: "sha256:content"},
		{name: "revision", channelID: 42, objectID: "f:9", revision: 8, contentHash: "sha256:content"},
		{name: "content hash", channelID: 42, objectID: "f:9", revision: 7, contentHash: "sha256:other"},
	}
	for _, variant := range variants {
		t.Run(variant.name, func(t *testing.T) {
			got, err := ResourceETag(ctx, variant.channelID, variant.objectID, variant.revision, variant.contentHash)
			if err != nil {
				t.Fatalf("ResourceETag: %v", err)
			}
			if got == base {
				t.Fatalf("identity change did not change ETag: %q", got)
			}
		})
	}
}

func TestResourceETagHonorsContextAndParsesAsIfMatch(t *testing.T) {
	if _, err := ResourceETag(nil, 1, "f:1", 1, ""); err == nil {
		t.Fatal("nil context unexpectedly accepted")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ResourceETag(cancelled, 1, "f:1", 1, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled ResourceETag error = %v", err)
	}

	etag, err := ResourceETag(context.Background(), 1, "f:1", 1, "")
	if err != nil {
		t.Fatalf("ResourceETag: %v", err)
	}
	condition, ok := parseETagConditions([]string{etag})
	if !ok || !condition.Present || condition.Any || len(condition.Tags) != 1 || condition.Tags[0].Weak {
		t.Fatalf("canonical ETag did not round-trip through If-Match: %+v/%t", condition, ok)
	}
	if `"`+condition.Tags[0].Opaque+`"` != etag {
		t.Fatalf("parsed ETag = %+v, want %q", condition.Tags[0], etag)
	}
}
