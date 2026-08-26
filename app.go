package main

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"TDrive/backend"
	"TDrive/backend/applog"
	"TDrive/backend/core"
	"TDrive/backend/mountcontroller"
	"TDrive/backend/mountlifecycle"
	"TDrive/backend/processlock"
	"TDrive/backend/projection"
	authsvc "TDrive/backend/services/auth"
	fileservice "TDrive/backend/services/file"
	folderservice "TDrive/backend/services/folder"
	lifecycleservice "TDrive/backend/services/lifecycle"
	readservice "TDrive/backend/services/read"
	userservice "TDrive/backend/services/user"
	"TDrive/backend/tgclient"
	"TDrive/backend/updater"

	"github.com/gotd/td/telegram"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx         context.Context
	engine      *core.Engine
	Client      *telegram.Client
	backendLock *processlock.Lock

	// version is the build stamp from main.appVersion ("dev" for local builds).
	version string
	// updates drives the release check/download/install lifecycle. It is
	// created in startup so its state events can reach the webview.
	updates *updater.Service

	// mountMu protects lazy controller construction. A transient construction
	// failure stays retryable for a later mount request.
	mountMu                sync.Mutex
	mountController        appMountController
	mountControllerFactory func(*core.Engine) (appMountController, error)
	mountDriveResolver     func() (mountcontroller.Drive, error)
	mountDrivesResolver    func([]int64) ([]mountcontroller.Drive, error)
	// mountEncryptionPolicyRefresh is a narrow test seam. Production uses the
	// core authoritative sync path; tests can model offline and partial history
	// without network access.
	mountEncryptionPolicyRefresh func(context.Context, int64) error
	// mountLifecycle serializes mount Start/Stop/Close with encryption policy
	// transitions. It is deliberately separate from the controller's internal
	// mutex because a vault lock must cover both controller shutdown and key
	// erasure as one indivisible operation.
	mountLifecycle         mountlifecycle.Gate
	mountLifecycleTerminal bool // guarded by mountLifecycle

	// encryptionServiceOverride is a narrow test seam for deterministic
	// key-state race tests. Production always uses Engine.EncryptionService.
	encryptionServiceOverride appEncryptionService

	// fileDropEnabled tracks whether the native OS file-drop handler is
	// registered. The frontend toggles it off during an internal drag-to-move so
	// macOS does not intercept the in-app HTML5 drag.
	fileDropMu      sync.Mutex
	fileDropEnabled bool

	// transferMu guards the cancel handles for the active upload/import and the
	// active download (both serialized by the frontend).
	transferMu     sync.Mutex
	uploadCancel   context.CancelFunc
	downloadCancel context.CancelFunc

	// nativeMedia owns out-of-webview player processes tied to media loopback
	// sessions. Each token must be closed before the backend shuts down so the
	// range reader and native surface do not outlive the app.
	nativeMediaMu sync.Mutex
	nativeMedia   map[string]*nativeMediaSession
}

type runtimeEventSink struct {
	app *App
}

func (s runtimeEventSink) Emit(name string, args ...any) {
	if s.app == nil {
		return
	}
	runtime.EventsEmit(s.app.ctx, name, args...)
}

// resolvePeer satisfies tdsync.PeerResolver through peerResolverFn. Keeping
// it unexported prevents Wails from exposing this internal sync helper.
func (a *App) resolvePeer(ctx context.Context, channelID int64) (tgclient.InputPeer, error) {
	if a.engine == nil {
		return tgclient.InputPeer{}, fmt.Errorf("tg client not ready")
	}
	return a.engine.ResolvePeer(ctx, channelID)
}

// channelPeer resolves the active drive's tgclient.InputPeer through the shared
// Telegram client. Used by every op that needs to send into Telegram; callers
// should not hold this across long operations.
func (a *App) channelPeer(ctx context.Context, channelID int64) (tgclient.InputPeer, error) {
	if a.engine == nil {
		return tgclient.InputPeer{}, fmt.Errorf("tg client not ready")
	}
	return a.engine.ChannelPeer(ctx, channelID)
}

// emitAndProject sends a control op and projects it locally. Returns the
// Telegram msg_id used as the op's identity.
//
// On send failure: returns the error; nothing is projected.
// On project failure after a successful send: logs, returns the error so
// the caller can surface it. The op IS in Telegram and will be projected on
// the next sync.
func (a *App) emitAndProject(channelID int64, op projection.Op) (int64, error) {
	if a.engine == nil {
		return 0, fmt.Errorf("tg client not ready")
	}
	return a.engine.EmitAndProject(channelID, op)
}

