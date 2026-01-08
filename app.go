package main

import (
	"context"
	"fmt"

	"TDrive/backend/auth"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx    context.Context
	Codech chan string
	Passch chan string
}

func (a *App) LoginPhoneNumber(phoneNumber string) {
	tgclient, err := auth.Connect()
	if err != nil {
		fmt.Println("CRITICAL ERROR: Could not connect to Telegram:", err)
		return
	}

	go auth.StartLogin(a.ctx, tgclient, a, phoneNumber)
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
	}
}

func (a *App) SumbitCode(code string) {
	a.Codech <- code
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
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
