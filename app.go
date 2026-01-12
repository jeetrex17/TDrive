package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"

	"TDrive/backend/auth"

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
	AccessHash int64  `json:"access_hash"` // We need this to download it later!
}

func (a *App) CheckLoginStatus() bool {
	if a.Client == nil {
		return false
	}
	login, err := auth.CheckLogin(a.ctx)
	if err != nil {
		fmt.Println("error auto login", err)
		return false
	}

	return login
}

func (a *App) SelectFile() (string, error) {
	uploadfilepath, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select file to upload",
	})
	if err != nil {
		return "", err
	}

	return uploadfilepath, err
}

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

func (a *App) LoginPhoneNumber(phoneNumber string) {
	var err error
	if a.Client == nil {
		a.Client, err = auth.Connect()
		if err != nil {
			fmt.Println("Could not connect to Telegram:", err)
			return
		}
	}

	go func() {
		err := auth.StartLogin(a.ctx, a.Client, a, phoneNumber)
		if err != nil {
			fmt.Println("Login failed:", err)

			return
		}

		fmt.Println("Login Flow Complete. Emitting Success Event.")
		runtime.EventsEmit(a.ctx, "login-success", true)
	}()
}

func (a *App) InitDrive() string {
	if a.Client == nil {
		var err error
		a.Client, err = auth.Connect()
		if err != nil {
			return "Error: Could not connect"
		}
	}

	var output string

	err := a.Client.Run(a.ctx, func(ctx context.Context) error {
		id, err := auth.GetTDriveChannel(ctx, a.Client)
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
		return nil // Return empty list on error
	}

	freshClient, err := auth.Connect()
	if err != nil {
		return nil
	}

	var fileList []TDriveFile

	err = freshClient.Run(a.ctx, func(ctx context.Context) error {
		dialogs, err := freshClient.API().MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
			Limit:      20,
			OffsetPeer: &tg.InputPeerEmpty{},
		})
		if err != nil {
			return err
		}

		var accessHash int64
		var chats []tg.ChatClass

		switch d := dialogs.(type) {
		case *tg.MessagesDialogs:
			chats = d.Chats
		case *tg.MessagesDialogsSlice:
			chats = d.Chats
		}

		for _, chat := range chats {
			if ch, ok := chat.(*tg.Channel); ok && ch.ID == channelid {
				accessHash = ch.AccessHash
				break
			}
		}

		req := &tg.MessagesGetHistoryRequest{
			Peer:  &tg.InputPeerChannel{ChannelID: channelid, AccessHash: accessHash},
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
		dialogs, err := freshClient.API().MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
			Limit:      20,
			OffsetPeer: &tg.InputPeerEmpty{},
		})
		if err != nil {
			return err
		}

		var accessHash int64
		var found bool

		// Extract chats
		var chats []tg.ChatClass
		switch d := dialogs.(type) {
		case *tg.MessagesDialogs:
			chats = d.Chats
		case *tg.MessagesDialogsSlice:
			chats = d.Chats
		}

		for _, chat := range chats {
			if ch, ok := chat.(*tg.Channel); ok && ch.ID == channelid {
				accessHash = ch.AccessHash
				found = true
				break
			}
		}
		if !found {
			status = "Error: Channel not found in recent chats"
			return nil
		}

		targetID := []tg.InputMessageClass{&tg.InputMessageID{ID: msgID}}

		result, err := freshClient.API().ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
			Channel: &tg.InputChannel{ChannelID: channelid, AccessHash: accessHash},
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
				originalName = fname.FileName // Fix: Use '=' not ':='
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

		_, err = d.Download(freshClient.API(), doc.AsInputDocumentFileLocation()).Stream(ctx, f)
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

func (a *App) SumbitCode(code string) {
	a.Codech <- code
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	ac, err := auth.Connect()
	if err != nil {
		return
	}

	a.Client = ac
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
