package auth

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
)

type getchanel interface {
	GetCodech() chan string
	GetPassch() chan string
	SendHint(hint string)
}

type AuthT struct {
	Client      *telegram.Client
	app         getchanel
	PhoneNumber string
}

func Connect() (*telegram.Client, error) {
	Tg_app_HASH := os.Getenv("TELEGRAM_APP_HASH")

	Tg_app_ID, err := strconv.Atoi(os.Getenv("TELEGRAM_APP_ID"))
	if err != nil {
		return nil, err
	}

	cwd, _ := os.Getwd()
	sessionPath := filepath.Join(cwd, "session.json")

	ses := &session.FileStorage{
		Path: sessionPath,
	}

	tgclient := telegram.NewClient(Tg_app_ID, Tg_app_HASH, telegram.Options{
		SessionStorage: ses,
	})

	return tgclient, nil
}

func (a AuthT) Phone(ctx context.Context) (string, error) {
	return a.PhoneNumber, nil
}

func (a AuthT) Code(ctx context.Context, sendcode *tg.AuthSentCode) (string, error) {
	// fmt.Scanln(&code)
	code := <-a.app.GetCodech()

	return code, nil
}

func (a AuthT) Password(ctx context.Context) (string, error) {
	passObj, err := a.Client.API().AccountGetPassword(ctx)
	if err != nil {
		return "", err
	}

	if passObj.Hint != "" {
		fmt.Println("Hint:", passObj.Hint)
		a.app.SendHint("Hint : " + passObj.Hint)
	} else {
		a.app.SendHint("NO HINT found")
	}

	// a.app.SendHint("Hint: THIS IS A TEST HINT ")
	Password := <-a.app.GetPassch()

	return Password, nil
}

func (a AuthT) AcceptTermsOfService(ctx context.Context, tos tg.HelpTermsOfService) error {
	return nil
}

func (a AuthT) SignUp(ctx context.Context) (auth.UserInfo, error) {
	return auth.UserInfo{}, fmt.Errorf("sign-up not implemented: please register manually")
}

func StartLogin(ctx context.Context, client *telegram.Client, ch getchanel, phone string) error {
	authenticator := AuthT{
		Client:      client,
		app:         ch,
		PhoneNumber: phone,
	}

	flow := auth.NewFlow(authenticator, auth.SendCodeOptions{})

	return client.Run(ctx, func(ctx context.Context) error {
		return client.Auth().IfNecessary(ctx, flow)
	})
}
