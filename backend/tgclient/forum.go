package tgclient

import (
	"context"
	"errors"
)

var (
	ErrForumUnavailable = errors.New("Telegram group does not have topics")
	ErrTopicUnavailable = errors.New("Telegram topic is no longer available")
)

// ForumCursor preserves Telegram's compound topic-list offset. Topic IDs alone
// cannot paginate a list ordered by its most recent messages.
type ForumCursor struct {
	Date, ID, TopicID int64
}

type ForumTopic struct {
	ID, Date, TopMessage int64
	Title                string
	IconColor            int
	IconEmojiID          int64
	Closed, Hidden       bool
}

type ForumTopics struct {
	Topics  []ForumTopic
	Next    ForumCursor
	HasMore bool
}

// ForumClient is a read-only optional transport boundary. Drive sync and upload
// clients do not need to implement forum browsing.
type ForumClient interface {
	GetForumTopics(context.Context, InputPeer, ForumCursor, int, string) (ForumTopics, error)
	GetForumTopicsByID(context.Context, InputPeer, []int64) ([]ForumTopic, error)
	GetForumHistory(context.Context, InputPeer, int64, int64, int) ([]HistoryMessage, error)
	SearchForumMessages(context.Context, InputPeer, int64, string, int64, int) ([]HistoryMessage, error)
}
