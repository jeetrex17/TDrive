package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math/rand"
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
	tdsync "TDrive/backend/sync"
	"TDrive/backend/tgclient"

	"github.com/google/uuid"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx             context.Context
	Codech          chan string
	Passch          chan string
	Client          *telegram.Client
	tg              tgclient.Client
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

/*

func (a *App) UploadToTelegram(fp string) string {
	channelid, err := auth.LoadConfig()
	if err != nil || channelid == 0 {
		return "Error: Drive ID not found"
	}

	freshClient, err := auth.Connect()
	if err != nil {
		return "Connection failed: " + err.Error()
	}

	var finalOutput string

	err = freshClient.Run(a.ctx, func(ctx context.Context) error {
		channels, err := freshClient.API().ChannelsGetChannels(ctx, []tg.InputChannelClass{
			&tg.InputChannel{ChannelID: channelid},
		})
		if err != nil {
			return fmt.Errorf("failed to get channel: %w", err)
		}

		var accessHash int64
		if chats, ok := channels.(*tg.MessagesChats); ok {
			for _, chat := range chats.Chats {
				if ch, ok := chat.(*tg.Channel); ok && ch.ID == channelid {
					accessHash = ch.AccessHash
					break
				}
			}
		}

		u := uploader.NewUploader(freshClient.API())
		fmt.Println("Uploading file:", fp)

		upload, err := u.FromPath(ctx, fp)
		if err != nil {
			return fmt.Errorf("upload failed: %w", err)
		}

		destination := &tg.InputPeerChannel{
			ChannelID:  channelid,
			AccessHash: accessHash,
		}

		pkgtosend := &tg.InputMediaUploadedDocument{
			File:     upload,
			MimeType: "application/octet-stream",
			Attributes: []tg.DocumentAttributeClass{
				&tg.DocumentAttributeFilename{
					FileName: filepath.Base(fp),
				},
			},
		}

		_, err = freshClient.API().MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{
			Peer:     destination,
			Media:    pkgtosend,
			RandomID: rand.Int63(),
			Message:  fmt.Sprintf("uploaded via Tdriv : ", filepath.Base(fp)),
		})
		if err != nil {
			return fmt.Errorf("send failed: %w", err)
		}

		finalOutput = fmt.Sprintf("Success! File uploaded. ID: %v", upload)
		return nil
	})
	if err != nil {
		return "Upload Error: " + err.Error()
	}

	return finalOutput
}

*/

// AIs Job
func extractMsgID(updates tg.UpdatesClass) int {
	switch u := updates.(type) {
	case *tg.Updates:
		for _, update := range u.Updates {
			if msg, ok := update.(*tg.UpdateNewMessage); ok {
				if m, ok := msg.Message.(*tg.Message); ok {
					return m.ID
				}
			}
			if msg, ok := update.(*tg.UpdateNewChannelMessage); ok {
				if m, ok := msg.Message.(*tg.Message); ok {
					return m.ID
				}
			}
		}
	case *tg.UpdatesCombined:
		for _, update := range u.Updates {
			if msg, ok := update.(*tg.UpdateNewChannelMessage); ok {
				if m, ok := msg.Message.(*tg.Message); ok {
					return m.ID
				}
			}
		}
	}
	return 0
}

type ProgressReader struct {
	Reader    io.Reader
	Total     int64
	Current   int64
	Ctx       context.Context
	LastPrint time.Time
	UploadID  int
}

func (pr *ProgressReader) Read(p []byte) (int, error) {
	n, err := pr.Reader.Read(p)

	if n > 0 {
		pr.Current += int64(n)
		if time.Since(pr.LastPrint) > 100*time.Millisecond {
			perct := 0.0
			if pr.Total > 0 {
				perct = (float64(pr.Current) / float64(pr.Total)) * 100
				if perct > 100 {
					perct = 100
				}
			}

			runtime.EventsEmit(pr.Ctx, "upload_progress", pr.UploadID, perct)

			pr.LastPrint = time.Now()
		}
	}

	if err != nil && err != io.EOF {
		return n, err
	}
	return n, err
}

