package core

import (
	"context"
	"fmt"

	"TDrive/backend/mountpolicy"
	"TDrive/backend/projection"
)

// ResolveMountEncryptionPolicy returns the encryption eligibility pinned into
// a mount session. Shared drives do not use the personal vault policy.
func (e *Engine) ResolveMountEncryptionPolicy(
	ctx context.Context,
	channelID int64,
	kind string,
	refresh mountpolicy.RefreshFunc,
	warnf WarnFunc,
) (mountpolicy.Policy, error) {
	if kind != projection.KindPersonal {
		return mountpolicy.Policy{}, nil
	}
	if e == nil {
		return mountpolicy.Policy{}, fmt.Errorf("mount: encryption eligibility is unavailable")
	}
	reads := e.ReadService()
	encryption := e.EncryptionService()
	if reads == nil || reads.DB == nil || encryption == nil {
		return mountpolicy.Policy{}, fmt.Errorf("mount: encryption eligibility is unavailable")
	}
	if refresh == nil {
		refresh = e.EnsureEncryptionPolicy
	}
	refreshWithWarning := func(ctx context.Context, channelID int64) error {
		err := refresh(ctx, channelID)
		if err != nil && warnf != nil {
			warnf("mount: refresh personal-drive encryption policy: %v\n", err)
		}
		return err
	}
	return mountpolicy.ResolvePersonal(
		ctx,
		reads.DB,
		channelID,
		refreshWithWarning,
		func() (bool, error) {
			status, err := encryption.StatusContext(ctx)
			return status.PasswordRemembered, err
		},
	)
}
