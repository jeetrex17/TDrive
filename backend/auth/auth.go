package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

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
	return filepath.Join(path, "TDrive", "imp_config.json")
}

func SaveImpCredentials(id int, hash string) error {
	path := GetConfigPath()

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("could not create config folder: %v", err)
	}

	ic := ImpCredentials{
		ApiID:   id,
		ApiHash: hash,
	}

	jsonData, err := json.MarshalIndent(ic, "", " ")
	if err != nil {
		return fmt.Errorf("error marshaling credentials: %v", err)
	}
	err = os.WriteFile(GetConfigPath(), jsonData, 0o644)
	if err != nil {
		return fmt.Errorf("error writing file: %v", err)
	}

	return nil
}

func LoadImpCredentials() (ImpCredentials, error) {
	impCongigPath := GetConfigPath()

	creds, err := os.ReadFile(impCongigPath)
	if err != nil {
		return ImpCredentials{}, err
	}

	var impCreds ImpCredentials

	err = json.Unmarshal(creds, &impCreds)
	if err != nil {
		return ImpCredentials{}, fmt.Errorf("error decoding json: %v", err)
	}

	return impCreds, nil
}

func Connect() (*telegram.Client, error) {
	// Tg_app_HASH := os.Getenv("TELEGRAM_APP_HASH")

	//Tg_app_ID, err := strconv.Atoi(os.Getenv("TELEGRAM_APP_ID"))
	//

	creds, err := LoadImpCredentials()
	if err != nil {
		return nil, fmt.Errorf("NO API AND AHSH lOADED ")
	}

	TgApiID := creds.ApiID
	TgApiHash := creds.ApiHash

	path, err := os.UserConfigDir()

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("could not create config folder: %v", err)
	}

	if err != nil {
		return nil, fmt.Errorf("Error while getiing config dir to save sessions.json : %v", err)
	}

	// cwd, _ := os.Getwd()
	sessionPath := filepath.Join(path, "TDrive", "session.json")
	ses := &session.FileStorage{
		Path: sessionPath,
	}

	tgclient := telegram.NewClient(TgApiID, TgApiHash, telegram.Options{
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

func ResolveDriveChannel(ctx context.Context, api *tg.Client, channelID int64) (*tg.InputChannel, *tg.InputPeerChannel, error) {
	chats, err := api.ChannelsGetChannels(ctx, []tg.InputChannelClass{
		&tg.InputChannel{ChannelID: channelID},
	})
	if err != nil {
		return nil, nil, err
	}

	var accessHash int64
	if cc, ok := chats.(*tg.MessagesChats); ok {
		for _, chat := range cc.Chats {
			if ch, ok := chat.(*tg.Channel); ok && ch.ID == channelID {
				accessHash = ch.AccessHash
				break
			}
		}
	}
	if accessHash == 0 {
		return nil, nil, fmt.Errorf("could not resolve access_hash for channel_id=%d", channelID)
	}

	inChan := &tg.InputChannel{ChannelID: channelID, AccessHash: accessHash}
	inPeer := &tg.InputPeerChannel{ChannelID: channelID, AccessHash: accessHash}
	return inChan, inPeer, nil
}
