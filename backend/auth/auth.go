package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

type ImpCredentials struct {
	APIID   int    `json:"API_ID"`
	APIHash string `json:"API_HASH"`
}

const (
	privateDirMode  os.FileMode = 0o700
	privateFileMode os.FileMode = 0o600
)

type getChannel interface {
	WaitCode(ctx context.Context) (string, error)
	WaitPassword(ctx context.Context, hint string) (string, error)
	SendHint(hint string)
	// CodeRejected signals that Telegram rejected the last code as invalid.
	// The flow stays alive and waits for another code, so the UI can let the
	// user fix a typo instead of restarting login from the phone step.
	CodeRejected()
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
	credentials := ImpCredentials{
		APIID:   id,
		APIHash: hash,
	}

	jsonData, err := json.MarshalIndent(credentials, "", " ")
	if err != nil {
		return fmt.Errorf("error marshaling credentials: %v", err)
	}
	if err := writePrivateFile(path, jsonData); err != nil {
		return fmt.Errorf("error writing file: %w", err)
	}
	return nil
}

func LoadImpCredentials() (ImpCredentials, error) {
	credentialsPath := GetConfigPath()

	creds, err := os.ReadFile(credentialsPath)
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
	return ConnectWithOptions(telegram.Options{})
}

func ConnectWithOptions(options telegram.Options) (*telegram.Client, error) {
	creds, err := LoadImpCredentials()
	if err != nil {
		return nil, fmt.Errorf("API credentials are not configured")
	}

	path, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("error getting config dir for session: %v", err)
	}

	dir := filepath.Join(path, "TDrive")
	if err := os.MkdirAll(dir, privateDirMode); err != nil {
		return nil, fmt.Errorf("could not create config folder: %v", err)
	}
	if err := os.Chmod(dir, privateDirMode); err != nil {
		return nil, fmt.Errorf("could not secure config folder: %w", err)
	}

	sessionPath := filepath.Join(dir, "session.json")
	if err := os.Chmod(sessionPath, privateFileMode); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("could not secure session file: %w", err)
	}

	options.SessionStorage = &privateSessionStorage{path: sessionPath}
	tgclient := telegram.NewClient(creds.APIID, creds.APIHash, options)

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

// StartLogin drives the Telegram user-authentication flow: send a code, then
// sign in. Unlike gotd's one-shot auth.Flow, this keeps the same phone-code
// hash and re-prompts on an invalid code, so a mistyped code can be corrected
// without requesting (and waiting for) a brand-new code.
func StartLogin(ctx context.Context, client *telegram.Client, ch getChannel, phone string) error {
	return client.Run(ctx, func(ctx context.Context) error {
		ac := client.Auth()

		status, err := ac.Status(ctx)
		if err != nil {
			return err
		}
		if status.Authorized {
			return nil
		}

		sent, err := ac.SendCode(ctx, phone, auth.SendCodeOptions{})
		if err != nil {
			return err
		}
		sentCode, ok := sent.(*tg.AuthSentCode)
		if !ok {
			return fmt.Errorf("unexpected sent-code type %T", sent)
		}
		codeHash := sentCode.PhoneCodeHash

		for {
			code, err := ch.WaitCode(ctx)
			if err != nil {
				return err
			}

			_, err = ac.SignIn(ctx, phone, code, codeHash)
			switch {
			case err == nil:
				return nil
			case errors.Is(err, auth.ErrPasswordAuthNeeded):
				return signInWithPassword(ctx, client, ch)
			case tgerr.Is(err, "PHONE_CODE_INVALID", "PHONE_CODE_EMPTY"):
				// Retryable: the code hash is still valid, just ask again.
				ch.CodeRejected()
				continue
			default:
				// Terminal (expired code, unregistered number, flood, ...).
				return err
			}
		}
	})
}

// signInWithPassword completes a 2FA login. The hint is best-effort: failing to
// fetch it must not block the password prompt.
func signInWithPassword(ctx context.Context, client *telegram.Client, ch getChannel) error {
	hint := "NO HINT found"
	if passObj, err := client.API().AccountGetPassword(ctx); err == nil && passObj.Hint != "" {
		hint = "Hint : " + passObj.Hint
	}

	password, err := ch.WaitPassword(ctx, hint)
	if err != nil {
		return err
	}

	_, err = client.Auth().Password(ctx, password)
	return err
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
