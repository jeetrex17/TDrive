package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"

	"TDrive/backend"
	"TDrive/backend/auth"
	"TDrive/backend/backfill"
	"TDrive/backend/projection"
	encservice "TDrive/backend/services/encryption"
	fileservice "TDrive/backend/services/file"
	folderservice "TDrive/backend/services/folder"
	lifecycleservice "TDrive/backend/services/lifecycle"
	readservice "TDrive/backend/services/read"
	tdsync "TDrive/backend/sync"
	"TDrive/backend/tgclient"

	"github.com/gotd/td/telegram"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx        context.Context
	Codech     chan string
	Passch     chan string
	Client     *telegram.Client
	tg         tgclient.Client
	enc        *encservice.Service
	files      *fileservice.Service
	folders    *folderservice.Service
	reads      *readservice.Service
	lifecycle  *lifecycleservice.Service
	syncEngine *tdsync.Engine
	active     *lifecycleservice.ActiveDrive
	selfUserID atomic.Int64
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

// channelPeer resolves the active drive's tgclient.InputPeer. Used by every
// op that needs to send into Telegram. Resolution may hit Telegram once per
// call; callers should not hold this across long operations.
func (a *App) channelPeer(ctx context.Context, channelID int64) (tgclient.InputPeer, error) {
	client, err := auth.Connect()
	if err != nil {
		return tgclient.InputPeer{}, err
	}
	var peer tgclient.InputPeer
	err = client.Run(ctx, func(rctx context.Context) error {
		_, ip, err := auth.ResolveDriveChannel(rctx, client.API(), channelID)
		if err != nil {
			return err
		}
		peer = tgclient.InputPeer{ChannelID: ip.ChannelID, AccessHash: ip.AccessHash}
		return nil
	})
	return peer, err
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
	if a.ctx == nil {
		return false
	}
	login, err := auth.CheckLogin(a.ctx)
	if err != nil {
		fmt.Println("error auto login", err)
		return false
	}

	return login
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

// UploadToDriveFS uploads each chosen file to the active drive. The
// `encrypt` flag is a per-batch choice made in the upload-options modal:
// true means encrypt-and-upload (the encryption password must already
// be remembered for this app session before this call), false means plain
// upload regardless of encryption password state.
func (a *App) UploadToDriveFS(filePaths []string, parentIDs []string, encrypt bool) ([]backend.FileMetaData, error) {
	files, err := a.fileService().Upload(a.ctx, a.ActiveChannelID(), filePaths, parentIDs, encrypt)
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
		DB: backend.DB,
		EmitOp: func(channelID int64, op projection.Op) error {
			_, err := a.emitAndProject(channelID, op)
			return err
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
		Events: runtimeEventSink{app: a},
		Warnf: func(format string, args ...any) {
			fmt.Printf(format, args...)
		},
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

func (a *App) LoginPhoneNumber(phoneNumber string) {
	client, err := auth.Connect()
	if err != nil {
		fmt.Println("Could not connect to Telegram:", err)
		return
	}

	go func() {
		err := auth.StartLogin(a.ctx, client, a, phoneNumber)
		if err != nil {
			fmt.Println("Login failed:", err)

			return
		}

		fmt.Println("Login Flow Complete. Emitting Success Event.")
		runtime.EventsEmit(a.ctx, "login-success", true)
	}()
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
	result := a.fileService().Download(a.ctx, a.ActiveChannelID(), msgID, TgMsgID, func(defaultName string) (string, error) {
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
	return a.Codech
}

func (a *App) GetPassch() chan string {
	return a.Passch
}

func NewApp() *App {
	return &App{
		ctx:    nil,
		Codech: make(chan string),
		Passch: make(chan string),
		Client: nil,
		active: lifecycleservice.NewActiveDrive(),
	}
}

func (a *App) CheckSystemStatus() string {
	_, err := auth.LoadImpCredentials()
	if err != nil {
		return "NEEDS_SETUP"
	}
	return "READY_FOR_LOGIN"
}

func (a *App) SaveSetup(apiId int, apiHash string) string {
	err := auth.SaveImpCredentials(apiId, apiHash)
	if err != nil {
		return "Error: " + err.Error()
	}
	return "Success"
}

func (a *App) SumbitCode(code string) {
	a.Codech <- code
}

func (a *App) SendHint(hint string) {
	runtime.EventsEmit(a.ctx, "gothint", hint)
}

func (a *App) SumbitPassword(password string) {
	a.Passch <- password
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

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
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

	a.enc = a.newEncryptionService()
	a.files = a.newFileService()
	a.folders = a.newFolderService()
	a.reads = a.newReadService()
	a.syncEngine = tdsync.NewEngine(backend.DB, a.tg, peerResolverFn(a.resolvePeer))
	a.lifecycle = a.newLifecycleService()

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

// GetOrphanedFiles returns files in the active channel whose parent is a
// tombstoned (or non-existent) folder. The frontend renders these in a
// virtual "Orphaned" bucket at root.
func (a *App) GetOrphanedFiles() ([]backend.FileMetaData, error) {
	files, err := a.readService().OrphanedFiles(a.ActiveChannelID())
	if err != nil {
		return nil, err
	}
	out := make([]backend.FileMetaData, 0, len(files))
	for _, f := range files {
		out = append(out, backend.FileMetaData{
			TgMsgID:    int(f.MsgID),
			Name:       f.Name,
			Size:       f.Size,
			ParentID:   f.ParentID,
			UploadTime: f.UploadTime,
			UploaderID: f.UploaderID,
		})
	}
	return out, nil
}

func (a *App) GetAllFsMsgIDs() ([]int, error) {
	return a.readService().AllFileMsgIDs(a.ActiveChannelID())
}

func (a *App) GetFolderSize(folderID string) (int64, error) {
	return a.readService().FolderSize(a.ActiveChannelID(), folderID)
}

func (a *App) DeleteFolder(folderID string) string {
	if err := a.folderService().Delete(a.ActiveChannelID(), folderID); err != nil {
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
	if err := a.fileService().Move(a.ActiveChannelID(), msgID, newParentID); err != nil {
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
