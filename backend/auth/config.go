package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type ChannelS struct {
	ChannelID int64 `json:"channel_id"`
}

func SaveConfig(id int64) error {
	schannel := ChannelS{
		ChannelID: id,
	}
	jsonData, err := json.MarshalIndent(schannel, "", " ")
	if err != nil {
		return err
	}

	path, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("error getting config dir: %v", err)
	}

	path = filepath.Join(path, "TDrive", "config.json")

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, privateDirMode); err != nil {
		return fmt.Errorf("could not create config folder: %v", err)
	}
	_ = os.Chmod(dir, privateDirMode)

	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmp.Write(jsonData); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := tmp.Chmod(privateFileMode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp config: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	_ = os.Chmod(path, privateFileMode)

	return nil
}

func LoadConfig() (int64, error) {
	path, err := os.UserConfigDir()
	if err != nil {
		return 0, fmt.Errorf("error getting config dir: %v", err)
	}

	path = filepath.Join(path, "TDrive", "config.json")

	file, err := os.ReadFile(path)

	if os.IsNotExist(err) {
		return 0, nil
	}

	if err != nil {
		return 0, fmt.Errorf("read config: %w", err)
	}

	channels := ChannelS{}

	err = json.Unmarshal(file, &channels)
	if err != nil {
		return 0, fmt.Errorf("parse config: %w", err)
	}

	cid := channels.ChannelID

	return cid, nil
}
