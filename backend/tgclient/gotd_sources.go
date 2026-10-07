package tgclient

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/gotd/td/telegram/query/dialogs"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

const maxSourceDialogs = 2000

func (g *Gotd) ListMediaSourcePeers(ctx context.Context) ([]SourcePeer, error) {
	var peers []SourcePeer
	err := g.run(ctx, func(ctx context.Context, api *tg.Client) error {
		selfUsers, err := api.UsersGetUsers(ctx, []tg.InputUserClass{&tg.InputUserSelf{}})
		if err != nil {
			return fmt.Errorf("tgclient: identify Saved Messages: %w", err)
		}
		var selfID int64
		for _, value := range selfUsers {
			if user, ok := value.(*tg.User); ok && user.Self {
				selfID = user.ID
				break
			}
		}
		seen := make(map[InputPeer]struct{})
		truncated := false
		for _, folder := range []int{0, 1} {
			iterator := dialogs.NewIterator(folderDialogsQuery{api: api, folderID: folder}, ownedDialogsBatchSize)
			visited := 0
			for iterator.Next(ctx) {
				visited++
				if visited > maxSourceDialogs/2 {
					truncated = true
					break
				}
				elem := iterator.Value()
				peer, ok := sourceFromDialog(elem.Peer, elem.Entities, selfID)
				if !ok {
					continue
				}
				key := InputPeer{Kind: peer.Kind, ChannelID: peer.ID}
				if _, duplicate := seen[key]; duplicate {
					continue
				}
				seen[key] = struct{}{}
				peers = append(peers, peer)
			}
			if err := iterator.Err(); err != nil {
				return fmt.Errorf("tgclient: list media dialogs: %w", err)
			}
		}
		peers = ensureSavedMessages(peers, selfID)
		if truncated {
			for index := range peers {
				peers[index].Truncated = true
			}
		}
		return nil
	})
	slices.SortFunc(peers, func(a, b SourcePeer) int {
		if n := strings.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title)); n != 0 {
			return n
		}
		if n := strings.Compare(string(a.Kind), string(b.Kind)); n != 0 {
			return n
		}
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return peers, err
}

func ensureSavedMessages(peers []SourcePeer, selfID int64) []SourcePeer {
	if selfID <= 0 {
		return peers
	}
	for _, peer := range peers {
		if peer.Kind == PeerSelf && peer.ID == selfID {
			return peers
		}
	}
	return append(peers, SourcePeer{Kind: PeerSelf, ID: selfID, Title: "Saved Messages"})
}

func sourceFromDialog(input tg.InputPeerClass, entities interface {
	User(int64) (*tg.User, bool)
	Chat(int64) (*tg.Chat, bool)
	Channel(int64) (*tg.Channel, bool)
}, selfID int64) (SourcePeer, bool) {
	switch peer := input.(type) {
	case *tg.InputPeerSelf:
		if selfID <= 0 {
			return SourcePeer{}, false
		}
		return SourcePeer{Kind: PeerSelf, ID: selfID, Title: "Saved Messages"}, true
	case *tg.InputPeerUser:
		user, ok := entities.User(peer.UserID)
		if !ok || user.Deleted || (!user.Self && user.AccessHash == 0) {
			return SourcePeer{}, false
		}
		if user.Self {
			return SourcePeer{Kind: PeerSelf, ID: user.ID, Title: "Saved Messages"}, true
		}
		return sourceFromUser(user), true
	case *tg.InputPeerChat:
		chat, ok := entities.Chat(peer.ChatID)
		if !ok || chat.Left || chat.Deactivated {
			return SourcePeer{}, false
		}
		return sourceFromChat(chat), true
	case *tg.InputPeerChannel:
		channel, ok := entities.Channel(peer.ChannelID)
		if !ok || channel.Left || channel.AccessHash == 0 {
			return SourcePeer{}, false
		}
		return sourceFromChannel(channel), true
	default:
		return SourcePeer{}, false
	}
}

func sourceFromUser(user *tg.User) SourcePeer {
	kind := PeerUser
	if user.Bot {
		kind = PeerBot
	}
	title := strings.TrimSpace(strings.TrimSpace(user.FirstName + " " + user.LastName))
	if title == "" {
		title = user.Username
	}
	if title == "" {
		title = "Telegram chat"
	}
	peer := SourcePeer{Kind: kind, ID: user.ID, AccessHash: user.AccessHash,
		Title: title, Username: user.Username, Restricted: user.Restricted && restrictedHere(user.RestrictionReason)}
	if photo, ok := user.Photo.(*tg.UserProfilePhoto); ok {
		peer.PhotoID = photo.PhotoID
	}
	return peer
}

