package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"TDrive/backend"
	"TDrive/backend/datadir"
	"TDrive/backend/photobackup"

	_ "modernc.org/sqlite"
)

const photoBackupPolicyTTL = 2 * time.Minute

// These reach the panel word for word, both as the status line and as the
// message inside a failed operation envelope, so they are written for the
// user rather than the log.
var (
	errPhotoBackupPaused            = errors.New("Backup is paused. Resume it to continue.")
	errPhotoBackupPolicyUnavailable = errors.New("Waiting for a connectivity update from the device.")
	errPhotoBackupWaitingForWiFi    = errors.New("Waiting for Wi-Fi.")
)

type PhotoBackupSettings struct {
	Enabled             bool   `json:"enabled"`
	Photos              bool   `json:"photos"`
	Videos              bool   `json:"videos"`
	FutureOnly          bool   `json:"future_only"`
	WiFiOnly            bool   `json:"wifi_only"`
	DestinationParentID string `json:"destination_parent_id"`
	Encrypt             bool   `json:"encrypt"`
}

type PhotoBackupSource struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Root    string `json:"root"`
	Enabled bool   `json:"enabled"`
	AddedAt int64  `json:"added_at"`
}

type PhotoBackupCapability struct {
	Supported bool   `json:"supported"`
	Label     string `json:"label"`
	Detail    string `json:"detail"`
}

type PhotoBackupCapabilities struct {
	WiFiOnly PhotoBackupCapability `json:"wifi_only"`
	Access   struct {
		Status string `json:"status"`
		Detail string `json:"detail"`
	} `json:"access"`
}

type PhotoBackupStatus struct {
	CurrentFile           string  `json:"current_file"`
	CurrentFileBytesDone  int64   `json:"current_file_bytes_done"`
	CurrentFileBytesTotal int64   `json:"current_file_bytes_total"`
	CurrentFilePercent    float64 `json:"current_file_percent"`
	Phase                 string  `json:"phase"`
	Pending               int64   `json:"pending"`
	Uploading             int64   `json:"uploading"`
	Complete              int64   `json:"complete"`
	Failed                int64   `json:"failed"`
	Paused                int64   `json:"paused,omitempty"`
	BytesDone             int64   `json:"bytes_done"`
	BytesTotal            int64   `json:"bytes_total"`
	Message               string  `json:"message"`
}

type PhotoBackupState struct {
	Settings     PhotoBackupSettings     `json:"settings"`
	Sources      []PhotoBackupSource     `json:"sources"`
	Status       PhotoBackupStatus       `json:"status"`
	Platform     string                  `json:"platform"`
	Capabilities PhotoBackupCapabilities `json:"capabilities"`
	Destination  struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Kind  string `json:"kind"`
	} `json:"destination"`
	ManualPaused       bool `json:"manual_paused"`
	EncryptionRequired bool `json:"encryption_required"`
}

type PhotoBackupPolicy struct {
	WiFi       bool  `json:"wifi"`
	ObservedAt int64 `json:"observed_at"`
}

func (a *App) initPhotoBackup() error {
	// No worker exists yet; remove only this feature's private crash leftovers.
	cache, err := datadir.CacheDir()
	if err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(cache, "photo-backup-desktop")); err != nil {
		return fmt.Errorf("photo backup: clean interrupted staging: %w", err)
	}
	dir, err := datadir.Dir()
	if err != nil {
		return err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "photo-backup.db"))
	if err != nil {
		return err
	}
	if err := backend.TuneSQLite(db); err != nil {
		db.Close()
		return err
	}
	engine, err := photobackup.Open(db, photobackup.Options{})
	if err != nil {
		db.Close()
		return err
	}
	if err := engine.Migrate(a.appContext()); err != nil {
		db.Close()
		return err
	}
	a.photoBackupMu.Lock()
	a.photoBackupDB, a.photoBackup = db, engine
	a.photoBackupWaiters = make(map[string]chan photoBackupMaterialization)
	a.photoBackupAdapters = make(map[string]*photobackup.LocalFolderAdapter)
	a.photoBackupClosed = false
	a.photoBackupMu.Unlock()
	if runtime.GOOS != "ios" && runtime.GOOS != "android" {
		a.photoBackupStop = make(chan struct{})
		go a.photoBackupDesktopLoop(a.photoBackupStop)
		go func() { _ = a.startPhotoBackup() }()
	}
	return nil
}

