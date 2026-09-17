package main

import (
	"errors"
	"testing"
)

func TestGalleryPreparationBoundaryEstimatesActiveDrive(t *testing.T) {
	app, db := setupEncryptionApp(t)
	if _, err := db.Exec(`INSERT INTO files(channel_id,msg_id,name,size,parent_id,upload_time) VALUES(?,9,'legacy.jpg',100,'',1700000000)`, testEncryptionChannelID); err != nil {
		t.Fatal(err)
	}
	state, err := app.GetGalleryPreparation()
	if err != nil || state.Running || state.ChannelID != testEncryptionChannelID || state.Total != 1 || state.BytesTotal != 100 {
		t.Fatalf("state=%+v error=%v", state, err)
	}
	if result := app.StartGalleryPreparation(testEncryptionChannelID + 1); result.OK || result.Err() == nil {
		t.Fatal("other drive accepted")
	}
	if result := app.StopGalleryPreparation(); !result.OK {
		t.Fatalf("stop=%+v", result)
	}
}

func TestGalleryPreparationBoundaryRejectsUnavailableBackend(t *testing.T) {
	app := &App{}
	if _, err := app.GetGalleryPreparation(); !errors.Is(err, errBackendUnavailable) {
		t.Fatalf("estimate error=%v", err)
	}
	if result := app.StartGalleryPreparation(0); result.OK {
		t.Fatal("zero drive accepted")
	}
	if result := app.StopGalleryPreparation(); !result.OK {
		t.Fatalf("empty stop=%+v", result)
	}
}

func TestGalleryPreparationPromptsForLockedVaultBeforeStarting(t *testing.T) {
	app, db := setupEncryptionApp(t)
	if _, err := db.Exec(`INSERT INTO files(channel_id,msg_id,name,size,parent_id,upload_time,encrypted,plaintext_size) VALUES(?,9,'private.jpg',100,'',1700000000,1,80)`, testEncryptionChannelID); err != nil {
		t.Fatal(err)
	}
	result := app.StartGalleryPreparation(testEncryptionChannelID)
	if result.OK || result.Error == nil || result.Error.Code != OperationCodeEncryptionPasswordRequired {
		t.Fatalf("locked result=%+v", result)
	}
	if runner := app.galleryPreparationRunner(false); runner != nil && runner.Snapshot().Running {
		t.Fatal("locked preflight started a worker")
	}
}

func TestGalleryPreparationStopDuringPreflightPreventsLateStart(t *testing.T) {
	app, db := setupEncryptionApp(t)
	if _, err := db.Exec(`INSERT INTO files(channel_id,msg_id,name,size,parent_id,upload_time,encrypted,plaintext_size) VALUES(?,9,'private.jpg',100,'',1700000000,1,80)`, testEncryptionChannelID); err != nil {
		t.Fatal(err)
	}
	svc, err := app.requireFileService()
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	svc.RequireEncryptionKey = func(bool) ([]byte, error) { close(entered); <-release; return make([]byte, 32), nil }
	result := make(chan OperationResult, 1)
	go func() { result <- app.StartGalleryPreparation(testEncryptionChannelID) }()
	<-entered
	app.stopGalleryPreparation()
	close(release)
	stopped := <-result
	if stopped.OK || stopped.Error == nil || stopped.Error.Code != OperationCodeCanceled {
		t.Fatalf("preflight stop=%+v", stopped)
	}
	if runner := app.galleryPreparationRunner(false); runner != nil && runner.Snapshot().Running {
		t.Fatal("canceled preflight started a worker")
	}
}

func TestGalleryPreparationCannotStartAfterTerminalLifecycle(t *testing.T) {
	app, _ := setupEncryptionApp(t)
	app.mountLifecycleTerminal = true
	result := app.StartGalleryPreparation(testEncryptionChannelID)
	if result.OK || !errors.Is(result.Err(), errAppMountLifecycleTerminal) {
		t.Fatalf("terminal start=%+v", result)
	}
	if runner := app.galleryPreparationRunner(false); runner != nil && runner.Snapshot().Running {
		t.Fatal("terminal lifecycle started a worker")
	}
}