func (a *App) ActiveChannelID() int64 {
	if a.engine == nil {
		return 0
	}
	return a.engine.ActiveChannelID()
}

func (a *App) setActiveChannelID(channelID int64) {
	if a.engine == nil {
		return
	}
	a.engine.SetActiveChannelID(channelID)
}

// MyUserID returns the logged-in Telegram user id (cached after first
// resolution). The frontend uses it to gate owner-only actions on shared
// drives. Returns an error if Telegram resolution fails — caller should
// default-deny the gated actions in that case.
func (a *App) MyUserID() (int64, error) {
	return a.actorID(a.ctx)
}

func (a *App) actorID(ctx context.Context) (int64, error) {
	if a.engine == nil {
		return 0, fmt.Errorf("tg client not ready")
	}
	return a.engine.ActorID(ctx)
}

func (a *App) SetActiveChannel(channelID int64) error {
	if a.engine == nil {
		return fmt.Errorf("backend not ready")
	}
	return a.engine.SetActiveChannel(channelID)
}

type TDriveFile struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	AccessHash int64  `json:"access_hash"`
	Date       int    `json:"date"`
}

type DownloadResult struct {
	Status    string `json:"status"`
	Message   string `json:"message"`
	SavedPath string `json:"saved_path,omitempty"`
}
type PreviewPayload struct {
	DataBase64 string `json:"data_base64"`
	MimeType   string `json:"mime_type"`
}

func (a *App) CheckLoginStatus() bool {
	svc := a.authService()
	return svc != nil && svc.IsLoggedIn(a.ctx)
}

func (a *App) SelectFiles() ([]string, error) {
	uploadfilepaths, err := runtime.OpenMultipleFilesDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select files to upload",
	})
	if err != nil {
		return nil, err
	}
	return uploadfilepaths, nil
}

// SelectFolder opens a directory picker and returns the chosen folder path
// (empty if the user cancels). Folder import walks it on the Go side.
func (a *App) SelectFolder() (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select a folder to upload",
	})
}

// UploadToDriveFS uploads each chosen file to the active drive. The
// `encrypt` flag is a per-batch choice made in the upload-options modal:
// true means encrypt-and-upload (the encryption password must already
// be remembered for this app session before this call), false means plain
// upload regardless of encryption password state.
// beginUpload starts a cancellable context for an upload/import and stores its
// cancel handle so CancelUpload can stop it. endUpload clears it.
func (a *App) beginUpload() context.Context {
	ctx, cancel := context.WithCancel(a.ctx)
	a.transferMu.Lock()
	if a.uploadCancel != nil {
		a.uploadCancel()
	}
	a.uploadCancel = cancel
	a.transferMu.Unlock()
	return ctx
}

func (a *App) endUpload() {
	a.transferMu.Lock()
	a.uploadCancel = nil
	a.transferMu.Unlock()
}

// sweepOrphanParts retries deleting the part bodies of already-deleted multipart
// files whose cleanup didn't finish. It only touches parts behind a tombstone,
// never an upload still in flight, so it's safe to run at the start of an upload
// flow. Best effort — a normal session finds nothing and never hits Telegram.
func (a *App) sweepOrphanParts(ctx context.Context) {
	svc := a.fileService()
	if svc == nil {
		return
	}
	if err := svc.SweepOrphanParts(ctx, a.ActiveChannelID()); err != nil {
		fmt.Printf("warn: orphan-part sweep failed: %v\n", err)
	}
}

