package main

import (
	"context"
	"fmt"

	"TDrive/backend/auth"

	"github.com/gotd/td/telegram"
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
