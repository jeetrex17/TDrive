package main

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"TDrive/backend/auth"

	"TDrive/backend"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx    context.Context
	Codech chan string
	Passch chan string
	Client *telegram.Client
}
type TDriveFile struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	AccessHash int64  `json:"access_hash"`
	Date       int    `json:"date"`
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
	if backend.CurrentFS == nil {
		return nil, fmt.Errorf("filesystem not ready")
	}

	sem := make(chan struct{}, 3)
	var wg sync.WaitGroup
	var mu sync.Mutex

	uploadedFiles := make([]backend.FileMetaData, 0, len(filePaths))
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

			meta, err := a.uploadSingleFile(uploadID, path, pid)
			if err != nil {
				mu.Lock()
				failed++
				mu.Unlock()
				runtime.EventsEmit(a.ctx, "upload_error", uploadID, filepath.Base(path), err.Error())
				return
			}

			mu.Lock()
			backend.CurrentFS.AddFile(meta.Name, meta.Size, meta.TgMsgID, meta.ParentID)
			uploadedFiles = append(uploadedFiles, meta)
			mu.Unlock()
		}(uploadID, path, pid)
	}

	wg.Wait()

	_ = backend.SaveTdriveFS()

	if failed > 0 {
		return uploadedFiles, fmt.Errorf("%d uploads failed", failed)
	}
	return uploadedFiles, nil
}