// CancelUpload cancels the in-flight upload or import: in-flight sends abort and
// the rest are skipped. The frontend marks the affected transfers canceled.
func (a *App) CancelUpload() {
	a.transferMu.Lock()
	cancel := a.uploadCancel
	a.transferMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// beginDownload / endDownload / CancelDownload do the same for the active
// download.
func (a *App) beginDownload() context.Context {
	ctx, cancel := context.WithCancel(a.ctx)
	a.transferMu.Lock()
	if a.downloadCancel != nil {
		a.downloadCancel()
	}
	a.downloadCancel = cancel
	a.transferMu.Unlock()
	return ctx
}

func (a *App) endDownload() {
	a.transferMu.Lock()
	a.downloadCancel = nil
	a.transferMu.Unlock()
}

// CancelDownload cancels the active download.
func (a *App) CancelDownload() {
	a.transferMu.Lock()
	cancel := a.downloadCancel
	a.transferMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (a *App) UploadToDriveFS(filePaths []string, parentIDs []string, encrypt bool) ([]backend.FileMetaData, error) {
	svc, err := a.requireFileService()
	if err != nil {
		return nil, err
	}
	ctx := a.beginUpload()
	defer a.endUpload()
	a.sweepOrphanParts(ctx)
	files, err := svc.Upload(ctx, a.ActiveChannelID(), filePaths, parentIDs, encrypt)
	if err != nil {
		out := make([]backend.FileMetaData, 0, len(files))
		for _, f := range files {
			out = append(out, uploadMetaToBackend(f))
		}
		return out, err
	}
	out := make([]backend.FileMetaData, 0, len(files))
	for _, f := range files {
		out = append(out, uploadMetaToBackend(f))
	}
	return out, nil
}

// PlanImport scans the selected paths and returns the counts shown in the
// import dialog (files, folders, total size, archives, oversize-skipped). It
// mutates nothing.
func (a *App) PlanImport(paths []string, encrypt bool, extractArchives bool) fileservice.ImportPlan {
	svc := a.fileService()
	if svc == nil {
		return fileservice.ImportPlan{Errors: []string{"backend not ready"}}
	}
	return svc.PlanImport(paths, encrypt, extractArchives)
}

// ImportPaths recreates the folder structure of the selected paths under
// parentID and uploads their files. Archives are extracted when extractArchives
// is set, otherwise uploaded as-is. Progress flows through the import_* and the
// per-file upload_* events.
func (a *App) ImportPaths(paths []string, parentID string, encrypt bool, extractArchives bool) error {
	svc, err := a.requireFileService()
	if err != nil {
		return err
	}
	ctx := a.beginUpload()
	defer a.endUpload()
	a.sweepOrphanParts(ctx)
	return svc.RunImport(ctx, a.ActiveChannelID(), paths, parentID, encrypt, extractArchives)
}

func uploadMetaToBackend(f fileservice.Metadata) backend.FileMetaData {
	return backend.FileMetaData{
		Name:          f.Name,
		Size:          f.Size,
		TgMsgID:       f.MsgID,
		ParentID:      f.ParentID,
		UploadTime:    f.UploadTime,
		Encrypted:     f.Encrypted,
		PlaintextSize: f.PlaintextSize,
	}
}

func (a *App) folderService() *folderservice.Service {
	if a.engine == nil {
		return nil
	}
	return a.engine.FolderService()
}

func (a *App) requireFolderService() (*folderservice.Service, error) {
	if svc := a.folderService(); svc != nil {
		return svc, nil
	}
	return nil, fmt.Errorf("backend not ready")
}

func (a *App) fileService() *fileservice.Service {
	if a.engine == nil {
		return nil
	}
	return a.engine.FileService()
}

func (a *App) requireFileService() (*fileservice.Service, error) {
	if svc := a.fileService(); svc != nil {
		return svc, nil
	}
	return nil, fmt.Errorf("backend not ready")
}

func (a *App) readService() *readservice.Service {
	if a.engine == nil {
		return nil
	}
	return a.engine.ReadService()
}

func (a *App) requireReadService() (*readservice.Service, error) {
	if svc := a.readService(); svc != nil {
		return svc, nil
	}
	return nil, fmt.Errorf("backend not ready")
}

func (a *App) lifecycleService() *lifecycleservice.Service {
	if a.engine == nil {
		return nil
	}
	return a.engine.LifecycleService()
}

func (a *App) requireLifecycleService() (*lifecycleservice.Service, error) {
	if svc := a.lifecycleService(); svc != nil {
		return svc, nil
	}
	return nil, fmt.Errorf("backend not ready")
}

func (a *App) userService() *userservice.Service {
	if a.engine == nil {
		return nil
	}
	return a.engine.UserService()
}

func (a *App) requireUserService() (*userservice.Service, error) {
	if svc := a.userService(); svc != nil {
		return svc, nil
	}
	return nil, fmt.Errorf("backend not ready")
}

func (a *App) authService() *authsvc.Service {
	if a.engine == nil {
		return authsvc.NewService(runtimeEventSink{app: a})
	}
	return a.engine.AuthService()
}

func (a *App) LoginPhoneNumber(phoneNumber string) error {
	svc := a.authService()
	if svc == nil {
		return fmt.Errorf("backend not ready")
	}
	return svc.StartLogin(a.ctx, phoneNumber)
}

func (a *App) InitDrive() string {
	svc, err := a.requireLifecycleService()
	if err != nil {
		return err.Error()
	}
	return svc.InitDrive(a.ctx)
}

func (a *App) GetFileList() []TDriveFile {
	svc, err := a.requireReadService()
	if err != nil {
		return nil
	}
	files, err := svc.TelegramRootFiles(a.ctx, a.ActiveChannelID())
	if err != nil {
		return nil
	}
	out := make([]TDriveFile, 0, len(files))
	for _, f := range files {
		out = append(out, TDriveFile{
			ID:         f.ID,
			Name:       f.Name,
			Size:       f.Size,
			AccessHash: f.AccessHash,
			Date:       f.Date,
		})
	}
	return out
}

func (a *App) PreviewThumbnail(msgID int) (PreviewPayload, error) {
	svc, err := a.requireFileService()
	if err != nil {
		return PreviewPayload{}, err
	}
	payload, err := svc.PreviewThumbnail(a.ctx, a.ActiveChannelID(), msgID)
	if err != nil {
		return PreviewPayload{}, err
	}
	return PreviewPayload(payload), nil
}

func (a *App) PreviewFile(msgID int) (PreviewPayload, error) {
	svc, err := a.requireFileService()
	if err != nil {
		return PreviewPayload{}, err
	}
	payload, err := svc.PreviewFile(a.ctx, a.ActiveChannelID(), msgID)
	if err != nil {
		return PreviewPayload{}, err
	}
	return PreviewPayload(payload), nil
}

func (a *App) DownloadFile(msgID int, TgMsgID int) DownloadResult {
	svc, err := a.requireFileService()
	if err != nil {
		return DownloadResult{Status: "error", Message: err.Error()}
	}
	ctx := a.beginDownload()
	defer a.endDownload()
	result := svc.Download(ctx, a.ActiveChannelID(), msgID, TgMsgID, func(defaultName string) (string, error) {
		return runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
			DefaultFilename: defaultName,
			Title:           "Save File As...",
		})
	})
	return DownloadResult{
		Status:    result.Status,
		Message:   result.Message,
		SavedPath: result.SavedPath,
	}
}

