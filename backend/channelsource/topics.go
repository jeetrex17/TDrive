package channelsource

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"TDrive/backend/tgclient"
)

var (
	ErrTopicUnavailable = errors.New("This topic is no longer available")
	ErrNotForum         = errors.New("This group no longer has topics enabled")
)

type ForumTopic struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	IconColor   int    `json:"icon_color"`
	IconEmojiID string `json:"icon_emoji_id"`
	Closed      bool   `json:"closed"`
	Hidden      bool   `json:"hidden"`
}

type ForumTopicPage struct {
	PeerKind        string       `json:"peer_kind"`
	PeerID          int64        `json:"peer_id"`
	AccountID       int64        `json:"account_id"`
	Generation      string       `json:"generation"`
	Forum           bool         `json:"forum"`
	Topics          []ForumTopic `json:"topics"`
	NextOffsetDate  int64        `json:"next_offset_date"`
	NextOffsetID    int64        `json:"next_offset_id"`
	NextOffsetTopic int64        `json:"next_offset_topic"`
	HasMore         bool         `json:"has_more"`
}

func publicTopic(topic tgclient.ForumTopic) ForumTopic {
	emoji := ""
	if topic.IconEmojiID != 0 {
		emoji = strconv.FormatInt(topic.IconEmojiID, 10)
	}
	return ForumTopic{ID: topic.ID, Title: topic.Title, IconColor: topic.IconColor,
		IconEmojiID: emoji, Closed: topic.Closed, Hidden: topic.Hidden}
}

func (s *Service) checkSourceScope(ctx context.Context, source SourceInfo) error {
	if accountID, err := s.account(ctx); err != nil || accountID != source.AccountID {
		return ErrUnavailable
	}
	current, _, err := s.storedSource(ctx, source.AccountID, source.PeerKind, source.PeerID)
	if err != nil || current.Generation != source.Generation {
		return ErrNotConnected
	}
	return nil
}

func (s *Service) ListSourceTopics(ctx context.Context, kind string, id, offsetDate, offsetID, offsetTopic int64, limit int, query string, expectedAccountID int64, expectedGeneration string) (ForumTopicPage, error) {
	if !validSource(kind, id) || offsetDate < 0 || offsetDate > math.MaxInt32 || offsetID < 0 || offsetID > math.MaxInt32 ||
		offsetTopic < 0 || offsetTopic > math.MaxInt32 || limit < 1 || limit > 100 || len(query) > 120 || expectedAccountID <= 0 || expectedGeneration == "" {
		return ForumTopicPage{}, ErrInvalidPage
	}
	// Reject stale UI requests before any Telegram lookup.
	if err := s.checkSourceScope(ctx, SourceInfo{PeerKind: kind, PeerID: id, AccountID: expectedAccountID, Generation: expectedGeneration}); err != nil {
		return ForumTopicPage{}, err
	}
	stored, _, source, err := s.currentSource(ctx, kind, id)
	if err != nil {
		return ForumTopicPage{}, err
	}
	if stored.AccountID != expectedAccountID || stored.Generation != expectedGeneration {
		return ForumTopicPage{}, ErrNotConnected
	}
	page := ForumTopicPage{PeerKind: kind, PeerID: id, AccountID: stored.AccountID, Generation: stored.Generation,
		Forum: source.Forum, Topics: make([]ForumTopic, 0)}
	if source.Forum && source.Kind == tgclient.PeerSupergroup {
		client, ok := s.tg.(tgclient.ForumClient)
		if !ok {
			return ForumTopicPage{}, ErrNotForum
		}
		peer := tgclient.InputPeer{Kind: source.Kind, ChannelID: id, AccessHash: source.AccessHash}
		cursor := tgclient.ForumCursor{Date: offsetDate, ID: offsetID, TopicID: offsetTopic}
		var result tgclient.ForumTopics
		err := s.telegram(ctx, func() error {
			var callErr error
			result, callErr = client.GetForumTopics(ctx, peer, cursor, limit, strings.TrimSpace(query))
			return callErr
		})
		if errors.Is(err, tgclient.ErrForumUnavailable) {
			page.Forum = false
		} else if err != nil {
			return ForumTopicPage{}, fmt.Errorf("media source: list topics: %w", err)
		} else {
			seen := make(map[int64]struct{}, len(result.Topics))
			for _, topic := range result.Topics {
				if topic.ID <= 0 {
					continue
				}
				if _, duplicate := seen[topic.ID]; duplicate {
					continue
				}
				seen[topic.ID] = struct{}{}
				page.Topics = append(page.Topics, publicTopic(topic))
				if len(page.Topics) == limit {
					break
				}
			}
			page.NextOffsetDate, page.NextOffsetID, page.NextOffsetTopic = result.Next.Date, result.Next.ID, result.Next.TopicID
			page.HasMore = result.HasMore && result.Next != cursor
		}
	}
	if err := s.checkSourceScope(ctx, stored); err != nil {
		return ForumTopicPage{}, err
	}
	return page, nil
}