// UploadToDriveFS uploads each chosen file to the active drive. The
// `encrypt` flag is a per-batch choice made in the upload-options modal:
// true means encrypt-and-upload (the encryption password must already
// be remembered for this app session before this call), false means plain
// upload regardless of encryption password state.
func (a *App) UploadToDriveFS(filePaths []string, parentIDs []string, encrypt bool) ([]backend.FileMetaData, error) {
	if len(filePaths) != len(parentIDs) {
		return nil, fmt.Errorf("filepaths and parentIDs length mismatch")
	}
	if backend.DB == nil {
		return nil, fmt.Errorf("db not ready")
	}
	channelID := a.ActiveChannelID()
	if channelID == 0 {
		return nil, fmt.Errorf("no active channel")
	}
	actorID, err := a.actorID(a.ctx)
	if err != nil {
		return nil, err
	}

	type uploadedResult struct {
		UploadID  int
		Meta      backend.FileMetaData
		RawHeader string
		Op        projection.Op
	}

	sem := make(chan struct{}, 3)
	var wg sync.WaitGroup
	var mu sync.Mutex

	uploaded := make([]uploadedResult, 0, len(filePaths))
	failed := 0

	for i := 0; i < len(filePaths); i++ {
		path := filePaths[i]
		pid := parentIDs[i]
		uploadID := i
		wg.Add(1)
		sem <- struct{}{}

		go func(uploadID int, path string, pid string) {
			defer wg.Done()
			defer func() { <-sem }()

			meta, op, header, err := a.uploadSingleFile(uploadID, path, pid, channelID, encrypt)
			if err != nil {
				mu.Lock()
				failed++
				mu.Unlock()
				runtime.EventsEmit(a.ctx, "upload_error", uploadID, filepath.Base(path), err.Error())
				return
			}

			mu.Lock()
			uploaded = append(uploaded, uploadedResult{
				UploadID:  uploadID,
				Meta:      meta,
				RawHeader: header,
				Op:        op,
			})
			mu.Unlock()
		}(uploadID, path, pid)
	}

	wg.Wait()

	uploadedFiles := make([]backend.FileMetaData, 0, len(uploaded))
	for _, item := range uploaded {
		uploadedFiles = append(uploadedFiles, item.Meta)
	}

	emitLocalIndexError := func(reason string) {
		for _, item := range uploaded {
			runtime.EventsEmit(a.ctx, "upload_error", item.UploadID, item.Meta.Name, reason)
		}
	}

	for _, item := range uploaded {
		if _, err := projection.ProjectFromOp(
			backend.DB,
			channelID,
			int64(item.Meta.TgMsgID),
			item.Op,
			actorID,
			item.RawHeader,
		); err != nil {
			emitLocalIndexError("local index write failed")
			return uploadedFiles, err
		}
	}

	for _, item := range uploaded {
		runtime.EventsEmit(a.ctx, "upload_complete", item.UploadID, item.Meta.Name)
	}

	if failed > 0 {
		return uploadedFiles, fmt.Errorf("%d uploads failed", failed)
	}
	return uploadedFiles, nil
}

