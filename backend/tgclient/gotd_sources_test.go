package tgclient

import (
	"testing"

	tgpeer "github.com/gotd/td/telegram/message/peer"
	"github.com/gotd/td/tg"
)

func TestDialogSourcesClassifyUserBotGroupSupergroupChannelAndSelf(t *testing.T) {
	entities := tgpeer.NewEntities(
		map[int64]*tg.User{
			10: {ID: 10, AccessHash: 101, FirstName: "Alice"},
			11: {ID: 11, AccessHash: 102, FirstName: "Helper", Bot: true},
			12: {ID: 12, Self: true},
		},
		map[int64]*tg.Chat{20: {ID: 20, Title: "Friends", Noforwards: true}},
		map[int64]*tg.Channel{
			30: {ID: 30, AccessHash: 301, Title: "Forum", Megagroup: true},
			31: {ID: 31, AccessHash: 302, Title: "News", Broadcast: true},
		},
	)
	for _, test := range []struct {
		input     tg.InputPeerClass
		kind      PeerKind
		id        int64
		protected bool
	}{
		{&tg.InputPeerUser{UserID: 10}, PeerUser, 10, false},
		{&tg.InputPeerUser{UserID: 11}, PeerBot, 11, false},
		{&tg.InputPeerUser{UserID: 12}, PeerSelf, 12, false},
		{&tg.InputPeerSelf{}, PeerSelf, 12, false},
		{&tg.InputPeerChat{ChatID: 20}, PeerGroup, 20, true},
		{&tg.InputPeerChannel{ChannelID: 30}, PeerSupergroup, 30, false},
		{&tg.InputPeerChannel{ChannelID: 31}, PeerChannel, 31, false},
	} {
		actual, ok := sourceFromDialog(test.input, entities, 12)
		if !ok || actual.Kind != test.kind || actual.ID != test.id || actual.Protected != test.protected {
			t.Errorf("%T = %#v, ok %v", test.input, actual, ok)
		}
	}
}

func TestSavedMessagesIsAvailableWithoutPriorDialog(t *testing.T) {
	peers := ensureSavedMessages(nil, 1234)
	if len(peers) != 1 || peers[0].Kind != PeerSelf || peers[0].ID != 1234 {
		t.Fatalf("self candidate = %#v", peers)
	}
	peers = ensureSavedMessages(peers, 1234)
	if len(peers) != 1 {
		t.Fatalf("duplicate self candidate = %#v", peers)
	}
}

func TestMediaPeerKindsUseDistinctTelegramInputPeers(t *testing.T) {
	for _, test := range []struct {
		kind PeerKind
		want any
	}{
		{PeerUser, &tg.InputPeerUser{}}, {PeerBot, &tg.InputPeerUser{}},
		{PeerGroup, &tg.InputPeerChat{}}, {PeerSelf, &tg.InputPeerSelf{}},
		{PeerSupergroup, &tg.InputPeerChannel{}}, {PeerChannel, &tg.InputPeerChannel{}},
	} {
		peer := toMediaPeer(InputPeer{Kind: test.kind, ChannelID: 42, AccessHash: 88})
		if peer.TypeID() != test.want.(interface{ TypeID() uint32 }).TypeID() {
			t.Errorf("%s input peer = %T, want %T", test.kind, peer, test.want)
		}
	}
}

func TestMediaMessageIdentityRejectsSameNumericIDInAnotherPeer(t *testing.T) {
	for _, test := range []struct {
		kind    PeerKind
		message tg.PeerClass
		want    bool
	}{
		{PeerUser, &tg.PeerUser{UserID: 42}, true},
		{PeerBot, &tg.PeerUser{UserID: 42}, true},
		{PeerSelf, &tg.PeerUser{UserID: 42}, true},
		{PeerGroup, &tg.PeerChat{ChatID: 42}, true},
		{PeerSupergroup, &tg.PeerChannel{ChannelID: 42}, true},
		{PeerUser, &tg.PeerChat{ChatID: 42}, false},
		{PeerGroup, &tg.PeerUser{UserID: 42}, false},
		{PeerSupergroup, &tg.PeerUser{UserID: 42}, false},
		{PeerUser, &tg.PeerUser{UserID: 43}, false},
	} {
		message := &tg.Message{ID: 7, PeerID: test.message}
		if got := messageBelongsToPeer(message, InputPeer{Kind: test.kind, ChannelID: 42}); got != test.want {
			t.Errorf("%s with %T = %v, want %v", test.kind, test.message, got, test.want)
		}
	}
}
