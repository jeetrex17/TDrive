package auth

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
)

type getchanel interface {
	GetCodech() chan string
	GetPassch() chan string
}

type AuthT struct {
	Client *telegram.Client
	app    getchanel
}

func Connect() (*telegram.Client, error) {
	Tg_app_HASH := os.Getenv("TELEGRAM_APP_HASH")

	Tg_app_ID, err := strconv.Atoi(os.Getenv("TELEGRAM_APP_ID"))
	if err != nil {
		return nil, err
	}

	tgclient := telegram.NewClient(Tg_app_ID, Tg_app_HASH, telegram.Options{})

	return tgclient, nil
}

func (a AuthT) Phone(ctx context.Context) (string, error) {
	var PhoneNumber string

	fmt.Print("Enter Phone: ")
	fmt.Scanln(&PhoneNumber)

	return PhoneNumber, nil
}

func (a AuthT) Code(ctx context.Context, sendcode *tg.AuthSentCode) (string, error) {
	fmt.Print("Enter code: ")

	// fmt.Scanln(&code)

	code := <-a.app.GetCodech()

	return code, nil
}

func (a AuthT) Password(ctx context.Context) (string, error) {
	passObj, err := a.Client.API().AccountGetPassword(ctx)
	if err != nil {
		return "", err // Returna the error instead of fatal to allow graceful handling
	}
	if passObj.Hint != "" {
		fmt.Println("Hint:", passObj.Hint)
	}

	fmt.Print("Enter 2FA Password: ")
	// bytePassword, err := term.ReadPassword(int(os.Stdin.Fd()))
	// if err != nil {
	// 	fmt.Println("\nError reading password:", err)
	// 	return "", err
	// }

	Password := <-a.app.GetPassch()

	return string(Password), nil
}

func (a AuthT) AcceptTermsOfService(ctx context.Context, tos tg.HelpTermsOfService) error {
	return nil
}

func (a AuthT) SignUp(ctx context.Context) (auth.UserInfo, error) {
	return auth.UserInfo{}, fmt.Errorf("sign-up not implemented: please register manually")
}

func StartLogin(ctx context.Context, client *telegram.Client, ch getchanel) error {
	authenticator := AuthT{
		Client: client,
		app:    ch,
	}

	flow := auth.NewFlow(authenticator, auth.SendCodeOptions{})

	return client.Auth().IfNecessary(ctx, flow)
}
