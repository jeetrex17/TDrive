package auth

import (
	"encoding/json"
	"fmt"
	"os"
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

	err = os.WriteFile("config.json", jsonData, 0o644)
	if err != nil {
		return err
	}

	fmt.Println("Config.json is saved with id : ", id)

	return nil
}

func LoadConfig() (int64, error) {
	filepath := "config.json"

	file, err := os.ReadFile(filepath)

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
