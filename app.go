package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"TDrive/backend"
	"TDrive/backend/auth"
	"TDrive/backend/backfill"
	"TDrive/backend/projection"
	authsvc "TDrive/backend/services/auth"
	encservice "TDrive/backend/services/encryption"
	fileservice "TDrive/backend/services/file"
	folderservice "TDrive/backend/services/folder"
	lifecycleservice "TDrive/backend/services/lifecycle"
	readservice "TDrive/backend/services/read"
	userservice "TDrive/backend/services/user"
	tdsync "TDrive/backend/sync"
	"TDrive/backend/tgclient"

	"github.com/gotd/td/telegram"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx        context.Context
	Client     *telegram.Client
	tg         tgclient.Client
	auth       *authsvc.Service
	enc        *encservice.Service
	files      *fileservice.Service
	folders    *folderservice.Service
	reads      *readservice.Service
	lifecycle  *lifecycleservice.Service
	users      *userservice.Service
	syncEngine *tdsync.Engine
	active     *lifecycleservice.ActiveDrive
	selfUserID atomic.Int64
	// fileDropEnabled tracks whether the native OS file-drop handler is
	// registered. The frontend toggles it off during an internal drag-to-move so
	// macOS does not intercept the in-app HTML5 drag.
	fileDropEnabled bool

	// transferMu guards the cancel handles for the active upload/import and the
	// active download (both serialized by the frontend).
	transferMu     sync.Mutex
	uploadCancel   context.CancelFunc
	downloadCancel context.CancelFunc
}

type peerResolverFn func(context.Context, int64) (tgclient.InputPeer, error)

func (f peerResolverFn) ResolvePeer(ctx context.Context, channelID int64) (tgclient.InputPeer, error) {
	return f(ctx, channelID)
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
	return a.channelPeer(ctx, channelID)
}

// channelPeer resolves the active drive's tgclient.InputPeer through the shared
// Telegram client. Used by every op that needs to send into Telegram; callers
// should not hold this across long operations.
func (a *App) channelPeer(ctx context.Context, channelID int64) (tgclient.InputPeer, error) {
	if a.tg == nil {
		return tgclient.InputPeer{}, fmt.Errorf("tg client not ready")
	}
	return a.tg.ResolveDriveChannel(ctx, channelID)
}

// emitAndProject sends a control op and projects it locally. Returns the
// Telegram msg_id used as the op's identity.
//
// On send failure: returns the error; nothing is projected.
// On project failure after a successful send: logs, returns the error so
// the caller can surface it. The op IS in Telegram and will be projected on
// the next sync.
func (a *App) emitAndProject(channelID int64, op projection.Op) (int64, error) {
	if a.tg == nil {
		return 0, fmt.Errorf("tg client not ready")
	}
	actorID, err := a.actorID(a.ctx)
	if err != nil {
		return 0, err
	}
	peer, err := a.channelPeer(a.ctx, channelID)
	if err != nil {
		return 0, err
	}
	header := projection.Format(op)
	msgID, err := a.tg.SendControl(a.ctx, peer, header, true)
	if err != nil {
		return 0, err
	}
	if _, err := projection.ProjectFromOp(backend.DB, channelID, msgID, op, actorID, header); err != nil {
		fmt.Printf("warn: projection failed after send msg=%d op=%s: %v\n", msgID, op.Type, err)
		return msgID, err
	}
	return msgID, nil
}

func (a *App) ActiveChannelID() int64 {
	if a.active == nil {
		return 0
	}
	return a.active.ID()
}

func (a *App) setActiveChannelID(channelID int64) {
	if a.active == nil {
		a.active = lifecycleservice.NewActiveDrive()
	}
	a.active.Set(channelID)
}

// MyUserID returns the logged-in Telegram user id (cached after first
// resolution). The frontend uses it to gate owner-only actions on shared
// drives. Returns an error if Telegram resolution fails — caller should
// default-deny the gated actions in that case.
func (a *App) MyUserID() (int64, error) {
	return a.actorID(a.ctx)
}

func (a *App) actorID(ctx context.Context) (int64, error) {
	if a.tg == nil {
		return 0, fmt.Errorf("tg client not ready")
	}
	if id := a.selfUserID.Load(); id != 0 {
		return id, nil
	}
	id, err := a.tg.SelfID(ctx)
	if err != nil {
		return 0, fmt.Errorf("self user id: %w", err)
	}
	if id == 0 {
		return 0, fmt.Errorf("self user id not found")
	}
	a.selfUserID.Store(id)
	return id, nil
}