func (a *App) uploadSingleFile(uploadID int, filePath string, parentID string) (backend.FileMetaData, error) {
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

	channelid, err := auth.LoadConfig()
	if err != nil || channelid == 0 {
		return backend.FileMetaData{}, fmt.Errorf("drive channel id not found")
	}

	freshClient, err := auth.Connect()
	if err != nil {
		return backend.FileMetaData{}, err
	}

	var msgID int

	err = freshClient.Run(a.ctx, func(ctx context.Context) error {
		_, inputPeer, err := auth.ResolveDriveChannel(ctx, freshClient.API(), channelid)
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

		runtime.EventsEmit(a.ctx, "upload_complete", uploadID, filename)
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

	var output string

	client, err := auth.Connect()
	if err != nil {
		return "Error: Could not connect: " + err.Error()
	}

	err = client.Run(a.ctx, func(ctx context.Context) error {
		id, err := auth.GetTDriveChannel(ctx, client)
		if err != nil {
			return err
		}

		output = fmt.Sprintf("Success , channel ID: %d", id)
		return nil
	})
	if err != nil {
		return "Error: " + err.Error()
	}

	return output
}

func (a *App) GetFileList() []TDriveFile {
	channelid, err := auth.LoadConfig()
	if err != nil || channelid == 0 {
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

func (a *App) DownloadFile(msgID int) string {
	channelid, err := auth.LoadConfig()
	if err != nil || channelid == 0 {
		return "Error: Drive ID not found"
	}

	freshClient, err := auth.Connect()
	if err != nil {
		return "Connection error: " + err.Error()
	}

	var status string = "Download Started..."

	err = freshClient.Run(a.ctx, func(ctx context.Context) error {
		inChan, _, err := auth.ResolveDriveChannel(ctx, freshClient.API(), channelid)
		if err != nil {
			status = "Error: " + err.Error()
			return nil
		}

		targetID := []tg.InputMessageClass{&tg.InputMessageID{ID: msgID}}

		result, err := freshClient.API().ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
			Channel: inChan,
			ID:      targetID,
		})
		if err != nil {
			return err
		}

		var targetMsg *tg.Message
		switch m := result.(type) {
		case *tg.MessagesChannelMessages:
			if len(m.Messages) > 0 {
				targetMsg, _ = m.Messages[0].(*tg.Message)
			}
		}

		if targetMsg == nil {
			status = "Error: Message deleted or not found"
			return nil
		}

		docMedia, ok := targetMsg.Media.(*tg.MessageMediaDocument)
		if !ok {
			status = "Error: This is not a file"
			return nil
		}

		doc, ok := docMedia.Document.(*tg.Document)
		if !ok {
			status = "Error: Empty document"
			return nil
		}

		originalName := "tdrive_download"
		for _, attr := range doc.Attributes {
			if fname, ok := attr.(*tg.DocumentAttributeFilename); ok {
				originalName = fname.FileName
			}
		}

		savePath, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
			DefaultFilename: originalName,
			Title:           "Save File As...",
			Filters: []runtime.FileFilter{
				{DisplayName: "All Files", Pattern: "*.*"},
			},
		})

		if err != nil || savePath == "" {
			status = "Download canceled"
			return nil
		}

		d := downloader.NewDownloader()

		f, err := os.Create(savePath)
		if err != nil {
			status = "Disk Error: " + err.Error()
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
		runtime.EventsEmit(a.ctx, "download_progress", 100.0)
		if err != nil {
			status = "Network Error: " + err.Error()
			return nil
		}

		status = "Download Complete! Saved to: " + savePath
		return nil
	})
	if err != nil {
		return "System Error: " + err.Error()
	}

	return status
}

func (a *App) DeleteFile(msgID int) string {
	if backend.CurrentFS == nil {
		return "Error: FileSystem not ready"
	}

	channelid, err := auth.LoadConfig()
	if err != nil || channelid == 0 {
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

	newFilesList := []backend.FileMetaData{}
	found := false

	for _, file := range backend.CurrentFS.Files {
		if file.TgMsgID == msgID {
			found = true
			continue
		}
		newFilesList = append(newFilesList, file)
	}

	if !found {
		return "deleted from tg, but not from local json."
	}

	backend.CurrentFS.Files = newFilesList

	err = backend.SaveTdriveFS()
	if err != nil {
		return "Deleted, but failed to save local config: " + err.Error()
	}

	return "Success"
}

func (a *App) GetStorageUsed() (int64, error) {
	channelid, err := auth.LoadConfig()
	if err != nil {
		return 0, fmt.Errorf("errro getting channel id : %v", err)
	}

	freshClient, err := auth.Connect()
	if err != nil {
		return 0, fmt.Errorf("Error making new tg client : %v", err)
	}

	var totalSize int64

	err = freshClient.Run(a.ctx, func(ctx context.Context) error {
		_, ip, err := auth.ResolveDriveChannel(ctx, freshClient.API(), channelid)
		if err != nil {
			return err
		}

		LastMsgID := 0

		for {
			history, err := freshClient.API().MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
				Peer:     ip,
				Limit:    100,
				OffsetID: LastMsgID,
			})
			if err != nil {
				return err
			}

			var messages []tg.MessageClass
			switch h := history.(type) {
			case *tg.MessagesMessages:
				messages = h.Messages
			case *tg.MessagesMessagesSlice:
				messages = h.Messages
			case *tg.MessagesChannelMessages:
				messages = h.Messages
			}

			if len(messages) == 0 {
				break
			}

			for _, msgObj := range messages {
				if msg, ok := msgObj.(*tg.Message); ok {
					LastMsgID = msg.ID

					if media, ok := msg.Media.(*tg.MessageMediaDocument); ok {
						if doc, ok := media.Document.(*tg.Document); ok {
							totalSize += doc.Size // Add bytes
						}
					}
				}
			}
		}

		return nil
	})
	if err != nil {
		return 0, err
	}

	return totalSize, nil
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
	if backend.CurrentFS == nil {
		return backend.Folder{}, fmt.Errorf("no cuurentFS folders")
	}

	for _, f := range backend.CurrentFS.Folders {
		if f.ParentID == parentID && f.Name == foldername {
			return backend.Folder{}, fmt.Errorf("folder '%s' already exists here", foldername)
		}
	}

	newFolder := backend.CurrentFS.AddFolder(foldername, parentID)

	err := backend.SaveTdriveFS()
	if err != nil {
		return backend.Folder{}, err
	}

	return newFolder, nil
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	ac, err := auth.Connect()
	if err != nil {
		fmt.Println("Warning: Telegram connect failed (offline?):", err)
	} else {
		a.Client = ac
	}

	err = backend.LoadTdriveFs()
	if err != nil {
		fmt.Printf("Warning: Failed to load local filesystem: %v\n", err)
	} else {
		fmt.Println("TDrive FileSystem Loaded Successfully!")
	}
}

func (a *App) GetFolderContents(parentID string) (backend.FileSystem, error) {
	if backend.CurrentFS == nil {
		return backend.FileSystem{}, fmt.Errorf(" FileSystem not ready")
	}

	var result backend.FileSystem
	result.Folders = []backend.Folder{}
	result.Files = []backend.FileMetaData{}

	for _, f := range backend.CurrentFS.Folders {
		if f.ParentID == parentID {
			result.Folders = append(result.Folders, f)
		}
	}

	for _, f := range backend.CurrentFS.Files {
		if f.ParentID == parentID {
			result.Files = append(result.Files, f)
		}
	}

	return result, nil
}

func collectDoomedIDs(targetFolderID string) (folderIDs []string, msgIDs []int) {
	folderIDs = append(folderIDs, targetFolderID)

	for _, f := range backend.CurrentFS.Files {
		if f.ParentID == targetFolderID {
			msgIDs = append(msgIDs, f.TgMsgID)
		}
	}

	for _, f := range backend.CurrentFS.Folders {
		if f.ParentID == targetFolderID {
			subFolders, subFiles := collectDoomedIDs(f.ID)

			folderIDs = append(folderIDs, subFolders...)
			msgIDs = append(msgIDs, subFiles...)
		}
	}

	return folderIDs, msgIDs
}

