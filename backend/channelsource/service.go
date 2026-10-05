// Package channelsource lists and streams media in joined Telegram broadcast
// channels without ingesting their messages into TDrive's drive projection.
package channelsource

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strings"
	"sync"
	"time"

	"TDrive/backend/media"
	"TDrive/backend/tgclient"
)

var (
	ErrNotConnected = errors.New("channel source is not connected")
	ErrUnavailable  = errors.New("channel is no longer available to this Telegram account")
	ErrInvalidPage  = errors.New("invalid channel media page request")
)

type SourceInfo struct {
	ChannelID  int64  `json:"channel_id"`
	Title      string `json:"title"`
	Username   string `json:"username,omitempty"`
	Connected  bool   `json:"connected"`
	Protected  bool   `json:"protected"`
	Available  bool   `json:"available"`
	AccountID  int64  `json:"account_id"`
	Generation string `json:"generation,omitempty"`
}

type MediaItem struct {
	MsgID       int64  `json:"msg_id"`
	Date        int64  `json:"date"`
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	Duration    int64  `json:"duration"` // whole seconds; 0 when Telegram did not say
	MimeType    string `json:"mime_type"`
	Kind        string `json:"kind"`
	Caption     string `json:"caption"`
	Streamable  bool   `json:"streamable"`
	BlockReason string `json:"block_reason,omitempty"`
	TelegramURL string `json:"telegram_url"`
}

type MediaPage struct {
	ChannelID    int64       `json:"channel_id"`
	AccountID    int64       `json:"account_id"`
	Generation   string      `json:"generation"`
	Items        []MediaItem `json:"items"`
	NextOffsetID int64       `json:"next_offset_id"`
	HasMore      bool        `json:"has_more"`
}

type Service struct {
	db    *sql.DB
	tg    tgclient.Client
	media *media.Service
	mu    sync.Mutex // serializes connection generation and media publication
	retry tgclient.FloodWaitRetryPolicy

	// The latest dialog walk, for photos of channels not added yet. A walk is
	// far too heavy to repeat per avatar.
	peersMu  sync.Mutex
	peers    map[int64]tgclient.JoinedBroadcastChannel
	peersFor int64 // the account the walk was for
	walkedAt time.Time
}

func NewService(db *sql.DB, tg tgclient.Client, mediaService *media.Service) (*Service, error) {
	if db == nil || tg == nil || mediaService == nil {
		return nil, fmt.Errorf("channel source: dependencies unavailable")
	}
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS connected_channel_sources (
		account_id INTEGER NOT NULL,
		channel_id INTEGER NOT NULL,
		title TEXT NOT NULL,
		username TEXT NOT NULL DEFAULT '',
		protected INTEGER NOT NULL DEFAULT 0,
		generation TEXT NOT NULL,
		access_hash INTEGER NOT NULL DEFAULT 0,
		photo_id INTEGER NOT NULL DEFAULT 0,
		photo BLOB,
		PRIMARY KEY(account_id, channel_id)
	)`)
	if err != nil {
		return nil, fmt.Errorf("channel source: create local metadata: %w", err)
	}
	if err := addPhotoColumns(db); err != nil {
		return nil, err
	}
	return &Service{db: db, tg: tg, media: mediaService, retry: tgclient.FloodWaitRetryPolicy{
		MaxRetries: 2, MaxWait: 30 * time.Second, MaxTotalWait: time.Minute,
	}}, nil
}

func (s *Service) telegram(ctx context.Context, call func() error) error {
	return s.retry.Do(ctx, call)
}

func (s *Service) account(ctx context.Context) (int64, error) {
	id, err := s.tg.SelfID(ctx)
	if err != nil {
		return 0, fmt.Errorf("channel source: Telegram account: %w", err)
	}
	if id <= 0 {
		return 0, ErrUnavailable
	}
	return id, nil
}

func newGeneration() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}

func (s *Service) connected(ctx context.Context, accountID, channelID int64) (SourceInfo, error) {
	var source SourceInfo
	var protected int
	err := s.db.QueryRowContext(ctx, `SELECT title, username, protected, generation
		FROM connected_channel_sources WHERE account_id=? AND channel_id=?`, accountID, channelID).
		Scan(&source.Title, &source.Username, &protected, &source.Generation)
	if errors.Is(err, sql.ErrNoRows) {
		return SourceInfo{}, ErrNotConnected
	}
	if err != nil {
		return SourceInfo{}, fmt.Errorf("channel source: read local metadata: %w", err)
	}
	source.ChannelID, source.AccountID, source.Connected = channelID, accountID, true
	source.Protected = protected != 0
	return source, nil
}

func (s *Service) ListCandidates(ctx context.Context) ([]SourceInfo, error) {
	accountID, err := s.account(ctx)
	if err != nil {
		return nil, err
	}
	s.peersMu.Lock()
	channels, err := s.walkLocked(ctx, accountID)
	s.peersMu.Unlock()
	if err != nil {
		return nil, err
	}
	out := make([]SourceInfo, 0, len(channels))
	for _, channel := range channels {
		if channel.ID <= 0 || channel.AccessHash == 0 {
			continue
		}
		source := SourceInfo{ChannelID: channel.ID, Title: channel.Title, Username: channel.Username,
			Protected: channel.Protected, Available: true, AccountID: accountID}
		stored, err := s.connected(ctx, accountID, channel.ID)
		if err == nil {
			source.Connected, source.Generation = true, stored.Generation
		} else if !errors.Is(err, ErrNotConnected) {
			return nil, err
		}
		out = append(out, source)
	}
	return out, nil
}

func (s *Service) ListConnected(ctx context.Context) ([]SourceInfo, error) {
	accountID, err := s.account(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT channel_id, title, username, protected, generation
		FROM connected_channel_sources WHERE account_id=? ORDER BY title COLLATE NOCASE, channel_id`, accountID)
	if err != nil {
		return nil, fmt.Errorf("channel source: list local metadata: %w", err)
	}
	defer rows.Close()
	out := make([]SourceInfo, 0)
	for rows.Next() {
		var source SourceInfo
		var protected int
		if err := rows.Scan(&source.ChannelID, &source.Title, &source.Username, &protected, &source.Generation); err != nil {
			return nil, err
		}
		source.AccountID, source.Connected, source.Protected = accountID, true, protected != 0
		out = append(out, source)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Local metadata remains visible offline. Availability and current content
	// protection are refreshed when opening a page or media item.
	return out, nil
}

