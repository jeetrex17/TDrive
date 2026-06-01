package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"TDrive/backend"
	"TDrive/backend/auth"
	"TDrive/backend/backfill"
	tdcrypto "TDrive/backend/crypto"
	"TDrive/backend/projection"
	encservice "TDrive/backend/services/encryption"
	fileservice "TDrive/backend/services/file"
	folderservice "TDrive/backend/services/folder"
	readservice "TDrive/backend/services/read"
	tdsync "TDrive/backend/sync"
	"TDrive/backend/tgclient"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx             context.Context
	Codech          chan string
	Passch          chan string
	Client          *telegram.Client
	tg              tgclient.Client
	enc             *encservice.Service
	files           *fileservice.Service
	folders         *folderservice.Service
	reads           *readservice.Service
	syncEngine      *tdsync.Engine
	backfillRunner  *backfill.Runner
	backfillMu      sync.Mutex
	backfilling     map[int64]bool
	previewMu       sync.Mutex
	activeChannelID atomic.Int64
	selfUserID      atomic.Int64
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
	return a.activeChannelID.Load()
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
	a.activeChannelID.Store(channelID)
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

const maxPreviewPayloadBytes int64 = 10 * 1024 * 1024

var (
	errPreviewNotFound                   = errors.New("File not found")
	errPreviewNotSupported               = errors.New("Not a supported image")
	errPreviewTooLarge                   = errors.New("File too large")
	errPreviewDownloadFailed             = errors.New("Download failed")
	errPreviewThumbMissing               = errors.New("Preview thumbnail unavailable")
	errPreviewEncryptionPasswordRequired = errors.New("encryption password required")
)

var previewMimeTypes = map[string]string{
	"jpg":  "image/jpeg",
	"jpeg": "image/jpeg",
	"png":  "image/png",
	"gif":  "image/gif",
	"webp": "image/webp",
	"bmp":  "image/bmp",
	"svg":  "image/svg+xml",
}

func previewFilenameFromDocument(doc *tg.Document) string {
	if doc == nil {
		return ""
	}

	for _, attr := range doc.Attributes {
		if fname, ok := attr.(*tg.DocumentAttributeFilename); ok {
			return strings.TrimSpace(fname.FileName)
		}
	}

	return ""
}

func lookupStoredFilename(channelID int64, msgID int, doc *tg.Document) string {
	if backend.DB != nil && channelID != 0 {
		if name := projection.LookupFileName(backend.DB, channelID, int64(msgID)); name != "" {
			return name
		}
	}

	return previewFilenameFromDocument(doc)
}

func previewMimeTypeForName(name string) (string, bool) {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(strings.TrimSpace(name))), ".")
	if ext == "" {
		return "", false
	}

	mimeType, ok := previewMimeTypes[ext]
	return mimeType, ok
}

func estimatedBase64Size(rawBytes int64) int64 {
	if rawBytes <= 0 {
		return 0
	}

	return ((rawBytes + 2) / 3) * 4
}

func exceedsPreviewPayloadBudget(rawBytes int64) bool {
	if rawBytes < 0 {
		return true
	}

	return estimatedBase64Size(rawBytes) > maxPreviewPayloadBytes
}

func detectedPreviewMimeType(data []byte, fallback string) string {
	detected := strings.TrimSpace(http.DetectContentType(data))
	if strings.HasPrefix(detected, "image/") {
		return detected
	}

	fallback = strings.TrimSpace(fallback)
	if fallback != "" {
		return fallback
	}

	return detected
}

func previewPayloadFromBytes(data []byte, mimeType string) (PreviewPayload, error) {
	if len(data) == 0 {
		return PreviewPayload{}, errPreviewDownloadFailed
	}
	if exceedsPreviewPayloadBudget(int64(len(data))) {
		return PreviewPayload{}, errPreviewTooLarge
	}

	return PreviewPayload{
		DataBase64: base64.StdEncoding.EncodeToString(data),
		MimeType:   detectedPreviewMimeType(data, mimeType),
	}, nil
}