func sourceFromChat(chat *tg.Chat) SourcePeer {
	peer := SourcePeer{Kind: PeerGroup, ID: chat.ID, Title: strings.TrimSpace(chat.Title), Protected: chat.Noforwards}
	if photo, ok := chat.Photo.(*tg.ChatPhoto); ok {
		peer.PhotoID = photo.PhotoID
	}
	return peer
}

func sourceFromChannel(channel *tg.Channel) SourcePeer {
	peer := joinedChannel(channel)
	peer.Kind = PeerChannel
	if channel.Megagroup {
		peer.Kind = PeerSupergroup
	}
	return peer
}

func (g *Gotd) GetMediaSourcePeer(ctx context.Context, peer InputPeer) (SourcePeer, error) {
	var out SourcePeer
	err := g.run(ctx, func(ctx context.Context, api *tg.Client) error {
		switch peer.PeerKind() {
		case PeerSelf:
			users, err := api.UsersGetUsers(ctx, []tg.InputUserClass{&tg.InputUserSelf{}})
			if err != nil {
				return err
			}
			for _, value := range users {
				if user, ok := value.(*tg.User); ok && user.Self && user.ID == peer.ChannelID {
					out = SourcePeer{Kind: PeerSelf, ID: user.ID, Title: "Saved Messages"}
					return nil
				}
			}
		case PeerUser, PeerBot:
			users, err := api.UsersGetUsers(ctx, []tg.InputUserClass{&tg.InputUser{UserID: peer.ChannelID, AccessHash: peer.AccessHash}})
			if err != nil {
				return err
			}
			for _, value := range users {
				if user, ok := value.(*tg.User); ok && user.ID == peer.ChannelID && !user.Deleted && !user.Self {
					out = sourceFromUser(user)
					if out.Kind != peer.PeerKind() {
						return ErrChannelUnavailable
					}
					return nil
				}
			}
		case PeerGroup:
			result, err := api.MessagesGetChats(ctx, []int64{peer.ChannelID})
			if err != nil {
				return err
			}
			for _, value := range result.GetChats() {
				if chat, ok := value.(*tg.Chat); ok && chat.ID == peer.ChannelID && !chat.Left && !chat.Deactivated {
					out = sourceFromChat(chat)
					return nil
				}
			}
		case PeerChannel, PeerSupergroup:
			result, err := api.ChannelsGetChannels(ctx, []tg.InputChannelClass{&tg.InputChannel{ChannelID: peer.ChannelID, AccessHash: peer.AccessHash}})
			if err != nil {
				return err
			}
			for _, value := range result.GetChats() {
				if channel, ok := value.(*tg.Channel); ok && channel.ID == peer.ChannelID {
					out = sourceFromChannel(channel)
					if out.Kind != peer.PeerKind() || (channel.Left && (channel.Megagroup || channel.Username == "")) {
						return ErrChannelUnavailable
					}
					return nil
				}
			}
		default:
			return ErrChannelUnavailable
		}
		return ErrChannelUnavailable
	})
	if tgerr.Is(err, "CHANNEL_PRIVATE", "CHANNEL_INVALID", "CHAT_ID_INVALID", "USER_ID_INVALID", "PEER_ID_INVALID") {
		return SourcePeer{}, ErrChannelUnavailable
	}
	return out, err
}

func (g *Gotd) ResolvePublicChannel(ctx context.Context, username string) (SourcePeer, error) {
	var out SourcePeer
	err := g.run(ctx, func(ctx context.Context, api *tg.Client) error {
		resolved, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: username})
		if err != nil {
			return err
		}
		channelPeer, ok := resolved.Peer.(*tg.PeerChannel)
		if !ok {
			return ErrChannelUnavailable
		}
		for _, value := range resolved.Chats {
			if channel, ok := value.(*tg.Channel); ok && channel.ID == channelPeer.ChannelID &&
				channel.Broadcast && !channel.Megagroup && channel.Username != "" {
				out = sourceFromChannel(channel)
				return nil
			}
		}
		return ErrChannelUnavailable
	})
	if tgerr.Is(err, "USERNAME_NOT_OCCUPIED", "USERNAME_INVALID", "CHANNEL_PRIVATE") {
		return SourcePeer{}, ErrChannelUnavailable
	}
	return out, err
}
