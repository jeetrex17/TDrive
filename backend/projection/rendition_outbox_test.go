package projection

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestRenditionOutboxFirstCiphertextWinsAndRemainsBounded(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	ref := renditionFixture()
	job := PendingRendition{ChannelID: testChan, JobID: strings.Repeat("a", 64), RandomID: 77, Header: Format(renditionOp(ref)), Payload: bytes.Repeat([]byte{7}, 100), CreatedAt: 1}
	if err := QueueRendition(ctx, db, job); err != nil {
		t.Fatal(err)
	}
	duplicate := job
	duplicate.RandomID = 88
	duplicate.Payload = bytes.Repeat([]byte{8}, 100)
	if err := QueueRendition(ctx, db, duplicate); err != nil {
		t.Fatal(err)
	}
	got, err := LoadPendingRendition(ctx, db, testChan, job.JobID)
	if err != nil || got.RandomID != 77 || !bytes.Equal(got.Payload, job.Payload) {
		t.Fatalf("uncertain ciphertext replaced: %+v %v", got, err)
	}
	for i := 1; i < 128; i++ {
		next := job
		next.JobID = fmt.Sprintf("%064d", i)
		if err := QueueRendition(ctx, db, next); err != nil {
			t.Fatal(err)
		}
	}
	extra := job
	extra.JobID = strings.Repeat("z", 64)
	if err := QueueRendition(ctx, db, extra); err == nil {
		t.Fatal("unbounded outbox")
	}
	ids, err := PendingRenditionIDs(ctx, db, testChan, 2)
	if err != nil || len(ids) != 2 {
		t.Fatalf("page: %v %v", ids, err)
	}
	if err := CompletePendingRendition(ctx, db, testChan, job.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPendingRendition(ctx, db, testChan, job.JobID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("completed intent retained: %v", err)
	}
	bad := job
	bad.Header = "bad"
	if err := QueueRendition(ctx, db, bad); err == nil {
		t.Fatal("accepted malformed intent")
	}
	if _, err := PendingRenditionIDs(ctx, db, testChan, 129); err == nil {
		t.Fatal("accepted unbounded page")
	}
}