func (s *Service) Connect(ctx context.Context, channelID int64) (SourceInfo, error) {
	accountID, err := s.account(ctx)
	if err != nil {
		return SourceInfo{}, err
	}
	return s.ConnectWithGate(ctx, channelID, accountID, func(save func() error) error { return save() })
}

func (s *Service) ConnectWithGate(ctx context.Context, channelID, expectedAccountID int64, gate func(func() error) error) (SourceInfo, error) {
	if channelID <= 0 || expectedAccountID <= 0 || gate == nil {
		return SourceInfo{}, ErrUnavailable
	}
	accountID, err := s.account(ctx)
	if err != nil {
		return SourceInfo{}, err
	}
	if accountID != expectedAccountID {
		return SourceInfo{}, ErrUnavailable
	}
	var channel tgclient.JoinedBroadcastChannel
	err = s.telegram(ctx, func() error {
		var callErr error
		channel, callErr = s.tg.GetJoinedBroadcastChannel(ctx, channelID)
		return callErr
	})
	if err != nil {
		return SourceInfo{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if currentAccount, err := s.account(ctx); err != nil || currentAccount != accountID {
		return SourceInfo{}, ErrUnavailable
	}
	generation, err := newGeneration()
	if err != nil {
		return SourceInfo{}, fmt.Errorf("channel source: generate connection: %w", err)
	}
	var out SourceInfo
	err = gate(func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		if existing, err := s.connected(ctx, accountID, channelID); err == nil {
			existing.Available, existing.Protected = true, channel.Protected
			out = existing
			return nil
		} else if !errors.Is(err, ErrNotConnected) {
			return err
		}
		_, err := s.db.ExecContext(ctx, `INSERT INTO connected_channel_sources
			(account_id, channel_id, title, username, protected, generation, access_hash, photo_id) VALUES(?,?,?,?,?,?,?,?)`,
			accountID, channelID, channel.Title, channel.Username, channel.Protected, generation, channel.AccessHash, channel.PhotoID)
		if err != nil {
			return fmt.Errorf("channel source: connect: %w", err)
		}
		out = SourceInfo{ChannelID: channelID, AccountID: accountID, Title: channel.Title,
			Username: channel.Username, Protected: channel.Protected, Available: true,
			Connected: true, Generation: generation}
		return nil
	})
	if err != nil {
		return SourceInfo{}, err
	}
	return out, nil
}

func (s *Service) Disconnect(ctx context.Context, channelID int64) ([]string, error) {
	accountID, err := s.account(ctx)
	if err != nil {
		return nil, err
	}
	connected, err := s.connected(ctx, accountID, channelID)
	if err != nil {
		return nil, err
	}
	return s.DisconnectWithGate(ctx, channelID, accountID, connected.Generation, func(remove func() error) error { return remove() })
}

func (s *Service) DisconnectWithGate(ctx context.Context, channelID, expectedAccountID int64, expectedGeneration string, gate func(func() error) error) ([]string, error) {
	if channelID <= 0 || expectedAccountID <= 0 || expectedGeneration == "" || gate == nil {
		return nil, ErrUnavailable
	}
	accountID, err := s.account(ctx)
	if err != nil {
		return nil, err
	}
	if accountID != expectedAccountID {
		return nil, ErrUnavailable
	}
	var tokens []string
	err = gate(func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		connected, err := s.connected(ctx, accountID, channelID)
		if err != nil || connected.Generation != expectedGeneration {
			return ErrNotConnected
		}
		if _, err := s.db.ExecContext(ctx, `DELETE FROM connected_channel_sources WHERE account_id=? AND channel_id=?`, accountID, channelID); err != nil {
			return fmt.Errorf("channel source: disconnect: %w", err)
		}
		tokens = s.media.CloseExternalSessions(accountID, channelID)
		return nil
	})
	return tokens, err
}