func previewMessageFromResult(result tg.MessagesMessagesClass) *tg.Message {
	switch m := result.(type) {
	case *tg.MessagesChannelMessages:
		for _, message := range m.Messages {
			if targetMsg, ok := message.(*tg.Message); ok {
				return targetMsg
			}
		}
	case *tg.MessagesMessages:
		for _, message := range m.Messages {
			if targetMsg, ok := message.(*tg.Message); ok {
				return targetMsg
			}
		}
	case *tg.MessagesMessagesSlice:
		for _, message := range m.Messages {
			if targetMsg, ok := message.(*tg.Message); ok {
				return targetMsg
			}
		}
	}

	return nil
}

func previewThumbScore(size tg.PhotoSizeClass) int {
	switch thumb := size.(type) {
	case *tg.PhotoCachedSize:
		score := thumb.W * thumb.H
		if score <= 0 {
			score = len(thumb.Bytes)
		}
		return score
	case *tg.PhotoSize:
		score := thumb.W * thumb.H
		if score <= 0 {
			score = thumb.Size
		}
		return score
	case *tg.PhotoSizeProgressive:
		score := thumb.W * thumb.H
		if n := len(thumb.Sizes); n > 0 && thumb.Sizes[n-1] > score {
			score = thumb.Sizes[n-1]
		}
		return score
	default:
		return 0
	}
}

func previewInlineThumbPayload(doc *tg.Document, fallbackMimeType string) (PreviewPayload, bool, error) {
	if doc == nil {
		return PreviewPayload{}, false, nil
	}

	var best *tg.PhotoCachedSize
	bestScore := 0

	for _, size := range doc.Thumbs {
		thumb, ok := size.(*tg.PhotoCachedSize)
		if !ok || len(thumb.Bytes) == 0 {
			continue
		}

		score := previewThumbScore(thumb)
		if best == nil || score > bestScore {
			best = thumb
			bestScore = score
		}
	}

	if best == nil {
		return PreviewPayload{}, false, nil
	}

	payload, err := previewPayloadFromBytes(best.Bytes, fallbackMimeType)
	if err != nil {
		return PreviewPayload{}, true, err
	}
	return payload, true, nil
}

func previewThumbTypeForDocument(doc *tg.Document) (string, bool) {
	if doc == nil {
		return "", false
	}

	bestType := ""
	bestScore := 0

	for _, size := range doc.Thumbs {
		switch size.(type) {
		case *tg.PhotoSize, *tg.PhotoSizeProgressive:
		default:
			continue
		}

		thumbType := strings.TrimSpace(size.GetType())
		if thumbType == "" {
			continue
		}

		score := previewThumbScore(size)
		if bestType == "" || score > bestScore {
			bestType = thumbType
			bestScore = score
		}
	}

	return bestType, bestType != ""
}

