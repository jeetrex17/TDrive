package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"TDrive/backend"
	"TDrive/backend/applog"
	"TDrive/backend/core"
	"TDrive/backend/datadir"
	"TDrive/backend/galleryimage"
	"TDrive/backend/mountcontroller"
	"TDrive/backend/mountlifecycle"
	"TDrive/backend/photobackup"
	"TDrive/backend/processlock"
	"TDrive/backend/projection"
	authsvc "TDrive/backend/services/auth"
	fileservice "TDrive/backend/services/file"
	folderservice "TDrive/backend/services/folder"
	lifecycleservice "TDrive/backend/services/lifecycle"
	readservice "TDrive/backend/services/read"
	userservice "TDrive/backend/services/user"

	"github.com/gotd/td/telegram"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type App struct {
	// ctx is the lifecycle context handed to ServiceStartup. It stays valid
	// for the life of the app and is cancelled right before shutdown; engine
	// and service calls thread it through for cancellation, not for reaching
	// the webview (that goes through wails below).
	ctx context.Context
	// wails is the running application handle, set once main() has created
	// it. Dialogs, events, the browser opener and Quit all go through it.
	wails           *application.App
	engine          *core.Engine
	authReady       atomic.Bool
	Client          *telegram.Client
	backendLock     *processlock.Lock
	galleryImagesMu sync.Mutex
	galleryImages   *galleryimage.Server
	// backendStartupDone publishes construction to bridge calls, including calls
	// dispatched before ServiceStartup begins. Closing it also publishes engine
	// and ctx; backendStartupMu protects completion errors and shutdown state.
	backendStartupMu       sync.Mutex
	backendStartupDone     chan struct{}
	backendStartupComplete bool
	backendStartupErr      error
	backendStopped         bool

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

	// trashSweepStop ends the background retention sweep. The sweep is the
	// only thing that ever destroys trashed bytes without the user asking, so
	// it is owned here and stopped with the rest of the process.
	trashSweepStop chan struct{}

	// fileDropEnabled tracks whether the native OS file-drop handler is
	// registered. The frontend toggles it off during an internal drag-to-move so
	// macOS does not intercept the in-app HTML5 drag.
	fileDropMu      sync.Mutex
	fileDropEnabled bool

	transfers transferState

	// The domains lifted out of App into their own bound services. They are
	// built once in initServices; see services.go for the graph and for
	// why startup orchestration stays here.
	device     *DeviceService
	drives     *DriveService
	encryption *EncryptionService
	media      *MediaService
	updates    *UpdateService

	photoBackupMu          sync.Mutex
	photoBackupDiscoveryMu sync.Mutex
	photoBackup            *photobackup.Engine
	photoBackupDB          *sql.DB
	photoBackupCancel      context.CancelFunc
	photoBackupDone        chan struct{}
	photoBackupRunID       uint64
	photoBackupRerun       bool
	photoBackupProgress    photoBackupProgressState
	photoBackupWaiters     map[string]chan photoBackupMaterialization
	photoBackupPolicy      PhotoBackupPolicy
	photoBackupStop        chan struct{}
	photoBackupAdapters    map[string]*photobackup.LocalFolderAdapter
	photoBackupBackground  photoBackupBackgroundState
	photoBackupClosed      bool
}

type runtimeEventSink struct {
	app *App
}

func (s runtimeEventSink) Emit(name string, args ...any) {
	if name == "login-success" {
		s.app.markAuthenticated()
	}
	s.app.emit(name, args...)
}

func (a *App) markAuthenticated() {
	a.authReady.Store(true)
	a.startAuthenticatedWork()
}

// emit forwards an event to the webview. It is a no-op until main has wired
// up the running application: tests and the pre-startup window construct
// plain *App values with no wails handle.
func (a *App) emit(name string, args ...any) {
	if a == nil || a.wails == nil {
		return
	}
	if args == nil {
		args = []any{}
	}
	// Pass args as ONE payload: the frontend always receives a JSON array
	// and spreads it, matching core.EventSink's variadic Emit contract.
	a.wails.Event.Emit(name, args)
}