func (a *App) photoBackupScope(ctx context.Context) (photobackup.Scope, error) {
	actor, err := a.actorID(ctx)
	if err != nil || actor <= 0 {
		return photobackup.Scope{}, fmt.Errorf("photo backup: account unavailable")
	}
	drive := a.ActiveChannelID()
	if drive <= 0 {
		return photobackup.Scope{}, fmt.Errorf("photo backup: drive unavailable")
	}
	return photobackup.Scope{AccountID: strconv.FormatInt(actor, 10), DriveID: drive}, nil
}

func (a *App) photoBackupEngine() (*photobackup.Engine, error) {
	a.photoBackupMu.Lock()
	defer a.photoBackupMu.Unlock()
	if a.photoBackup == nil || a.photoBackupClosed {
		return nil, fmt.Errorf("photo backup unavailable")
	}
	return a.photoBackup, nil
}

func (a *App) GetPhotoBackupState() (PhotoBackupState, error) {
	ctx := a.appContext()
	engine, err := a.photoBackupEngine()
	if err != nil {
		return PhotoBackupState{}, err
	}
	scope, err := a.photoBackupScope(ctx)
	if err != nil {
		return PhotoBackupState{}, err
	}
	settings, err := engine.GetSettings(ctx, scope)
	if errors.Is(err, sql.ErrNoRows) {
		settings = photobackup.Settings{Scope: scope, Photos: true, Videos: true}
		if err = engine.PutSettings(ctx, settings); err != nil {
			return PhotoBackupState{}, err
		}
	} else if err != nil {
		return PhotoBackupState{}, err
	}
	sources, err := engine.ListSources(ctx, scope)
	if err != nil {
		return PhotoBackupState{}, err
	}
	status, err := engine.Status(ctx, scope)
	if err != nil {
		return PhotoBackupState{}, err
	}
	state := photoBackupState(settings, sources, status, a.photoBackupIsRunning(), settings.ManualPaused)
	if err := populatePhotoBackupDestination(&state, sources); err != nil {
		return PhotoBackupState{}, err
	}
	if settings.Enabled && !settings.ManualPaused {
		if policyErr := a.photoBackupPolicyAllows(settings); policyErr != nil {
			state.Status.Phase, state.Status.Message = "paused", policyErr.Error()
		}
	}
	return a.photoBackupAccessState(a.withPhotoBackupProgress(state, scope)), nil
}

func (a *App) SavePhotoBackupSettings(value PhotoBackupSettings) (PhotoBackupState, error) {
	if runtime.GOOS != "android" && value.WiFiOnly {
		return PhotoBackupState{}, fmt.Errorf("photo backup: Wi-Fi conditions are unavailable on this platform")
	}
	ctx := a.appContext()
	engine, err := a.photoBackupEngine()
	if err != nil {
		return PhotoBackupState{}, err
	}
	scope, err := a.photoBackupScope(ctx)
	if err != nil {
		return PhotoBackupState{}, err
	}
	enc, err := a.encryption.EncryptionStatus()
	if err != nil {
		return PhotoBackupState{}, err
	}
	if enc.PasswordSet {
		value.Encrypt = true
	}
	current, getErr := engine.GetSettings(ctx, scope)
	if getErr != nil && !errors.Is(getErr, sql.ErrNoRows) {
		return PhotoBackupState{}, getErr
	}
	settings := photoBackupSettingsForSave(scope, current, value)
	a.stopPhotoBackup()
	if err := engine.PutSettings(ctx, settings); err != nil {
		return PhotoBackupState{}, err
	}
	return a.GetPhotoBackupState()
}

func (a *App) AddPhotoBackupFolder() (PhotoBackupSource, error) {
	if runtime.GOOS == "ios" || runtime.GOOS == "android" {
		return PhotoBackupSource{}, fmt.Errorf("photo backup: folder sources are unavailable on mobile")
	}
	root, err := a.SelectFolder()
	if err != nil || root == "" {
		return PhotoBackupSource{}, err
	}
	return a.UpsertPhotoBackupSource(PhotoBackupSource{ID: stableFolderSourceID(root), Kind: "folder", Name: filepath.Base(root), Root: root, Enabled: true})
}