func normalizePreviewError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, errPreviewNotFound):
		return errPreviewNotFound
	case errors.Is(err, errPreviewNotSupported):
		return errPreviewNotSupported
	case errors.Is(err, errPreviewTooLarge):
		return errPreviewTooLarge
	case errors.Is(err, errPreviewEncryptionPasswordRequired), errors.Is(err, ErrEncryptionPasswordRequired):
		return errPreviewEncryptionPasswordRequired
	default:
		return errPreviewDownloadFailed
	}
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
		RequireEncryptionKey: func(encrypted bool) error {
			if _, err := a.encryptionService().RequireMasterKeyForFile(encrypted); err != nil {
				return ErrEncryptionPasswordRequired
			}
			return nil
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
	if a.ctx == nil {
		return "Error: App context not ready"
	}

	var (
		output    string
		channelID int64
	)

	client, err := auth.Connect()
	if err != nil {
		return "Error: Could not connect: " + err.Error()
	}

	err = client.Run(a.ctx, func(ctx context.Context) error {
		id, err := auth.GetTDriveChannel(ctx, client)
		if err != nil {
			return err
		}
		channelID = id
		output = fmt.Sprintf("Success , channel ID: %d", id)
		return nil
	})
	if err != nil {
		return "Error: " + err.Error()
	}

	if channelID != 0 && backend.DB != nil {
		if err := backend.MigratePersonalChannel(channelID); err != nil {
			return "Error: migration failed: " + err.Error()
		}
		a.activeChannelID.Store(channelID)
		a.kickoffPersonalBackfill(channelID)
	}

	return output
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

type ProgressWriter struct {
	Writer    io.Writer
	Total     int64
	Current   int64
	Ctx       context.Context
	LastPrint time.Time
}

type PreviewProgressWriter struct {
	Writer    io.Writer
	Total     int64
	Current   int64
	Ctx       context.Context
	LastPrint time.Time
	MsgID     int
}

// i named it Dwrite first but then i learnt that
// WE MUST NAME THIS "Write" coz
// the Telegram library Stream() function demands an argument of type 'io.Writer'.
// In Go, to satisfy 'io.Writer', a struct MUST have a method with the exact signature:
//     Write(p []byte) (n int, err error)
//
// If we named this "DWrite" or anything else, this struct would not match the
// interface, and the compiler would reject it.

func (pw *ProgressWriter) Write(p []byte) (int, error) {
	n, err := pw.Writer.Write(p)
	if err != nil {
		return n, fmt.Errorf("erorr upladting downlad progress : %v", err)
	}

	pw.Current += int64(n)
	if time.Since(pw.LastPrint) > 100*time.Millisecond {
		var perct float64

		if pw.Total > 0 {
			perct = (float64(pw.Current) / float64(pw.Total)) * 100
		} else {
			perct = 100
		}

		runtime.EventsEmit(pw.Ctx, "download_progress", perct)

		pw.LastPrint = time.Now()
	}

	return n, nil
}

func (pw *PreviewProgressWriter) Write(p []byte) (int, error) {
	n, err := pw.Writer.Write(p)
	if err != nil {
		return n, fmt.Errorf("error updating preview progress: %v", err)
	}

	pw.Current += int64(n)
	if time.Since(pw.LastPrint) > 100*time.Millisecond {
		var perct float64

		if pw.Total > 0 {
			perct = (float64(pw.Current) / float64(pw.Total)) * 100
		} else {
			perct = 100
		}

		if perct > 100 {
			perct = 100
		}

		runtime.EventsEmit(pw.Ctx, "preview_progress", pw.MsgID, perct)
		pw.LastPrint = time.Now()
	}

	return n, nil
}

func (a *App) withPreviewSession(fn func(ctx context.Context, api *tg.Client, inChan *tg.InputChannel, channelID int64) error) error {
	if a.ctx == nil {
		return errPreviewDownloadFailed
	}

	channelID := a.ActiveChannelID()
	if channelID == 0 {
		return errPreviewDownloadFailed
	}

	a.previewMu.Lock()
	defer a.previewMu.Unlock()

	client, err := auth.Connect()
	if err != nil {
		return errPreviewDownloadFailed
	}

	return client.Run(a.ctx, func(ctx context.Context) error {
		inChan, _, err := auth.ResolveDriveChannel(ctx, client.API(), channelID)
		if err != nil {
			return errPreviewDownloadFailed
		}

		return fn(ctx, client.API(), inChan, channelID)
	})
}

func loadPreviewDocument(ctx context.Context, api *tg.Client, inChan *tg.InputChannel, channelID int64, msgID int) (*tg.Document, string, string, error) {
	result, err := api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
		Channel: inChan,
		ID: []tg.InputMessageClass{
			&tg.InputMessageID{ID: msgID},
		},
	})
	if err != nil {
		return nil, "", "", errPreviewDownloadFailed
	}

	targetMsg := previewMessageFromResult(result)
	if targetMsg == nil {
		return nil, "", "", errPreviewNotFound
	}

	docMedia, ok := targetMsg.Media.(*tg.MessageMediaDocument)
	if !ok {
		return nil, "", "", errPreviewNotFound
	}

	doc, ok := docMedia.Document.(*tg.Document)
	if !ok || doc == nil {
		return nil, "", "", errPreviewNotFound
	}

	filename := lookupStoredFilename(channelID, msgID, doc)
	mimeType, ok := previewMimeTypeForName(filename)
	if !ok {
		return nil, "", "", errPreviewNotSupported
	}

	return doc, filename, mimeType, nil
}

