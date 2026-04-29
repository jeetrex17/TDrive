package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"TDrive/backend/auth"

	"github.com/gotd/td/tg"
)

// ResolveUsernames maps Telegram user IDs to display names. Used by the
// frontend uploader-chip cache.
//
// Returns map keys as decimal strings (Wails encodes int64 keys as strings
// in JSON, so this avoids a JS-side conversion). Zero / negative IDs are
// silently dropped from the input. Names that can't be resolved (privacy
// settings, deleted account, network error on a single user) are simply
// missing from the result; the frontend treats absent entries as "no chip."
//
// Display name picked in this order:
//  1. First name (+ optional last initial)
//  2. Username (without @ prefix)
//  3. "User <id>" fallback
//
// The current user is auto-tagged as "You" so the frontend doesn't have
// to re-derive that.
func (a *App) ResolveUsernames(userIDs []int64) (map[string]string, error) {
	if a.tg == nil {
		return nil, fmt.Errorf("tg client not ready")
	}

	out := make(map[string]string, len(userIDs))

	// Dedupe + drop zeros. Carve out self up front; we don't need to ask
	// Telegram for our own name.
	self, _ := a.actorID(a.ctx)
	seen := make(map[int64]struct{}, len(userIDs))
	toAsk := make([]int64, 0, len(userIDs))
	for _, id := range userIDs {
		if id <= 0 {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		if id == self {
			out[strconv.FormatInt(id, 10)] = "You"
			continue
		}
		toAsk = append(toAsk, id)
	}
	if len(toAsk) == 0 {
		return out, nil
	}

	client, err := auth.Connect()
	if err != nil {
		return out, fmt.Errorf("connect: %w", err)
	}

	err = client.Run(a.ctx, func(ctx context.Context) error {
		// Telegram's users.getUsers takes up to 100 IDs per call. Batch.
		const batchSize = 100
		for i := 0; i < len(toAsk); i += batchSize {
			end := i + batchSize
			if end > len(toAsk) {
				end = len(toAsk)
			}
			batch := toAsk[i:end]
			req := make([]tg.InputUserClass, 0, len(batch))
			for _, id := range batch {
				req = append(req, &tg.InputUser{UserID: id})
			}
			resolved, err := client.API().UsersGetUsers(ctx, req)
			if err != nil {
				return err
			}
			for _, u := range resolved {
				user, ok := u.(*tg.User)
				if !ok || user.ID == 0 {
					continue
				}
				out[strconv.FormatInt(user.ID, 10)] = pickDisplayName(user)
			}
		}
		return nil
	})
	if err != nil {
		// Partial result is still useful (e.g., self + earlier batches);
		// return what we have alongside the error.
		return out, err
	}
	return out, nil
}

func pickDisplayName(u *tg.User) string {
	first := strings.TrimSpace(u.FirstName)
	last := strings.TrimSpace(u.LastName)
	username := strings.TrimSpace(u.Username)

	if first != "" {
		// Add last initial if present, e.g. "Jeet R."
		if last != "" {
			r := []rune(last)
			if len(r) > 0 {
				return first + " " + strings.ToUpper(string(r[0])) + "."
			}
		}
		return first
	}
	if username != "" {
		return username
	}
	return fmt.Sprintf("User %d", u.ID)
}