// FlushEvents emits events_flushed with token behind every event emitted
// before this call. Wails delivers events in order, but apart from call
// results, so a call can resolve before its last events arrive; a caller that
// waits for the flush knows they have.
func (a *App) FlushEvents(token string) {
	a.emit("events_flushed", token)
}

func (a *App) ActiveChannelID() int64 {
	if a.engine == nil {
		return 0
	}
	return a.engine.ActiveChannelID()
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
	a.stopPhotoBackup()
	if err := a.engine.SetActiveChannel(channelID); err != nil {
		return err
	}
	a.revokeGalleryImages()
	return nil
}

type TDriveFile struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	AccessHash int64  `json:"access_hash"`
	Date       int    `json:"date"`
	UploaderID int64  `json:"uploader_id"`
}

type PreviewPayload struct {
	DataBase64 string `json:"data_base64"`
	MimeType   string `json:"mime_type"`
}

func (a *App) CheckLoginStatus() bool {
	if a.authReady.Load() {
		return true
	}
	svc := a.authService()
	loggedIn := svc != nil && svc.IsLoggedIn(a.ctx)
	if loggedIn {
		a.markAuthenticated()
	} else {
		a.authReady.Store(false)
	}
	return loggedIn
}

func (a *App) SelectFiles() ([]string, error) {
	uploadfilepaths, err := a.wails.Dialog.OpenFile().
		SetTitle("Select files to upload").
		PromptForMultipleSelection()
	if err != nil {
		return nil, err
	}
	if application.System.IsMobile() {
		a.transfers.rememberPickerSources(uploadfilepaths)
	}
	return uploadfilepaths, nil
}

// ReleaseUploadSources discards abandoned mobile picker copies. Only paths
// registered by SelectFiles remain eligible; caller-owned files and sources
// already transferred into durable upload storage are never removed.
func (a *App) ReleaseUploadSources(paths []string) OperationResult {
	var failures []error
	for _, path := range paths {
		if !a.transfers.takePickerSource(path) {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			a.transfers.rememberPickerSources([]string{path})
			failures = append(failures, fmt.Errorf("release picked upload source: %w", err))
			continue
		}
		removePickerSource(path)
	}
	return operationFailure(errors.Join(failures...))
}

// SelectFolder opens a directory picker and returns the chosen folder path
// (empty if the user cancels). Folder import walks it on the Go side.
func (a *App) SelectFolder() (string, error) {
	return a.wails.Dialog.OpenFile().
		CanChooseFiles(false).
		CanChooseDirectories(true).
		SetTitle("Select a folder to upload").
		PromptForSingleSelection()
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
	a.transfers.cancel(uploadTransfer)
}

// CancelUploadByID cancels one file of the running batch. Both cancels exist
// because uploads run several at a time: a transfer row's × stops that one
// file, while CancelUpload above is the deliberate "stop everything".
func (a *App) CancelUploadByID(uploadID int) {
	if svc := a.fileService(); svc != nil {
		svc.CancelUpload(uploadID)
	}
}

// PauseUploads holds back the files of the running upload that have not
// started. Files already sending finish: an ordinary upload cannot stop part
// way and continue later.
func (a *App) PauseUploads() {
	if svc := a.fileService(); svc != nil {
		svc.PauseUploads()
	}
}

// ResumeUploads lets held-back files start again.
func (a *App) ResumeUploads() {
	if svc := a.fileService(); svc != nil {
		svc.ResumeUploads()
	}
}

// CancelDownload cancels the active download.
func (a *App) CancelDownload() {
	a.transfers.cancel(downloadTransfer)
}

// UploadToDriveFS uploads the selected batch. Encryption is chosen per batch;
// encrypted uploads require an already unlocked vault.
func (a *App) UploadToDriveFS(filePaths []string, parentIDs []string, encrypt bool) UploadResult {
	return a.uploadToDriveFS(filePaths, parentIDs, encrypt, a.ActiveChannelID(), false)
}