func (a *App) PreviewThumbnail(msgID int) (PreviewPayload, error) {
	if msgID <= 0 {
		return PreviewPayload{}, errPreviewNotFound
	}

	var payload PreviewPayload

	err := a.withPreviewSession(func(ctx context.Context, api *tg.Client, inChan *tg.InputChannel, channelID int64) error {
		doc, _, mimeType, err := loadPreviewDocument(ctx, api, inChan, channelID, msgID)
		if err != nil {
			return err
		}

		if inlinePayload, ok, err := previewInlineThumbPayload(doc, mimeType); ok || err != nil {
			if err != nil {
				return err
			}
			payload = inlinePayload
			return nil
		}

		thumbType, ok := previewThumbTypeForDocument(doc)
		if !ok {
			return errPreviewThumbMissing
		}

		location := &tg.InputDocumentFileLocation{
			ID:            doc.ID,
			AccessHash:    doc.AccessHash,
			FileReference: doc.FileReference,
			ThumbSize:     thumbType,
		}

		var buf bytes.Buffer
		d := downloader.NewDownloader()
		if _, err := d.Download(api, location).Stream(ctx, &buf); err != nil {
			return errPreviewDownloadFailed
		}

		payload, err = previewPayloadFromBytes(buf.Bytes(), mimeType)
		return err
	})
	if err != nil {
		return PreviewPayload{}, normalizePreviewError(err)
	}

	return payload, nil
}

func (a *App) PreviewFile(msgID int) (PreviewPayload, error) {
	if msgID <= 0 {
		return PreviewPayload{}, errPreviewNotFound
	}

	var payload PreviewPayload

	err := a.withPreviewSession(func(ctx context.Context, api *tg.Client, inChan *tg.InputChannel, channelID int64) error {
		doc, _, mimeType, err := loadPreviewDocument(ctx, api, inChan, channelID, msgID)
		if err != nil {
			return err
		}

		// For encrypted files, gate the preview budget on the plaintext
		// size and require the master key. Telegram's `doc.Size` is the
		// ciphertext size and doesn't reflect what the user will see.
		encrypted := false
		plaintextSize := int64(doc.Size)
		if backend.DB != nil {
			enc, psz, _, lookupErr := projection.FileEncryptionMeta(backend.DB, channelID, int64(msgID))
			if lookupErr == nil && enc {
				encrypted = true
				if psz > 0 {
					plaintextSize = psz
				}
			}
		}
		if exceedsPreviewPayloadBudget(plaintextSize) {
			return errPreviewTooLarge
		}
		masterKey, err := a.encryptionService().RequireMasterKeyForFile(encrypted)
		if err != nil {
			return err
		}

		var buf bytes.Buffer
		pw := &PreviewProgressWriter{
			Writer:    &buf,
			Total:     int64(doc.Size),
			Ctx:       a.ctx,
			LastPrint: time.Now(),
			MsgID:     msgID,
		}
		d := downloader.NewDownloader()
		if _, err := d.Download(api, doc.AsInputDocumentFileLocation()).Stream(ctx, pw); err != nil {
			return errPreviewDownloadFailed
		}

		if encrypted {
			var plain bytes.Buffer
			if _, err := tdcrypto.DecryptStream(&buf, &plain, masterKey); err != nil {
				return errPreviewDownloadFailed
			}
			runtime.EventsEmit(a.ctx, "preview_progress", msgID, 100.0)
			payload, err = previewPayloadFromBytes(plain.Bytes(), mimeType)
			return err
		}

		runtime.EventsEmit(a.ctx, "preview_progress", msgID, 100.0)
		payload, err = previewPayloadFromBytes(buf.Bytes(), mimeType)
		return err
	})
	if err != nil {
		return PreviewPayload{}, normalizePreviewError(err)
	}

	return payload, nil
}

