package mountcontroller

import (
	"context"
	"errors"
	"fmt"

	"TDrive/backend/core"
	encservice "TDrive/backend/services/encryption"
)

type engineMountKeyLeaser struct {
	engine *core.Engine
}

func (provider engineMountKeyLeaser) Acquire(ctx context.Context, drive Drive) (MountKeyLease, error) {
	if ctx == nil {
		return nil, ErrInvalidContext
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if provider.engine == nil || !drive.Encrypted || !drive.EncryptionUnlocked || drive.Kind != DriveKindPersonal {
		return nil, ErrEncryptionPasswordRequired
	}
	lease, err := provider.engine.EncryptionService().AcquireMasterKeyLease()
	if errors.Is(err, encservice.ErrPasswordRequired) {
		return nil, ErrEncryptionPasswordRequired
	}
	if err != nil {
		return nil, fmt.Errorf("%w: encrypted mount key is unavailable", ErrWritableUnavailable)
	}
	return lease, nil
}

func closeSessionKey(active *session) {
	if active == nil || active.key == nil {
		return
	}
	active.key.Close()
	active.key = nil
}

var _ MountKeyLeaser = engineMountKeyLeaser{}
