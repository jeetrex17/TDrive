package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
)

type ImpCredentials struct {
	ApiID   int    `json:"API_ID"`
	ApiHash string `json:"API_HASH"`
}

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

func GetConfigPath() string {
	path, err := os.UserConfigDir()
	if err != nil {
		return err.Error()
	}
	path = path + "/TDrive/imp_config.json"

	return path
}

func SaveImpCredentials(id int, hash string) {
	ic := ImpCredentials{
		ApiID:   id,
		ApiHash: hash,
	}

	jsonData, err := json.MarshalIndent(ic, "", " ")
	if err != nil {
		log.Fatal("Error marshaling imp credentials: ", err)
	}
	err = os.WriteFile(GetConfigPath(), jsonData, 0o644)
	if err != nil {
		log.Fatal("Error writing imp JSON : ", err)
	}
}

func LoadImpCredentials() ImpCredentials {
	impCongigPath := GetConfigPath()

	creds, err := os.ReadFile(impCongigPath)
	if err != nil {
		log.Fatalf("Error reading file: %v", err)
	}

	var impCreds ImpCredentials

	err = json.Unmarshal(creds, &impCreds)
	if err != nil {
		log.Fatalf("JSON decode error: %v", err)
	}

	return impCreds
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

func CheckLogin(ctx context.Context) (bool, error) {
	client, err := Connect()
	if err != nil {
		return false, err
	}

	var isValid bool

	err = client.Run(ctx, func(ctx context.Context) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return err
		}

		isValid = status.Authorized

		return nil
	})
	if err != nil {
		return false, err
	}

	return isValid, nil
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