func (a *App) DownloadFile(msgID int, TgMsgID int) DownloadResult {
	channelid := a.ActiveChannelID()
	if channelid == 0 {
		return DownloadResult{Status: "error", Message: "Drive ID not found"}
	}

	freshClient, err := auth.Connect()
	if err != nil {
		return DownloadResult{Status: "error", Message: "Connection error: " + err.Error()}
	}

	downloadResult := DownloadResult{Status: "error", Message: "Download failed"}

	err = freshClient.Run(a.ctx, func(ctx context.Context) error {
		inChan, _, err := auth.ResolveDriveChannel(ctx, freshClient.API(), channelid)
		if err != nil {
			downloadResult = DownloadResult{Status: "error", Message: "Error: " + err.Error()}
			return nil
		}

		targetID := []tg.InputMessageClass{&tg.InputMessageID{ID: msgID}}

		messageResult, err := freshClient.API().ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
			Channel: inChan,
			ID:      targetID,
		})
		if err != nil {
			return err
		}

		var targetMsg *tg.Message
		switch m := messageResult.(type) {
		case *tg.MessagesChannelMessages:
			if len(m.Messages) > 0 {
				targetMsg, _ = m.Messages[0].(*tg.Message)
			}
		}

		if targetMsg == nil {
			downloadResult = DownloadResult{Status: "error", Message: "Message deleted or not found"}
			return nil
		}

		docMedia, ok := targetMsg.Media.(*tg.MessageMediaDocument)
		if !ok {
			downloadResult = DownloadResult{Status: "error", Message: "This is not a file"}
			return nil
		}

		doc, ok := docMedia.Document.(*tg.Document)
		if !ok {
			downloadResult = DownloadResult{Status: "error", Message: "Empty document"}
			return nil
		}

		/*
			originalName := "tdrive_download"
			for _, attr := range doc.Attributes {
				if fname, ok := attr.(*tg.DocumentAttributeFilename); ok {
					originalName = fname.FileName
				}
			}
		*/

		originalName := "tdrive_download"
		lookupID := msgID
		if TgMsgID != 0 {
			lookupID = TgMsgID
		}
		if backend.DB != nil {
			if name := projection.LookupFileName(backend.DB, channelid, int64(lookupID)); name != "" {
				originalName = name
			}
		}

		// Decide ahead of time whether to decrypt. The per-file flag in
		// the projection is the source of truth — channel-wide enable
		// alone isn't enough because mixed plaintext+ciphertext history
		// is a normal state. Done BEFORE the save dialog so a missing
		// password doesn't make the user pick a save location twice.
		encrypted := false
		if backend.DB != nil {
			enc, _, _, err := projection.FileEncryptionMeta(backend.DB, channelid, int64(lookupID))
			if err == nil {
				encrypted = enc
			}
		}
		masterKey, err := a.encryptionService().RequireMasterKeyForFile(encrypted)
		if err != nil {
			downloadResult = DownloadResult{Status: "error", Message: ErrEncryptionPasswordRequired.Error()}
			return nil
		}

		savePath, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
			DefaultFilename: originalName,
			Title:           "Save File As...",
		})

		if err != nil {
			downloadResult = DownloadResult{Status: "error", Message: "Failed to choose download location: " + err.Error()}
			return nil
		}

		if savePath == "" {
			downloadResult = DownloadResult{Status: "canceled", Message: "Download canceled"}
			return nil
		}

		d := downloader.NewDownloader()

		f, err := os.Create(savePath)
		if err != nil {
			downloadResult = DownloadResult{Status: "error", Message: "Disk Error: " + err.Error()}
			return nil
		}
		defer f.Close()

		pw := &ProgressWriter{
			Writer:    f,
			Total:     int64(doc.Size),
			Ctx:       a.ctx,
			LastPrint: time.Now(),
		}

		if !encrypted {
			if _, err := d.Download(freshClient.API(), doc.AsInputDocumentFileLocation()).Stream(ctx, pw); err != nil {
				downloadResult = DownloadResult{Status: "error", Message: "Network Error: " + err.Error()}
				return nil
			}
		} else {
			// Stream ciphertext into a temp file (with progress so the
			// user sees something during the network step), then decrypt
			// into the user-chosen savePath. Two-step rather than piped
			// because the AEAD stream layer reads chunks of arbitrary
			// length and the gotd downloader does not produce them.
			cipher, err := os.CreateTemp("", "tdrive-dl-*")
			if err != nil {
				downloadResult = DownloadResult{Status: "error", Message: "Disk Error: " + err.Error()}
				return nil
			}
			defer func() {
				_ = cipher.Close()
				_ = os.Remove(cipher.Name())
			}()
			cipherPW := &ProgressWriter{
				Writer:    cipher,
				Total:     int64(doc.Size),
				Ctx:       a.ctx,
				LastPrint: time.Now(),
			}
			if _, err := d.Download(freshClient.API(), doc.AsInputDocumentFileLocation()).Stream(ctx, cipherPW); err != nil {
				downloadResult = DownloadResult{Status: "error", Message: "Network Error: " + err.Error()}
				return nil
			}
			if _, err := cipher.Seek(0, io.SeekStart); err != nil {
				downloadResult = DownloadResult{Status: "error", Message: "Disk Error: " + err.Error()}
				return nil
			}
			if _, err := tdcrypto.DecryptStream(cipher, f, masterKey); err != nil {
				_ = os.Remove(savePath)
				downloadResult = DownloadResult{Status: "error", Message: "Decrypt failed: " + err.Error()}
				return nil
			}
		}

		runtime.EventsEmit(a.ctx, "download_progress", 100.0)
		downloadResult = DownloadResult{
			Status:    "success",
			Message:   "Download complete",
			SavedPath: savePath,
		}
		return nil
	})
	if err != nil {
		return DownloadResult{Status: "error", Message: "System Error: " + err.Error()}
	}

	return downloadResult
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
	a.backfillRunner = backfill.NewRunner(backend.DB, a.tg, peerResolverFn(a.resolvePeer))
	a.backfilling = make(map[int64]bool)

	if savedID, err := auth.LoadConfig(); err == nil && savedID != 0 {
		if err := backend.MigratePersonalChannel(savedID); err != nil {
			fmt.Printf("Warning: migration failed: %v\n", err)
		} else {
			a.activeChannelID.Store(savedID)
			a.kickoffPersonalBackfill(savedID)
		}
	}

	fmt.Println("TDrive DB ready!")
}