func (s *Service) topicMetadata(ctx context.Context, peer tgclient.InputPeer, ids []int64) ([]ForumTopic, error) {
	var out []ForumTopic
	err := s.telegram(ctx, func() error {
		var callErr error
		out, callErr = s.fetchTopicMetadata(ctx, peer, ids)
		return callErr
	})
	return out, err
}

func (s *Service) fetchTopicMetadata(ctx context.Context, peer tgclient.InputPeer, ids []int64) ([]ForumTopic, error) {
	client, ok := s.tg.(tgclient.ForumClient)
	if !ok {
		return nil, ErrNotForum
	}
	topics, err := client.GetForumTopicsByID(ctx, peer, ids)
	if errors.Is(err, tgclient.ErrForumUnavailable) {
		return nil, ErrNotForum
	}
	if errors.Is(err, tgclient.ErrTopicUnavailable) {
		return nil, ErrTopicUnavailable
	}
	if err != nil {
		return nil, fmt.Errorf("media source: read topics: %w", err)
	}
	out := make([]ForumTopic, 0, len(topics))
	for _, topic := range topics {
		if slices.Contains(ids, topic.ID) {
			out = append(out, publicTopic(topic))
		}
	}
	return out, nil
}

func (s *Service) PageSourceByTopic(ctx context.Context, kind string, id, topicID, offsetID int64, limit int, search, mediaKind string) (MediaPage, error) {
	return s.PageSourceByTopicScoped(ctx, kind, id, topicID, offsetID, limit, search, mediaKind, 0, "")
}

