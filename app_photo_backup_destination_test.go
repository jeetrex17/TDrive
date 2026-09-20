package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
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

	first, err := resolver.resolve(context.Background(), 41, "d:selected", "Workstation", "Camera Roll", "")
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	second, err := resolver.resolve(context.Background(), 41, "d:selected", "Workstation", "Camera Roll", "")
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

func TestResolvePhotoBackupDestinationMirrorsNestedFolders(t *testing.T) {
	store := &fakePhotoBackupFolderStore{}
	resolver := newPhotoBackupDestinationResolver(store)
	ctx := context.Background()

	nested, err := resolver.resolve(ctx, 41, "", "Workstation", "Pictures", "Summer/Beach")
	if err != nil {
		t.Fatalf("nested resolve: %v", err)
	}
	if store.creates != 5 {
		t.Fatalf("created %d folders, want Photo backup/device/source/Summer/Beach", store.creates)
	}
	names := make([]string, 0, len(store.folders))
	for _, folder := range store.folders {
		names = append(names, folder.Name)
	}
	if got := strings.Join(names, "/"); got != "Photo backup/Workstation/Pictures/Summer/Beach" {
		t.Fatalf("hierarchy = %q", got)
	}
	if store.folders[4].ParentID != store.folders[3].ID || store.folders[3].ParentID != store.folders[2].ID {
		t.Fatalf("subfolders were not chained: %#v", store.folders)
	}

	// A sibling deeper in the same tree reuses everything it shares, and a file
	// at the source root still belongs to the source folder itself.
	sibling, err := resolver.resolve(ctx, 41, "", "Workstation", "Pictures", "Summer/Beach/Day 2")
	if err != nil {
		t.Fatalf("sibling resolve: %v", err)
	}
	if store.creates != 6 || sibling.ParentID != nested.ID {
		t.Fatalf("deeper sibling creates=%d parent=%q", store.creates, sibling.ParentID)
	}
	root, err := resolver.resolve(ctx, 41, "", "Workstation", "Pictures", "")
	if err != nil {
		t.Fatalf("root resolve: %v", err)
	}
	if store.creates != 6 || root.ID != store.folders[2].ID {
		t.Fatalf("root file creates=%d id=%q", store.creates, root.ID)
	}
}

func TestResolvePhotoBackupDestinationSanitizesEveryMirroredLevel(t *testing.T) {
	store := &fakePhotoBackupFolderStore{}
	// Empty and dot components are skipped rather than becoming folders, and a
	// name the namespace rejects falls back instead of failing the upload.
	if _, err := newPhotoBackupDestinationResolver(store).resolve(context.Background(), 41, "", "PC", "Pics", "a//./b:c/ "); err != nil {
		t.Fatal(err)
	}
	var mirrored []string
	for _, folder := range store.folders[3:] {
		mirrored = append(mirrored, folder.Name)
	}
	if got := strings.Join(mirrored, "|"); got != "a|b_c|Folder" {
		t.Fatalf("mirrored levels = %q", got)
	}
}

func TestPhotoBackupRelativeDirOnlyMirrorsContainedFolderSources(t *testing.T) {
	root := t.TempDir()
	source := photobackup.Source{Root: root}
	for _, tc := range []struct{ name, path, want string }{
		{"nested file", filepath.Join(root, "Summer", "Beach", "a.jpg"), "Summer/Beach"},
		{"file at the root", filepath.Join(root, "a.jpg"), ""},
		{"file outside the root", filepath.Join(filepath.Dir(root), "elsewhere", "a.jpg"), ""},
	} {
		if got := photoBackupRelativeDir(source, photobackup.Asset{Path: tc.path}); got != tc.want {
			t.Fatalf("%s = %q, want %q", tc.name, got, tc.want)
		}
	}
	// A photo-library resource has no path and no tree to mirror.
	if got := photoBackupRelativeDir(photobackup.Source{Root: "library"}, photobackup.Asset{ResourceID: "media:image:9"}); got != "" {
		t.Fatalf("native asset = %q", got)
	}
	// A watched folder on a phone has no path either -- its bytes are staged in
	// the app's cache -- so the host's own answer is what nests it.
	folder := photobackup.Source{Kind: photoBackupDeviceFolderKind, Root: "external_primary:DCIM/Camera/"}
	for _, tc := range []struct{ name, relDir, want string }{
		{"nested", "Trips/Rome", "Trips/Rome"},
		{"at the top", "", ""},
		{"traversal is dropped, not obeyed", "../../etc/Trips", "etc/Trips"},
		{"empty components collapse", "Trips//Rome/", "Trips/Rome"},
	} {
		if got := photoBackupRelativeDir(folder, photobackup.Asset{ResourceID: "media:image:9", RelDir: tc.relDir}); got != tc.want {
			t.Fatalf("%s = %q, want %q", tc.name, got, tc.want)
		}
	}
	deep := strings.Repeat("a/", photoBackupMaxRelDirDepth+4)
	if got := photoBackupRelativeDir(folder, photobackup.Asset{ResourceID: "media:image:9", RelDir: deep}); strings.Count(got, "/")+1 != photoBackupMaxRelDirDepth {
		t.Fatalf("deep chain = %q", got)
	}
}

