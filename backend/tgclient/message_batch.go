package tgclient

import (
	"context"
	"fmt"
	"slices"

	"github.com/gotd/td/tg"
)

// ChannelMessageBatchReader fetches several messages from one channel.
// On success, missing IDs are omitted from the response.
type ChannelMessageBatchReader interface {
	GetChannelMessages(context.Context, InputPeer, []int64) (map[int64]HistoryMessage, error)
}

func (g *Gotd) GetChannelMessages(ctx context.Context, peer InputPeer, ids []int64) (map[int64]HistoryMessage, error) {
	found := make(map[int64]HistoryMessage, len(ids))
	if len(ids) == 0 {
		return found, nil
	}
	err := g.run(ctx, func(ctx context.Context, api *tg.Client) error {
		for batch := range slices.Chunk(ids, channelsGetMessagesLimit) {
			messages, err := channelMessages(ctx, api, peer, batch)
			if err != nil {
				return err
			}
			for _, id := range batch {
				raw, present := messages[id]
				if !present {
					continue
				}
				if _, missing := raw.(*tg.MessageEmpty); missing {
					continue
				}
				msg, ok := historyMessageFromTG(raw)
				if !ok || msg.MsgID != id || (!msg.Placeholder && !messageBelongsToPeer(raw, peer)) {
					return fmt.Errorf("tgclient: invalid message %d in batch response", id)
				}
				found[id] = msg
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return found, nil
}

func (f *Fake) GetChannelMessages(ctx context.Context, peer InputPeer, ids []int64) (map[int64]HistoryMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	pending := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		pending[id] = struct{}{}
	}
	found := make(map[int64]HistoryMessage, len(ids))
	for _, message := range f.history {
		_, requested := pending[message.MsgID]
		if requested && (message.ChannelID == 0 || message.ChannelID == peer.ChannelID) &&
			(peer.Kind == "" || message.PeerKind == peer.PeerKind()) {
			found[message.MsgID] = message
			delete(pending, message.MsgID)
			if len(pending) == 0 {
				break
			}
		}
	}
	return found, nil
}
