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
		return fmt.Errorf("error getting config dir : %v", err)
	}

	path = filepath.Join(path, "TDrive", "config.json")

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("could not create config folder: %v", err)
	}

	if err != nil {
		return fmt.Errorf("Error gettin use config dir : %v", err)
	}

	err = os.WriteFile(path, jsonData, 0o644)
	if err != nil {
		return err
	}

	fmt.Println("Config.json is saved with id : ", id)

	return nil
}

func LoadConfig() (int64, error) {
	path, err := os.UserConfigDir()
	if err != nil {
		return 0, fmt.Errorf("error getting config dir : %v", err)
	}

	path = filepath.Join(path, "TDrive", "config.json")

	file, err := os.ReadFile(path)

	if os.IsNotExist(err) {
		return 0, nil
	}

	if err != nil {
		return 0, nil
	}

	channels := ChannelS{}

	err = json.Unmarshal(file, &channels)
	if err != nil {
		return 0, nil
	}

	cid := channels.ChannelID

	return cid, nil
}
