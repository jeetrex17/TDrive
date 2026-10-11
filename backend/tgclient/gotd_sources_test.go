package tgclient

import (
	"context"
	"github.com/gotd/td/bin"
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
			30: {ID: 30, AccessHash: 301, Title: "Forum", Megagroup: true, Forum: true},
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
		if !ok || actual.Kind != test.kind || actual.ID != test.id || actual.Protected != test.protected || actual.Forum != (test.kind == PeerSupergroup) {
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

func TestForumMessageIdentity(t *testing.T) {
	for _, test := range []struct {
		name  string
		reply *tg.MessageReplyHeader
		want  int64
	}{
		{"ordinary reply", &tg.MessageReplyHeader{ReplyToMsgID: 42}, 0},
		{"ordinary thread", &tg.MessageReplyHeader{ReplyToMsgID: 42, ReplyToTopID: 10}, 0},
		{"direct topic reply", &tg.MessageReplyHeader{ForumTopic: true, ReplyToMsgID: 42}, 42},
		{"nested topic reply", &tg.MessageReplyHeader{ForumTopic: true, ReplyToMsgID: 99, ReplyToTopID: 42}, 42},
	} {
		t.Run(test.name, func(t *testing.T) {
			message, ok := historyMessageFromTG(&tg.Message{ID: 100, ReplyTo: test.reply})
			if !ok || message.TopicID != test.want {
				t.Fatalf("topic ID = %d, want %d", message.TopicID, test.want)
			}
		})
	}
}

type forumTestInvoker func(context.Context, bin.Encoder, bin.Decoder) error

func (invoke forumTestInvoker) Invoke(ctx context.Context, request bin.Encoder, response bin.Decoder) error {
	return invoke(ctx, request, response)
}

func TestForumGeneralUsesScopedTelegramRequests(t *testing.T) {
	for _, query := range []string{"", "movie"} {
		t.Run(query, func(t *testing.T) {
			called := false
			api := tg.NewClient(forumTestInvoker(func(ctx context.Context, request bin.Encoder, response bin.Decoder) error {
				called = true
				if query == "" {
					req, ok := request.(*tg.MessagesGetRepliesRequest)
					if !ok || req.MsgID != 1 || req.OffsetID != 100 || req.Limit != 50 {
						t.Fatalf("General history request: %#v", request)
					}
				} else {
					req, ok := request.(*tg.MessagesSearchRequest)
					if !ok {
						t.Fatalf("General search request: %T", request)
					}
					id, set := req.GetTopMsgID()
					if !set || id != 1 || req.Q != query || req.OffsetID != 100 || req.Limit != 50 {
						t.Fatalf("General search scope: %#v", req)
					}
				}
				buffer := &bin.Buffer{}
				if err := (&tg.MessagesMessages{Messages: []tg.MessageClass{&tg.Message{ID: 99, PeerID: &tg.PeerChannel{ChannelID: 77}, Message: "result"}}}).Encode(buffer); err != nil {
					return err
				}
				return response.Decode(buffer)
			}))
			messages, err := requestForumMessages(t.Context(), api, InputPeer{Kind: PeerSupergroup, ChannelID: 77, AccessHash: 88}, 1, 100, 50, query)
			if err != nil || !called || len(messages) != 1 || messages[0].MsgID != 99 {
				t.Fatalf("messages %#v, %v", messages, err)
			}
		})
	}
}

func TestForumTopicCursorUsesServerOrderingAndDates(t *testing.T) {
	result := &tg.MessagesForumTopics{Count: 3,
		Topics:   []tg.ForumTopicClass{&tg.ForumTopic{ID: 90, TopMessage: 999, Date: 100, Pinned: true}, &tg.ForumTopic{ID: 20, TopMessage: 555, Date: 200}},
		Messages: []tg.MessageClass{&tg.Message{ID: 999, Date: 400}, &tg.Message{ID: 555, Date: 300}},
	}
	page := forumTopicsFromTG(result, ForumCursor{}, 2)
	if !page.HasMore || page.Next != (ForumCursor{Date: 300, ID: 555, TopicID: 20}) || page.Topics[0].ID != 90 {
		t.Fatalf("activity cursor: %#v", page)
	}
	result.OrderByCreateDate = true
	page = forumTopicsFromTG(result, ForumCursor{}, 2)
	if page.Next.Date != 200 {
		t.Fatalf("creation cursor: %#v", page.Next)
	}
	stalled := forumTopicsFromTG(result, page.Next, 2)
	if stalled.HasMore {
		t.Fatal("identical cursor must stop paging")
	}
}
