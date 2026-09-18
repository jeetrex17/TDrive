package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"TDrive/backend/datadir"
	"TDrive/backend/photobackup"
	"TDrive/backend/projection"
	folderservice "TDrive/backend/services/folder"
)

const photoBackupRootFolderName = "Photo backup"

// photoBackupDestinationMu admits one destination resolution at a time across
// the process. Folder creation is remotely projected, so two uploads racing to
// create the same level would each publish it before either could see the other.
var photoBackupDestinationMu sync.Mutex

func photoBackupSettingsForSave(scope photobackup.Scope, current photobackup.Settings, value PhotoBackupSettings) photobackup.Settings {
	destinationParentID := strings.TrimSpace(value.DestinationParentID)
	if destinationParentID == "" {
		destinationParentID = current.DestinationParentID
	}
	return photobackup.Settings{
		Scope: scope, Enabled: value.Enabled, Photos: value.Photos, Videos: value.Videos,
		FutureOnly: value.FutureOnly, WiFiOnly: value.WiFiOnly, DestinationParentID: destinationParentID,
		Encrypt: value.Encrypt, ManualPaused: current.ManualPaused,
	}
}

type photoBackupFolder struct {
	ChannelID int64
	ID        string
	ParentID  string
	Name      string
}

type photoBackupFolderStore interface {
	find(context.Context, int64, string, string) (photoBackupFolder, bool, error)
	create(context.Context, int64, string, string) (photoBackupFolder, error)
}

// photoBackupDestinationResolver is built per upload and serialized by the
// package-level photoBackupDestinationMu, so it carries no lock of its own.
type photoBackupDestinationResolver struct {
	store photoBackupFolderStore
}

func newPhotoBackupDestinationResolver(store photoBackupFolderStore) *photoBackupDestinationResolver {
	return &photoBackupDestinationResolver{store: store}
}

// photoBackupRelativeDir is the folder chain to recreate under a source, or ""
// when there is none to mirror. Native library assets carry no path: an album
// is a flat set of resources, not a tree, so only watched folders nest.
//
// The result is derived from the file's own location rather than its ledger id
// so that it means the same thing however the id was formed, and it is empty
// unless the file really sits inside the root -- validatePhotoBackupPath
// enforces that too, and a destination must never be built from a path that
// escaped it.
func photoBackupRelativeDir(source photobackup.Source, asset photobackup.Asset) string {
	if asset.Path == "" || source.Root == "" {
		return ""
	}
	root, err := filepath.Abs(source.Root)
	if err != nil {
		return ""
	}
	dir, err := filepath.Abs(filepath.Dir(asset.Path))
	if err != nil {
		return ""
	}
	relative, err := filepath.Rel(root, dir)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(relative)
}

// resolve returns the folder an upload belongs in, creating what is missing.
//
// The first three levels are fixed -- Photo backup / <device> / <source> -- and
// relativeDir mirrors the file's own folders beneath them, so a watched tree
// arrives in the drive shaped the way it sits on disk. Without it every file in
// the tree landed in one folder, where two photos named the same in different
// subfolders collided.
//
// Each level costs one indexed lookup, and a remote folder creation only the
// first time a folder is seen. Deliberately uncached: the saving over an upload
// that takes seconds is noise, and a cached id survives the folder being
// deleted, which would send files to a tombstone.
func (r *photoBackupDestinationResolver) resolve(ctx context.Context, channelID int64, selectedParentID, deviceName, sourceName, relativeDir string) (photoBackupFolder, error) {
	if err := ctx.Err(); err != nil {
		return photoBackupFolder{}, err
	}
	if r == nil || r.store == nil || channelID <= 0 {
		return photoBackupFolder{}, fmt.Errorf("photo backup: invalid destination")
	}
	parentID := strings.TrimSpace(selectedParentID)
	names := []string{
		photoBackupRootFolderName,
		photoBackupFolderName(deviceName, photoBackupDeviceFallback()),
		photoBackupFolderName(sourceName, "Photos"),
	}
	for _, component := range strings.Split(relativeDir, "/") {
		if component == "" || component == "." {
			continue
		}
		// A component is sanitized the same way every other level is, so a
		// folder the local filesystem allows but the namespace does not still
		// lands somewhere predictable instead of failing the upload.
		names = append(names, photoBackupFolderName(component, "Folder"))
	}
	var folder photoBackupFolder
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return photoBackupFolder{}, err
		}
		var err error
		folder, err = r.ensure(ctx, channelID, parentID, name)
		if err != nil {
			return photoBackupFolder{}, err
		}
		parentID = folder.ID
	}
	return folder, nil
}

