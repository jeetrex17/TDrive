package mountwrite

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
)

type preparingRemote struct {
	*fakeRemote
	prepared []byte
	failure  error
}

func (r *preparingRemote) PrepareCommittedRenditions(_ context.Context, _ HiddenUpload, _ MutationResult, source io.ReadSeeker) error {
	payload, err := io.ReadAll(source)
	if err != nil {
		return err
	}
	r.prepared = payload
	return r.failure
}
func TestCommittedRenditionPreparationUsesStagingBeforeRemoval(t *testing.T) {
	for _, failure := range []error{nil, errors.New("preview network unavailable")} {
		events := &eventLog{}
		remote := &preparingRemote{fakeRemote: &fakeRemote{events: events}, failure: failure}
		c, _, staging := newTestCoordinator(t, remote, &fakeInvalidator{events: events})
		payload := []byte("local photo source")
		result, err := c.Put(context.Background(), PutRequest{DriveID: 42, Name: "photo.jpg", ContentLength: int64(len(payload)), MaxBytes: 1024}, bytes.NewReader(payload))
		if err != nil || result.ObjectID == "" {
			t.Fatalf("derivative changed committed outcome: %+v %v", result, err)
		}
		if !bytes.Equal(remote.prepared, payload) {
			t.Fatal("staged original unavailable to preparation")
		}
		if staging.UsedBytes() != 0 {
			t.Fatal("staging leaked after derivative preparation")
		}
	}
}
