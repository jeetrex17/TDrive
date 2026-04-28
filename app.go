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
	"TDrive/backend/projection"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx              context.Context
	Codech           chan string
	Passch           chan string
	Client           *telegram.Client
	previewMu        sync.Mutex
	activeChannelID  atomic.Int64
}

func (a *App) ActiveChannelID() int64 {
	return a.activeChannelID.Load()
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
	errPreviewNotFound       = errors.New("File not found")
	errPreviewNotSupported   = errors.New("Not a supported image")
	errPreviewTooLarge       = errors.New("File too large")
	errPreviewDownloadFailed = errors.New("Download failed")
	errPreviewThumbMissing   = errors.New("Preview thumbnail unavailable")
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

// AIs Job
func (a *App) UploadToDriveFS(filePaths []string, parentIDs []string) ([]backend.FileMetaData, error) {
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

	type uploadedResult struct {
		UploadID int
		Meta     backend.FileMetaData
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

			meta, err := a.uploadSingleFile(uploadID, path, pid, channelID)
			if err != nil {
				mu.Lock()
				failed++
				mu.Unlock()
				runtime.EventsEmit(a.ctx, "upload_error", uploadID, filepath.Base(path), err.Error())
				return
			}

			mu.Lock()
			uploaded = append(uploaded, uploadedResult{
				UploadID: uploadID,
				Meta:     meta,
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

	files := make([]projection.FileSlim, 0, len(uploaded))
	for _, item := range uploaded {
		m := item.Meta
		files = append(files, projection.FileSlim{
			MsgID:      int64(m.TgMsgID),
			Name:       m.Name,
			Size:       m.Size,
			ParentID:   m.ParentID,
			UploadTime: m.UploadTime,
		})
	}
	if err := projection.LocalRegisterFiles(backend.DB, channelID, files, 0); err != nil {
		emitLocalIndexError("local index write failed")
		return uploadedFiles, err
	}

	for _, item := range uploaded {
		runtime.EventsEmit(a.ctx, "upload_complete", item.UploadID, item.Meta.Name)
	}

	if failed > 0 {
		return uploadedFiles, fmt.Errorf("%d uploads failed", failed)
	}
	return uploadedFiles, nil
}

func (a *App) uploadSingleFile(uploadID int, filePath string, parentID string, channelID int64) (backend.FileMetaData, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return backend.FileMetaData{}, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return backend.FileMetaData{}, err
	}
	filename := filepath.Base(filePath)
	totalSize := info.Size()

	pu := &ProgressReader{
		Reader:    f,
		Total:     totalSize,
		Ctx:       a.ctx,
		LastPrint: time.Now(),
		UploadID:  uploadID,
	}

	if channelID == 0 {
		return backend.FileMetaData{}, fmt.Errorf("drive channel id not found")
	}

	freshClient, err := auth.Connect()
	if err != nil {
		return backend.FileMetaData{}, err
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
			Message:  fmt.Sprintf("TDrive File: %s", filename),
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
		return backend.FileMetaData{}, err
	}

	return backend.FileMetaData{
		Name:       filename,
		Size:       totalSize,
		TgMsgID:    msgID,
		ParentID:   parentID,
		UploadTime: time.Now().Unix(),
	}, nil
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

		if exceedsPreviewPayloadBudget(int64(doc.Size)) {
			return errPreviewTooLarge
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

		_, err = d.Download(freshClient.API(), doc.AsInputDocumentFileLocation()).Stream(ctx, pw)
		if err != nil {
			downloadResult = DownloadResult{Status: "error", Message: "Network Error: " + err.Error()}
			return nil
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

	freshClient, err := auth.Connect()
	if err != nil {
		return "Connection error: " + err.Error()
	}

	err = freshClient.Run(a.ctx, func(ctx context.Context) error {
		inChan, _, err := auth.ResolveDriveChannel(ctx, freshClient.API(), channelid)
		if err != nil {
			return err
		}

		targetID := []int{msgID}
		_, err = freshClient.API().ChannelsDeleteMessages(ctx, &tg.ChannelsDeleteMessagesRequest{
			Channel: inChan,
			ID:      targetID,
		})
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return "Telegram Error: " + err.Error()
	}

	if err := projection.LocalDeleteFile(backend.DB, channelid, int64(msgID)); err != nil {
		if errors.Is(err, projection.ErrFileNotFound) {
			return "Deleted from tg, but not found in local DB."
		}
		return "Deleted, but failed to update local DB: " + err.Error()
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

	created, err := projection.LocalCreateFolder(backend.DB, channelID, parentID, foldername)
	if err != nil {
		switch {
		case errors.Is(err, projection.ErrInvalidName):
			return backend.Folder{}, fmt.Errorf("folder name can't be empty")
		case errors.Is(err, projection.ErrParentMissing):
			return backend.Folder{}, fmt.Errorf("parent folder not found")
		case errors.Is(err, projection.ErrNameTaken):
			return backend.Folder{}, fmt.Errorf("folder '%s' already exists here", foldername)
		case errors.Is(err, projection.ErrInvalidParent):
			return backend.Folder{}, fmt.Errorf("invalid parent folder id")
		default:
			return backend.Folder{}, err
		}
	}

	return backend.Folder{
		ID:       created.ID,
		Name:     created.Name,
		ParentID: created.ParentID,
	}, nil
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

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

	if savedID, err := auth.LoadConfig(); err == nil && savedID != 0 {
		if err := backend.MigratePersonalChannel(savedID); err != nil {
			fmt.Printf("Warning: migration failed: %v\n", err)
		} else {
			a.activeChannelID.Store(savedID)
		}
	}

	fmt.Println("TDrive DB ready!")
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
			TgMsgID:    int(f.MsgID),
			Name:       f.Name,
			Size:       f.Size,
			ParentID:   f.ParentID,
			UploadTime: f.UploadTime,
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
				Path:       buildFolderPath(h.ParentID),
			})
		}
	}
	return results, nil
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

func collectDoomedIDsDB(channelID int64, targetFolderID string) (folderIDs []string, msgIDs []int, err error) {
	folders, msgIDs64, err := projection.CollectDescendants(backend.DB, channelID, targetFolderID)
	if err != nil {
		return nil, nil, err
	}
	msgIDs = make([]int, 0, len(msgIDs64))
	for _, id := range msgIDs64 {
		msgIDs = append(msgIDs, int(id))
	}
	return folders, msgIDs, nil
}

// fully written by AI
func (a *App) DeleteFolder(folderID string) string {
	if backend.DB == nil {
		return "Error: DB not ready"
	}
	channelid := a.ActiveChannelID()
	if channelid == 0 {
		return "Error: No active channel"
	}

	doomedFolders, doomedMsgs, err := collectDoomedIDsDB(channelid, folderID)
	if err != nil {
		return "Error: " + err.Error()
	}

	fmt.Printf("Deleting Folder: Found %d sub-folders and %d files to delete.\n", len(doomedFolders), len(doomedMsgs))

	if len(doomedMsgs) > 0 {
		freshClient, err := auth.Connect()
		if err != nil {
			return "Connection Error"
		}

		err = freshClient.Run(a.ctx, func(ctx context.Context) error {
			inChan, _, err := auth.ResolveDriveChannel(ctx, freshClient.API(), channelid)
			if err != nil {
				return err
			}

			batchSize := 100
			for i := 0; i < len(doomedMsgs); i += batchSize {
				end := i + batchSize
				if end > len(doomedMsgs) {
					end = len(doomedMsgs)
				}

				batch := doomedMsgs[i:end]
				_, err := freshClient.API().ChannelsDeleteMessages(ctx, &tg.ChannelsDeleteMessagesRequest{
					Channel: inChan,
					ID:      batch,
				})
				if err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return "Telegram Delete Failed: " + err.Error()
		}
	}

	doomedMsgIDs64 := make([]int64, 0, len(doomedMsgs))
	for _, m := range doomedMsgs {
		doomedMsgIDs64 = append(doomedMsgIDs64, int64(m))
	}
	if err := projection.LocalDeleteFolderTree(backend.DB, channelid, doomedFolders, doomedMsgIDs64); err != nil {
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

	if err := projection.LocalRegisterFile(
		backend.DB, channelID, int64(msgID),
		name, size, parentID, time.Now().Unix(), 0,
	); err != nil {
		switch {
		case errors.Is(err, projection.ErrParentMissing):
			return "Error: Target folder not found"
		case errors.Is(err, projection.ErrInvalidParent):
			return "Error: Invalid parent folder id"
		default:
			return "Error: " + err.Error()
		}
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
	if err := projection.LocalRenameFile(backend.DB, channelID, int64(msgID), newName); err != nil {
		switch {
		case errors.Is(err, projection.ErrInvalidName):
			return "Error: Invalid name"
		case errors.Is(err, projection.ErrFileNotFound):
			return "Error: File not found"
		default:
			return "Error: " + err.Error()
		}
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
	if err := projection.LocalRenameFolder(backend.DB, channelID, folderID, newName); err != nil {
		switch {
		case errors.Is(err, projection.ErrInvalidName):
			return "Error: Invalid name"
		case errors.Is(err, projection.ErrFolderNotFound), errors.Is(err, projection.ErrInvalidParent):
			return "Error: Folder not found"
		default:
			return "Error: " + err.Error()
		}
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
	if err := projection.LocalMoveFile(backend.DB, channelID, int64(msgID), newParentID); err != nil {
		switch {
		case errors.Is(err, projection.ErrFileNotFound):
			return "Error: File not found"
		case errors.Is(err, projection.ErrParentMissing):
			return "Error: Target folder not found"
		case errors.Is(err, projection.ErrInvalidParent):
			return "Error: Invalid target folder id"
		case errors.Is(err, projection.ErrSamePlace):
			return "Error: File is already in this folder"
		default:
			return "Error: " + err.Error()
		}
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
	if err := projection.LocalMoveFolder(backend.DB, channelID, folderID, newParentID); err != nil {
		switch {
		case errors.Is(err, projection.ErrFolderNotFound):
			return "Error: Folder not found"
		case errors.Is(err, projection.ErrParentMissing):
			return "Error: Target folder not found"
		case errors.Is(err, projection.ErrInvalidParent):
			return "Error: Invalid target folder id"
		case errors.Is(err, projection.ErrCycleRejected):
			return "Error: Cannot move folder into its own subfolder"
		case errors.Is(err, projection.ErrSamePlace):
			return "Error: Folder is already here"
		default:
			return "Error: " + err.Error()
		}
	}
	return "Success"
}