func TestPhotoBackupDeviceFolderRootIsNormalizedAndChecked(t *testing.T) {
	for _, tc := range []struct{ name, root, want string }{
		{"plain", "external_primary:DCIM/Camera", "external_primary:DCIM/Camera/"},
		{"already terminated", "external_primary:DCIM/Camera/", "external_primary:DCIM/Camera/"},
		{"traversal removed", "external_primary:DCIM/../Camera", "external_primary:DCIM/Camera/"},
		{"card volume", "1aef-2b03:Pictures", "1aef-2b03:Pictures/"},
		// iOS has no media index: a picked folder is a place in Files, and
		// the leading slash of its path is a component like any other.
		{"ios files folder", "files:/private/var/mobile/Library/Mobile Documents/com~apple~CloudDocs/Camera", "files:private/var/mobile/Library/Mobile Documents/com~apple~CloudDocs/Camera/"},
	} {
		got, err := validatePhotoBackupDeviceFolderRoot(tc.root)
		if err != nil || got != tc.want {
			t.Fatalf("%s = %q err=%v, want %q", tc.name, got, err, tc.want)
		}
	}
	for _, tc := range []struct{ name, root string }{
		{"no volume", "DCIM/Camera"},
		{"whole volume", "external_primary:"},
		{"hostile volume", "../../secrets:DCIM"},
		{"empty", ""},
	} {
		if _, err := validatePhotoBackupDeviceFolderRoot(tc.root); err == nil {
			t.Fatalf("%s was accepted", tc.name)
		}
	}
}

// Overlapping folders are refused: the ledger would dedupe the uploads, but
// the file's place in the drive would then depend on which source scanned it
// first, which is not something a user can predict or find.
func TestPhotoBackupOverlappingDeviceFolderIsNamed(t *testing.T) {
	existing := []photobackup.Source{
		{ID: "tree:external_primary:DCIM/", Kind: photoBackupDeviceFolderKind, Root: "external_primary:DCIM/", Name: "DCIM"},
		{ID: "bucket:9", Kind: "album", Root: "content://media", Name: "Camera"},
	}
	if got := photoBackupOverlappingDeviceFolder(existing, "tree:external_primary:DCIM/Camera/", "external_primary:DCIM/Camera/"); got != "DCIM" {
		t.Fatalf("contained folder = %q", got)
	}
	if got := photoBackupOverlappingDeviceFolder(existing, "tree:external_primary:Pictures/", "external_primary:Pictures/"); got != "" {
		t.Fatalf("unrelated folder = %q", got)
	}
	// Re-picking the same folder updates it rather than clashing with itself.
	if got := photoBackupOverlappingDeviceFolder(existing, "tree:external_primary:DCIM/", "external_primary:DCIM/"); got != "" {
		t.Fatalf("same folder = %q", got)
	}
	// A different volume is a different place, even with the same path.
	if got := photoBackupOverlappingDeviceFolder(existing, "tree:1aef-2b03:DCIM/", "1aef-2b03:DCIM/"); got != "" {
		t.Fatalf("other volume = %q", got)
	}
}

func TestPhotoBackupSettingsSavePreservesOmittedDestination(t *testing.T) {
	scope := photobackup.Scope{AccountID: "7", DriveID: 41}
	current := photobackup.Settings{Scope: scope, DestinationParentID: "d:selected", ManualPaused: true}
	got := photoBackupSettingsForSave(scope, current, PhotoBackupSettings{Enabled: true, Photos: true})
	if got.DestinationParentID != "d:selected" || !got.ManualPaused || !got.Enabled || !got.Photos || !got.Encrypt {
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

	one, err := resolver.resolve(context.Background(), 41, "", "Phone", "Photos", "")
	if err != nil {
		t.Fatal(err)
	}
	two, err := resolver.resolve(context.Background(), 42, "", "Phone", "Photos", "")
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
	if _, err := newPhotoBackupDestinationResolver(store).resolve(ctx, 41, "", "Phone", "Photos", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled resolve error = %v", err)
	}
	boom := errors.New("projection unavailable")
	store.err = boom
	if _, err := newPhotoBackupDestinationResolver(store).resolve(context.Background(), 41, "", "Phone", "Photos", ""); !errors.Is(err, boom) {
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
