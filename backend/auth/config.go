package auth

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
)

type ChannelS struct {
	ChannelID int64 `json:"channel_id"`
}

func SaveConfig(id int64) {
	schannel := ChannelS{
		ChannelID: id,
	}
	jsonData, err := json.Marshal(schannel)
	if err != nil {
		return
	}

	err = os.WriteFile("config.json", jsonData, 0o644)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Config.json is saved")
}

func LoadConfig() int64 {
	filepath := "config.json"

	file, err := os.ReadFile(filepath)
	if err != nil {
		log.Fatal(err)
	}

	channels := ChannelS{}

	err = json.Unmarshal(file, &channels)
	if err != nil {
		log.Fatal(err)
	}

	cid := channels.ChannelID

	return cid
}
