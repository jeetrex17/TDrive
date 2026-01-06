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

type AuthT struct{}

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
	var code string

	fmt.Print("Enter code: ")

	fmt.Scanln(&code)

	return code, nil
}

func (a AuthT) Password(ctx context.Context) (string, error) {
	var Password string
	fmt.Print("Enter 2FA Password: ")

	fmt.Scanln(&Password)
	return Password, nil
}

func (a AuthT) AcceptTermsOfService(ctx context.Context, tos tg.HelpTermsOfService) error {
	return nil
}

func (a AuthT) SignUp(ctx context.Context) (auth.UserInfo, error) {
	return auth.UserInfo{}, fmt.Errorf("sign-up not implemented: please register manually")
}

func StartLogin(ctx context.Context, client *telegram.Client) error {
	authenticator := AuthT{}

	flow := auth.NewFlow(authenticator, auth.SendCodeOptions{})

	return client.Auth().IfNecessary(ctx, flow)
}
