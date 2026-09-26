package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"TDrive/backend/datadir"
	"TDrive/backend/photobackup"
	_ "modernc.org/sqlite"
)

func TestPhotoBackupScopeDoesNotQueryTelegramBeforeDriveSelection(t *testing.T) {
	app, _, telegram := setupEncryptionAppWithPolicyRefresh(t, nil)
	t.Cleanup(app.engine.Close)
	app.engine.SetActiveChannelID(0)
	telegram.SetSelfID(0)

	if _, err := app.photoBackupScope(context.Background()); err == nil || err.Error() != "photo backup: drive unavailable" {
		t.Fatalf("scope before drive selection error = %v, want drive unavailable", err)
	}

	app.engine.SetActiveChannelID(testEncryptionChannelID)
	if _, err := app.photoBackupScope(context.Background()); err == nil || err.Error() != "photo backup: account unavailable" {
		t.Fatalf("scope with selected drive error = %v, want account unavailable", err)
	}
}

func TestPhotoBackupAcceptsAndroidNamedStage(t *testing.T) {
	datadir.SetCache(t.TempDir())
	t.Cleanup(func() { datadir.SetCache("") })
	cache, err := datadir.CacheDir()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(cache, "photo-backup-stage", "image-42")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "IMG_0042.jpg")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateNativePhotoBackupPath(path); err != nil {
		t.Fatalf("native Android staging rejected: %v", err)
	}
}

func TestValidatePhotoBackupPathRequiresRegularExactSourceFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "photo.jpg")
	if err := os.WriteFile(path, []byte("photo"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := photobackup.Source{Root: root}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	asset := photobackup.Asset{Path: path, Size: 5, Version: fmt.Sprintf("%d:%d", info.ModTime().UnixNano(), info.Size())}
	if err := validatePhotoBackupPath(path, asset, source); err != nil {
		t.Fatalf("valid path: %v", err)
	}
	asset.Size = 6
	if err := validatePhotoBackupPath(path, asset, source); err == nil || !strings.Contains(err.Error(), "size changed") {
		t.Fatalf("size error=%v", err)
	}
	outside := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(outside, []byte("photo"), 0o600); err != nil {
		t.Fatal(err)
	}
	asset.Path, asset.Size = outside, 5
	outsideInfo, err := os.Stat(outside)
	if err != nil {
		t.Fatal(err)
	}
	asset.Version = fmt.Sprintf("%d:%d", outsideInfo.ModTime().UnixNano(), outsideInfo.Size())
	if err := validatePhotoBackupPath(outside, asset, source); err == nil || !strings.Contains(err.Error(), "escaped") {
		t.Fatalf("escape error=%v", err)
	}
}

func TestStagePhotoBackupFileCreatesPrivateNamedSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "holiday.jpg")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	asset := photobackup.Asset{Name: "holiday.jpg", Version: fmt.Sprintf("%d:%d", info.ModTime().UnixNano(), info.Size()), Size: info.Size()}
	staged, cleanup, err := stagePhotoBackupFile(context.Background(), path, asset)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if filepath.Base(staged) != "holiday.jpg" || filepath.Dir(staged) == filepath.Dir(path) {
		t.Fatalf("staged=%q", staged)
	}
	if err := os.WriteFile(path, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(staged)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "original" {
		t.Fatalf("snapshot=%q", content)
	}
}

func TestStagePhotoBackupFileRejectsOversizedSparseResource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversized.mov")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxPhotoBackupResourceBytes + 1); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	asset := photobackup.Asset{Name: "oversized.mov", Version: fmt.Sprintf("%d:%d", info.ModTime().UnixNano(), info.Size()), Size: info.Size()}
	if _, _, err := stagePhotoBackupFile(context.Background(), path, asset); err == nil || !strings.Contains(err.Error(), "4 GiB") {
		t.Fatalf("error=%v", err)
	}
}

func TestPhotoBackupPolicyFailsClosedAndExpires(t *testing.T) {
	app := &App{}
	if err := app.photoBackupPolicyAllows(photobackup.Settings{}); err != nil {
		t.Fatalf("unrestricted policy: %v", err)
	}
	settings := photobackup.Settings{WiFiOnly: true}
	if err := app.photoBackupPolicyAllows(settings); err == nil || !errors.Is(err, errPhotoBackupPolicyUnavailable) {
		t.Fatalf("missing policy error=%v", err)
	}
	app.SetPhotoBackupPolicy(PhotoBackupPolicy{WiFi: true, ObservedAt: time.Now().Add(-photoBackupPolicyTTL - time.Second).UnixMilli()})
	if err := app.photoBackupPolicyAllows(settings); err == nil || !errors.Is(err, errPhotoBackupPolicyUnavailable) {
		t.Fatalf("expired policy error=%v", err)
	}
	app.SetPhotoBackupPolicy(PhotoBackupPolicy{ObservedAt: time.Now().UnixMilli()})
	if err := app.photoBackupPolicyAllows(settings); err == nil || !errors.Is(err, errPhotoBackupWaitingForWiFi) {
		t.Fatalf("Wi-Fi error=%v", err)
	}
	app.SetPhotoBackupPolicy(PhotoBackupPolicy{WiFi: true, ObservedAt: time.Now().UnixMilli()})
	if err := app.photoBackupPolicyAllows(settings); err != nil {
		t.Fatalf("allowed policy: %v", err)
	}
}