func (a *App) DeleteFile(msgID int) string {
	svc, err := a.requireFileService()
	if err != nil {
		return "Error: " + err.Error()
	}
	if err := svc.Delete(a.ctx, a.ActiveChannelID(), msgID); err != nil {
		return "Error: " + err.Error()
	}
	return "Success"
}

func (a *App) GetStorageUsed() (int64, error) {
	svc, err := a.requireReadService()
	if err != nil {
		return 0, err
	}
	return svc.StorageUsed(a.ActiveChannelID())
}

func (a *App) GetCodech() chan string {
	return a.authService().Codech()
}

func (a *App) GetPassch() chan string {
	return a.authService().Passch()
}

func NewApp() *App {
	return &App{}
}

func (a *App) CheckSystemStatus() string {
	return a.authService().SystemStatus()
}

func (a *App) SaveSetup(apiId int, apiHash string) string {
	return a.authService().SaveSetup(apiId, apiHash)
}

func (a *App) SumbitCode(code string) {
	a.authService().SubmitCode(code)
}

func (a *App) SendHint(hint string) {
	a.authService().SendHint(hint)
}

func (a *App) SumbitPassword(password string) {
	a.authService().SubmitPassword(password)
}

func (a *App) CreateFolder(foldername string, parentID string) (backend.Folder, error) {
	channelID := a.ActiveChannelID()
	svc, err := a.requireFolderService()
	if err != nil {
		return backend.Folder{}, err
	}
	folder, err := svc.Create(channelID, foldername, parentID)
	if err != nil {
		return backend.Folder{}, err
	}
	return backend.Folder{
		ID:       folder.ID,
		Name:     folder.Name,
		ParentID: folder.ParentID,
	}, nil
}