// RetryUploadToDriveFS pins the original destination and never replaces another
// upload. Callers select only failures explicitly reported as safe to retry.
func (a *App) RetryUploadToDriveFS(filePaths []string, parentIDs []string, encrypt bool, expectedChannelID int64) UploadResult {
	if expectedChannelID == 0 || a.ActiveChannelID() != expectedChannelID {
		err := errors.New("upload destination changed")
		return UploadResult{Result: operationFailureMessage(err, "Switch back to the original drive before retrying these files"), Failures: fileservice.NotSentFailures(filePaths, err)}
	}
	return a.uploadToDriveFS(filePaths, parentIDs, encrypt, expectedChannelID, true)
}

func (a *App) uploadToDriveFS(filePaths []string, parentIDs []string, encrypt bool, channelID int64, retry bool) UploadResult {
	// Failures before the service call never sent anything: report them
	// per file so the selection can be retried without a new picker round.
	svc, err := a.requireFileService()
	if err != nil {
		return UploadResult{Result: operationFailure(err), Failures: fileservice.NotSentFailures(filePaths, err)}
	}
	var ctx context.Context
	var finish func()
	if retry {
		ctx, finish, err = a.transfers.resumeUpload(a.ctx)
		if err != nil {
			return UploadResult{Result: operationFailure(err), Failures: fileservice.NotSentFailures(filePaths, err)}
		}
	} else {
		ctx, finish = a.transfers.begin(a.ctx, uploadTransfer)
	}
	defer finish()
	sources := filePaths
	// Finished attempts retain their readable source bytes for safe manual
	// retries, but no longer own disposable queued picker entries.
	defer a.transfers.forgetPickerSources(sources)
	filePaths, staged, err := a.stageMobileUploadPaths(ctx, filePaths, encrypt)
	if err != nil {
		return UploadResult{Result: operationFailure(err), Failures: fileservice.NotSentFailures(sources, err)}
	}
	defer a.releaseUnreferencedUploadSources(svc, staged)
	a.sweepOrphanParts(ctx)
	files, failures, err := svc.UploadWithFailures(ctx, channelID, filePaths, parentIDs, encrypt)
	out := make([]backend.FileMetaData, 0, len(files))
	for _, f := range files {
		out = append(out, uploadMetaToBackend(f))
	}
	if err != nil {
		return UploadResult{Result: operationFailure(err), Files: out, Failures: failures}
	}
	return UploadResult{Result: operationSuccess(), Files: out, Failures: failures}
}

// ListResumableUploads returns backend-confirmed multipart upload checkpoints
// for the selected drive. The frontend's transfer history is not a receipt.
func (a *App) ListResumableUploads() ([]fileservice.ResumableUpload, error) {
	svc, err := a.requireFileService()
	if err != nil {
		return nil, err
	}
	return svc.ListResumableUploads(a.ctx, a.ActiveChannelID())
}

// ResumeResumableUpload continues one paused multipart upload. An empty source
// path reuses the original path; a selected replacement is verified by the
// service before it can contribute bytes to an existing upload.
func (a *App) ResumeResumableUpload(jobID, sourcePath string) UploadResult {
	channelID := a.ActiveChannelID()
	svc, err := a.requireFileService()
	if err != nil {
		return UploadResult{Result: operationFailure(err)}
	}
	ctx, finish, err := a.transfers.resumeUpload(a.ctx)
	if err != nil {
		return UploadResult{Result: operationFailure(err)}
	}
	defer finish()
	previousSource, err := svc.UploadJobSource(ctx, channelID, jobID)
	if err != nil {
		return UploadResult{Result: operationFailure(err)}
	}
	var staged []string
	if sourcePath != "" {
		paths, newStaged, stageErr := a.stageMobileUploadPaths(ctx, []string{sourcePath}, false)
		if stageErr != nil {
			return UploadResult{Result: operationFailure(stageErr)}
		}
		sourcePath, staged = paths[0], newStaged
	}
	defer a.releaseUnreferencedUploadSources(svc, append(staged, previousSource))
	meta, err := svc.ResumeUpload(ctx, channelID, jobID, sourcePath)
	if err != nil {
		return UploadResult{Result: operationFailure(err)}
	}
	return UploadResult{Result: operationSuccess(), Files: []backend.FileMetaData{uploadMetaToBackend(meta)}}
}