// kickoffPersonalBackfill runs RunPersonal in a background goroutine if it
// isn't already running for this channel. Safe to call repeatedly — the
// per-channel guard prevents concurrent runs and the runner short-circuits
// if personal_backfill_done is already set.
func (a *App) kickoffPersonalBackfill(channelID int64) {
	a.backfillMu.Lock()
	if a.backfilling[channelID] {
		a.backfillMu.Unlock()
		return
	}
	a.backfilling[channelID] = true
	a.backfillMu.Unlock()

	go func() {
		defer func() {
			a.backfillMu.Lock()
			delete(a.backfilling, channelID)
			a.backfillMu.Unlock()
		}()
		err := a.backfillRunner.RunPersonal(a.ctx, channelID, func(ev backfill.ProgressEvent) {
			runtime.EventsEmit(a.ctx, "backfill_progress", ev.ChannelID, ev.Done, ev.Total, ev.Phase)
		})
		if err != nil {
			fmt.Printf("backfill: %v\n", err)
			runtime.EventsEmit(a.ctx, "backfill_error", channelID, err.Error())
		}
	}()
}

// SyncChannel triggers an incremental sync for the given channel. Wails-bound
// for the future "Refresh" UI button and for debug.
func (a *App) SyncChannel(channelID int64) error {
	if a.syncEngine == nil {
		return fmt.Errorf("sync engine not ready")
	}
	if channelID == 0 {
		channelID = a.ActiveChannelID()
	}
	if channelID == 0 {
		return fmt.Errorf("no active channel")
	}
	return a.syncEngine.Incremental(a.ctx, channelID)
}

// RebuildProjection wipes and replays the local projection for a channel
// from the channel's replay_log. Hidden Wails method for debug/recovery.
func (a *App) RebuildProjection(channelID int64) error {
	if backend.DB == nil {
		return fmt.Errorf("db not ready")
	}
	if channelID == 0 {
		channelID = a.ActiveChannelID()
	}
	if channelID == 0 {
		return fmt.Errorf("no active channel")
	}
	return projection.RebuildProjection(backend.DB, channelID)
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