func stableFolderSourceID(root string) string {
	clean, _ := filepath.Abs(root)
	return "folder:" + filepath.Clean(clean)
}

func (a *App) UpsertPhotoBackupSource(value PhotoBackupSource) (PhotoBackupSource, error) {
	ctx := a.appContext()
	engine, err := a.photoBackupEngine()
	if err != nil {
		return PhotoBackupSource{}, err
	}
	scope, err := a.photoBackupScope(ctx)
	if err != nil {
		return PhotoBackupSource{}, err
	}
	if value.ID == "" || value.Kind == "" {
		return PhotoBackupSource{}, photobackup.ErrInvalid
	}
	if value.Kind == "folder" {
		root, rootErr := validatePhotoBackupFolderRoot(value.Root)
		if rootErr != nil {
			return PhotoBackupSource{}, rootErr
		}
		value.Root = root
	} else if value.Root == "" {
		value.Root = value.Name
		if value.Root == "" {
			value.Root = value.ID
		}
	}
	addedAt := time.Time{}
	if value.AddedAt > 0 {
		addedAt = time.UnixMilli(value.AddedAt)
	}
	source := photobackup.Source{Scope: scope, ID: value.ID, Kind: value.Kind, Root: value.Root, Name: value.Name, Enabled: value.Enabled, AddedAt: addedAt}
	if err := engine.UpsertSource(ctx, source); err != nil {
		return PhotoBackupSource{}, err
	}
	if source.AddedAt.IsZero() {
		source.AddedAt = time.Now()
	}
	value.AddedAt = source.AddedAt.UnixMilli()
	if value.Name == "" {
		value.Name = filepath.Base(value.Root)
	}
	return value, nil
}

func (a *App) RemovePhotoBackupSource(id string) error {
	a.stopPhotoBackup()
	engine, err := a.photoBackupEngine()
	if err != nil {
		return err
	}
	scope, err := a.photoBackupScope(a.appContext())
	if err != nil {
		return err
	}
	return engine.RemoveSource(a.appContext(), scope, id)
}

func (a *App) EnqueuePhotoBackupAssets(sourceID string, values []PhotoBackupAsset) (int, error) {
	if len(values) > 128 {
		return 0, photobackup.ErrInvalid
	}
	engine, err := a.photoBackupEngine()
	if err != nil {
		return 0, err
	}
	scope, err := a.photoBackupScope(a.appContext())
	if err != nil {
		return 0, err
	}
	assets := make([]photobackup.Asset, 0, len(values))
	for _, value := range values {
		resourceID := value.ResourceID
		if resourceID == "" {
			resourceID = value.ID
		}
		assets = append(assets, photobackup.Asset{ID: value.ID, Version: value.Version, Name: value.Name, MediaType: value.MediaType, ResourceID: resourceID, ModifiedAt: time.UnixMilli(value.ModifiedAt), CapturedAt: timeFromMillis(value.CreatedAt), Size: value.Size})
	}
	return engine.EnqueuePage(a.appContext(), scope, sourceID, assets)
}

// The backup controls answer with the common operation envelope so the
// frontend can branch on a stable code — a locked vault opens the password
// prompt, anything else is shown in the backend's own words — rather than
// sniffing display text out of a wrapped Go error.
func (a *App) RunPhotoBackup() OperationResult    { return operationFailure(a.runPhotoBackup()) }
func (a *App) PausePhotoBackup() OperationResult  { return operationFailure(a.pausePhotoBackup()) }
func (a *App) ResumePhotoBackup() OperationResult { return operationFailure(a.resumePhotoBackup()) }
func (a *App) RetryPhotoBackup() OperationResult  { return operationFailure(a.retryPhotoBackup()) }

