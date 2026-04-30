package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"TDrive/backend/auth"

	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
)

// SelfUser is a small projection of the logged-in Telegram user, shaped
// for the frontend profile menu. Photo download is best-effort; an empty
// PhotoBase64 means "no photo available" and the UI should fall back to
// initials.
type SelfUser struct {
	UserID      int64  `json:"user_id"`
	DisplayName string `json:"display_name"`
	Username    string `json:"username,omitempty"`
	Phone       string `json:"phone,omitempty"`
	PhotoBase64 string `json:"photo_base64,omitempty"`
}

// selfUser is process-wide cache for Me(). Shape matches what we return.
var selfUserCache atomic.Pointer[SelfUser]

// Me resolves the logged-in Telegram user and a small base64-encoded
// avatar photo. The result is cached for the lifetime of the process —
// avatar changes during a session are rare and a re-login refreshes it.
func (a *App) Me() (SelfUser, error) {
	if cached := selfUserCache.Load(); cached != nil {
		return *cached, nil
	}

	client, err := auth.Connect()
	if err != nil {
		return SelfUser{}, fmt.Errorf("connect: %w", err)
	}

	var u *tg.User
	err = client.Run(a.ctx, func(ctx context.Context) error {
		users, err := client.API().UsersGetUsers(ctx, []tg.InputUserClass{&tg.InputUserSelf{}})
		if err != nil {
			return err
		}
		for _, raw := range users {
			if user, ok := raw.(*tg.User); ok && user.ID != 0 {
				u = user
				return nil
			}
		}
		return fmt.Errorf("users.getUsers returned no self user")
	})
	if err != nil {
		return SelfUser{}, err
	}

	out := SelfUser{
		UserID:      u.ID,
		DisplayName: fullDisplayName(u),
		Username:    strings.TrimSpace(u.Username),
		Phone:       formatPhone(u.Phone),
	}

	// Photo download is best-effort — privacy settings, network blips, or
	// a missing photo all mean "no photo," which the UI handles via
	// initials.
	if photo, ok := u.Photo.(*tg.UserProfilePhoto); ok {
		if data, err := downloadSelfPhoto(a.ctx, client, photo.PhotoID); err == nil {
			out.PhotoBase64 = base64.StdEncoding.EncodeToString(data)
		} else {
			fmt.Printf("self photo download failed: %v\n", err)
		}
	}

	selfUserCache.Store(&out)
	return out, nil
}

// downloadSelfPhoto fetches the small (160x160) profile photo for the
// logged-in user. Caller has already created `client`; we run a fresh
// session to keep this independent of any wider transaction.
func downloadSelfPhoto(ctx context.Context, client clientRunner, photoID int64) ([]byte, error) {
	dlCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var buf bytes.Buffer
	err := client.Run(dlCtx, func(ctx context.Context) error {
		loc := &tg.InputPeerPhotoFileLocation{
			Big:     false,
			Peer:    &tg.InputPeerSelf{},
			PhotoID: photoID,
		}
		_, err := downloader.NewDownloader().Download(client.API(), loc).Stream(ctx, &buf)
		return err
	})
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// clientRunner is the minimal slice of *telegram.Client we need. Declaring
// it makes downloadSelfPhoto trivially testable without standing up a
// real client.
type clientRunner interface {
	Run(ctx context.Context, f func(ctx context.Context) error) error
	API() *tg.Client
}

func fullDisplayName(u *tg.User) string {
	first := strings.TrimSpace(u.FirstName)
	last := strings.TrimSpace(u.LastName)
	name := strings.TrimSpace(first + " " + last)
	if name != "" {
		return name
	}
	if uname := strings.TrimSpace(u.Username); uname != "" {
		return "@" + uname
	}
	return fmt.Sprintf("User %d", u.ID)
}

func formatPhone(raw string) string {
	digits := strings.TrimSpace(raw)
	if digits == "" {
		return ""
	}
	if strings.HasPrefix(digits, "+") {
		return digits
	}
	return "+" + digits
}