func (a *App) uploadSingleFile(uploadID int, filePath string, parentID string, channelID int64, wantEncrypted bool) (backend.FileMetaData, projection.Op, string, error) {
	if channelID == 0 {
		return backend.FileMetaData{}, projection.Op{}, "", fmt.Errorf("drive channel id not found")
	}

	plainFile, err := os.Open(filePath)
	if err != nil {
		return backend.FileMetaData{}, projection.Op{}, "", err
	}
	defer plainFile.Close()

	info, err := plainFile.Stat()
	if err != nil {
		return backend.FileMetaData{}, projection.Op{}, "", err
	}
	filename := filepath.Base(filePath)
	plaintextSize := info.Size()

	masterKey, err := masterKeyForUpload(channelID, wantEncrypted)
	if err != nil {
		return backend.FileMetaData{}, projection.Op{}, "", err
	}
	encrypted := wantEncrypted

	// uploadSource is what the Telegram uploader streams. For plaintext,
	// it's the raw file. For encrypted, we materialise a temp ciphertext
	// file on disk and stream from there — keeps memory bounded and lets
	// the existing ProgressReader work without changes.
	var uploadSource *os.File = plainFile
	uploadSize := plaintextSize
	if encrypted {
		tempCipher, err := writeCiphertextTemp(plainFile, plaintextSize, masterKey)
		if err != nil {
			return backend.FileMetaData{}, projection.Op{}, "", fmt.Errorf("encrypt: %w", err)
		}
		defer func() {
			_ = tempCipher.Close()
			_ = os.Remove(tempCipher.Name())
		}()
		ciphInfo, err := tempCipher.Stat()
		if err != nil {
			return backend.FileMetaData{}, projection.Op{}, "", err
		}
		uploadSource = tempCipher
		uploadSize = ciphInfo.Size()
	}

	pu := &ProgressReader{
		Reader:    uploadSource,
		Total:     uploadSize,
		Ctx:       a.ctx,
		LastPrint: time.Now(),
		UploadID:  uploadID,
	}

	uploadTime := time.Now().Unix()
	op := projection.Op{
		Type:           projection.OpFileUpload,
		Parent:         normalizeOpParent(parentID),
		Name:           filename,
		FileSize:       uploadSize,
		FileUploadTime: uploadTime,
	}
	if encrypted {
		op.Encrypted = true
		op.PlaintextSize = plaintextSize
		op.EncryptionVersion = 1
	}
	header := projection.Format(op)
	caption := header + "\nTDrive: " + filename
	totalSize := uploadSize

	freshClient, err := auth.Connect()
	if err != nil {
		return backend.FileMetaData{}, projection.Op{}, "", err
	}

	var msgID int

	err = freshClient.Run(a.ctx, func(ctx context.Context) error {
		_, inputPeer, err := auth.ResolveDriveChannel(ctx, freshClient.API(), channelID)
		if err != nil {
			return err
		}

		u := uploader.NewUploader(freshClient.API())
		fmt.Printf("Starting upload: %s\n", filename)

		runtime.EventsEmit(a.ctx, "upload_start", uploadID, filename, totalSize, parentID)

		uploadResult, err := u.FromReader(ctx, filename, pu)
		if err != nil {
			return err
		}

		req := &tg.MessagesSendMediaRequest{
			Peer: inputPeer,
			Media: &tg.InputMediaUploadedDocument{
				File:      uploadResult,
				MimeType:  "application/octet-stream",
				ForceFile: true,
				Attributes: []tg.DocumentAttributeClass{
					&tg.DocumentAttributeFilename{FileName: filename},
				},
			},
			RandomID: rand.Int63(),
			Message:  caption,
		}

		updates, err := freshClient.API().MessagesSendMedia(ctx, req)
		if err != nil {
			return err
		}

		msgID = extractMsgID(updates)
		if msgID == 0 {
			return fmt.Errorf("upload success, but could not find msgID")
		}

		runtime.EventsEmit(a.ctx, "upload_progress", uploadID, 100.0)
		return nil
	})
	if err != nil {
		return backend.FileMetaData{}, projection.Op{}, "", err
	}

	return backend.FileMetaData{
		Name:       filename,
		Size:       totalSize,
		TgMsgID:    msgID,
		ParentID:   normalizeOpParent(parentID),
		UploadTime: uploadTime,
	}, op, header, nil
}

