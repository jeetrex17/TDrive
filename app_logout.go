package main

import (
	"context"
	"fmt"
	"time"

	"TDrive/backend"
	"TDrive/backend/auth"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Logout signs the user out of TDrive.
//
// Soft mode drops only the local gotd session token, so the same Telegram
// account can re-login quickly without re-projecting their drives. The
// session stays valid server-side until Telegram itself expires it.
//
// Full mode revokes the session on Telegram's side via auth.logOut and then
// deletes all user-scoped files on disk (session, personal channel id,
// SQLite cache, API credentials).
//
// In both cases the app quits at the end. We rely on the next launch to
// rebuild syncEngine, backfillRunner, and the DB handle from a clean slate
// instead of tearing them down mid-process while goroutines may still be
// touching backend.DB.
func (a *App) Logout(mode string) error {
	m := auth.LogoutMode(mode)
	if mode == "" {
		// Empty arg defaults to the safer choice: a no-arg call shouldn't
		// silently leave a server-side session live.
		m = auth.LogoutFull
	}
	if m != auth.LogoutSoft && m != auth.LogoutFull {
		return fmt.Errorf("logout: unknown mode %q", mode)
	}

	if m == auth.LogoutFull {
		a.revokeTelegramSession()
	}

	if backend.DB != nil {
		if err := backend.DB.Close(); err != nil {
			fmt.Printf("logout: close db: %v\n", err)
		}
		backend.DB = nil
	}

	// Drop the cached self user so a re-login (without re-launching, in
	// dev mode) doesn't show the previous account's avatar.
	selfUserCache.Store(nil)

	if err := auth.ClearUserData(m); err != nil {
		return err
	}

	runtime.Quit(a.ctx)
	return nil
}

// revokeTelegramSession is best-effort. The user has already chosen to log
// out, so a network failure shouldn't keep them stuck on the dashboard —
// the local cleanup still runs and the next launch will see no session.
func (a *App) revokeTelegramSession() {
	if a.Client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()

	err := a.Client.Run(ctx, func(ctx context.Context) error {
		_, err := a.Client.API().AuthLogOut(ctx)
		return err
	})
	if err != nil {
		fmt.Printf("logout: revoke telegram session: %v\n", err)
	}
}