// PauseResumableUpload stops the current attempt while retaining its durable
// Telegram message checkpoints.
func (a *App) PauseResumableUpload(jobID string) OperationResult {
	svc, err := a.requireFileService()
	if err != nil {
		return operationFailure(err)
	}
	if !svc.PauseUpload(a.ActiveChannelID(), jobID) {
		return operationFailure(fmt.Errorf("resumable upload is not running"))
	}
	return operationSuccess()
}

// CancelResumableUpload abandons a job and removes only the remote parts that
// the backend has verified belong to that job.
func (a *App) CancelResumableUpload(jobID string) OperationResult {
	svc, err := a.requireFileService()
	if err != nil {
		return operationFailure(err)
	}
	source, err := svc.UploadJobSource(a.ctx, a.ActiveChannelID(), jobID)
	if err != nil {
		return operationFailure(err)
	}
	if err := svc.CancelResumableUpload(a.ctx, a.ActiveChannelID(), jobID); err != nil {
		return operationFailure(err)
	}
	a.releaseUnreferencedUploadSources(svc, []string{source})
	return operationSuccess()
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
// is set, otherwise uploaded as-is. Progress flows through aggregate import_*
// events.
func (a *App) ImportPaths(paths []string, parentID string, encrypt bool, extractArchives bool) OperationResult {
	return a.importPaths(paths, parentID, encrypt, extractArchives, a.ActiveChannelID(), false)
}

// ImportPathsForDrive starts a waiting import only in its original drive and
// never replaces an active upload.
func (a *App) ImportPathsForDrive(paths []string, parentID string, encrypt bool, extractArchives bool, expectedChannelID int64) OperationResult {
	if expectedChannelID == 0 || a.ActiveChannelID() != expectedChannelID {
		return operationFailureMessage(errors.New("upload destination changed"), "Switch back to the original drive before starting this import")
	}
	return a.importPaths(paths, parentID, encrypt, extractArchives, expectedChannelID, true)
}

func (a *App) importPaths(paths []string, parentID string, encrypt bool, extractArchives bool, channelID int64, queued bool) OperationResult {
	svc, err := a.requireFileService()
	if err != nil {
		return operationFailure(err)
	}
	var ctx context.Context
	var finish func()
	if queued {
		ctx, finish, err = a.transfers.resumeUpload(a.ctx)
		if err != nil {
			return operationFailure(err)
		}
	} else {
		ctx, finish = a.transfers.begin(a.ctx, uploadTransfer)
	}
	defer finish()
	defer a.transfers.forgetPickerSources(paths)
	a.sweepOrphanParts(ctx)
	return operationFailure(svc.RunImport(ctx, channelID, paths, parentID, encrypt, extractArchives))
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
	return nil, errBackendUnavailable
}

func (a *App) fileService() *fileservice.Service {
	if a.engine == nil {
		return nil
	}
	return a.engine.FileService()
}

func (a *App) requireFileService() (*fileservice.Service, error) {
	return engineFileService(a.engine)
}

func (a *App) requireReadService() (*readservice.Service, error) {
	return engineReadService(a.engine)
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
	return nil, errBackendUnavailable
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
	return nil, errBackendUnavailable
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
			UploaderID: f.UploaderID,
		})
	}
	return out
}

func (a *App) PreviewThumbnail(msgID int) PreviewResult {
	svc, err := a.requireFileService()
	if err != nil {
		return previewOperationResult(PreviewPayload{}, err)
	}
	payload, err := svc.PreviewThumbnail(a.ctx, a.ActiveChannelID(), msgID)
	return previewOperationResult(PreviewPayload(payload), err)
}

func (a *App) PreviewFile(msgID int) PreviewResult {
	svc, err := a.requireFileService()
	if err != nil {
		return previewOperationResult(PreviewPayload{}, err)
	}
	payload, err := svc.PreviewFile(a.ctx, a.ActiveChannelID(), msgID)
	return previewOperationResult(PreviewPayload(payload), err)
}

