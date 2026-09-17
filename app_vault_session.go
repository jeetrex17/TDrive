package main

import (
	"context"
	"fmt"
	"time"
)

// Clearing the vault key is a mount operation as much as an encryption one, so
// it stays on the root service rather than moving into EncryptionService with
// the password methods. The reason is the ordering: the mounted filesystem, the
// open native players and the gallery's decrypted renditions all have to be
// torn down *before* the key goes, and all three live on App today. Putting
// these helpers behind the bound encryption API would have meant handing that
// service the mount, the player table and the gallery -- the exact coupling the
// service split exists to remove.
//
// When the mount moves into its own service this file is what moves with it.

// encryptionMountTransitionTimeout bounds how long a key operation will wait
// for a mounted drive to drain. It is deliberately generous: a drive with
// writes in flight can take most of a minute to close cleanly, and failing
// early would leave the key and the filesystem disagreeing.
const encryptionMountTransitionTimeout = 55 * time.Second

func (a *App) clearEncryptionSession() {
	if a == nil {
		return
	}
	a.media.closeEncryptedNativeMedia()
	a.stopGalleryPreparation()
	a.revokeGalleryImages()
	if a.engine != nil {
		a.engine.ClearEncryptionSession()
	}
	a.emit("encrypted_media_sessions_closed")
}

func (a *App) closeMountForEncryptionTransitionLocked(ctx context.Context) error {
	if err := a.closeMountControllerLocked(ctx); err != nil {
		return fmt.Errorf("eject TDrive before changing the encryption session: %w", err)
	}
	return nil
}

func (a *App) lockEncryptionSession() error {
	ctx, cancel := context.WithTimeout(context.Background(), encryptionMountTransitionTimeout)
	defer cancel()
	release, err := a.acquireMountLifecycle(ctx)
	if err != nil {
		return fmt.Errorf("eject TDrive before changing the encryption session: %w", err)
	}
	defer release()
	return a.lockEncryptionSessionLocked(ctx)
}

// lockEncryptionSessionLocked requires mountLifecycle to be held. Keeping the
// controller close and key erasure under one gate prevents a racing Start from
// acquiring a lease between those two steps.
func (a *App) lockEncryptionSessionLocked(ctx context.Context) error {
	if err := a.closeMountForEncryptionTransitionLocked(ctx); err != nil {
		return err
	}
	a.clearEncryptionSession()
	return nil
}

// runWithClosedMountForLogout holds the lifecycle gate through all local
// logout cleanup. A queued mount can only resume after logout has removed the
// session data, at which point it cannot acquire an encryption key lease.
func (a *App) runWithClosedMountForLogout(action func() error) error {
	ctx, cancel := context.WithTimeout(context.Background(), encryptionMountTransitionTimeout)
	defer cancel()
	release, err := a.acquireMountLifecycle(ctx)
	if err != nil {
		return fmt.Errorf("eject TDrive before changing the encryption session: %w", err)
	}
	defer release()
	if err := a.lockEncryptionSessionLocked(ctx); err != nil {
		return err
	}
	if action != nil {
		if err := action(); err != nil {
			return err
		}
	}
	// Only successful local cleanup is terminal. If cleanup fails, the mount is
	// already safely closed and the key cleared, but the user may unlock and
	// remount instead of being trapped in a half-logged-out process.
	a.mountLifecycleTerminal = true
	return nil
}