func (a *App) runPhotoBackup() error {
	engine, err := a.photoBackupEngine()
	if err != nil {
		return err
	}
	scope, err := a.photoBackupScope(a.appContext())
	if err != nil {
		return err
	}
	settings, err := engine.GetSettings(a.appContext(), scope)
	if err != nil {
		return err
	}
	if settings.ManualPaused {
		return errPhotoBackupPaused
	}
	if runtime.GOOS != "ios" && runtime.GOOS != "android" && !a.photoBackupIsRunning() {
		a.resetDesktopPhotoBackupDiscovery(a.appContext())
	}
	return a.startPhotoBackup()
}

func (a *App) pausePhotoBackup() error {
	engine, err := a.photoBackupEngine()
	if err != nil {
		return err
	}
	scope, err := a.photoBackupScope(a.appContext())
	if err != nil {
		return err
	}
	if err := engine.SetManualPaused(a.appContext(), scope, true); err != nil {
		return err
	}
	a.stopPhotoBackup()
	a.emit("photo-backup:state")
	return nil
}

func (a *App) resumePhotoBackup() error {
	engine, err := a.photoBackupEngine()
	if err != nil {
		return err
	}
	scope, err := a.photoBackupScope(a.appContext())
	if err != nil {
		return err
	}
	if err := engine.SetManualPaused(a.appContext(), scope, false); err != nil {
		return err
	}
	return a.startPhotoBackup()
}

func (a *App) retryPhotoBackup() error {
	engine, err := a.photoBackupEngine()
	if err != nil {
		return err
	}
	scope, err := a.photoBackupScope(a.appContext())
	if err != nil {
		return err
	}
	if err := engine.RetryErrors(a.appContext(), scope); err != nil {
		return err
	}
	return a.runPhotoBackup()
}

func (a *App) SetPhotoBackupPolicy(policy PhotoBackupPolicy) {
	if policy.ObservedAt <= 0 {
		policy.ObservedAt = time.Now().UnixMilli()
	}
	a.photoBackupMu.Lock()
	a.photoBackupPolicy = policy
	a.photoBackupMu.Unlock()
	engine, engineErr := a.photoBackupEngine()
	if engineErr != nil {
		return
	}
	scope, scopeErr := a.photoBackupScope(a.appContext())
	if scopeErr != nil {
		return
	}
	settings, settingsErr := engine.GetSettings(a.appContext(), scope)
	if settingsErr == nil {
		if policyErr := a.photoBackupPolicyAllows(settings); policyErr != nil {
			a.stopPhotoBackup()
		}
	}
}

