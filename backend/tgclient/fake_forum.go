package tgclient

import (
	"context"
	"slices"
	"strings"
	"time"
)

var _ ForumClient = (*Fake)(nil)

func (f *Fake) SeedForumTopics(peerID int64, topics ...ForumTopic) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.forumTopics == nil {
		f.forumTopics = make(map[int64][]ForumTopic)
	}
	f.forumTopics[peerID] = slices.Clone(topics)
}

func (f *Fake) GetForumTopics(ctx context.Context, peer InputPeer, cursor ForumCursor, limit int, query string) (ForumTopics, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ForumTopics{}, err
	}
	page := ForumTopics{Next: cursor, Topics: make([]ForumTopic, 0)}
	started := cursor.TopicID == 0
	for _, topic := range f.forumTopics[peer.ChannelID] {
		if !strings.Contains(strings.ToLower(topic.Title), strings.ToLower(query)) {
			continue
		}
		if !started {
			started = topic.ID == cursor.TopicID
			continue
		}
		if len(page.Topics) == limit {
			page.HasMore = true
			break
		}
		page.Topics = append(page.Topics, topic)
		page.Next = ForumCursor{Date: topic.Date, ID: topic.TopMessage, TopicID: topic.ID}
	}
	return page, nil
}

func (f *Fake) GetForumTopicsByID(ctx context.Context, peer InputPeer, ids []int64) ([]ForumTopic, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]ForumTopic, 0, len(ids))
	for _, topic := range f.forumTopics[peer.ChannelID] {
		if slices.Contains(ids, topic.ID) {
			out = append(out, topic)
		}
	}
	return out, nil
}

func (f *Fake) GetForumHistory(ctx context.Context, peer InputPeer, topicID, offsetID int64, limit int) ([]HistoryMessage, error) {
	return f.forumMessages(ctx, peer, topicID, "", offsetID, limit)
}

func (f *Fake) SearchForumMessages(ctx context.Context, peer InputPeer, topicID int64, query string, offsetID int64, limit int) ([]HistoryMessage, error) {
	return f.forumMessages(ctx, peer, topicID, query, offsetID, limit)
}

func (f *Fake) forumMessages(ctx context.Context, peer InputPeer, topicID int64, query string, offsetID int64, limit int) ([]HistoryMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.readFloodWait > 0 {
		f.readFloodWait--
		return nil, NewFloodWaitError(time.Millisecond)
	}
	out := make([]HistoryMessage, 0, limit)
	for i := len(f.history) - 1; i >= 0; i-- {
		message := f.history[i]
		topic := message.TopicID
		if topic == 0 {
			topic = 1 // callers explicitly requested forum history
		}
		if message.PeerKind != peer.PeerKind() || message.ChannelID != peer.ChannelID || topic != topicID ||
			(offsetID > 0 && message.MsgID >= offsetID) || !strings.Contains(strings.ToLower(message.Text), strings.ToLower(query)) {
			continue
		}
		out = append(out, message)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}
