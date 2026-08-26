package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/gotd/td/telegram"
)

// ErrPersonalDriveSetupRequired means the user is authenticated but has not
// explicitly selected or created the Telegram channel TDrive should use.
// Missing configuration must never create a remote channel as a side effect.
var ErrPersonalDriveSetupRequired = errors.New("personal drive setup required")

func GetTDriveChannel(_ context.Context, _ *telegram.Client) (int64, error) {
	savedId, err := LoadConfig()
	if err != nil {
		return 0, fmt.Errorf("load TDrive channel config: %w", err)
	}

	if savedId != 0 {
		return savedId, nil
	}

	return 0, ErrPersonalDriveSetupRequired
}
