package mountadapter

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"TDrive/backend/mountwrite"
	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

type preparingHiddenStore struct {
	fakeHiddenStore
	received projection.File
	bytes    []byte
}

func (s *preparingHiddenStore) PrepareStoredRenditions(_ context.Context, file projection.File, reader io.ReadSeeker) error {
	s.received = file
	payload, err := io.ReadAll(reader)
	s.bytes = payload
	return err
}
func TestMountedPreparationPinsCommittedRevision(t *testing.T) {
	db := newProjectionDB(t)
	project(t, db, 100, projection.Op{Type: projection.OpFileUpload, Name: "photo.jpg", FileSize: 5})
	remote := newTestTelegramRemote(t, db, tgclient.NewFake(99), time.Now())
	store := &preparingHiddenStore{}
	remote.files = store
	req := mountwrite.HiddenUpload{DriveID: testDriveID, StoredSize: 5}
	result := mountwrite.MutationResult{ObjectID: "f:100", Revision: 1}
	if err := remote.PrepareCommittedRenditions(context.Background(), req, result, bytes.NewReader([]byte("photo"))); err != nil {
		t.Fatal(err)
	}
	if store.received.MsgID != 100 || string(store.bytes) != "photo" {
		t.Fatal("wrong source provided")
	}
	result.Revision = 2
	if err := remote.PrepareCommittedRenditions(context.Background(), req, result, bytes.NewReader(nil)); err == nil {
		t.Fatal("accepted a different revision's staged bytes")
	}
	result.ObjectID = "folder"
	if err := remote.PrepareCommittedRenditions(context.Background(), req, result, bytes.NewReader(nil)); err == nil {
		t.Fatal("accepted invalid parent identity")
	}
}
