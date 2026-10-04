package tgclient

import (
	"context"
	"fmt"
	"strings"

	"github.com/gotd/td/telegram/query/dialogs"
	"github.com/gotd/td/tg"
)

const ownedDialogsBatchSize = 100

// folderDialogsQuery sets the folder flag explicitly, including for folder 0.
// Assigning FolderID directly does not set the MTProto conditional-field bit.
type folderDialogsQuery struct {
	api      *tg.Client
	folderID int
}

func (q folderDialogsQuery) Query(ctx context.Context, request dialogs.Request) (tg.MessagesDialogsClass, error) {
	req := &tg.MessagesGetDialogsRequest{
		OffsetDate: request.OffsetDate,
		OffsetID:   request.OffsetID,
		OffsetPeer: request.OffsetPeer,
		Limit:      request.Limit,
	}
	req.SetFolderID(q.folderID)
	return q.api.MessagesGetDialogs(ctx, req)
}

// collectOwnedBroadcastChannels walks every supplied dialog folder to
// completion. It returns no partial list when any page fails, because an
// incomplete picker could incorrectly imply that a user's drive is absent.
func collectOwnedBroadcastChannels(ctx context.Context, queries ...dialogs.Query) ([]OwnedBroadcastChannel, error) {
	seen := make(map[int64]struct{})
	channels := make([]OwnedBroadcastChannel, 0)
	for _, query := range queries {
		iterator := dialogs.NewIterator(query, ownedDialogsBatchSize)
		for iterator.Next(ctx) {
			elem := iterator.Value()
			peer, ok := elem.Peer.(*tg.InputPeerChannel)
			if !ok {
				continue
			}
			channel, ok := elem.Entities.Channel(peer.ChannelID)
			if !ok || !channel.Creator || !channel.Broadcast || channel.Megagroup || channel.Left {
				continue
			}
			if _, exists := seen[channel.ID]; exists {
				continue
			}
			seen[channel.ID] = struct{}{}
			dialog, _ := elem.Dialog.(*tg.Dialog)
			channels = append(channels, OwnedBroadcastChannel{
				ID:          channel.ID,
				AccessHash:  channel.AccessHash,
				Title:       strings.TrimSpace(channel.Title),
				CreatedAt:   int64(channel.Date),
				HasActivity: dialog != nil && dialog.TopMessage > 0,
			})
		}
		if err := iterator.Err(); err != nil {
			return nil, fmt.Errorf("tgclient: list owned broadcast channels: %w", err)
		}
	}
	return channels, nil
}

func (g *Gotd) ListOwnedBroadcastChannels(ctx context.Context) ([]OwnedBroadcastChannel, error) {
	var channels []OwnedBroadcastChannel
	err := g.run(ctx, func(ctx context.Context, api *tg.Client) error {
		var err error
		channels, err = collectOwnedBroadcastChannels(ctx,
			folderDialogsQuery{api: api, folderID: 0},
			folderDialogsQuery{api: api, folderID: 1},
		)
		return err
	})
	if err != nil {
		return nil, err
	}
	return channels, nil
}

// collectJoinedBroadcastChannels includes both primary and archived dialogs.
// It does not require creator or admin rights.
func collectJoinedBroadcastChannels(ctx context.Context, queries ...dialogs.Query) ([]JoinedBroadcastChannel, error) {
	seen := make(map[int64]struct{})
	out := make([]JoinedBroadcastChannel, 0)
	for _, query := range queries {
		iterator := dialogs.NewIterator(query, ownedDialogsBatchSize)
		for iterator.Next(ctx) {
			elem := iterator.Value()
			peer, ok := elem.Peer.(*tg.InputPeerChannel)
			if !ok {
				continue
			}
			channel, ok := elem.Entities.Channel(peer.ChannelID)
			if !ok || !channel.Broadcast || channel.Megagroup || channel.Left {
				continue
			}
			if _, exists := seen[channel.ID]; exists {
				continue
			}
			seen[channel.ID] = struct{}{}
			out = append(out, JoinedBroadcastChannel{
				ID: channel.ID, AccessHash: channel.AccessHash,
				Title: strings.TrimSpace(channel.Title), Username: channel.Username,
				Protected: channel.Noforwards,
			})
		}
		if err := iterator.Err(); err != nil {
			return nil, fmt.Errorf("tgclient: list joined broadcast channels: %w", err)
		}
	}
	return out, nil
}

func (g *Gotd) ListJoinedBroadcastChannels(ctx context.Context) ([]JoinedBroadcastChannel, error) {
	var channels []JoinedBroadcastChannel
	err := g.run(ctx, func(ctx context.Context, api *tg.Client) error {
		var err error
		channels, err = collectJoinedBroadcastChannels(ctx,
			folderDialogsQuery{api: api, folderID: 0},
			folderDialogsQuery{api: api, folderID: 1},
		)
		return err
	})
	return channels, err
}

func (g *Gotd) GetJoinedBroadcastChannel(ctx context.Context, channelID int64) (JoinedBroadcastChannel, error) {
	channels, err := g.ListJoinedBroadcastChannels(ctx)
	if err != nil {
		return JoinedBroadcastChannel{}, err
	}
	for _, channel := range channels {
		if channel.ID == channelID {
			return channel, nil
		}
	}
	return JoinedBroadcastChannel{}, fmt.Errorf("tgclient: joined broadcast channel %d unavailable", channelID)
}

func (g *Gotd) CreateBroadcastChannel(ctx context.Context, title, about string) (OwnedBroadcastChannel, error) {
	var created OwnedBroadcastChannel
	err := g.run(ctx, func(ctx context.Context, api *tg.Client) error {
		updates, err := api.ChannelsCreateChannel(ctx, &tg.ChannelsCreateChannelRequest{
			Broadcast: true,
			Megagroup: false,
			Title:     title,
			About:     about,
		})
		if err != nil {
			return fmt.Errorf("tgclient: create broadcast channel: %w", err)
		}
		var chats []tg.ChatClass
		switch value := updates.(type) {
		case *tg.Updates:
			chats = value.Chats
		case *tg.UpdatesCombined:
			chats = value.Chats
		}
		for _, chat := range chats {
			channel, ok := chat.(*tg.Channel)
			if !ok || channel.ID == 0 {
				continue
			}
			created = OwnedBroadcastChannel{
				ID:         channel.ID,
				AccessHash: channel.AccessHash,
				Title:      strings.TrimSpace(channel.Title),
				CreatedAt:  int64(channel.Date),
			}
			return nil
		}
		return fmt.Errorf("tgclient: create broadcast channel: no channel in response")
	})
	return created, err
}
