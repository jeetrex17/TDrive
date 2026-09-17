package read

import (
	"context"
	"errors"
	"testing"

	"TDrive/backend/projection"
)

func TestGalleryReadServicePreservesContentAndLimits(t *testing.T) {
	svc, _, _ := newTestService(t)
	project(t, svc.DB, 20, projection.Op{Type: projection.OpFileUpload, Name: "photo.jpg", FileSize: 100, FileUploadTime: 1000})
	timeline, err := svc.MediaTimeline(context.Background(), testChannelID)
	if err != nil || timeline.TotalCount != 1 {
		t.Fatalf("timeline=%+v err=%v", timeline, err)
	}
	page, err := svc.MediaPage(context.Background(), testChannelID, timeline.Anchors[0].Cursor, 128)
	if err != nil || len(page.Items) != 1 || page.Items[0].MsgID != 20 || page.Items[0].ContentMsgID != 20 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	location, err := svc.LocateMedia(context.Background(), testChannelID, 20, timeline.Generation)
	if err != nil || location.Index != 0 {
		t.Fatalf("locate=%+v err=%v", location, err)
	}
	neighbors, err := svc.MediaNeighbors(context.Background(), testChannelID, 20, 2, 2, timeline.Generation)
	if err != nil || len(neighbors.Items) != 1 {
		t.Fatalf("neighbors=%+v err=%v", neighbors, err)
	}
	if _, err := svc.MediaPage(context.Background(), testChannelID, "", 513); !errors.Is(err, projection.ErrInvalidGalleryLimit) {
		t.Fatalf("limit error=%v", err)
	}
}

func TestGalleryReadServiceRejectsMissingDBAndContext(t *testing.T) {
	svc := &Service{}
	if _, err := svc.MediaTimeline(context.Background(), testChannelID); err == nil {
		t.Fatal("missing DB accepted")
	}
	if _, err := svc.MediaPage(context.Background(), testChannelID, "", 128); err == nil {
		t.Fatal("missing DB accepted")
	}
	if _, err := svc.LocateMedia(context.Background(), testChannelID, 1, ""); err == nil {
		t.Fatal("missing DB accepted")
	}
	if _, err := svc.MediaNeighbors(context.Background(), testChannelID, 1, 1, 1, ""); err == nil {
		t.Fatal("missing DB accepted")
	}
	svc, _, _ = newTestService(t)
	if _, err := svc.MediaTimeline(nil, testChannelID); err == nil {
		t.Fatal("nil context accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.MediaTimeline(ctx, testChannelID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel err=%v", err)
	}
}