func (a *App) startPhotoBackup() error {
	engine, err := a.photoBackupEngine()
	if err != nil {
		return err
	}
	scope, err := a.photoBackupScope(a.appContext())
	if err != nil {
		return err
	}
	settings, err := engine.GetSettings(a.appContext(), scope)
	if err != nil {
		return err
	}
	if err := a.photoBackupPolicyAllows(settings); err != nil {
		return err
	}
	if !settings.Enabled {
		return nil
	}
	if settings.ManualPaused {
		return errPhotoBackupPaused
	}
	if settings.Encrypt {
		status, err := a.encryption.EncryptionStatus()
		if err != nil {
			return err
		}
		if !status.PasswordRemembered {
			return ErrEncryptionPasswordRequired
		}
	}
	a.photoBackupMu.Lock()
	if a.photoBackupClosed {
		a.photoBackupMu.Unlock()
		return fmt.Errorf("photo backup unavailable")
	}
	if a.photoBackupCancel != nil {
		a.photoBackupMu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(a.appContext())
	a.photoBackupRunID++
	runID := a.photoBackupRunID
	a.photoBackupCancel = cancel
	done := make(chan struct{})
	a.photoBackupDone = done
	a.photoBackupMu.Unlock()
	go func() {
		defer func() {
			a.photoBackupMu.Lock()
			if a.photoBackupRunID == runID {
				a.photoBackupCancel = nil
				a.photoBackupDone = nil
			}
			a.photoBackupMu.Unlock()
			close(done)
		}()
		_ = engine.RecoverInterrupted(ctx, scope)
		for ctx.Err() == nil {
			// A native background lease may finish the item that was already in
			// flight, but a suspended WebView cannot safely discover or stage the
			// next one. Foreground resume restarts this durable queue.
			if !a.photoBackupMayStartNextJob() {
				break
			}
			done, runErr := engine.RunOnce(ctx, scope, a.uploadPhotoBackup)
			if runErr != nil {
				break
			}
			if done == 0 && (runtime.GOOS == "ios" || runtime.GOOS == "android" || !a.discoverDesktopPhotoBackup(ctx)) {
				break
			}
		}
		a.emit("photo-backup:state")
	}()
	return nil
}

func (a *App) uploadPhotoBackup(ctx context.Context, request photobackup.UploadRequest) (photobackup.UploadResult, error) {
	engine, err := a.photoBackupEngine()
	if err != nil {
		return photobackup.UploadResult{}, err
	}
	settings, err := engine.GetSettings(ctx, request.Scope)
	if err != nil {
		return photobackup.UploadResult{}, err
	}
	if err := a.photoBackupPolicyAllows(settings); err != nil {
		return photobackup.UploadResult{}, err
	}
	progress, finishProgress := a.beginPhotoBackupProgress(ctx, request.Scope, request.Asset.Name, request.Asset.Size)
	defer finishProgress()
	path := request.Asset.Path
	var token string
	if path == "" {
		token = randomPhotoBackupToken()
		wait := make(chan photoBackupMaterialization, 1)
		a.photoBackupMu.Lock()
		a.photoBackupWaiters[token] = wait
		a.photoBackupMu.Unlock()
		sourceDTO := photoBackupSourceDTO(request.Source)
		assetDTO := photoBackupAssetDTO(request.Asset)
		a.emit("photo-backup:materialize", map[string]any{"token": token, "source": sourceDTO, "asset": assetDTO})
		defer func() {
			a.emit("photo-backup:release", map[string]any{"token": token, "source": sourceDTO, "asset": assetDTO, "path": path})
		}()
		select {
		case <-ctx.Done():
			a.removePhotoBackupWaiter(token)
			return photobackup.UploadResult{}, ctx.Err()
		case result := <-wait:
			if result.err != "" {
				return photobackup.UploadResult{}, errors.New(result.err)
			}
			path = result.path
		case <-time.After(30 * time.Minute):
			a.removePhotoBackupWaiter(token)
			return photobackup.UploadResult{}, fmt.Errorf("photo backup: materialization timed out")
		}
	}
	if err := validatePhotoBackupPath(path, request.Asset, request.Source); err != nil {
		return photobackup.UploadResult{}, err
	}
	if request.Asset.Path != "" {
		staged, cleanup, stageErr := stagePhotoBackupFile(ctx, path, request.Asset)
		if stageErr != nil {
			return photobackup.UploadResult{}, stageErr
		}
		defer cleanup()
		path = staged
	}
	svc, err := a.requireFileService()
	if err != nil {
		return photobackup.UploadResult{}, err
	}
	destinationParentID, err := a.resolvePhotoBackupUploadParent(ctx, request)
	if err != nil {
		return photobackup.UploadResult{}, err
	}
	meta, err := svc.UploadBackup(ctx, request.ChannelID, path, destinationParentID, request.Encrypt, progress)
	if meta.MsgID > 0 {
		return photobackup.UploadResult{RemoteMessageID: int64(meta.MsgID)}, nil
	}
	return photobackup.UploadResult{}, err
}

func (a *App) ResolvePhotoBackupResource(token, path, errorMessage string) error {
	a.photoBackupMu.Lock()
	wait, ok := a.photoBackupWaiters[token]
	if ok {
		delete(a.photoBackupWaiters, token)
	}
	a.photoBackupMu.Unlock()
	if !ok {
		return fmt.Errorf("photo backup: unknown materialization token")
	}
	if errorMessage == "" {
		if err := validateNativePhotoBackupPath(path); err != nil {
			errorMessage = err.Error()
		}
	}
	wait <- photoBackupMaterialization{path: path, err: errorMessage}
	return nil
}

func (a *App) removePhotoBackupWaiter(token string) {
	a.photoBackupMu.Lock()
	delete(a.photoBackupWaiters, token)
	a.photoBackupMu.Unlock()
}

func randomPhotoBackupToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b[:])
}