// fully written by AI
func (a *App) DeleteFolder(folderID string) string {
	if backend.CurrentFS == nil {
		return "Error: System not ready"
	}

	doomedFolders, doomedMsgs := collectDoomedIDs(folderID)

	fmt.Printf("Deleting Folder: Found %d sub-folders and %d files to delete.\n", len(doomedFolders), len(doomedMsgs))

	if len(doomedMsgs) > 0 {
		channelid, err := auth.LoadConfig()
		if err != nil {
			return "Error: No channel ID"
		}

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

	deadMsgMap := make(map[int]bool)
	for _, id := range doomedMsgs {
		deadMsgMap[id] = true
	}

	keepFiles := []backend.FileMetaData{}
	for _, f := range backend.CurrentFS.Files {
		if !deadMsgMap[f.TgMsgID] {
			keepFiles = append(keepFiles, f)
		}
	}
	backend.CurrentFS.Files = keepFiles

	deadFolderMap := make(map[string]bool)
	for _, id := range doomedFolders {
		deadFolderMap[id] = true
	}

	keepFolders := []backend.Folder{}
	for _, f := range backend.CurrentFS.Folders {
		if !deadFolderMap[f.ID] {
			keepFolders = append(keepFolders, f)
		}
	}
	backend.CurrentFS.Folders = keepFolders

	backend.SaveTdriveFS()

	return "Success"
}

func (a *App) MsgToTdriveSystem(msgID int, name string, size int64, parentID string) string {
	if backend.CurrentFS == nil {
		return "Error: FileSystem not ready"
	}

	if msgID <= 0 {
		return "Error: Invalid msgID"
	}

	name = strings.TrimSpace(name)
	if name == "" {
		name = "Untitled"
	}

	parentID = strings.TrimSpace(parentID)
	if parentID != "" {
		found := false
		for _, folder := range backend.CurrentFS.Folders {
			if folder.ID == parentID {
				found = true
				break
			}
		}
		if !found {
			return "Error: Target folder not found"
		}
	}

	for _, file := range backend.CurrentFS.Files {
		if file.TgMsgID == msgID {
			return "Success"
		}
	}

	backend.CurrentFS.AddFile(name, size, msgID, parentID)
	if err := backend.SaveTdriveFS(); err != nil {
		return "Error saving: " + err.Error()
	}

	return "Success"
}

func (a *App) RenameFile(msgID int, newName string) string {
	if backend.CurrentFS == nil {
		return "Error: FileSystem not ready"
	}

	for i, file := range backend.CurrentFS.Files {
		if file.TgMsgID == msgID {
			backend.CurrentFS.Files[i].Name = newName
			break
		}
	}

	err := backend.SaveTdriveFS()
	if err != nil {
		return "Error saving: " + err.Error()
	}
	return "Success"
}

func (a *App) RenameFolder(folderID string, newName string) string {
	if backend.CurrentFS == nil {
		return "Error: FileSystem not ready"
	}

	for i, folder := range backend.CurrentFS.Folders {
		if folder.ID == folderID {
			backend.CurrentFS.Folders[i].Name = newName
			break
		}
	}

	err := backend.SaveTdriveFS()
	if err != nil {
		return "Error saving: " + err.Error()
	}
	return "Success"
}

func (a *App) MoveFile(msgID int, newParentID string) string {
	if backend.CurrentFS == nil {
		return "Error: FileSystem not ready"
	}

	for i, file := range backend.CurrentFS.Files {
		if file.TgMsgID == msgID {
			if file.ParentID == newParentID {
				return "Error: File is already in this folder"
			}
			backend.CurrentFS.Files[i].ParentID = newParentID
			break
		}
	}

	err := backend.SaveTdriveFS()
	if err != nil {
		return "Error saving: " + err.Error()
	}
	return "Success"
}

func (a *App) MoveFolder(folderID string, newParentID string) string {
	if backend.CurrentFS == nil {
		return "Error: FileSystem not ready"
	}

	if folderID == newParentID {
		return "Error: Cannot move folder into itself"
	}

	for i, folder := range backend.CurrentFS.Folders {
		if folder.ID == folderID {
			if folder.ParentID == newParentID {
				return "Error: Folder is already here"
			}
			backend.CurrentFS.Folders[i].ParentID = newParentID
			break
		}
	}

	err := backend.SaveTdriveFS()
	if err != nil {
		return "Error saving: " + err.Error()
	}
	return "Success"
}