// DownloadFile downloads from the explicitly selected drive. The channel is an
// argument rather than a late ActiveChannelID lookup because a queued frontend
// transfer may begin after the user has switched drives.
func (a *App) DownloadFile(channelID int64, msgID int, TgMsgID int, requestID string) DownloadResult {
	if !validFrontendDownloadRequestID(requestID) || !validFrontendDownloadID(channelID) || !validFrontendDownloadID(int64(msgID)) || !validFrontendDownloadID(int64(TgMsgID)) {
		return DownloadResult{Result: operationFailure(errors.New("invalid download request identifiers"))}
	}
	svc, err := a.requireFileService()
	if err != nil {
		return DownloadResult{Result: operationFailure(err)}
	}
	downloadCtx, finish, err := a.transfers.beginDownload(a.appContext())
	if err != nil {
		return DownloadResult{Result: operationFailure(err)}
	}
	ctx := fileservice.WithDownloadProgressID(downloadCtx, requestID)
	defer finish()
	result := svc.StartResumableDownload(ctx, channelID, msgID, TgMsgID, a.chooseDownloadPath)
	// An iPhone download stays in the app container, so offer the share sheet
	// as soon as the bytes are on disk: Files can list the container, but
	// sending the file straight on is the thing worth saving a trip for.
	//
	// Android must not do this. There the host moves the download into public
	// Downloads once this call returns and deletes the sandbox copy, so a
	// chooser opened here would be holding a file that is about to vanish.
	shareCompletedIOSDownload(result)
	return downloadOperationResult(result)
}

// DownloadFolder restores the selected projected folder beneath a destination
// parent chosen once by the user. It shares the serialized/cancellable download
// slot with single-file downloads, so the existing CancelDownload action stops
// either transfer type.
func (a *App) DownloadFolder(channelID int64, folderID string, requestID string) DownloadResult {
	if !validFrontendDownloadRequestID(requestID) || strings.TrimSpace(folderID) == "" || !validFrontendDownloadID(channelID) {
		return DownloadResult{Result: operationFailure(errors.New("invalid download request identifiers"))}
	}
	svc, err := a.requireFileService()
	if err != nil {
		return DownloadResult{Result: operationFailure(err)}
	}
	downloadCtx, finish, err := a.transfers.beginDownload(a.appContext())
	if err != nil {
		return DownloadResult{Result: operationFailure(err)}
	}
	ctx := fileservice.WithDownloadProgressID(downloadCtx, requestID)
	defer finish()
	result := svc.DownloadFolder(ctx, channelID, folderID, a.chooseDownloadDir)
	return downloadOperationResult(result)
}

const (
	maxFrontendSafeInteger        int64 = 9_007_199_254_740_991
	maxFrontendDownloadRequestLen int   = 256
)

// Wails transports JavaScript numbers, so IDs outside the safe-integer range
// can silently change before reaching Go. Reject them at the native boundary.
func validFrontendDownloadID(id int64) bool {
	return id > 0 && id <= maxFrontendSafeInteger
}