func (a *App) photoBackupPolicyAllows(settings photobackup.Settings) error {
	if !settings.WiFiOnly {
		return nil
	}
	a.photoBackupMu.Lock()
	policy := a.photoBackupPolicy
	a.photoBackupMu.Unlock()
	observed := time.UnixMilli(policy.ObservedAt)
	if policy.ObservedAt <= 0 || time.Since(observed) > photoBackupPolicyTTL || observed.After(time.Now().Add(30*time.Second)) {
		return errPhotoBackupPolicyUnavailable
	}
	if settings.WiFiOnly && !policy.WiFi {
		return errPhotoBackupWaitingForWiFi
	}
	return nil
}

func (a *App) photoBackupIsRunning() bool {
	a.photoBackupMu.Lock()
	defer a.photoBackupMu.Unlock()
	return a.photoBackupCancel != nil
}
func (a *App) stopPhotoBackup() {
	a.photoBackupMu.Lock()
	cancel := a.photoBackupCancel
	done := a.photoBackupDone
	waiters := a.photoBackupWaiters
	a.photoBackupWaiters = make(map[string]chan photoBackupMaterialization)
	a.photoBackupMu.Unlock()
	if cancel != nil {
		cancel()
	}
	for _, wait := range waiters {
		wait <- photoBackupMaterialization{err: "photo backup canceled"}
	}
	if done != nil {
		<-done
	}
	a.photoBackupDiscoveryMu.Lock()
	a.photoBackupMu.Lock()
	for _, adapter := range a.photoBackupAdapters {
		adapter.Close()
	}
	a.photoBackupAdapters = make(map[string]*photobackup.LocalFolderAdapter)
	a.photoBackupMu.Unlock()
	a.photoBackupDiscoveryMu.Unlock()
}

func (a *App) closePhotoBackup() {
	a.photoBackupMu.Lock()
	if a.photoBackupClosed {
		a.photoBackupMu.Unlock()
		return
	}
	a.photoBackupClosed = true
	a.photoBackupMu.Unlock()
	a.stopPhotoBackup()
	a.photoBackupMu.Lock()
	stop, db := a.photoBackupStop, a.photoBackupDB
	a.photoBackupStop, a.photoBackupDB, a.photoBackup = nil, nil, nil
	a.photoBackupAdapters = nil
	a.photoBackupMu.Unlock()
	if stop != nil {
		close(stop)
	}
	if db != nil {
		_ = db.Close()
	}
}

func (a *App) photoBackupDesktopLoop(stop <-chan struct{}) {
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if !a.photoBackupIsRunning() {
				_ = a.runPhotoBackup()
			}
		}
	}
}

func (a *App) discoverDesktopPhotoBackup(ctx context.Context) bool {
	a.photoBackupDiscoveryMu.Lock()
	defer a.photoBackupDiscoveryMu.Unlock()
	engine, err := a.photoBackupEngine()
	if err != nil {
		return false
	}
	scope, err := a.photoBackupScope(ctx)
	if err != nil {
		return false
	}
	sources, err := engine.ListSources(ctx, scope)
	if err != nil {
		return false
	}
	cache, _ := datadir.CacheDir()
	return a.discoverDesktopSources(ctx, engine, scope, sources, cache)
}

func (a *App) discoverDesktopSources(ctx context.Context, engine *photobackup.Engine, scope photobackup.Scope, sources []photobackup.Source, cache string) bool {
	more := false
	for _, source := range sources {
		if source.Kind == "folder" && source.Enabled {
			key := photoBackupAdapterKey(scope, source.ID)
			a.photoBackupMu.Lock()
			adapter := a.photoBackupAdapters[key]
			if adapter == nil {
				adapter = photobackup.NewLocalFolderAdapter(32, []string{cache})
				a.photoBackupAdapters[key] = adapter
			}
			a.photoBackupMu.Unlock()
			added, done, discoverErr := engine.DiscoverPage(ctx, scope, source.ID, adapter)
			if done || discoverErr != nil {
				adapter.Close()
				a.photoBackupMu.Lock()
				delete(a.photoBackupAdapters, key)
				a.photoBackupMu.Unlock()
			}
			if discoverErr == nil && (added > 0 || !done) {
				more = true
			}
		}
	}
	return more
}

