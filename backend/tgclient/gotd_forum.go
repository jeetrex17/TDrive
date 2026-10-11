package tgclient

import (
	"context"
	"fmt"
	"math"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

var _ ForumClient = (*Gotd)(nil)

// Only the forum flag gives reply IDs topic semantics. A normal reply or
// discussion-thread top ID must never become a forum selection.
func forumMessageTopic(message *tg.Message) int64 {
	header, ok := message.ReplyTo.(*tg.MessageReplyHeader)
	if !ok || !header.ForumTopic {
		return 0
	}
	if header.ReplyToTopID > 0 {
		return int64(header.ReplyToTopID)
	}
	return int64(max(0, header.ReplyToMsgID))
}

func validForumRequest(peer InputPeer, topicID, offsetID int64, limit int) bool {
	return peer.PeerKind() == PeerSupergroup && peer.ChannelID > 0 && peer.AccessHash != 0 &&
		topicID >= 0 && topicID <= math.MaxInt32 && offsetID >= 0 && offsetID <= math.MaxInt32 && limit > 0 && limit <= 100
}

func forumError(err error) error {
	if tgerr.Is(err, "CHANNEL_FORUM_MISSING") {
		return ErrForumUnavailable
	}
	if tgerr.Is(err, "TOPIC_ID_INVALID", "TOPIC_DELETED", "MSG_ID_INVALID") {
		return ErrTopicUnavailable
	}
	return err
}

func topicFromTG(topic *tg.ForumTopic) ForumTopic {
	return ForumTopic{ID: int64(topic.ID), Date: int64(topic.Date), TopMessage: int64(topic.TopMessage),
		Title: topic.Title, IconColor: topic.IconColor, IconEmojiID: topic.IconEmojiID, Closed: topic.Closed, Hidden: topic.Hidden}
}

func forumTopicsFromTG(result *tg.MessagesForumTopics, cursor ForumCursor, limit int) ForumTopics {
	page := ForumTopics{Topics: make([]ForumTopic, 0, min(limit, len(result.Topics))), Next: cursor}
	dates := make(map[int]int64, len(result.Messages))
	for _, message := range result.Messages {
		switch value := message.(type) {
		case *tg.Message:
			dates[value.ID] = int64(value.Date)
		case *tg.MessageService:
			dates[value.ID] = int64(value.Date)
		}
	}
	for _, value := range result.Topics {
		if topic, ok := value.(*tg.ForumTopic); ok && topic.ID > 0 {
			page.Topics = append(page.Topics, topicFromTG(topic))
			date := dates[topic.TopMessage]
			if result.OrderByCreateDate {
				date = int64(topic.Date)
			}
			// Keep the server order, including pinned topics. Sorting before
			// deriving these offsets would skip or repeat unrelated topics.
			page.Next = ForumCursor{Date: date, ID: int64(topic.TopMessage), TopicID: int64(topic.ID)}
		}
		if len(page.Topics) == limit {
			break
		}
	}
	page.HasMore = len(result.Topics) > 0 && len(result.Topics) < result.Count && page.Next != cursor
	return page
}

func (g *Gotd) GetForumTopics(ctx context.Context, peer InputPeer, cursor ForumCursor, limit int, query string) (ForumTopics, error) {
	if !validForumRequest(peer, cursor.TopicID, cursor.ID, limit) || cursor.Date < 0 || cursor.Date > math.MaxInt32 || len(query) > 120 {
		return ForumTopics{}, fmt.Errorf("tgclient: invalid forum page")
	}
	var out ForumTopics
	err := g.run(ctx, func(ctx context.Context, api *tg.Client) error {
		req := &tg.MessagesGetForumTopicsRequest{Peer: toMediaPeer(peer), OffsetDate: int(cursor.Date),
			OffsetID: int(cursor.ID), OffsetTopic: int(cursor.TopicID), Limit: limit}
		if query != "" {
			req.SetQ(query)
		}
		result, err := api.MessagesGetForumTopics(ctx, req)
		if err != nil {
			return err
		}
		out = forumTopicsFromTG(result, cursor, limit)
		return nil
	})
	return out, forumError(err)
}

func (g *Gotd) GetForumTopicsByID(ctx context.Context, peer InputPeer, ids []int64) ([]ForumTopic, error) {
	if !validForumRequest(peer, 0, 0, 1) || len(ids) < 1 || len(ids) > 100 {
		return nil, fmt.Errorf("tgclient: invalid forum topics")
	}
	topics := make([]int, len(ids))
	for i, id := range ids {
		if id <= 0 || id > math.MaxInt32 {
			return nil, fmt.Errorf("tgclient: invalid topic ID")
		}
		topics[i] = int(id)
	}
	var out []ForumTopic
	err := g.run(ctx, func(ctx context.Context, api *tg.Client) error {
		result, err := api.MessagesGetForumTopicsByID(ctx, &tg.MessagesGetForumTopicsByIDRequest{Peer: toMediaPeer(peer), Topics: topics})
		if err != nil {
			return err
		}
		out = forumTopicsFromTG(result, ForumCursor{}, len(ids)).Topics
		return nil
	})
	return out, forumError(err)
}

func historyFromForum(result tg.MessagesMessagesClass) []HistoryMessage {
	var messages []tg.MessageClass
	switch value := result.(type) {
	case *tg.MessagesMessages:
		messages = value.Messages
	case *tg.MessagesMessagesSlice:
		messages = value.Messages
	case *tg.MessagesChannelMessages:
		messages = value.Messages
	}
	out := make([]HistoryMessage, 0, len(messages))
	for _, message := range messages {
		if converted, ok := historyMessageFromTG(message); ok {
			out = append(out, converted)
		}
	}
	return out
}

// requestForumMessages keeps history and search on Telegram's thread-scoped
// endpoints, including General (root 1). The group-wide history is topic 0.
func requestForumMessages(ctx context.Context, api *tg.Client, peer InputPeer, topicID, offsetID int64, limit int, query string) ([]HistoryMessage, error) {
	var result tg.MessagesMessagesClass
	var err error
	if query == "" {
		result, err = api.MessagesGetReplies(ctx, &tg.MessagesGetRepliesRequest{Peer: toMediaPeer(peer),
			MsgID: int(topicID), OffsetID: int(offsetID), Limit: limit})
	} else {
		req := &tg.MessagesSearchRequest{Peer: toMediaPeer(peer), Q: query, Filter: &tg.InputMessagesFilterEmpty{},
			OffsetID: int(offsetID), Limit: limit}
		req.SetTopMsgID(int(topicID))
		result, err = api.MessagesSearch(ctx, req)
	}
	if err != nil {
		return nil, err
	}
	return historyFromForum(result), nil
}

func (g *Gotd) GetForumHistory(ctx context.Context, peer InputPeer, topicID, offsetID int64, limit int) ([]HistoryMessage, error) {
	return g.readForumMessages(ctx, peer, topicID, offsetID, limit, "")
}

func (g *Gotd) SearchForumMessages(ctx context.Context, peer InputPeer, topicID int64, query string, offsetID int64, limit int) ([]HistoryMessage, error) {
	return g.readForumMessages(ctx, peer, topicID, offsetID, limit, query)
}

func (g *Gotd) readForumMessages(ctx context.Context, peer InputPeer, topicID, offsetID int64, limit int, query string) ([]HistoryMessage, error) {
	if topicID <= 0 || !validForumRequest(peer, topicID, offsetID, limit) || len(query) > 120 {
		return nil, fmt.Errorf("tgclient: invalid topic media request")
	}
	var out []HistoryMessage
	err := g.run(ctx, func(ctx context.Context, api *tg.Client) error {
		var callErr error
		out, callErr = requestForumMessages(ctx, api, peer, topicID, offsetID, limit, query)
		return callErr
	})
	return out, forumError(err)
}