func (s *Service) PageSourceByTopicScoped(ctx context.Context, kind string, id, topicID, offsetID int64, limit int, search, mediaKind string, expectedAccountID int64, expectedGeneration string) (MediaPage, error) {
	if !validSource(kind, id) || topicID < 0 || topicID > math.MaxInt32 || offsetID < 0 || offsetID > math.MaxInt32 ||
		limit < 1 || limit > 100 || len(search) > 120 || !validMediaKind(mediaKind) {
		return MediaPage{}, ErrInvalidPage
	}
	if expectedAccountID < 0 || (expectedAccountID == 0) != (expectedGeneration == "") {
		return MediaPage{}, ErrInvalidPage
	}
	if expectedAccountID > 0 {
		if err := s.checkSourceScope(ctx, SourceInfo{PeerKind: kind, PeerID: id, AccountID: expectedAccountID, Generation: expectedGeneration}); err != nil {
			return MediaPage{}, err
		}
	}
	stored, _, source, err := s.currentSource(ctx, kind, id)
	if err != nil {
		return MediaPage{}, err
	}
	if expectedAccountID > 0 && (stored.AccountID != expectedAccountID || stored.Generation != expectedGeneration) {
		return MediaPage{}, ErrNotConnected
	}
	page := MediaPage{PeerKind: kind, PeerID: id, AccountID: stored.AccountID, Generation: stored.Generation,
		Forum: source.Forum, TopicID: topicID, Items: make([]MediaItem, 0, limit), Topics: make([]ForumTopic, 0)}
	if source.Kind == tgclient.PeerChannel {
		page.ChannelID = id
	}
	peer := tgclient.InputPeer{Kind: source.Kind, ChannelID: id, AccessHash: source.AccessHash}
	if topicID > 0 {
		if !source.Forum || source.Kind != tgclient.PeerSupergroup {
			return page, ErrNotForum
		}
		topics, err := s.topicMetadata(ctx, peer, []int64{topicID})
		if err != nil {
			return page, err
		}
		if len(topics) == 0 {
			return page, ErrTopicUnavailable
		}
		page.Topics = topics
	}
	if err := s.fillTopicMedia(ctx, peer, source, &page, offsetID, limit, strings.TrimSpace(search), mediaKind); err != nil {
		return page, err
	}
	if source.Forum && topicID == 0 && len(page.Items) > 0 {
		ids := make([]int64, 0, len(page.Items))
		for _, item := range page.Items {
			ids = append(ids, item.TopicID)
		}
		ids = slices.Compact(slices.Sorted(slices.Values(ids)))
		// Names are optional. One short lookup has no flood-wait retries, so
		// rate limiting cannot hold an otherwise usable page for a minute.
		metadataCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		topics, metadataErr := s.fetchTopicMetadata(metadataCtx, peer, ids)
		cancel()
		if metadataErr == nil {
			page.Topics = topics
		}
	}
	if err := s.checkSourceScope(ctx, stored); err != nil {
		return MediaPage{}, err
	}
	return page, nil
}

func (s *Service) fillTopicMedia(ctx context.Context, peer tgclient.InputPeer, source tgclient.SourcePeer, page *MediaPage, cursor int64, limit int, search, mediaKind string) error {
	seen := make(map[int64]struct{})
	const batchSize = 100
	for range 4 {
		var messages []tgclient.HistoryMessage
		err := s.telegram(ctx, func() error {
			var callErr error
			if page.TopicID > 0 {
				client := s.tg.(tgclient.ForumClient) // selected topic was validated above
				if search == "" {
					messages, callErr = client.GetForumHistory(ctx, peer, page.TopicID, cursor, batchSize)
				} else {
					messages, callErr = client.SearchForumMessages(ctx, peer, page.TopicID, search, cursor, batchSize)
				}
			} else if search == "" {
				messages, callErr = s.tg.GetHistory(ctx, peer, 0, cursor, batchSize)
			} else {
				messages, callErr = s.tg.SearchChannelMessages(ctx, peer, search, cursor, batchSize)
			}
			return callErr
		})
		if errors.Is(err, tgclient.ErrTopicUnavailable) {
			return ErrTopicUnavailable
		}
		if errors.Is(err, tgclient.ErrForumUnavailable) {
			return ErrNotForum
		}
		if err != nil {
			return fmt.Errorf("media source: page media: %w", err)
		}
		page.HasMore = len(messages) > 0
		for _, message := range messages {
			if message.MsgID <= 0 || (cursor > 0 && message.MsgID >= cursor) {
				continue
			}
			if page.NextOffsetID == 0 || message.MsgID < page.NextOffsetID {
				page.NextOffsetID = message.MsgID
			}
			if _, duplicate := seen[message.MsgID]; duplicate {
				continue
			}
			seen[message.MsgID] = struct{}{}
			item, ok := mediaItem(source, message)
			if ok && (page.TopicID == 0 || item.TopicID == page.TopicID) && matchesMediaKind(item, mediaKind) {
				page.Items = append(page.Items, item)
			}
			if len(page.Items) >= limit {
				break
			}
		}
		if !page.HasMore || page.NextOffsetID <= 0 || page.NextOffsetID == cursor {
			page.HasMore = false
			break
		}
		if len(page.Items) >= limit {
			break
		}
		cursor = page.NextOffsetID
	}
	return nil
}
