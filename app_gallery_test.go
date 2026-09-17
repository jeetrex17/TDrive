package main

import (
	"encoding/json"
	"errors"
	"testing"

	"TDrive/backend/projection"
)

func TestGalleryAppBoundaryPreservesWireContract(t *testing.T) {
	app, db := setupEncryptionApp(t)
	if _, err := db.Exec(`INSERT INTO files(channel_id,msg_id,name,size,parent_id,upload_time,content_msg_id,content_hash,revision) VALUES(?,9,'photo.jpg',100,'',1700000000,99,'content-hash',3)`, testEncryptionChannelID); err != nil {
		t.Fatal(err)
	}
	timeline, err := app.GetMediaTimeline()
	if err != nil || timeline.TotalCount != 1 {
		t.Fatalf("timeline=%+v error=%v", timeline, err)
	}
	page, err := app.ListMediaPage(timeline.Anchors[0].Cursor, 128)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("page=%+v error=%v", page, err)
	}
	payload, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	item := decoded["items"].([]any)[0].(map[string]any)
	if item["msg_id"] != float64(9) || item["content_msg_id"] != float64(99) || item["revision"] != float64(3) || item["content_hash"] != "content-hash" {
		t.Fatalf("wire=%s", payload)
	}
	location, err := app.LocateMedia(9, timeline.Generation)
	if err != nil || location.Index != 0 {
		t.Fatalf("location=%+v err=%v", location, err)
	}
	neighbors, err := app.GetMediaNeighbors(9, 1, 1, timeline.Generation)
	if err != nil || len(neighbors.Items) != 1 {
		t.Fatalf("neighbors=%+v err=%v", neighbors, err)
	}
	if _, err := db.Exec(`UPDATE files SET revision=4 WHERE msg_id=9`); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ListMediaPage(timeline.Anchors[0].Cursor, 128); !errors.Is(err, projection.ErrGalleryStale) {
		t.Fatalf("stale=%v", err)
	}
}

func TestGalleryAppBoundaryRejectsUnavailableBackend(t *testing.T) {
	app := &App{}
	if _, err := app.GetMediaTimeline(); !errors.Is(err, errBackendUnavailable) {
		t.Fatalf("timeline error=%v", err)
	}
	if _, err := app.ListMediaPage("", 128); !errors.Is(err, errBackendUnavailable) {
		t.Fatalf("page error=%v", err)
	}
	if _, err := app.LocateMedia(1, ""); !errors.Is(err, errBackendUnavailable) {
		t.Fatalf("locate error=%v", err)
	}
	if _, err := app.GetMediaNeighbors(1, 1, 1, ""); !errors.Is(err, errBackendUnavailable) {
		t.Fatalf("neighbors error=%v", err)
	}
}
