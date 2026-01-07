package main

import (
	"context"
	"fmt"

	"TDrive/backend/auth"
)

type App struct {
	ctx    context.Context
	Codech chan string
	Passch chan string
}

func (a *App) LoginPhoneNumber(phoneNumber string) {
	tgclient, _ := auth.Connect()
	auth.StartLogin(a.ctx, tgclient, a)

	fmt.Println("Login started")
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

func (a *App) Code(code string) {
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