func normalizeOpParent(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return projection.RootParent
	}
	return p
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
	channelid := a.ActiveChannelID()
	if channelid == 0 {
		return nil
	}

	freshClient, err := auth.Connect()
	if err != nil {
		return nil
	}

	var fileList []TDriveFile

	err = freshClient.Run(a.ctx, func(ctx context.Context) error {
		_, peer, err := auth.ResolveDriveChannel(ctx, freshClient.API(), channelid)
		if err != nil {
			return err
		}

		req := &tg.MessagesGetHistoryRequest{
			Peer:  peer,
			Limit: 100,
		}

		result, err := freshClient.API().MessagesGetHistory(ctx, req)
		if err != nil {
			return err
		}

		var messages []tg.MessageClass
		switch r := result.(type) {
		case *tg.MessagesMessages:
			messages = r.Messages
		case *tg.MessagesMessagesSlice:
			messages = r.Messages
		case *tg.MessagesChannelMessages:
			messages = r.Messages
		}

		for _, msg := range messages {
			fullMsg, ok := msg.(*tg.Message)
			if !ok {
				continue
			}

			if docMedia, ok := fullMsg.Media.(*tg.MessageMediaDocument); ok {
				if doc, ok := docMedia.Document.(*tg.Document); ok {

					filename := "Unknown"
					for _, attr := range doc.Attributes {
						if fname, ok := attr.(*tg.DocumentAttributeFilename); ok {
							filename = fname.FileName
						}
					}

					newFile := TDriveFile{
						ID:         fullMsg.ID,
						Name:       filename,
						Size:       doc.Size,
						Date:       fullMsg.Date,
						AccessHash: doc.AccessHash,
					}

					fileList = append(fileList, newFile)
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil
	}

	return fileList
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
		masterKey, err := requireMasterKeyForFile(encrypted)
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
		masterKey, err := requireMasterKeyForFile(encrypted)
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
	if backend.DB == nil {
		return "Error: DB not ready"
	}
	channelid := a.ActiveChannelID()
	if channelid == 0 {
		return "Error: Drive ID not found"
	}

	if !projection.FileExists(backend.DB, channelid, int64(msgID)) {
		return "Error: File not found"
	}

	if encrypted, _, _, err := projection.FileEncryptionMeta(backend.DB, channelid, int64(msgID)); err == nil {
		if _, err := requireMasterKeyForFile(encrypted); err != nil {
			return "Error: " + ErrEncryptionPasswordRequired.Error()
		}
	}

	// Step 4 safety gate: in a shared drive, only the uploader may tomb a
	// file. Otherwise B could hide A's file for everyone with no recourse;
	// admin-delete will arrive with proper Telegram permission checking in
	// a later step.
	ch, err := projection.GetChannel(backend.DB, channelid)
	if err != nil {
		return "Error: " + err.Error()
	}
	if ch.Kind == projection.KindShared {
		actorID, aerr := a.actorID(a.ctx)
		if aerr != nil {
			return "Error: " + aerr.Error()
		}
		uploader, uerr := projection.FileUploader(backend.DB, channelid, int64(msgID))
		if uerr != nil {
			return "Error: " + uerr.Error()
		}
		if uploader == 0 || uploader != actorID {
			return "Error: Only the uploader can delete this file in a shared drive"
		}
	}

	// Tomb first: visibility convergence is the contract; body delete is
	// best-effort. If we deleted the body first and then failed to publish
	// a tomb, other clients would see "the message vanished" with no signal.
	tombOp := projection.Op{
		Type: projection.OpTomb,
		Obj:  fmt.Sprintf("%s%d", projection.FileIDPrefix, msgID),
	}
	if _, err := a.emitAndProject(channelid, tombOp); err != nil {
		return "Error: " + err.Error()
	}

	peer, err := a.channelPeer(a.ctx, channelid)
	if err != nil {
		// Tomb succeeded; body cleanup deferred. Visible state is correct.
		fmt.Printf("warn: tomb succeeded but peer resolve failed for msg=%d: %v\n", msgID, err)
		return "Success"
	}
	if err := a.tg.DeleteMessages(a.ctx, peer, []int64{int64(msgID)}); err != nil {
		fmt.Printf("warn: tomb succeeded but body delete failed for msg=%d: %v\n", msgID, err)
	}

	return "Success"
}

func (a *App) GetStorageUsed() (int64, error) {
	if backend.DB == nil {
		return 0, fmt.Errorf("db not ready")
	}
	channelID := a.ActiveChannelID()
	if channelID == 0 {
		return 0, nil
	}
	return projection.StorageUsed(backend.DB, channelID)
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

func (a *App) Greet(name string) string {
	return fmt.Sprintf("Hello %s, It's show time!", name)
}

func (a *App) SendHint(hint string) {
	runtime.EventsEmit(a.ctx, "gothint", hint)
}

func (a *App) SumbitPassword(password string) {
	a.Passch <- password
}

func (a *App) CreateFolder(foldername string, parentID string) (backend.Folder, error) {
	if backend.DB == nil {
		return backend.Folder{}, fmt.Errorf("db not ready")
	}
	channelID := a.ActiveChannelID()
	if channelID == 0 {
		return backend.Folder{}, fmt.Errorf("no active channel")
	}

	foldername = strings.TrimSpace(foldername)
	if foldername == "" {
		return backend.Folder{}, fmt.Errorf("folder name can't be empty")
	}
	parent := normalizeOpParent(parentID)
	if parent != projection.RootParent {
		if !projection.IsFolderID(parent) {
			return backend.Folder{}, fmt.Errorf("invalid parent folder id")
		}
		if !projection.FolderExists(backend.DB, channelID, parent) {
			return backend.Folder{}, fmt.Errorf("parent folder not found")
		}
	}
	taken, err := projection.FolderSiblingHasName(backend.DB, channelID, parent, foldername)
	if err != nil {
		return backend.Folder{}, err
	}
	if taken {
		return backend.Folder{}, fmt.Errorf("folder '%s' already exists here", foldername)
	}

	folderID := projection.FolderIDPrefix + uuid.NewString()
	op := projection.Op{
		Type:   projection.OpMkdir,
		Obj:    folderID,
		Parent: parent,
		Name:   foldername,
	}
	if _, err := a.emitAndProject(channelID, op); err != nil {
		return backend.Folder{}, fmt.Errorf("create folder failed: %w", err)
	}

	return backend.Folder{
		ID:       folderID,
		Name:     foldername,
		ParentID: parent,
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
	if backend.DB == nil {
		return backend.FileSystem{}, fmt.Errorf("db not ready")
	}
	channelID := a.ActiveChannelID()
	if channelID == 0 {
		return backend.FileSystem{Folders: []backend.Folder{}, Files: []backend.FileMetaData{}}, nil
	}

	folders, files, err := projection.ListFolderContents(backend.DB, channelID, parentID)
	if err != nil {
		return backend.FileSystem{}, err
	}

	result := backend.FileSystem{
		Folders: make([]backend.Folder, 0, len(folders)),
		Files:   make([]backend.FileMetaData, 0, len(files)),
	}
	for _, f := range folders {
		result.Folders = append(result.Folders, backend.Folder{
			ID:       f.ID,
			Name:     f.Name,
			ParentID: f.ParentID,
		})
	}
	for _, f := range files {
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
	if backend.DB == nil {
		return nil, fmt.Errorf("db not ready")
	}
	channelID := a.ActiveChannelID()
	if channelID == 0 {
		return []backend.SearchResult{}, nil
	}

	query = strings.TrimSpace(query)
	if query == "" {
		return []backend.SearchResult{}, nil
	}

	allFolders, err := projection.ListAllFolders(backend.DB, channelID)
	if err != nil {
		return nil, err
	}
	folderMap := make(map[string]projection.FolderSlim, len(allFolders))
	for _, f := range allFolders {
		if f.ID != "" {
			folderMap[f.ID] = f
		}
	}

	buildFolderPath := func(folderID string) string {
		folderID = strings.TrimSpace(folderID)
		if folderID == projection.RootParent {
			return "My Drive"
		}
		names := make([]string, 0, 8)
		visited := make(map[string]bool)
		cur := folderID
		for cur != projection.RootParent && !visited[cur] {
			visited[cur] = true
			folder, ok := folderMap[cur]
			if !ok {
				break
			}
			if name := strings.TrimSpace(folder.Name); name != "" {
				names = append(names, name)
			}
			cur = strings.TrimSpace(folder.ParentID)
		}
		if len(names) == 0 {
			return "My Drive"
		}
		for i, j := 0, len(names)-1; i < j; i, j = i+1, j-1 {
			names[i], names[j] = names[j], names[i]
		}
		return "My Drive / " + strings.Join(names, " / ")
	}

	hits, err := projection.Search(backend.DB, channelID, query, limit)
	if err != nil {
		return nil, err
	}

	results := make([]backend.SearchResult, 0, len(hits))
	for _, h := range hits {
		switch h.Type {
		case "folder":
			results = append(results, backend.SearchResult{
				Type:     "folder",
				ID:       h.ID,
				Name:     h.Name,
				ParentID: h.ParentID,
				Path:     buildFolderPath(h.ID),
			})
		case "file":
			results = append(results, backend.SearchResult{
				Type:       "file",
				ID:         fmt.Sprintf("%d", h.MsgID),
				Name:       h.Name,
				ParentID:   h.ParentID,
				Size:       h.Size,
				UploadTime: h.Time,
				UploaderID: h.UploaderID,
				Path:       buildFolderPath(h.ParentID),
			})
		}
	}
	return results, nil
}

// GetOrphanedFiles returns files in the active channel whose parent is a
// tombstoned (or non-existent) folder. The frontend renders these in a
// virtual "Orphaned" bucket at root.
func (a *App) GetOrphanedFiles() ([]backend.FileMetaData, error) {
	if backend.DB == nil {
		return nil, fmt.Errorf("db not ready")
	}
	channelID := a.ActiveChannelID()
	if channelID == 0 {
		return []backend.FileMetaData{}, nil
	}
	files, err := projection.OrphanedFiles(backend.DB, channelID)
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
	if backend.DB == nil {
		return nil, fmt.Errorf("db not ready")
	}
	channelID := a.ActiveChannelID()
	if channelID == 0 {
		return []int{}, nil
	}
	ids64, err := projection.AllFileMsgIDs(backend.DB, channelID)
	if err != nil {
		return nil, err
	}
	out := make([]int, 0, len(ids64))
	for _, id := range ids64 {
		out = append(out, int(id))
	}
	return out, nil
}

func (a *App) GetFolderSize(folderID string) (int64, error) {
	if backend.DB == nil {
		return 0, fmt.Errorf("db not ready")
	}
	channelID := a.ActiveChannelID()
	if channelID == 0 {
		return 0, fmt.Errorf("no active channel")
	}
	return projection.FolderSize(backend.DB, channelID, folderID)
}

func (a *App) DeleteFolder(folderID string) string {
	if backend.DB == nil {
		return "Error: DB not ready"
	}
	channelid := a.ActiveChannelID()
	if channelid == 0 {
		return "Error: No active channel"
	}
	if !projection.IsFolderID(folderID) || !projection.FolderExists(backend.DB, channelid, folderID) {
		return "Error: Folder not found"
	}

	op := projection.Op{Type: projection.OpRmdir, Obj: folderID}
	if _, err := a.emitAndProject(channelid, op); err != nil {
		return "Error: " + err.Error()
	}

	return "Success"
}

func (a *App) MsgToTdriveSystem(msgID int, name string, size int64, parentID string) string {
	if backend.DB == nil {
		return "Error: DB not ready"
	}
	channelID := a.ActiveChannelID()
	if channelID == 0 {
		return "Error: No active channel"
	}
	if msgID <= 0 {
		return "Error: Invalid msgID"
	}

	if projection.FileExists(backend.DB, channelID, int64(msgID)) {
		return "Success"
	}

	parent := normalizeOpParent(parentID)
	if parent != projection.RootParent {
		if !projection.IsFolderID(parent) {
			return "Error: Invalid parent folder id"
		}
		if !projection.FolderExists(backend.DB, channelID, parent) {
			return "Error: Target folder not found"
		}
	}
	cleanName := strings.TrimSpace(name)
	if cleanName == "" {
		cleanName = "Untitled"
	}

	op := projection.Op{
		Type:           projection.OpMeta,
		Obj:            fmt.Sprintf("%s%d", projection.FileIDPrefix, msgID),
		Parent:         parent,
		Name:           cleanName,
		FileSize:       size,
		FileUploadTime: time.Now().Unix(),
	}
	if _, err := a.emitAndProject(channelID, op); err != nil {
		return "Error: " + err.Error()
	}
	return "Success"
}

func (a *App) RenameFile(msgID int, newName string) string {
	if backend.DB == nil {
		return "Error: DB not ready"
	}
	channelID := a.ActiveChannelID()
	if channelID == 0 {
		return "Error: No active channel"
	}
	newName = strings.TrimSpace(newName)
	if newName == "" {
		return "Error: Invalid name"
	}
	if !projection.FileExists(backend.DB, channelID, int64(msgID)) {
		return "Error: File not found"
	}

	// Same owner-only gate as DeleteFile in shared drives. Frontend hides
	// the action for non-owners but the backend stays authoritative.
	ch, err := projection.GetChannel(backend.DB, channelID)
	if err != nil {
		return "Error: " + err.Error()
	}
	if ch.Kind == projection.KindShared {
		actorID, aerr := a.actorID(a.ctx)
		if aerr != nil {
			return "Error: " + aerr.Error()
		}
		uploader, uerr := projection.FileUploader(backend.DB, channelID, int64(msgID))
		if uerr != nil {
			return "Error: " + uerr.Error()
		}
		if uploader == 0 || uploader != actorID {
			return "Error: Only the uploader can rename this file in a shared drive"
		}
	}

	op := projection.Op{
		Type: projection.OpRename,
		Obj:  fmt.Sprintf("%s%d", projection.FileIDPrefix, msgID),
		Name: newName,
	}
	if _, err := a.emitAndProject(channelID, op); err != nil {
		return "Error: " + err.Error()
	}
	return "Success"
}

func (a *App) RenameFolder(folderID string, newName string) string {
	if backend.DB == nil {
		return "Error: DB not ready"
	}
	channelID := a.ActiveChannelID()
	if channelID == 0 {
		return "Error: No active channel"
	}
	newName = strings.TrimSpace(newName)
	if newName == "" {
		return "Error: Invalid name"
	}
	if !projection.IsFolderID(folderID) || !projection.FolderExists(backend.DB, channelID, folderID) {
		return "Error: Folder not found"
	}
	op := projection.Op{
		Type: projection.OpRename,
		Obj:  folderID,
		Name: newName,
	}
	if _, err := a.emitAndProject(channelID, op); err != nil {
		return "Error: " + err.Error()
	}
	return "Success"
}

func (a *App) MoveFile(msgID int, newParentID string) string {
	if backend.DB == nil {
		return "Error: DB not ready"
	}
	channelID := a.ActiveChannelID()
	if channelID == 0 {
		return "Error: No active channel"
	}
	parent := normalizeOpParent(newParentID)
	if parent != projection.RootParent {
		if !projection.IsFolderID(parent) {
			return "Error: Invalid target folder id"
		}
		if !projection.FolderExists(backend.DB, channelID, parent) {
			return "Error: Target folder not found"
		}
	}
	cur, err := projection.FileParent(backend.DB, channelID, int64(msgID))
	if err != nil {
		return "Error: File not found"
	}
	if cur == parent {
		return "Error: File is already in this folder"
	}
	op := projection.Op{
		Type:   projection.OpMove,
		Obj:    fmt.Sprintf("%s%d", projection.FileIDPrefix, msgID),
		Parent: parent,
	}
	if _, err := a.emitAndProject(channelID, op); err != nil {
		return "Error: " + err.Error()
	}
	return "Success"
}

func (a *App) MoveFolder(folderID string, newParentID string) string {
	if backend.DB == nil {
		return "Error: DB not ready"
	}
	channelID := a.ActiveChannelID()
	if channelID == 0 {
		return "Error: No active channel"
	}
	if !projection.IsFolderID(folderID) {
		return "Error: Invalid folder id"
	}
	parent := normalizeOpParent(newParentID)
	if folderID == parent {
		return "Error: Cannot move folder into its own subfolder"
	}
	if !projection.FolderExists(backend.DB, channelID, folderID) {
		return "Error: Folder not found"
	}
	cur, err := projection.FolderParent(backend.DB, channelID, folderID)
	if err != nil {
		return "Error: Folder not found"
	}
	if cur == parent {
		return "Error: Folder is already here"
	}
	if parent != projection.RootParent {
		if !projection.IsFolderID(parent) {
			return "Error: Invalid target folder id"
		}
		if !projection.FolderExists(backend.DB, channelID, parent) {
			return "Error: Target folder not found"
		}
		isAnc, err := projection.IsAncestor(backend.DB, channelID, folderID, parent)
		if err != nil {
			return "Error: " + err.Error()
		}
		if isAnc {
			return "Error: Cannot move folder into its own subfolder"
		}
	}
	op := projection.Op{
		Type:   projection.OpMove,
		Obj:    folderID,
		Parent: parent,
	}
	if _, err := a.emitAndProject(channelID, op); err != nil {
		return "Error: " + err.Error()
	}
	return "Success"
}