func (s *Service) current(ctx context.Context, channelID int64) (SourceInfo, tgclient.JoinedBroadcastChannel, error) {
	accountID, err := s.account(ctx)
	if err != nil {
		return SourceInfo{}, tgclient.JoinedBroadcastChannel{}, err
	}
	source, err := s.connected(ctx, accountID, channelID)
	if err != nil {
		return SourceInfo{}, tgclient.JoinedBroadcastChannel{}, err
	}
	var channel tgclient.JoinedBroadcastChannel
	err = s.telegram(ctx, func() error {
		var callErr error
		channel, callErr = s.tg.GetJoinedBroadcastChannel(ctx, channelID)
		return callErr
	})
	if err != nil {
		return SourceInfo{}, tgclient.JoinedBroadcastChannel{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if channel.AccessHash == 0 {
		return SourceInfo{}, tgclient.JoinedBroadcastChannel{}, ErrUnavailable
	}
	// A new profile photo drops the stored one, so the next avatar request
	// fetches the picture the channel shows now.
	if _, err := s.db.ExecContext(ctx, `UPDATE connected_channel_sources
		SET access_hash=?, photo=CASE WHEN photo_id=? THEN photo ELSE NULL END, photo_id=?
		WHERE account_id=? AND channel_id=? AND (access_hash<>? OR photo_id<>?)`,
		channel.AccessHash, channel.PhotoID, channel.PhotoID, source.AccountID, channelID, channel.AccessHash, channel.PhotoID); err != nil {
		return SourceInfo{}, tgclient.JoinedBroadcastChannel{}, fmt.Errorf("channel source: refresh local metadata: %w", err)
	}
	source.Title, source.Username, source.Protected, source.Available = channel.Title, channel.Username, channel.Protected, true
	return source, channel, nil
}

func telegramURL(channel tgclient.JoinedBroadcastChannel, msgID int64) string {
	if username := strings.TrimPrefix(channel.Username, "@"); username != "" {
		return "https://t.me/" + url.PathEscape(username) + "/" + fmt.Sprint(msgID)
	}
	return fmt.Sprintf("https://t.me/c/%d/%d", channel.ID, msgID)
}

func mediaItem(channel tgclient.JoinedBroadcastChannel, message tgclient.HistoryMessage) (MediaItem, bool) {
	if !message.HasMedia && !message.Paid {
		return MediaItem{}, false
	}
	if message.DocumentID == 0 && !message.Paid {
		return MediaItem{}, false
	}
	name := media.ExternalName(message)
	kind := media.ExternalKind(message)
	item := MediaItem{MsgID: message.MsgID, Date: message.Date, Name: name, Size: message.MediaSize,
		Duration: int64(math.Round(message.Duration)), MimeType: message.MimeType, Kind: string(kind), Caption: message.Text,
		TelegramURL: telegramURL(channel, message.MsgID)}
	switch {
	case channel.Protected || message.NoForwards:
		item.BlockReason = "protected"
	case message.Paid:
		item.BlockReason = "paid"
	case message.TTLSeconds > 0:
		item.BlockReason = "expires"
	case kind == media.StreamKindUnknown:
		item.BlockReason = "unsupported_format"
	default:
		item.Streamable = true
	}
	if item.Name == "" {
		item.Name = "Telegram media"
	}
	return item, true
}

func (s *Service) Page(ctx context.Context, channelID, offsetID int64, limit int, search, kind string) (MediaPage, error) {
	if channelID <= 0 || offsetID < 0 || limit < 1 || limit > 100 || len(search) > 120 || (kind != "all" && kind != "video" && kind != "audio") {
		return MediaPage{}, ErrInvalidPage
	}
	source, channel, err := s.current(ctx, channelID)
	if err != nil {
		return MediaPage{}, err
	}
	peer := tgclient.InputPeer{ChannelID: channelID, AccessHash: channel.AccessHash}
	search = strings.TrimSpace(search)
	page := MediaPage{ChannelID: channelID, AccountID: source.AccountID,
		Generation: source.Generation, Items: make([]MediaItem, 0, limit)}
	// History includes text and unsupported media. Scan at most four bounded
	// batches so a run of ordinary posts does not bury older playable items.
	batchSize := min(100, max(limit, 40))
	cursor := offsetID
	for range 4 {
		var messages []tgclient.HistoryMessage
		err = s.telegram(ctx, func() error {
			var callErr error
			if search == "" {
				messages, callErr = s.tg.GetHistory(ctx, peer, 0, cursor, batchSize)
			} else {
				messages, callErr = s.tg.SearchChannelMessages(ctx, peer, search, cursor, batchSize)
			}
			return callErr
		})
		if err != nil {
			return MediaPage{}, fmt.Errorf("channel source: page media: %w", err)
		}
		page.HasMore = len(messages) == batchSize
		for index, message := range messages {
			if message.MsgID <= 0 {
				continue
			}
			if page.NextOffsetID == 0 || message.MsgID < page.NextOffsetID {
				page.NextOffsetID = message.MsgID
			}
			item, ok := mediaItem(channel, message)
			if ok && (kind == "all" || item.Kind == kind) {
				page.Items = append(page.Items, item)
			}
			if len(page.Items) >= limit {
				page.HasMore = page.HasMore || index < len(messages)-1
				break
			}
		}
		if len(page.Items) >= limit || !page.HasMore || page.NextOffsetID <= 0 || page.NextOffsetID == cursor {
			break
		}
		cursor = page.NextOffsetID
	}
	// Reject a page whose connection was replaced while Telegram was loading.
	currentAccount, err := s.account(ctx)
	if err != nil || currentAccount != source.AccountID {
		return MediaPage{}, ErrUnavailable
	}
	latest, err := s.connected(ctx, source.AccountID, channelID)
	if err != nil || latest.Generation != source.Generation {
		return MediaPage{}, ErrNotConnected
	}
	return page, nil
}

func (s *Service) Open(ctx context.Context, channelID, msgID, expectedAccountID int64, expectedGeneration string) (media.OpenResult, error) {
	return s.OpenWithGate(ctx, channelID, msgID, expectedAccountID, expectedGeneration, func(add func() error) error { return add() })
}

// OpenWithGate lets a platform lifecycle gate serialize final capability
// publication with logout, without holding it across Telegram network I/O.
func (s *Service) OpenWithGate(ctx context.Context, channelID, msgID, expectedAccountID int64, expectedGeneration string, gate func(func() error) error) (media.OpenResult, error) {
	if gate == nil || expectedAccountID <= 0 || expectedGeneration == "" {
		return media.OpenResult{}, ErrUnavailable
	}
	if channelID <= 0 || msgID <= 0 {
		return media.OpenResult{}, ErrUnavailable
	}
	source, channel, err := s.current(ctx, channelID)
	if err != nil {
		return media.OpenResult{}, err
	}
	if source.AccountID != expectedAccountID || source.Generation != expectedGeneration {
		return media.OpenResult{}, ErrNotConnected
	}
	if channel.Protected {
		return media.OpenResult{}, media.ErrExternalRestricted
	}
	peer := tgclient.InputPeer{ChannelID: channelID, AccessHash: channel.AccessHash}
	var message tgclient.HistoryMessage
	err = s.telegram(ctx, func() error {
		var callErr error
		message, callErr = s.tg.GetChannelMessage(ctx, peer, msgID)
		return callErr
	})
	if err != nil {
		return media.OpenResult{}, fmt.Errorf("channel source: fetch media: %w", err)
	}
	item, ok := mediaItem(channel, message)
	if !ok || !item.Streamable {
		return media.OpenResult{}, media.ErrExternalRestricted
	}
	return s.media.OpenExternal(ctx, media.ExternalMedia{Peer: peer, Client: s.tg,
		Message: message, AccountID: source.AccountID, Generation: source.Generation,
		Protected: channel.Protected}, func(add func() error) error {
		// SelfID may be network-backed. Check immediately before the platform
		// logout gate without holding that gate during a Telegram RPC.
		accountID, err := s.account(ctx)
		if err != nil || accountID != source.AccountID {
			return ErrUnavailable
		}
		return gate(func() error {
			s.mu.Lock()
			defer s.mu.Unlock()
			current, err := s.connected(ctx, source.AccountID, channelID)
			if err != nil || current.Generation != source.Generation {
				return ErrNotConnected
			}
			return add()
		})
	})
}