func TestPhotoBackupStateUsesStableWirePhases(t *testing.T) {
	settings := photobackup.Settings{Enabled: true, Photos: true, DestinationParentID: "d:photos"}
	sources := []photobackup.Source{{ID: "camera", Kind: "library", Root: "Camera", Enabled: true}}
	state := photoBackupState(settings, sources, photobackup.Status{Pending: 2}, false, false)
	if state.Status.Phase != "queued" || state.Status.Pending != 2 || state.Destination.ID != "d:photos" || len(state.Sources) != 1 || !state.Settings.Encrypt {
		t.Fatalf("state=%+v", state)
	}
	state = photoBackupState(settings, sources, photobackup.Status{Error: 1, LastError: "offline"}, false, true)
	if state.Status.Phase != "paused" || state.Status.Message != "Paused by you." {
		t.Fatalf("paused state=%+v", state.Status)
	}
	state = photoBackupState(settings, sources, photobackup.Status{Pending: 1, Paused: 1, LastError: "upload interrupted; remote outcome unknown"}, true, false)
	if state.Status.Phase != "uploading" || state.Status.Paused != 1 {
		t.Fatalf("running with interrupted item state=%+v", state.Status)
	}
}

func TestResolvePhotoBackupResourceRejectsUnknownAndUntrustedPaths(t *testing.T) {
	app := &App{photoBackupWaiters: make(map[string]chan photoBackupMaterialization)}
	if err := app.ResolvePhotoBackupResource("missing", "/tmp/photo.jpg", ""); err == nil {
		t.Fatal("unknown token accepted")
	}
	wait := make(chan photoBackupMaterialization, 1)
	app.photoBackupWaiters["known"] = wait
	if err := app.ResolvePhotoBackupResource("known", "/tmp/photo.jpg", ""); err != nil {
		t.Fatal(err)
	}
	result := <-wait
	if !strings.Contains(result.err, "outside native staging") {
		t.Fatalf("result=%+v", result)
	}
	if len(app.photoBackupWaiters) != 0 {
		t.Fatal("resolved token retained")
	}
}

func TestDesktopDiscoveryRetainsCursorReconcilesAndRestarts(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "backup.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	engine, err := photobackup.Open(db, photobackup.Options{PageSize: 128})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	scope := photobackup.Scope{AccountID: "actor", DriveID: 42}
	if err := engine.PutSettings(ctx, photobackup.Settings{Scope: scope, Enabled: true, Photos: true}); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for index := 0; index < 300; index++ {
		path := filepath.Join(root, fmt.Sprintf("%03d.jpg", index))
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	source := photobackup.Source{Scope: scope, ID: "folder", Kind: "folder", Root: root, Enabled: true}
	if err := engine.UpsertSource(ctx, source); err != nil {
		t.Fatal(err)
	}
	app := &App{photoBackupAdapters: make(map[string]*photobackup.LocalFolderAdapter)}
	discoverAll := func() {
		for attempts := 0; attempts < 10 && app.discoverDesktopSources(ctx, engine, scope, []photobackup.Source{source}, ""); attempts++ {
		}
	}
	discoverAll()
	status, err := engine.Status(ctx, scope)
	if err != nil || status.Pending != 300 {
		t.Fatalf("initial status=%+v err=%v", status, err)
	}
	if err := os.WriteFile(filepath.Join(root, "new.jpg"), []byte("y"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := engine.ResetDiscovery(ctx, scope, source.ID); err != nil {
		t.Fatal(err)
	}
	app.photoBackupAdapters = make(map[string]*photobackup.LocalFolderAdapter)
	discoverAll()
	status, err = engine.Status(ctx, scope)
	if err != nil || status.Pending != 301 {
		t.Fatalf("reconciled status=%+v err=%v", status, err)
	}
	if err := engine.ResetDiscovery(ctx, scope, source.ID); err != nil {
		t.Fatal(err)
	}
	if !app.discoverDesktopSources(ctx, engine, scope, []photobackup.Source{source}, "") {
		t.Fatal("first restart page reported complete")
	}
	app.photoBackupAdapters = make(map[string]*photobackup.LocalFolderAdapter)
	discoverAll()
	status, err = engine.Status(ctx, scope)
	if err != nil || status.Pending != 301 {
		t.Fatalf("restart status=%+v err=%v", status, err)
	}
}
