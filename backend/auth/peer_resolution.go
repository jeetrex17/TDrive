package auth

import (
	"context"
	"fmt"

	"github.com/gotd/td/telegram/query/dialogs"
	"github.com/gotd/td/tg"
)

// FolderDialogsQuery sets the MTProto folder flag explicitly, including for
// folder 0. gotd's generated query builder only assigns the value, which does
// not mark this conditional field present on the wire.
type FolderDialogsQuery struct {
	API      *tg.Client
	FolderID int
}

func (q FolderDialogsQuery) Query(ctx context.Context, request dialogs.Request) (tg.MessagesDialogsClass, error) {
	req := &tg.MessagesGetDialogsRequest{
		OffsetDate: request.OffsetDate,
		OffsetID:   request.OffsetID,
		OffsetPeer: request.OffsetPeer,
		Limit:      request.Limit,
	}
	req.SetFolderID(q.FolderID)
	return q.API.MessagesGetDialogs(ctx, req)
}

// ResolveDriveChannel discovers a fresh, account-specific hash from dialog
// entities. channels.getChannels requires an already known hash and cannot
// bootstrap this lookup. Shared drives can be megagroups as well as channels.
func ResolveDriveChannel(ctx context.Context, api *tg.Client, channelID int64) (*tg.InputChannel, *tg.InputPeerChannel, error) {
	if channelID <= 0 {
		return nil, nil, fmt.Errorf("invalid channel id %d", channelID)
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	for _, folderID := range []int{0, 1} {
		iterator := dialogs.NewIterator(FolderDialogsQuery{API: api, FolderID: folderID}, 100)
		for iterator.Next(ctx) {
			elem := iterator.Value()
			channel, ok := elem.Entities.Channel(channelID)
			if !ok || channel.Left || channel.Min || channel.AccessHash == 0 {
				continue
			}
			return &tg.InputChannel{ChannelID: channelID, AccessHash: channel.AccessHash},
				&tg.InputPeerChannel{ChannelID: channelID, AccessHash: channel.AccessHash}, nil
		}
		if err := iterator.Err(); err != nil {
			return nil, nil, fmt.Errorf("resolve channel %d dialogs: %w", channelID, err)
		}
	}
	return nil, nil, fmt.Errorf("could not resolve access_hash for channel_id=%d", channelID)
}
