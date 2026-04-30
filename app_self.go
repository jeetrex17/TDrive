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

	var out SelfUser
	err = client.Run(a.ctx, func(ctx context.Context) error {
		users, err := client.API().UsersGetUsers(ctx, []tg.InputUserClass{&tg.InputUserSelf{}})
		if err != nil {
			return err
		}
		var u *tg.User
		for _, raw := range users {
			if user, ok := raw.(*tg.User); ok && user.ID != 0 {
				u = user
				break
			}
		}
		if u == nil {
			return fmt.Errorf("users.getUsers returned no self user")
		}

		out = SelfUser{
			UserID:      u.ID,
			DisplayName: fullDisplayName(u),
			Username:    strings.TrimSpace(u.Username),
		}

		// Photo download stays inside this Run so the same MTProto session
		// handles auth + file fetch. Best-effort: privacy settings, a
		// missing photo, or transient errors all fall back to initials.
		photo, ok := u.Photo.(*tg.UserProfilePhoto)
		if !ok {
			return nil
		}
		dlCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		var buf bytes.Buffer
		loc := &tg.InputPeerPhotoFileLocation{
			Big:     false,
			Peer:    &tg.InputPeerSelf{},
			PhotoID: photo.PhotoID,
		}
		if _, err := downloader.NewDownloader().Download(client.API(), loc).Stream(dlCtx, &buf); err != nil {
			fmt.Printf("self photo download failed: %v\n", err)
			return nil
		}
		out.PhotoBase64 = base64.StdEncoding.EncodeToString(buf.Bytes())
		return nil
	})
	if err != nil {
		return SelfUser{}, err
	}

	selfUserCache.Store(&out)
	return out, nil
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

