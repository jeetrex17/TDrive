package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"TDrive/backend/datadir"
)

// ErrConfigInvalid marks a config.json that exists but cannot be parsed.
// Callers that can ask the user for an explicit drive choice may treat it as
// "not configured"; nothing may create or overwrite a drive silently on it.
var ErrConfigInvalid = errors.New("invalid personal drive config")

type ChannelS struct {
	ChannelID int64 `json:"channel_id"`
}

func SaveConfig(id int64) error {
	channel := ChannelS{
		ChannelID: id,
	}
	jsonData, err := json.MarshalIndent(channel, "", " ")
	if err != nil {
		return err
	}

	dir, err := datadir.Dir()
	if err != nil {
		return fmt.Errorf("error getting config dir: %v", err)
	}
	path := filepath.Join(dir, "config.json")

	if err := writePrivateFile(path, jsonData); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

func LoadConfig() (int64, error) {
	dir, err := datadir.Dir()
	if err != nil {
		return 0, fmt.Errorf("error getting config dir: %v", err)
	}

	path := filepath.Join(dir, "config.json")

	file, err := os.ReadFile(path)

	if os.IsNotExist(err) {
		return 0, nil
	}

	if err != nil {
		return 0, fmt.Errorf("read config: %w", err)
	}

	channels := ChannelS{}
	if err := json.Unmarshal(file, &channels); err != nil {
		return 0, fmt.Errorf("%w: %v", ErrConfigInvalid, err)
	}
	return channels.ChannelID, nil
}
