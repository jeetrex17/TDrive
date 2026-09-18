package file

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

// seedCommittedFile publishes one file whose body lives in its own hidden
// message, which is the shape every purge has to plan for.
func seedCommittedFile(t *testing.T, db *sql.DB, msgID, bodyMsgID int64, name string) {
	t.Helper()
	project(t, db, personalChannelID, msgID, 7, projection.Op{
		Type: projection.OpFileCommit, ProtocolVersion: 1,
		OpID: fmt.Sprintf("put-%d", msgID), Name: name,
		ContentMsgID: bodyMsgID, FileSize: 8,
	})
}

func trashedObjectIDs(t *testing.T, svc *Service) []string {
	t.Helper()
	listings, err := svc.ListTrash(personalChannelID)
	if err != nil {
		t.Fatalf("ListTrash: %v", err)
	}
	ids := make([]string, 0, len(listings))
	for _, listing := range listings {
		ids = append(ids, listing.ObjectID)
	}
	return ids
}

func TestDeleteTrashesWithoutTouchingTelegram(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	seedCommittedFile(t, db, 2001, 9001, "keep.txt")

	if err := svc.Delete(context.Background(), personalChannelID, 2001); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if projection.FileExists(db, personalChannelID, 2001) {
		t.Fatalf("deleted file is still visible")
	}
	if batches := fakeTG.DeletedBatches(); len(batches) != 0 {
		t.Fatalf("delete destroyed Telegram bodies: %+v", batches)
	}
	listings, err := svc.ListTrash(personalChannelID)
	if err != nil {
		t.Fatalf("ListTrash: %v", err)
	}
	if len(listings) != 1 || listings[0].ObjectID != "f:2001" || listings[0].OriginalName != "keep.txt" {
		t.Fatalf("trash listing = %+v", listings)
	}
	// The window is anchored on the service clock, not on wall time.
	if listings[0].DeletedAt != 1234 || listings[0].PurgeAfter != 1234+int64(projection.DefaultTrashRetention.Seconds()) {
		t.Fatalf("retention window = [%d, %d]", listings[0].DeletedAt, listings[0].PurgeAfter)
	}
}

func TestRestoreBringsBackAFileWhoseNameWasTakenMeanwhile(t *testing.T) {
	svc, db, _, _ := newTestService(t)
	seedCommittedFile(t, db, 2001, 9001, "notes.txt")

	if err := svc.Delete(context.Background(), personalChannelID, 2001); err != nil {
		t.Fatalf("delete: %v", err)
	}
	seedCommittedFile(t, db, 2002, 9002, "notes.txt")

	if err := svc.RestoreObject(context.Background(), personalChannelID, "f:2001"); err != nil {
		t.Fatalf("restore: %v", err)
	}
	file, ok, err := projection.FileByID(db, personalChannelID, 2001)
	if err != nil || !ok {
		t.Fatalf("restored file missing: ok=%v err=%v", ok, err)
	}
	if file.Name != "notes (2).txt" || file.ParentID != projection.RootParent {
		t.Fatalf("restored as %s under %q, want \"notes (2).txt\" at the root", file.Name, file.ParentID)
	}
	if ids := trashedObjectIDs(t, svc); len(ids) != 0 {
		t.Fatalf("trash still holds %v after restore", ids)
	}
}

func TestPurgeDeletesOnlyThePlannedBodies(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	seedCommittedFile(t, db, 2001, 9001, "gone.txt")
	seedCommittedFile(t, db, 2002, 9002, "stays.txt")

	if err := svc.Delete(context.Background(), personalChannelID, 2001); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := svc.PurgeObject(context.Background(), personalChannelID, "f:2001"); err != nil {
		t.Fatalf("purge: %v", err)
	}

	deleted := map[int64]bool{}
	for _, batch := range fakeTG.DeletedBatches() {
		for _, id := range batch {
			deleted[id] = true
		}
	}
	if !deleted[9001] {
		t.Fatalf("purge left the body behind: %v", deleted)
	}
	// Neither the untouched file's body nor the control messages that make up
	// the drive's history may ever enter a plan.
	for _, id := range []int64{9002, 2001, 2002} {
		if deleted[id] {
			t.Fatalf("purge deleted msg %d, which was never its body", id)
		}
	}
	if ids := trashedObjectIDs(t, svc); len(ids) != 0 {
		t.Fatalf("purged entry is still listed: %v", ids)
	}
	if !projection.FileExists(db, personalChannelID, 2002) {
		t.Fatalf("purge hid an unrelated file")
	}
}

