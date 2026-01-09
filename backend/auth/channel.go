package auth

import (
	"context"
	"fmt"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

func GetTDriveChannel(ctx context.Context, Client *telegram.Client) (int64, error) {
	savedId, err := LoadConfig()

	if err == nil && savedId != 0 {
		fmt.Println("channel TDrive found :", savedId)

		return savedId, nil
	} else {
		fmt.Println("chneel donest exists , making new private Tdriive channel")
	}

	return CreateTDriveChannel(ctx, Client)
}

func CreateTDriveChannel(ctx context.Context, Clinet *telegram.Client) (int64, error) {
	updates, err := Clinet.API().ChannelsCreateChannel(ctx, &tg.ChannelsCreateChannelRequest{
		Broadcast: true,
		Megagroup: false,
		Title:     "TDrive",
		About:     "Tdrive not so private Personal Storage",
		Address:   "",
	})
	if err != nil {
		return 0, err
	}

	var newID int64

	switch u := updates.(type) {
	case *tg.Updates:
		newID = findChannelID(u.Chats)
	case *tg.UpdatesCombined:
		newID = findChannelID(u.Chats)
	}
	if newID == 0 {
		return 0, err
	}

	SaveConfig(newID)
	return newID, nil
}

func findChannelID(chats []tg.ChatClass) int64 {
	for _, chat := range chats {
		if channel, ok := chat.(*tg.Channel); ok {
			return channel.ID
		}
	}
	return 0
}