func (r *photoBackupDestinationResolver) ensure(ctx context.Context, channelID int64, parentID, name string) (photoBackupFolder, error) {
	if folder, found, err := r.store.find(ctx, channelID, parentID, name); err != nil || found {
		return folder, err
	}
	folder, err := r.store.create(ctx, channelID, parentID, name)
	if err == nil {
		return folder, nil
	}
	// Folder creation is remotely projected. If another upload won the race,
	// resolve the now-visible sibling rather than creating a suffixed duplicate.
	if existing, found, findErr := r.store.find(ctx, channelID, parentID, name); findErr != nil {
		return photoBackupFolder{}, errors.Join(err, findErr)
	} else if found {
		return existing, nil
	}
	return photoBackupFolder{}, err
}

type appPhotoBackupFolderStore struct{ service *folderservice.Service }

func (s appPhotoBackupFolderStore) find(ctx context.Context, channelID int64, parentID, name string) (photoBackupFolder, bool, error) {
	if err := ctx.Err(); err != nil {
		return photoBackupFolder{}, false, err
	}
	if s.service == nil || s.service.DB == nil {
		return photoBackupFolder{}, false, fmt.Errorf("photo backup: folder store unavailable")
	}
	parentID = strings.TrimSpace(parentID)
	wantedKey, err := projection.CanonicalNameKey(name)
	if err != nil {
		return photoBackupFolder{}, false, err
	}
	var id, displayName string
	err = s.service.DB.QueryRowContext(ctx, `
		SELECT d.object_id, d.display_name
		FROM dirents d
		JOIN folders f ON f.channel_id=d.channel_id AND f.id=d.object_id
		WHERE d.channel_id=? AND d.parent_id=? AND d.name_key=?
		  AND d.object_kind='folder' AND d.tombstoned=0 AND f.tombstoned=0
		LIMIT 1`, channelID, parentID, wantedKey).Scan(&id, &displayName)
	if errors.Is(err, sql.ErrNoRows) {
		return photoBackupFolder{}, false, nil
	}
	if err != nil {
		return photoBackupFolder{}, false, err
	}
	return photoBackupFolder{ChannelID: channelID, ID: id, ParentID: parentID, Name: displayName}, true, nil
}

func (s appPhotoBackupFolderStore) create(ctx context.Context, channelID int64, parentID, name string) (photoBackupFolder, error) {
	folder, err := s.service.CreateContext(ctx, channelID, name, parentID)
	if err != nil {
		return photoBackupFolder{}, fmt.Errorf("photo backup: create destination folder %q: %w", name, err)
	}
	return photoBackupFolder{ChannelID: channelID, ID: folder.ID, ParentID: folder.ParentID, Name: folder.Name}, nil
}

func (a *App) resolvePhotoBackupUploadParent(ctx context.Context, request photobackup.UploadRequest) (string, error) {
	if request.ChannelID <= 0 || request.ChannelID != request.Scope.DriveID {
		return "", fmt.Errorf("photo backup: destination drive mismatch")
	}
	service, err := a.requireFolderService()
	if err != nil {
		return "", err
	}
	sourceName := request.Source.Name
	if strings.TrimSpace(sourceName) == "" {
		sourceName = request.Source.Root
		if index := strings.LastIndexAny(sourceName, `/\\`); index >= 0 {
			sourceName = sourceName[index+1:]
		}
	}
	photoBackupDestinationMu.Lock()
	defer photoBackupDestinationMu.Unlock()
	deviceName, err := photoBackupDeviceName()
	if err != nil {
		return "", err
	}
	resolver := newPhotoBackupDestinationResolver(appPhotoBackupFolderStore{service: service})
	folder, err := resolver.resolve(ctx, request.ChannelID, request.ParentID, deviceName, sourceName, photoBackupRelativeDir(request.Source, request.Asset))
	if err != nil {
		return "", err
	}
	return folder.ID, nil
}