func (a *App) resetDesktopPhotoBackupDiscovery(ctx context.Context) {
	a.photoBackupDiscoveryMu.Lock()
	defer a.photoBackupDiscoveryMu.Unlock()
	engine, err := a.photoBackupEngine()
	if err != nil {
		return
	}
	scope, err := a.photoBackupScope(ctx)
	if err != nil {
		return
	}
	sources, err := engine.ListSources(ctx, scope)
	if err != nil {
		return
	}
	for _, source := range sources {
		if source.Kind == "folder" {
			_ = engine.ResetDiscovery(ctx, scope, source.ID)
		}
	}
	a.photoBackupMu.Lock()
	for _, adapter := range a.photoBackupAdapters {
		adapter.Close()
	}
	a.photoBackupAdapters = make(map[string]*photobackup.LocalFolderAdapter)
	a.photoBackupMu.Unlock()
}

func photoBackupAdapterKey(scope photobackup.Scope, sourceID string) string {
	return scope.AccountID + ":" + strconv.FormatInt(scope.DriveID, 10) + ":" + sourceID
}

func photoBackupSourceDTO(source photobackup.Source) PhotoBackupSource {
	name := source.Name
	if name == "" {
		name = filepath.Base(source.Root)
	}
	return PhotoBackupSource{ID: source.ID, Kind: source.Kind, Name: name, Root: source.Root, Enabled: source.Enabled, AddedAt: source.AddedAt.UnixMilli()}
}

func photoBackupAssetDTO(asset photobackup.Asset) PhotoBackupAsset {
	return PhotoBackupAsset{ID: asset.ID, Version: asset.Version, Name: asset.Name, MediaType: asset.MediaType, ResourceID: asset.ResourceID, ModifiedAt: asset.ModifiedAt.UnixMilli(), CreatedAt: millisFromTime(asset.CapturedAt), Size: asset.Size}
}

// The wire carries an unknown capture time as 0. Both directions have to agree
// on that, or a host that reports nothing would round-trip as a 1970 photo
// and "new items only" would skip everything it ever sends.
func timeFromMillis(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

func millisFromTime(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func photoBackupState(settings photobackup.Settings, sources []photobackup.Source, status photobackup.Status, running, manualPaused bool) PhotoBackupState {
	state := PhotoBackupState{Platform: runtime.GOOS, Settings: PhotoBackupSettings{Enabled: settings.Enabled, Photos: settings.Photos, Videos: settings.Videos, FutureOnly: settings.FutureOnly, WiFiOnly: settings.WiFiOnly, DestinationParentID: settings.DestinationParentID, Encrypt: settings.Encrypt}}
	state.ManualPaused = manualPaused
	state.Sources = make([]PhotoBackupSource, 0, len(sources))
	for _, source := range sources {
		state.Sources = append(state.Sources, photoBackupSourceDTO(source))
	}
	state.Status = PhotoBackupStatus{Phase: "idle", Pending: status.Pending, Uploading: status.Uploading, Complete: status.Complete, Failed: status.Error, Paused: status.Paused, Message: status.LastError}
	if manualPaused {
		state.Status.Phase = "paused"
		state.Status.Message = "Paused by you."
	} else if running || status.Uploading > 0 {
		state.Status.Phase = "uploading"
	} else if status.Error > 0 {
		state.Status.Phase = "failed"
	} else if status.Pending > 0 {
		state.Status.Phase = "queued"
	} else if status.Paused > 0 {
		state.Status.Phase = "paused"
	} else if status.Complete > 0 {
		state.Status.Phase = "complete"
	}
	policySupported := runtime.GOOS == "android"
	detail := "Wi-Fi conditions are available on Android."
	if policySupported {
		detail = "Requires a recent device connectivity update."
	}
	state.Capabilities.WiFiOnly = PhotoBackupCapability{Supported: policySupported, Label: "Wi-Fi only", Detail: detail}
	state.Capabilities.Access.Status, state.Capabilities.Access.Detail = "available", "Photo access is managed by the device."
	state.Destination.ID = settings.DestinationParentID
	state.Destination.Title = "Photo backup"
	state.Destination.Kind = "folder"
	return state
}
