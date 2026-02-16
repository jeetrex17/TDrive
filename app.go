package main

import (
	"context"
	"database/sql"
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

	"github.com/google/uuid"
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
	if backend.DB == nil {
		return nil, fmt.Errorf("db not ready")
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

			meta, err := a.uploadSingleFile(uploadID, path, pid)
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

	tx, err := backend.DB.Begin()
	if err != nil {
		emitLocalIndexError("local index write failed")
		return uploadedFiles, err
	}
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO files (msg_id, name, size, parent_id, upload_time) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		_ = tx.Rollback()
		emitLocalIndexError("local index write failed")
		return uploadedFiles, err
	}
	for _, item := range uploaded {
		meta := item.Meta
		if _, err := stmt.Exec(meta.TgMsgID, meta.Name, meta.Size, meta.ParentID, meta.UploadTime); err != nil {
			_ = stmt.Close()
			_ = tx.Rollback()
			emitLocalIndexError("local index write failed")
			return uploadedFiles, err
		}
	}
	_ = stmt.Close()
	if err := tx.Commit(); err != nil {
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

func (a *App) DownloadFile(msgID int, TgMsgID int) string {
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
			var name string
			if err := backend.DB.QueryRow(`SELECT name FROM files WHERE msg_id = ?`, lookupID).Scan(&name); err == nil {
				name = strings.TrimSpace(name)
				if name != "" {
					originalName = name
				}
			}
		}

		savePath, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
			DefaultFilename: originalName,
			Title:           "Save File As...",
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
	if backend.DB == nil {
		return "Error: DB not ready"
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

	res, err := backend.DB.Exec(`DELETE FROM files WHERE msg_id = ?`, msgID)
	if err != nil {
		return "Deleted, but failed to update local DB: " + err.Error()
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return "Deleted from tg, but not found in local DB."
	}

	return "Success"
}

func (a *App) GetStorageUsed() (int64, error) {
	if backend.DB == nil {
		return 0, fmt.Errorf("db not ready")
	}

	var total int64
	if err := backend.DB.QueryRow(`SELECT COALESCE(SUM(size), 0) FROM files`).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
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

	foldername = strings.TrimSpace(foldername)
	if foldername == "" {
		return backend.Folder{}, fmt.Errorf("folder name can't be empty")
	}

	parentID = strings.TrimSpace(parentID)
	if parentID != "" {
		var tmp int
		if err := backend.DB.QueryRow(`SELECT 1 FROM folders WHERE id = ? LIMIT 1`, parentID).Scan(&tmp); err != nil {
			if err == sql.ErrNoRows {
				return backend.Folder{}, fmt.Errorf("parent folder not found")
			}
			return backend.Folder{}, err
		}
	}

	var tmp int
	err := backend.DB.QueryRow(`SELECT 1 FROM folders WHERE parent_id = ? AND name = ? LIMIT 1`, parentID, foldername).Scan(&tmp)
	if err == nil {
		return backend.Folder{}, fmt.Errorf("folder '%s' already exists here", foldername)
	}
	if err != nil && err != sql.ErrNoRows {
		return backend.Folder{}, err
	}

	newFolder := backend.Folder{
		Name:     foldername,
		ParentID: parentID,
		ID:       uuid.NewString(),
	}
	if _, err := backend.DB.Exec(`INSERT INTO folders (id, name, parent_id) VALUES (?, ?, ?)`, newFolder.ID, newFolder.Name, newFolder.ParentID); err != nil {
		return backend.Folder{}, err
	}

	return newFolder, nil
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	rand.Seed(time.Now().UnixNano())

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
	fmt.Println("TDrive DB ready!")
}

func (a *App) GetFolderContents(parentID string) (backend.FileSystem, error) {
	if backend.DB == nil {
		return backend.FileSystem{}, fmt.Errorf("db not ready")
	}

	result := backend.FileSystem{
		Folders: []backend.Folder{},
		Files:   []backend.FileMetaData{},
	}

	folderRows, err := backend.DB.Query(`SELECT id, name, parent_id FROM folders WHERE parent_id = ? ORDER BY name`, parentID)
	if err != nil {
		return backend.FileSystem{}, err
	}
	defer folderRows.Close()
	for folderRows.Next() {
		var folder backend.Folder
		if err := folderRows.Scan(&folder.ID, &folder.Name, &folder.ParentID); err != nil {
			return backend.FileSystem{}, err
		}
		result.Folders = append(result.Folders, folder)
	}
	if err := folderRows.Err(); err != nil {
		return backend.FileSystem{}, err
	}

	fileRows, err := backend.DB.Query(`SELECT msg_id, name, size, parent_id, upload_time FROM files WHERE parent_id = ? ORDER BY upload_time DESC`, parentID)
	if err != nil {
		return backend.FileSystem{}, err
	}
	defer fileRows.Close()
	for fileRows.Next() {
		var file backend.FileMetaData
		if err := fileRows.Scan(&file.TgMsgID, &file.Name, &file.Size, &file.ParentID, &file.UploadTime); err != nil {
			return backend.FileSystem{}, err
		}
		result.Files = append(result.Files, file)
	}
	if err := fileRows.Err(); err != nil {
		return backend.FileSystem{}, err
	}

	return result, nil
}

func (a *App) Search(query string, limit int) ([]backend.SearchResult, error) {
	if backend.DB == nil {
		return nil, fmt.Errorf("db not ready")
	}

	query = strings.TrimSpace(query)
	if query == "" {
		return []backend.SearchResult{}, nil
	}

	if limit <= 0 {
		limit = 200
	}
	if limit > 500 {
		limit = 500
	}

	folderMap := make(map[string]backend.Folder)
	allFolders, err := backend.DB.Query(`SELECT id, name, parent_id FROM folders`)
	if err != nil {
		return nil, err
	}
	for allFolders.Next() {
		var folder backend.Folder
		if err := allFolders.Scan(&folder.ID, &folder.Name, &folder.ParentID); err != nil {
			_ = allFolders.Close()
			return nil, err
		}
		if folder.ID != "" {
			folderMap[folder.ID] = folder
		}
	}
	if err := allFolders.Err(); err != nil {
		_ = allFolders.Close()
		return nil, err
	}
	_ = allFolders.Close()

	buildFolderPath := func(folderID string) string {
		folderID = strings.TrimSpace(folderID)
		if folderID == "" {
			return "My Drive"
		}

		names := make([]string, 0, 8)
		visited := make(map[string]bool)
		cur := folderID

		for cur != "" && !visited[cur] {
			visited[cur] = true
			folder, ok := folderMap[cur]
			if !ok {
				break
			}
			name := strings.TrimSpace(folder.Name)
			if name != "" {
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

	pattern := "%" + query + "%"
	results := make([]backend.SearchResult, 0, limit*2)

	folderRows, err := backend.DB.Query(`SELECT id, name, parent_id FROM folders WHERE name LIKE ? COLLATE NOCASE ORDER BY name LIMIT ?`, pattern, limit)
	if err != nil {
		return nil, err
	}
	for folderRows.Next() {
		var folder backend.Folder
		if err := folderRows.Scan(&folder.ID, &folder.Name, &folder.ParentID); err != nil {
			_ = folderRows.Close()
			return nil, err
		}
		results = append(results, backend.SearchResult{
			Type:     "folder",
			ID:       folder.ID,
			Name:     folder.Name,
			ParentID: folder.ParentID,
			Path:     buildFolderPath(folder.ID),
		})
	}
	if err := folderRows.Err(); err != nil {
		_ = folderRows.Close()
		return nil, err
	}
	_ = folderRows.Close()

	fileRows, err := backend.DB.Query(`SELECT msg_id, name, size, parent_id, upload_time FROM files WHERE name LIKE ? COLLATE NOCASE ORDER BY upload_time DESC LIMIT ?`, pattern, limit)
	if err != nil {
		return nil, err
	}
	for fileRows.Next() {
		var file backend.FileMetaData
		if err := fileRows.Scan(&file.TgMsgID, &file.Name, &file.Size, &file.ParentID, &file.UploadTime); err != nil {
			_ = fileRows.Close()
			return nil, err
		}
		results = append(results, backend.SearchResult{
			Type:       "file",
			ID:         fmt.Sprintf("%d", file.TgMsgID),
			Name:       file.Name,
			ParentID:   file.ParentID,
			Size:       file.Size,
			UploadTime: file.UploadTime,
			Path:       buildFolderPath(file.ParentID),
		})
	}
	if err := fileRows.Err(); err != nil {
		_ = fileRows.Close()
		return nil, err
	}
	_ = fileRows.Close()

	return results, nil
}

func (a *App) GetAllFsMsgIDs() ([]int, error) {
	if backend.DB == nil {
		return nil, fmt.Errorf("db not ready")
	}

	rows, err := backend.DB.Query(`SELECT msg_id FROM files`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]int, 0, 512)
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (a *App) GetFolderSize(folderID string) (int64, error) {
	if backend.DB == nil {
		return 0, fmt.Errorf("db not ready")
	}

	folderID = strings.TrimSpace(folderID)
	if folderID == "" {
		return 0, fmt.Errorf("invalid folder id")
	}

	var tmp int
	if err := backend.DB.QueryRow(`SELECT 1 FROM folders WHERE id = ? LIMIT 1`, folderID).Scan(&tmp); err != nil {
		if err == sql.ErrNoRows {
			return 0, fmt.Errorf("folder not found")
		}
		return 0, err
	}

	var total int64
	query := `
WITH RECURSIVE descendants(id, path) AS (
    SELECT ?, ',' || ? || ','
    UNION ALL
    SELECT f.id, d.path || f.id || ','
    FROM folders f
    JOIN descendants d ON f.parent_id = d.id
    WHERE instr(d.path, ',' || f.id || ',') = 0
)
SELECT COALESCE(SUM(files.size), 0)
FROM files
JOIN descendants d ON files.parent_id = d.id;
`
	if err := backend.DB.QueryRow(query, folderID, folderID).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

func collectDoomedIDsDB(targetFolderID string) (folderIDs []string, msgIDs []int, err error) {
	visited := make(map[string]bool)
	queue := []string{targetFolderID}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == "" || visited[cur] {
			continue
		}
		visited[cur] = true
		folderIDs = append(folderIDs, cur)

		fileRows, err := backend.DB.Query(`SELECT msg_id FROM files WHERE parent_id = ?`, cur)
		if err != nil {
			return nil, nil, err
		}
		for fileRows.Next() {
			var id int
			if err := fileRows.Scan(&id); err != nil {
				_ = fileRows.Close()
				return nil, nil, err
			}
			msgIDs = append(msgIDs, id)
		}
		if err := fileRows.Err(); err != nil {
			_ = fileRows.Close()
			return nil, nil, err
		}
		_ = fileRows.Close()

		folderRows, err := backend.DB.Query(`SELECT id FROM folders WHERE parent_id = ?`, cur)
		if err != nil {
			return nil, nil, err
		}
		for folderRows.Next() {
			var id string
			if err := folderRows.Scan(&id); err != nil {
				_ = folderRows.Close()
				return nil, nil, err
			}
			if id != "" && !visited[id] {
				queue = append(queue, id)
			}
		}
		if err := folderRows.Err(); err != nil {
			_ = folderRows.Close()
			return nil, nil, err
		}
		_ = folderRows.Close()
	}

	return folderIDs, msgIDs, nil
}

// fully written by AI
func (a *App) DeleteFolder(folderID string) string {
	if backend.DB == nil {
		return "Error: DB not ready"
	}

	doomedFolders, doomedMsgs, err := collectDoomedIDsDB(folderID)
	if err != nil {
		return "Error: " + err.Error()
	}

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

	tx, err := backend.DB.Begin()
	if err != nil {
		return "Error: " + err.Error()
	}

	for _, msgID := range doomedMsgs {
		if _, err := tx.Exec(`DELETE FROM files WHERE msg_id = ?`, msgID); err != nil {
			_ = tx.Rollback()
			return "Error: " + err.Error()
		}
	}
	for _, id := range doomedFolders {
		if _, err := tx.Exec(`DELETE FROM folders WHERE id = ?`, id); err != nil {
			_ = tx.Rollback()
			return "Error: " + err.Error()
		}
	}

	if err := tx.Commit(); err != nil {
		return "Error: " + err.Error()
	}

	return "Success"
}

func (a *App) MsgToTdriveSystem(msgID int, name string, size int64, parentID string) string {
	if backend.DB == nil {
		return "Error: DB not ready"
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
		var tmp int
		if err := backend.DB.QueryRow(`SELECT 1 FROM folders WHERE id = ? LIMIT 1`, parentID).Scan(&tmp); err != nil {
			if err == sql.ErrNoRows {
				return "Error: Target folder not found"
			}
			return "Error: " + err.Error()
		}
	}

	var tmp int
	if err := backend.DB.QueryRow(`SELECT 1 FROM files WHERE msg_id = ? LIMIT 1`, msgID).Scan(&tmp); err == nil {
		return "Success"
	} else if err != sql.ErrNoRows {
		return "Error: " + err.Error()
	}

	if _, err := backend.DB.Exec(`INSERT OR IGNORE INTO files (msg_id, name, size, parent_id, upload_time) VALUES (?, ?, ?, ?, ?)`, msgID, name, size, parentID, time.Now().Unix()); err != nil {
		return "Error: " + err.Error()
	}

	return "Success"
}

func (a *App) RenameFile(msgID int, newName string) string {
	if backend.DB == nil {
		return "Error: DB not ready"
	}

	newName = strings.TrimSpace(newName)
	if newName == "" {
		return "Error: Invalid name"
	}

	if _, err := backend.DB.Exec(`UPDATE files SET name = ? WHERE msg_id = ?`, newName, msgID); err != nil {
		return "Error: " + err.Error()
	}
	return "Success"
}

func (a *App) RenameFolder(folderID string, newName string) string {
	if backend.DB == nil {
		return "Error: DB not ready"
	}

	newName = strings.TrimSpace(newName)
	if newName == "" {
		return "Error: Invalid name"
	}

	if _, err := backend.DB.Exec(`UPDATE folders SET name = ? WHERE id = ?`, newName, folderID); err != nil {
		return "Error: " + err.Error()
	}
	return "Success"
}

func (a *App) MoveFile(msgID int, newParentID string) string {
	if backend.DB == nil {
		return "Error: DB not ready"
	}

	newParentID = strings.TrimSpace(newParentID)
	if newParentID != "" {
		var tmp int
		if err := backend.DB.QueryRow(`SELECT 1 FROM folders WHERE id = ? LIMIT 1`, newParentID).Scan(&tmp); err != nil {
			if err == sql.ErrNoRows {
				return "Error: Target folder not found"
			}
			return "Error: " + err.Error()
		}
	}

	var curParent string
	if err := backend.DB.QueryRow(`SELECT parent_id FROM files WHERE msg_id = ?`, msgID).Scan(&curParent); err != nil {
		if err == sql.ErrNoRows {
			return "Error: File not found"
		}
		return "Error: " + err.Error()
	}
	if curParent == newParentID {
		return "Error: File is already in this folder"
	}

	if _, err := backend.DB.Exec(`UPDATE files SET parent_id = ? WHERE msg_id = ?`, newParentID, msgID); err != nil {
		return "Error: " + err.Error()
	}

	return "Success"
}

func (a *App) MoveFolder(folderID string, newParentID string) string {
	if backend.DB == nil {
		return "Error: DB not ready"
	}

	newParentID = strings.TrimSpace(newParentID)

	if folderID == newParentID {
		return "Error: Cannot move folder into itself"
	}

	var curParent string
	if err := backend.DB.QueryRow(`SELECT parent_id FROM folders WHERE id = ?`, folderID).Scan(&curParent); err != nil {
		if err == sql.ErrNoRows {
			return "Error: Folder not found"
		}
		return "Error: " + err.Error()
	}
	if curParent == newParentID {
		return "Error: Folder is already here"
	}

	if newParentID != "" {
		cur := newParentID
		for cur != "" {
			if cur == folderID {
				return "Error: Cannot move folder into its own subfolder"
			}
			var next string
			if err := backend.DB.QueryRow(`SELECT parent_id FROM folders WHERE id = ?`, cur).Scan(&next); err != nil {
				if err == sql.ErrNoRows {
					return "Error: Target folder not found"
				}
				return "Error: " + err.Error()
			}
			cur = next
		}
	}

	if _, err := backend.DB.Exec(`UPDATE folders SET parent_id = ? WHERE id = ?`, newParentID, folderID); err != nil {
		return "Error: " + err.Error()
	}

	return "Success"
}
