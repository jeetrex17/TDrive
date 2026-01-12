package main

import (
	"context"
	"fmt"
	"math/rand"
	"path/filepath"

	"TDrive/backend/auth"

	"github.com/gotd/td/telegram"
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
			Message:  "Uploaded via TDrive 🚀",
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

// Greet returns a greeting for the given name
func (a *App) Greet(name string) string {
	return fmt.Sprintf("Hello %s, It's show time!", name)
}

func (a *App) SendHint(hint string) {
	runtime.EventsEmit(a.ctx, "gothint", hint)
}

func (a *App) SumbitPassword(password string) {
	a.Passch <- password
}
