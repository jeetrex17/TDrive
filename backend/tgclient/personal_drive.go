package tgclient

import (
	"context"
	"fmt"
	"strings"

	"TDrive/backend/auth"

	"github.com/gotd/td/telegram/query/dialogs"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"golang.org/x/sync/errgroup"
)

const ownedDialogsBatchSize = 100

// folderDialogsQuery shares the folder-flag handling used by fresh drive peer
// resolution, so archived and primary dialog requests have the same semantics.
type folderDialogsQuery struct {
	api      *tg.Client
	folderID int
}

func (q folderDialogsQuery) Query(ctx context.Context, request dialogs.Request) (tg.MessagesDialogsClass, error) {
	return (auth.FolderDialogsQuery{API: q.api, FolderID: q.folderID}).Query(ctx, request)
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
// It does not require creator or admin rights. The folders are walked at
// once, since each is a chain of requests that cannot start before the last
// page answers; the result still lists the first folder first.
func collectJoinedBroadcastChannels(ctx context.Context, queries ...dialogs.Query) ([]JoinedBroadcastChannel, error) {
	folders := make([][]JoinedBroadcastChannel, len(queries))
	group, ctx := errgroup.WithContext(ctx)
	for index, query := range queries {
		group.Go(func() error {
			iterator := dialogs.NewIterator(query, ownedDialogsBatchSize)
			for iterator.Next(ctx) {
				elem := iterator.Value()
				peer, ok := elem.Peer.(*tg.InputPeerChannel)
				if !ok {
					continue
				}
				channel, ok := elem.Entities.Channel(peer.ChannelID)
				if ok && channel.Broadcast && !channel.Megagroup && !channel.Left {
					folders[index] = append(folders[index], joinedChannel(channel))
				}
			}
			if err := iterator.Err(); err != nil {
				return fmt.Errorf("tgclient: list joined broadcast channels: %w", err)
			}
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}
	seen := make(map[int64]struct{})
	out := make([]JoinedBroadcastChannel, 0)
	for _, folder := range folders {
		for _, channel := range folder {
			if _, exists := seen[channel.ID]; exists {
				continue
			}
			seen[channel.ID] = struct{}{}
			out = append(out, channel)
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

func joinedChannel(channel *tg.Channel) JoinedBroadcastChannel {
	joined := JoinedBroadcastChannel{
		ID: channel.ID, AccessHash: channel.AccessHash,
		Title: strings.TrimSpace(channel.Title), Username: channel.Username,
		Protected:  channel.Noforwards,
		Restricted: channel.Restricted && restrictedHere(channel.RestrictionReason),
	}
	if photo, ok := channel.Photo.(*tg.ChatPhoto); ok {
		joined.PhotoID = photo.PhotoID
	}
	return joined
}

// GetBroadcastChannel is one channels.getChannels call, where listing every
// dialog to find the channel cost a request per hundred chats.
func (g *Gotd) GetBroadcastChannel(ctx context.Context, peer InputPeer) (JoinedBroadcastChannel, error) {
	var out JoinedBroadcastChannel
	err := g.run(ctx, func(ctx context.Context, api *tg.Client) error {
		result, err := api.ChannelsGetChannels(ctx, []tg.InputChannelClass{
			&tg.InputChannel{ChannelID: peer.ChannelID, AccessHash: peer.AccessHash},
		})
		if tgerr.Is(err, "CHANNEL_PRIVATE", "CHANNEL_INVALID") {
			return ErrChannelUnavailable
		}
		if err != nil {
			return err
		}
		out, err = broadcastChannelFrom(result, peer)
		return err
	})
	return out, err
}

// broadcastChannelFrom picks the channel out of a channels.getChannels answer.
// A banned account gets ChannelForbidden instead, and one that left still gets
// the channel, marked left.
func broadcastChannelFrom(result tg.MessagesChatsClass, peer InputPeer) (JoinedBroadcastChannel, error) {
	for _, chat := range result.GetChats() {
		channel, ok := chat.(*tg.Channel)
		if !ok || channel.ID != peer.ChannelID {
			continue
		}
		if !channel.Broadcast || channel.Megagroup || channel.Left {
			return JoinedBroadcastChannel{}, ErrChannelUnavailable
		}
		joined := joinedChannel(channel)
		if joined.AccessHash == 0 {
			joined.AccessHash = peer.AccessHash
		}
		return joined, nil
	}
	return JoinedBroadcastChannel{}, ErrChannelUnavailable
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