// shutdown runs on app exit. Tear down the shared Telegram connection so the
// background Run scope's goroutine exits cleanly.
func (a *App) shutdown(ctx context.Context) {
	mountCtx, cancel := context.WithTimeout(context.Background(), encryptionMountTransitionTimeout)
	if err := a.shutdownMountController(mountCtx); err != nil {
		fmt.Printf("Warning: Failed to disconnect TDrive mount: %v\n", err)
	}
	cancel()
	a.closeAllNativeMedia()
	if a.engine != nil {
		a.engine.Close()
	}
	applog.Close()
	if a.backendLock != nil {
		if err := a.backendLock.Release(); err != nil {
			fmt.Printf("Warning: backend lock release failed: %v\n", err)
		}
		a.backendLock = nil
	}
}

// enableFileDrop registers the native OS file-drop handler (idempotent).
func (a *App) enableFileDrop() {
	a.fileDropMu.Lock()
	defer a.fileDropMu.Unlock()
	if a.fileDropEnabled {
		return
	}
	runtime.OnFileDrop(a.ctx, func(x, y int, paths []string) {
		if len(paths) > 0 {
			runtime.EventsEmit(a.ctx, "files_dropped", map[string]any{
				"x":     x,
				"y":     y,
				"paths": paths,
			})
		}
	})
	a.fileDropEnabled = true
}