func TestExpiredSweepDeletesNothingBeforePurgeAfter(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	seedCommittedFile(t, db, 2001, 9001, "pending.txt")
	if err := svc.Delete(context.Background(), personalChannelID, 2001); err != nil {
		t.Fatalf("delete: %v", err)
	}
	listings, err := svc.ListTrash(personalChannelID)
	if err != nil {
		t.Fatalf("ListTrash: %v", err)
	}
	purgeAfter := listings[0].PurgeAfter

	if err := svc.PurgeExpiredTrash(context.Background(), personalChannelID, purgeAfter-1); err != nil {
		t.Fatalf("sweep before purge_after: %v", err)
	}
	if batches := fakeTG.DeletedBatches(); len(batches) != 0 {
		t.Fatalf("sweep destroyed bodies one second early: %+v", batches)
	}
	if ids := trashedObjectIDs(t, svc); len(ids) != 1 {
		t.Fatalf("entry left the trash early: %v", ids)
	}
	// Registering the intent is the gate, so it must refuse on its own too.
	opID := projection.DeterministicOpID("purge", "f:2001", 2)
	if _, err := projection.RegisterExpiredTrashPurgeIntent(
		context.Background(), db, personalChannelID, opID, "f:2001", purgeAfter-1,
	); !errors.Is(err, projection.ErrTrashEntryNotExpired) {
		t.Fatalf("early intent = %v, want %v", err, projection.ErrTrashEntryNotExpired)
	}

	if err := svc.PurgeExpiredTrash(context.Background(), personalChannelID, purgeAfter); err != nil {
		t.Fatalf("sweep at purge_after: %v", err)
	}
	if ids := trashedObjectIDs(t, svc); len(ids) != 0 {
		t.Fatalf("expired entry survived its own sweep: %v", ids)
	}
	if !sawDeletedMessage(fakeTG, 9001) {
		t.Fatalf("expired sweep did not delete the body")
	}
}

func TestPurgeResumesAfterTheMarkerIsAlreadyProjected(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	seedCommittedFile(t, db, 2001, 9001, "interrupted.txt")
	if err := svc.Delete(context.Background(), personalChannelID, 2001); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// Stand in for a crash between the marker and the body deletion: the plan
	// exists, the trash entry is already gone, and nothing has been deleted.
	opID := projection.DeterministicOpID("purge", "f:2001", 2)
	revision, err := projection.RegisterTrashPurgeIntent(context.Background(), db, personalChannelID, opID, "f:2001")
	if err != nil {
		t.Fatalf("register intent: %v", err)
	}
	project(t, db, personalChannelID, 3001, 7, projection.HardDeleteOp(opID, "f:2001", revision))
	if len(fakeTG.DeletedBatches()) != 0 {
		t.Fatalf("marker deleted bodies on its own")
	}

	if err := svc.PurgeObject(context.Background(), personalChannelID, "f:2001"); err != nil {
		t.Fatalf("resume purge: %v", err)
	}
	if !sawDeletedMessage(fakeTG, 9001) {
		t.Fatalf("resumed purge did not delete the stranded body")
	}
}

func sawDeletedMessage(fakeTG *tgclient.Fake, msgID int64) bool {
	for _, batch := range fakeTG.DeletedBatches() {
		for _, id := range batch {
			if id == msgID {
				return true
			}
		}
	}
	return false
}