func populatePhotoBackupDestination(state *PhotoBackupState, sources []photobackup.Source) error {
	if state == nil {
		return fmt.Errorf("photo backup: destination state unavailable")
	}
	deviceName, err := photoBackupDeviceName()
	if err != nil {
		return err
	}
	sourceName := "Source folder"
	if len(sources) == 1 {
		sourceName = sources[0].Name
		if strings.TrimSpace(sourceName) == "" {
			sourceName = sources[0].Root
			if index := strings.LastIndexAny(sourceName, `/\\`); index >= 0 {
				sourceName = sourceName[index+1:]
			}
		}
		sourceName = photoBackupFolderName(sourceName, "Photos")
	}
	state.Destination.Title = strings.Join([]string{photoBackupRootFolderName, deviceName, sourceName}, " / ")
	return nil
}

func photoBackupDeviceName() (string, error) {
	name, err := os.Hostname()
	name = strings.TrimSpace(name)
	if err != nil || name == "" || strings.EqualFold(name, "localhost") || strings.HasPrefix(strings.ToLower(name), "localhost.") {
		name = photoBackupDeviceFallback()
	}
	dir, err := datadir.Dir()
	if err != nil {
		return "", fmt.Errorf("photo backup: device identity directory: %w", err)
	}
	return loadOrCreatePhotoBackupDeviceName(filepath.Join(dir, "photo-backup-device-name"), name, rand.Reader)
}

func loadOrCreatePhotoBackupDeviceName(path, nativeName string, randomSource io.Reader) (string, error) {
	if stored, err := os.ReadFile(path); err == nil {
		if name := strings.TrimSpace(string(stored)); name != "" {
			if _, validateErr := projection.CanonicalNameKey(name); validateErr == nil {
				return name, nil
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("photo backup: read device identity: %w", err)
	}
	id := make([]byte, 4)
	if _, err := io.ReadFull(randomSource, id); err != nil {
		return "", fmt.Errorf("photo backup: create device identity: %w", err)
	}
	name := photoBackupFolderName(nativeName, photoBackupDeviceFallback()) + " (" + hex.EncodeToString(id) + ")"
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		stored, readErr := os.ReadFile(path)
		if readErr != nil {
			return "", fmt.Errorf("photo backup: read concurrent device identity: %w", readErr)
		}
		return strings.TrimSpace(string(stored)), nil
	}
	if err != nil {
		return "", fmt.Errorf("photo backup: persist device identity: %w", err)
	}
	if _, err = file.WriteString(name + "\n"); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("photo backup: persist device identity: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("photo backup: persist device identity: %w", err)
	}
	return name, nil
}

func photoBackupDeviceFallback() string {
	switch runtime.GOOS {
	case "android":
		return "Android device"
	case "ios":
		return "iPhone or iPad"
	case "darwin":
		return "Mac"
	case "windows":
		return "Windows PC"
	default:
		return "Device"
	}
}

func photoBackupFolderName(value, fallback string) string {
	value = strings.TrimSpace(strings.ToValidUTF8(value, "_"))
	var result strings.Builder
	for _, char := range value {
		if char < 0x20 || unicode.IsControl(char) || unicode.Is(unicode.Bidi_Control, char) || strings.ContainsRune(`<>:"/\|?*`, char) {
			result.WriteRune('_')
		} else {
			result.WriteRune(char)
		}
	}
	name := strings.Trim(result.String(), " .")
	if name == "" {
		name = fallback
	}
	for len([]byte(name)) > 200 {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	if _, err := projection.CanonicalNameKey(name); err != nil {
		name = "_" + name
	}
	if _, err := projection.CanonicalNameKey(name); err != nil {
		return fallback
	}
	return name
}