func (a *App) SetActiveChannel(channelID int64) error {
	if channelID <= 0 {
		return fmt.Errorf("invalid channel id")
	}
	if backend.DB == nil {
		return fmt.Errorf("db not ready")
	}
	var got int64
	err := backend.DB.QueryRow(`SELECT channel_id FROM channels WHERE channel_id = ?`, channelID).Scan(&got)
	if err != nil {
		return fmt.Errorf("channel not known locally")
	}
	a.setActiveChannelID(channelID)
	return nil
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
	return a.authService().IsLoggedIn(a.ctx)
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
	if err := a.fileService().SweepOrphanParts(ctx, a.ActiveChannelID()); err != nil {
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
	ctx := a.beginUpload()
	defer a.endUpload()
	a.sweepOrphanParts(ctx)
	files, err := a.fileService().Upload(ctx, a.ActiveChannelID(), filePaths, parentIDs, encrypt)
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
	return svc.PlanImport(paths, encrypt, extractArchives)
}

// ImportPaths recreates the folder structure of the selected paths under
// parentID and uploads their files. Archives are extracted when extractArchives
// is set, otherwise uploaded as-is. Progress flows through the import_* and the
// per-file upload_* events.
func (a *App) ImportPaths(paths []string, parentID string, encrypt bool, extractArchives bool) error {
	svc := a.fileService()
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

func normalizeOpParent(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return projection.RootParent
	}
	return p
}

func (a *App) newFolderService() *folderservice.Service {
	return &folderservice.Service{
		DB:    backend.DB,
		TG:    a.tg,
		Peers: peerResolverFn(a.resolvePeer),
		EmitOp: func(channelID int64, op projection.Op) error {
			_, err := a.emitAndProject(channelID, op)
			return err
		},
		ActorID: func(ctx context.Context) (int64, error) {
			return a.actorID(ctx)
		},
		RequireEncryptionKey: func(encrypted bool) ([]byte, error) {
			key, err := a.encryptionService().RequireMasterKeyForFile(encrypted)
			if err != nil {
				return nil, ErrEncryptionPasswordRequired
			}
			return key, nil
		},
		Warnf: func(format string, args ...any) {
			fmt.Printf(format, args...)
		},
	}
}

func (a *App) folderService() *folderservice.Service {
	if a.folders == nil {
		a.folders = a.newFolderService()
	}
	return a.folders
}

func (a *App) newFileService() *fileservice.Service {
	return &fileservice.Service{
		DB:    backend.DB,
		TG:    a.tg,
		Peers: peerResolverFn(a.resolvePeer),
		EmitOp: func(channelID int64, op projection.Op) (int64, error) {
			return a.emitAndProject(channelID, op)
		},
		ActorID: func(ctx context.Context) (int64, error) {
			return a.actorID(ctx)
		},
		RequireEncryptionKey: func(encrypted bool) ([]byte, error) {
			key, err := a.encryptionService().RequireMasterKeyForFile(encrypted)
			if err != nil {
				return nil, ErrEncryptionPasswordRequired
			}
			return key, nil
		},
		MasterKeyForUpload: func(channelID int64, wantEncrypted bool) ([]byte, error) {
			return a.encryptionService().MasterKeyForUpload(channelID, wantEncrypted)
		},
		WriteCiphertextTemp: func(plain io.Reader, plaintextSize int64, masterKey []byte) (*os.File, error) {
			return a.encryptionService().WriteCiphertextTemp(plain, plaintextSize, masterKey)
		},
		CreateFolder: func(channelID int64, name, parentID string) (string, error) {
			f, err := a.folderService().Create(channelID, name, parentID)
			return f.ID, err
		},
		Events: runtimeEventSink{app: a},
		Warnf: func(format string, args ...any) {
			fmt.Printf(format, args...)
		},
		Thumbs: newThumbnailCache(),
	}
}

func (a *App) fileService() *fileservice.Service {
	if a.files == nil {
		a.files = a.newFileService()
	}
	return a.files
}

func (a *App) newReadService() *readservice.Service {
	return &readservice.Service{
		DB:    backend.DB,
		TG:    a.tg,
		Peers: peerResolverFn(a.resolvePeer),
	}
}

func (a *App) readService() *readservice.Service {
	if a.reads == nil {
		a.reads = a.newReadService()
	}
	return a.reads
}

func (a *App) newLifecycleService() *lifecycleservice.Service {
	return lifecycleservice.NewService(lifecycleservice.Config{
		DB:       backend.DB,
		Sync:     a.syncEngine,
		Backfill: backfill.NewRunner(backend.DB, a.tg, peerResolverFn(a.resolvePeer)),
		Active:   a.active,
		Events:   runtimeEventSink{app: a},
		PersonalChannel: func(ctx context.Context) (int64, error) {
			client, err := auth.Connect()
			if err != nil {
				return 0, fmt.Errorf("Could not connect: %w", err)
			}
			var channelID int64
			err = client.Run(ctx, func(ctx context.Context) error {
				id, err := auth.GetTDriveChannel(ctx, client)
				if err != nil {
					return err
				}
				channelID = id
				return nil
			})
			return channelID, err
		},
		Warnf: func(format string, args ...any) {
			fmt.Printf(format, args...)
		},
	})
}

func (a *App) lifecycleService() *lifecycleservice.Service {
	if a.lifecycle == nil {
		a.lifecycle = a.newLifecycleService()
	}
	return a.lifecycle
}

func (a *App) newUserService() *userservice.Service {
	return &userservice.Service{
		DB:    backend.DB,
		TG:    a.tg,
		Peers: peerResolverFn(a.resolvePeer),
		ActorID: func(ctx context.Context) (int64, error) {
			return a.actorID(ctx)
		},
		Active: a.ActiveChannelID,
	}
}

func (a *App) userService() *userservice.Service {
	if a.users == nil {
		a.users = a.newUserService()
	}
	return a.users
}

func (a *App) newAuthService() *authsvc.Service {
	return authsvc.NewService(runtimeEventSink{app: a})
}

func (a *App) authService() *authsvc.Service {
	if a.auth == nil {
		a.auth = a.newAuthService()
	}
	return a.auth
}

func (a *App) LoginPhoneNumber(phoneNumber string) error {
	return a.authService().StartLogin(a.ctx, phoneNumber)
}

func (a *App) InitDrive() string {
	return a.lifecycleService().InitDrive(a.ctx)
}

func (a *App) GetFileList() []TDriveFile {
	files, err := a.readService().TelegramRootFiles(a.ctx, a.ActiveChannelID())
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
	payload, err := a.fileService().PreviewThumbnail(a.ctx, a.ActiveChannelID(), msgID)
	if err != nil {
		return PreviewPayload{}, err
	}
	return PreviewPayload(payload), nil
}

func (a *App) PreviewFile(msgID int) (PreviewPayload, error) {
	payload, err := a.fileService().PreviewFile(a.ctx, a.ActiveChannelID(), msgID)
	if err != nil {
		return PreviewPayload{}, err
	}
	return PreviewPayload(payload), nil
}

func (a *App) DownloadFile(msgID int, TgMsgID int) DownloadResult {
	ctx := a.beginDownload()
	defer a.endDownload()
	result := a.fileService().Download(ctx, a.ActiveChannelID(), msgID, TgMsgID, func(defaultName string) (string, error) {
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
	if err := a.fileService().Delete(a.ctx, a.ActiveChannelID(), msgID); err != nil {
		return "Error: " + err.Error()
	}
	return "Success"
}

func (a *App) GetStorageUsed() (int64, error) {
	return a.readService().StorageUsed(a.ActiveChannelID())
}

func (a *App) GetCodech() chan string {
	return a.authService().Codech()
}

func (a *App) GetPassch() chan string {
	return a.authService().Passch()
}

func NewApp() *App {
	return &App{
		Client: nil,
		active: lifecycleservice.NewActiveDrive(),
	}
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
	folder, err := a.folderService().Create(channelID, foldername, parentID)
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
	if a.tg != nil {
		a.tg.Close()
	}
}

// enableFileDrop registers the native OS file-drop handler (idempotent).
func (a *App) enableFileDrop() {
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
	if a.fileDropEnabled {
		runtime.OnFileDropOff(a.ctx)
		a.fileDropEnabled = false
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	// Native file drop: hand the dropped absolute paths to the frontend, which
	// resolves the target folder and runs the import flow. Drop zones opt in via
	// the --wails-drop-target CSS property.
	a.enableFileDrop()

	if a.active == nil {
		a.active = lifecycleservice.NewActiveDrive()
	}

	a.tg = tgclient.NewGotd(auth.Connect)

	ac, err := auth.Connect()
	if err != nil {
		fmt.Println("Warning: Telegram connect failed (offline?):", err)
	} else {
		a.Client = ac
	}

	if err := backend.InitDB(); err != nil {
		fmt.Printf("Warning: Failed to init local db: %v\n", err)
		return
	}
	if err := backend.EnsureSchema(); err != nil {
		fmt.Printf("Warning: Failed to init db schema: %v\n", err)
		return
	}

	a.auth = a.newAuthService()
	a.enc = a.newEncryptionService()
	a.files = a.newFileService()
	a.folders = a.newFolderService()
	a.reads = a.newReadService()
	a.syncEngine = tdsync.NewEngine(backend.DB, a.tg, peerResolverFn(a.resolvePeer))
	a.lifecycle = a.newLifecycleService()
	a.users = a.newUserService()

	if savedID, err := auth.LoadConfig(); err == nil && savedID != 0 {
		if err := a.lifecycle.UsePersonalChannel(a.ctx, savedID); err != nil {
			fmt.Printf("Warning: migration failed: %v\n", err)
		}
	}

	fmt.Println("TDrive DB ready!")
}

// SyncChannel triggers an incremental sync for the given channel. Wails-bound
// for the future "Refresh" UI button and for debug.
func (a *App) SyncChannel(channelID int64) error {
	return a.lifecycleService().SyncChannel(a.ctx, channelID)
}

// RebuildProjection wipes and replays the local projection for a channel
// from the channel's replay_log. Hidden Wails method for debug/recovery.
func (a *App) RebuildProjection(channelID int64) error {
	return a.lifecycleService().RebuildProjection(channelID)
}

func (a *App) GetFolderContents(parentID string) (backend.FileSystem, error) {
	fs, err := a.readService().FolderContents(a.ActiveChannelID(), parentID)
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
	hits, err := a.readService().Search(a.ActiveChannelID(), query, limit)
	if err != nil {
		return nil, err
	}

	results := make([]backend.SearchResult, 0, len(hits))
	for _, h := range hits {
		results = append(results, backend.SearchResult{
			Type:       h.Type,
			ID:         h.ID,
			Name:       h.Name,
			ParentID:   h.ParentID,
			Size:       h.Size,
			UploadTime: h.UploadTime,
			UploaderID: h.UploaderID,
			Path:       h.Path,
		})
	}
	return results, nil
}

func (a *App) GetAllFsMsgIDs() ([]int, error) {
	return a.readService().AllFileMsgIDs(a.ActiveChannelID())
}

func (a *App) GetFolderSize(folderID string) (int64, error) {
	return a.readService().FolderSize(a.ActiveChannelID(), folderID)
}

func (a *App) DeleteFolder(folderID string) string {
	if err := a.folderService().Delete(a.ctx, a.ActiveChannelID(), folderID); err != nil {
		return "Error: " + err.Error()
	}
	return "Success"
}

func (a *App) MsgToTdriveSystem(msgID int, name string, size int64, parentID string) string {
	if err := a.fileService().Meta(a.ActiveChannelID(), msgID, name, size, parentID); err != nil {
		return "Error: " + err.Error()
	}
	return "Success"
}

func (a *App) RenameFile(msgID int, newName string) string {
	if err := a.fileService().Rename(a.ctx, a.ActiveChannelID(), msgID, newName); err != nil {
		return "Error: " + err.Error()
	}
	return "Success"
}

func (a *App) RenameFolder(folderID string, newName string) string {
	if err := a.folderService().Rename(a.ActiveChannelID(), folderID, newName); err != nil {
		return "Error: " + err.Error()
	}
	return "Success"
}

func (a *App) MoveFile(msgID int, newParentID string) string {
	if err := a.fileService().Move(a.ctx, a.ActiveChannelID(), msgID, newParentID); err != nil {
		return "Error: " + err.Error()
	}
	return "Success"
}

func (a *App) MoveFolder(folderID string, newParentID string) string {
	if err := a.folderService().Move(a.ActiveChannelID(), folderID, newParentID); err != nil {
		return "Error: " + err.Error()
	}
	return "Success"
}
