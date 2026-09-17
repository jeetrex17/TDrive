package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"TDrive/backend/photobackup"
	folderservice "TDrive/backend/services/folder"
)

type fakePhotoBackupFolderStore struct {
	folders []photoBackupFolder
	creates int
	err     error
}

func (s *fakePhotoBackupFolderStore) find(_ context.Context, channelID int64, parentID, name string) (photoBackupFolder, bool, error) {
	if s.err != nil {
		return photoBackupFolder{}, false, s.err
	}
	for _, folder := range s.folders {
		if folder.ChannelID == channelID && folder.ParentID == parentID && folder.Name == name {
			return folder, true, nil
		}
	}
	return photoBackupFolder{}, false, nil
}

func (s *fakePhotoBackupFolderStore) create(ctx context.Context, channelID int64, parentID, name string) (photoBackupFolder, error) {
	if err := ctx.Err(); err != nil {
		return photoBackupFolder{}, err
	}
	if s.err != nil {
		return photoBackupFolder{}, s.err
	}
	s.creates++
	folder := photoBackupFolder{ChannelID: channelID, ID: fmt.Sprintf("d:%d-%d", channelID, s.creates), ParentID: parentID, Name: name}
	s.folders = append(s.folders, folder)
	return folder, nil
}

func TestResolvePhotoBackupDestinationBuildsAndReusesHierarchy(t *testing.T) {
	store := &fakePhotoBackupFolderStore{}
	resolver := newPhotoBackupDestinationResolver(store)

	first, err := resolver.resolve(context.Background(), 41, "d:selected", "Workstation", "Camera Roll")
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	second, err := resolver.resolve(context.Background(), 41, "d:selected", "Workstation", "Camera Roll")
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("destination changed: first=%q second=%q", first.ID, second.ID)
	}
	if store.creates != 3 {
		t.Fatalf("created %d folders, want one Photo backup/device/source hierarchy", store.creates)
	}
	if got := store.folders; got[0].Name != "Photo backup" || got[0].ParentID != "d:selected" || got[1].Name != "Workstation" || got[1].ParentID != got[0].ID || got[2].Name != "Camera Roll" || got[2].ParentID != got[1].ID {
		t.Fatalf("unexpected hierarchy: %#v", got)
	}
}

func TestPhotoBackupSettingsSavePreservesOmittedDestination(t *testing.T) {
	scope := photobackup.Scope{AccountID: "7", DriveID: 41}
	current := photobackup.Settings{Scope: scope, DestinationParentID: "d:selected", ManualPaused: true}
	got := photoBackupSettingsForSave(scope, current, PhotoBackupSettings{Enabled: true, Photos: true})
	if got.DestinationParentID != "d:selected" || !got.ManualPaused || !got.Enabled || !got.Photos {
		t.Fatalf("saved settings = %+v", got)
	}
	overridden := photoBackupSettingsForSave(scope, current, PhotoBackupSettings{DestinationParentID: "d:new"})
	if overridden.DestinationParentID != "d:new" {
		t.Fatalf("explicit destination = %q", overridden.DestinationParentID)
	}
}

func TestResolvePhotoBackupDestinationIsolatesDrives(t *testing.T) {
	store := &fakePhotoBackupFolderStore{}
	resolver := newPhotoBackupDestinationResolver(store)

	one, err := resolver.resolve(context.Background(), 41, "", "Phone", "Photos")
	if err != nil {
		t.Fatal(err)
	}
	two, err := resolver.resolve(context.Background(), 42, "", "Phone", "Photos")
	if err != nil {
		t.Fatal(err)
	}
	if one.ID == two.ID || store.creates != 6 {
		t.Fatalf("drive destinations were shared: one=%q two=%q creates=%d", one.ID, two.ID, store.creates)
	}
}

func TestResolvePhotoBackupDestinationHonorsCancellationAndErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := &fakePhotoBackupFolderStore{}
	if _, err := newPhotoBackupDestinationResolver(store).resolve(ctx, 41, "", "Phone", "Photos"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled resolve error = %v", err)
	}
	boom := errors.New("projection unavailable")
	store.err = boom
	if _, err := newPhotoBackupDestinationResolver(store).resolve(context.Background(), 41, "", "Phone", "Photos"); !errors.Is(err, boom) {
		t.Fatalf("store error = %v", err)
	}
}

func TestPhotoBackupFolderNameMakesPortableFallbacks(t *testing.T) {
	for _, tc := range []struct{ input, fallback, want string }{
		{"  Pixel 9  ", "Device", "Pixel 9"},
		{"Camera/Uploads", "Photos", "Camera_Uploads"},
		{"CON", "Device", "_CON"},
		{"...", "Photos", "Photos"},
	} {
		if got := photoBackupFolderName(tc.input, tc.fallback); got != tc.want {
			t.Errorf("photoBackupFolderName(%q, %q) = %q, want %q", tc.input, tc.fallback, got, tc.want)
		}
	}
}

func TestLoadOrCreatePhotoBackupDeviceNamePersistsUniqueLabel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "device-name")
	first, err := loadOrCreatePhotoBackupDeviceName(path, "localhost", bytes.NewReader([]byte{1, 2, 3, 4}))
	if err != nil {
		t.Fatal(err)
	}
	second, err := loadOrCreatePhotoBackupDeviceName(path, "renamed-host", bytes.NewReader([]byte{9, 9, 9, 9}))
	if err != nil {
		t.Fatal(err)
	}
	if first != "localhost (01020304)" || second != first {
		t.Fatalf("device names = %q then %q, want persisted unique label", first, second)
	}
}

func TestAppPhotoBackupFolderStoreUsesScopedLiveDirent(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE folders(channel_id INTEGER, id TEXT, name TEXT, parent_id TEXT, tombstoned INTEGER);
		CREATE TABLE dirents(channel_id INTEGER, object_id TEXT, object_kind TEXT, parent_id TEXT, display_name TEXT, name_key TEXT, tombstoned INTEGER);
		CREATE UNIQUE INDEX idx_dirents_live_sibling_name ON dirents(channel_id,parent_id,name_key) WHERE tombstoned=0;
		INSERT INTO folders VALUES(41,'d:phone','Phone','','0');
		INSERT INTO dirents VALUES(41,'d:phone','folder','','Phone','phone',0);
		INSERT INTO folders VALUES(42,'d:other','Phone','','0');
		INSERT INTO dirents VALUES(42,'d:other','folder','','Phone','phone',0);
		INSERT INTO folders VALUES(41,'d:deleted','Old','','1');
		INSERT INTO dirents VALUES(41,'d:deleted','folder','','Old','old',0);
	`); err != nil {
		t.Fatal(err)
	}
	store := appPhotoBackupFolderStore{service: &folderservice.Service{DB: db}}
	folder, found, err := store.find(context.Background(), 41, "", "PHONE")
	if err != nil || !found || folder.ID != "d:phone" {
		t.Fatalf("find scoped canonical folder = (%+v, %v, %v)", folder, found, err)
	}
	if _, found, err := store.find(context.Background(), 41, "", "Old"); err != nil || found {
		t.Fatalf("tombstoned folder found=%v err=%v", found, err)
	}
}