// SetFileDropEnabled toggles the native OS file-drop handler. The frontend turns
// it off for the duration of an internal drag-to-move: on macOS the webview's
// native drop destination otherwise intercepts the in-app HTML5 drag, which
// breaks the move and pops the upload dialog.
func (a *App) SetFileDropEnabled(enabled bool) {
	if enabled {
		a.enableFileDrop()
		return
	}
	a.fileDropMu.Lock()
	defer a.fileDropMu.Unlock()
	if a.fileDropEnabled {
		runtime.OnFileDropOff(a.ctx)
		a.fileDropEnabled = false
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	lock, err := processlock.Acquire("gui")
	if err != nil {
		if errors.Is(err, processlock.ErrAlreadyRunning) {
			fmt.Printf("TDrive backend is already running: %v\n", err)
		} else {
			fmt.Printf("Warning: Failed to acquire backend lock: %v\n", err)
		}
		runtime.Quit(ctx)
		return
	}
	a.backendLock = lock
	applog.Init()
	a.initUpdater()

	// Native file drop: hand the dropped absolute paths to the frontend, which
	// resolves the target folder and runs the import flow. Drop zones opt in via
	// the --wails-drop-target CSS property.
	a.enableFileDrop()

	engine, err := core.New(ctx, core.Config{
		Events: runtimeEventSink{app: a},
		Warnf: func(format string, args ...any) {
			fmt.Printf(format, args...)
		},
		Thumbs: newThumbnailCache(),
	})
	if err != nil {
		fmt.Printf("Warning: Failed to init TDrive backend: %v\n", err)
		applog.Close()
		if releaseErr := a.backendLock.Release(); releaseErr != nil {
			fmt.Printf("Warning: backend lock release failed: %v\n", releaseErr)
		}
		a.backendLock = nil
		runtime.Quit(ctx)
		return
	}
	a.engine = engine
	a.Client = engine.RawClient()
	_, mountInitErr := a.ensureMountController()
	if mountInitErr != nil {
		fmt.Printf("Warning: Failed to initialize TDrive mount: %v\n", mountInitErr)
	}

	fmt.Println("TDrive DB ready!")
	a.finishUpdateCleanup(mountInitErr)
}

// SyncChannel triggers an incremental sync for the given channel. Wails-bound
// for the future "Refresh" UI button and for debug.
func (a *App) SyncChannel(channelID int64) error {
	svc, err := a.requireLifecycleService()
	if err != nil {
		return err
	}
	return svc.SyncChannel(a.ctx, channelID)
}

// RebuildProjection wipes and replays the local projection for a channel
// from the channel's replay_log. Hidden Wails method for debug/recovery.
func (a *App) RebuildProjection(channelID int64) error {
	svc, err := a.requireLifecycleService()
	if err != nil {
		return err
	}
	return svc.RebuildProjection(channelID)
}

func (a *App) GetFolderContents(parentID string) (backend.FileSystem, error) {
	svc, err := a.requireReadService()
	if err != nil {
		return backend.FileSystem{}, err
	}
	fs, err := svc.FolderContents(a.ActiveChannelID(), parentID)
	if err != nil {
		return backend.FileSystem{}, err
	}

	result := backend.FileSystem{
		Folders: make([]backend.Folder, 0, len(fs.Folders)),
		Files:   make([]backend.FileMetaData, 0, len(fs.Files)),
	}
	for _, f := range fs.Folders {
		result.Folders = append(result.Folders, backend.Folder{
			ID:       f.ID,
			Name:     f.Name,
			ParentID: f.ParentID,
		})
	}
	for _, f := range fs.Files {
		result.Files = append(result.Files, backend.FileMetaData{
			TgMsgID:       int(f.MsgID),
			Name:          f.Name,
			Size:          f.Size,
			ParentID:      f.ParentID,
			UploadTime:    f.UploadTime,
			UploaderID:    f.UploaderID,
			Encrypted:     f.Encrypted,
			PlaintextSize: f.PlaintextSize,
		})
	}
	return result, nil
}

func (a *App) Search(query string, limit int) ([]backend.SearchResult, error) {
	svc, err := a.requireReadService()
	if err != nil {
		return nil, err
	}
	hits, err := svc.Search(a.ActiveChannelID(), query, limit)
	if err != nil {
		return nil, err
	}

	results := make([]backend.SearchResult, 0, len(hits))
	for _, h := range hits {
		results = append(results, backend.SearchResult{
			Type:          h.Type,
			ID:            h.ID,
			Name:          h.Name,
			ParentID:      h.ParentID,
			Size:          h.Size,
			UploadTime:    h.UploadTime,
			UploaderID:    h.UploaderID,
			Encrypted:     h.Encrypted,
			PlaintextSize: h.PlaintextSize,
			Path:          h.Path,
		})
	}
	return results, nil
}

func (a *App) GetAllFsMsgIDs() ([]int, error) {
	svc, err := a.requireReadService()
	if err != nil {
		return nil, err
	}
	return svc.AllFileMsgIDs(a.ActiveChannelID())
}

func (a *App) GetFolderSize(folderID string) (int64, error) {
	svc, err := a.requireReadService()
	if err != nil {
		return 0, err
	}
	return svc.FolderSize(a.ActiveChannelID(), folderID)
}

func (a *App) GetFolderSizes(parentID string) (map[string]int64, error) {
	svc, err := a.requireReadService()
	if err != nil {
		return nil, err
	}
	return svc.ChildFolderSizes(a.ActiveChannelID(), parentID)
}

func (a *App) DeleteFolder(folderID string) string {
	svc, err := a.requireFolderService()
	if err != nil {
		return "Error: " + err.Error()
	}
	if err := svc.Delete(a.ctx, a.ActiveChannelID(), folderID); err != nil {
		return "Error: " + err.Error()
	}
	return "Success"
}

func (a *App) MsgToTdriveSystem(msgID int, name string, size int64, parentID string) string {
	svc, err := a.requireFileService()
	if err != nil {
		return "Error: " + err.Error()
	}
	if err := svc.Meta(a.ActiveChannelID(), msgID, name, size, parentID); err != nil {
		return "Error: " + err.Error()
	}
	return "Success"
}

func (a *App) RenameFile(msgID int, newName string) string {
	svc, err := a.requireFileService()
	if err != nil {
		return "Error: " + err.Error()
	}
	if err := svc.Rename(a.ctx, a.ActiveChannelID(), msgID, newName); err != nil {
		return "Error: " + err.Error()
	}
	return "Success"
}

func (a *App) RenameFolder(folderID string, newName string) string {
	svc, err := a.requireFolderService()
	if err != nil {
		return "Error: " + err.Error()
	}
	if err := svc.Rename(a.ActiveChannelID(), folderID, newName); err != nil {
		return "Error: " + err.Error()
	}
	return "Success"
}

func (a *App) MoveFile(msgID int, newParentID string) string {
	svc, err := a.requireFileService()
	if err != nil {
		return "Error: " + err.Error()
	}
	if err := svc.Move(a.ctx, a.ActiveChannelID(), msgID, newParentID); err != nil {
		return "Error: " + err.Error()
	}
	return "Success"
}

func (a *App) MoveFolder(folderID string, newParentID string) string {
	svc, err := a.requireFolderService()
	if err != nil {
		return "Error: " + err.Error()
	}
	if err := svc.Move(a.ActiveChannelID(), folderID, newParentID); err != nil {
		return "Error: " + err.Error()
	}
	return "Success"
}