func validFrontendDownloadRequestID(id string) bool {
	if id == "" || len(id) > maxFrontendDownloadRequestLen {
		return false
	}
	for _, r := range id {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func (a *App) DeleteFile(msgID int) OperationResult {
	svc, err := a.requireFileService()
	if err != nil {
		return operationFailure(err)
	}
	if err := svc.Delete(a.ctx, a.ActiveChannelID(), msgID); err != nil {
		return operationFailure(err)
	}
	return operationSuccess()
}

func (a *App) GetStorageUsed() (int64, error) {
	svc, err := a.requireReadService()
	if err != nil {
		return 0, err
	}
	return svc.StorageUsed(a.ActiveChannelID())
}

// NewApp builds the root service together with the domain services that hang
// off it, so run.go only has to register what services() returns. The version
// is the build stamp the updater compares releases against; it arrives here
// because only main knows it, and passing it in at construction keeps it from
// being a mutable field anyone could rewrite later.
func NewApp(version string) *App {
	app := &App{backendStartupDone: make(chan struct{})}
	app.initServices(version)
	return app
}

func (a *App) CheckSystemStatus() string {
	return a.authService().SystemStatus()
}

func (a *App) SaveSetup(apiId int, apiHash string) string {
	return a.authService().SaveSetup(apiId, apiHash)
}

func (a *App) SubmitCode(code string) {
	a.authService().SubmitCode(code)
}

func (a *App) SendHint(hint string) {
	a.authService().SendHint(hint)
}

func (a *App) SubmitPassword(password string) {
	a.authService().SubmitPassword(password)
}

func (a *App) CreateFolder(foldername string, parentID string) (backend.Folder, error) {
	return a.createFolder(foldername, parentID, a.ActiveChannelID())
}

// CreateFolderForDrive keeps queued folder preparation in its original drive.
func (a *App) CreateFolderForDrive(foldername string, parentID string, expectedChannelID int64) (backend.Folder, error) {
	if expectedChannelID == 0 || a.ActiveChannelID() != expectedChannelID {
		return backend.Folder{}, errors.New("upload destination changed; switch back to the original drive before creating this folder")
	}
	return a.createFolder(foldername, parentID, expectedChannelID)
}

func (a *App) createFolder(foldername string, parentID string, channelID int64) (backend.Folder, error) {
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

// ServiceShutdown runs on app exit. Tear down the shared Telegram connection
// so the background Run scope's goroutine exits cleanly.
func (a *App) ServiceShutdown() error {
	a.encryption.cancelBiometricAttempt()
	a.backendStartupMu.Lock()
	a.backendStopped = true
	a.backendStartupMu.Unlock()
	a.finishBackendStartup(context.Canceled)
	mountCtx, cancel := context.WithTimeout(context.Background(), encryptionMountTransitionTimeout)
	if err := a.shutdownMountController(mountCtx); err != nil {
		fmt.Printf("Warning: Failed to disconnect TDrive mount: %v\n", err)
	}
	cancel()
	a.stopTrashSweep()
	a.media.closeAllNativeMedia()
	a.closeGalleryImages()
	a.closePhotoBackup()
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
	return nil
}

// SetFileDropEnabled toggles whether the native OS file-drop handler (wired
// up once in run.go against the window) forwards drops to the frontend. The
// frontend turns it off for the duration of an internal drag-to-move: on
// macOS the webview's native drop destination otherwise intercepts the
// in-app HTML5 drag, which breaks the move and pops the upload dialog.
func (a *App) SetFileDropEnabled(enabled bool) {
	a.fileDropMu.Lock()
	a.fileDropEnabled = enabled
	a.fileDropMu.Unlock()
}

// fileDropAllowed reports whether the native file-drop handler in run.go
// should forward the current drop to the frontend.
func (a *App) fileDropAllowed() bool {
	a.fileDropMu.Lock()
	defer a.fileDropMu.Unlock()
	return a.fileDropEnabled
}

func (a *App) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	a.ctx = ctx

	// Android's home dir is /sdcard when HOME is unset, so os.UserConfigDir /
	// os.UserCacheDir land on shared external storage; point both at the
	// app-private sandbox instead. iOS resolves inside the app container
	// already and keeps its cache default (Library/Caches, excluded from
	// backups), but its data dir is pinned to the same place Wails reports.
	if application.System.IsMobile() {
		if p := application.Mobile.StoragePath(); p != "" {
			datadir.Set(p)
			if application.System.IsPlatform(application.PlatformAndroid) {
				datadir.SetCache(p)
			}
		}
	}

	lock, err := processlock.Acquire("gui")
	if err != nil {
		a.finishBackendStartup(fmt.Errorf("acquire backend lock: %w", err))
		if errors.Is(err, processlock.ErrAlreadyRunning) {
			fmt.Printf("TDrive backend is already running: %v\n", err)
		} else {
			fmt.Printf("Warning: Failed to acquire backend lock: %v\n", err)
		}
		a.quit()
		return nil
	}
	a.backendLock = lock
	applog.Init()
	a.updates.initUpdater()

	// Native file drop: hand the dropped absolute paths to the frontend, which
	// resolves the target folder and runs the import flow. Drop zones opt in via
	// the data-file-drop-target attribute. run.go registers the OS-level
	// handler once against the window; this just turns forwarding on.
	a.SetFileDropEnabled(true)

	engine, err := core.New(ctx, core.Config{
		Events: runtimeEventSink{app: a},
		Warnf: func(format string, args ...any) {
			fmt.Printf(format, args...)
		},
		Thumbs: newThumbnailCache(),
	})
	if err != nil {
		a.finishBackendStartup(fmt.Errorf("initialize backend: %w", err))
		fmt.Printf("Warning: Failed to init TDrive backend: %v\n", err)
		applog.Close()
		if releaseErr := a.backendLock.Release(); releaseErr != nil {
			fmt.Printf("Warning: backend lock release failed: %v\n", releaseErr)
		}
		a.backendLock = nil
		a.quit()
		return nil
	}
	a.engine = engine
	a.Client = engine.RawClient()
	if application.System.IsMobile() {
		a.sweepUnreferencedUploadSources()
	}
	if err := a.initPhotoBackup(); err != nil {
		fmt.Printf("Warning: Failed to initialize photo backup: %v\n", err)
	}
	_, mountInitErr := a.ensureMountController()
	if mountInitErr != nil {
		fmt.Printf("Warning: Failed to initialize TDrive mount: %v\n", mountInitErr)
	}
	a.startTrashSweep()

	fmt.Println("TDrive DB ready!")
	a.updates.finishCleanup(mountInitErr)
	a.finishBackendStartup(nil)
	return nil
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
			Revision:      f.Revision,
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

// GetAllFsMsgIDs lists the Telegram messages this drive already accounts for.
// The root listing subtracts them from the channel's history to show what was
// posted outside TDrive; a deleted file stays on the list, because its message
// is still ours until the trash purges it.
func (a *App) GetAllFsMsgIDs() ([]int, error) {
	svc, err := a.requireReadService()
	if err != nil {
		return nil, err
	}
	return svc.ManagedMsgIDs(a.ActiveChannelID())
}

func (a *App) GetFolderSize(folderID string) (int64, error) {
	svc, err := a.requireReadService()
	if err != nil {
		return 0, err
	}
	return svc.FolderSize(a.ActiveChannelID(), folderID)
}

func (a *App) GetFolderStats(parentID string) ([]projection.FolderStats, error) {
	svc, err := a.requireReadService()
	if err != nil {
		return nil, err
	}
	return svc.ChildFolderStats(a.ActiveChannelID(), parentID)
}

func (a *App) DeleteFolder(folderID string) OperationResult {
	svc, err := a.requireFolderService()
	if err != nil {
		return operationFailure(err)
	}
	if err := svc.Delete(a.ctx, a.ActiveChannelID(), folderID); err != nil {
		return operationFailure(err)
	}
	return operationSuccess()
}

func (a *App) MsgToTdriveSystem(msgID int, name string, size int64, parentID string) OperationResult {
	svc, err := a.requireFileService()
	if err != nil {
		return operationFailure(err)
	}
	if err := svc.MetaContext(a.ctx, a.ActiveChannelID(), msgID, name, size, parentID); err != nil {
		return operationFailure(err)
	}
	return operationSuccess()
}

func (a *App) RenameFile(msgID int, newName string) OperationResult {
	svc, err := a.requireFileService()
	if err != nil {
		return operationFailure(err)
	}
	if err := svc.Rename(a.ctx, a.ActiveChannelID(), msgID, newName); err != nil {
		return operationFailure(err)
	}
	return operationSuccess()
}

func (a *App) RenameFolder(folderID string, newName string) OperationResult {
	svc, err := a.requireFolderService()
	if err != nil {
		return operationFailure(err)
	}
	if err := svc.Rename(a.ActiveChannelID(), folderID, newName); err != nil {
		return operationFailure(err)
	}
	return operationSuccess()
}

func (a *App) MoveFile(msgID int, newParentID string) OperationResult {
	svc, err := a.requireFileService()
	if err != nil {
		return operationFailure(err)
	}
	if err := svc.Move(a.ctx, a.ActiveChannelID(), msgID, newParentID); err != nil {
		return operationFailure(err)
	}
	return operationSuccess()
}

func (a *App) MoveFolder(folderID string, newParentID string) OperationResult {
	svc, err := a.requireFolderService()
	if err != nil {
		return operationFailure(err)
	}
	if err := svc.Move(a.ActiveChannelID(), folderID, newParentID); err != nil {
		return operationFailure(err)
	}
	return operationSuccess()
}
