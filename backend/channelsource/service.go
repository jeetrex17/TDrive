// Package channelsource lists and streams readable Telegram chat and channel
// media without ingesting messages into TDrive's drive projection.
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
	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

var (
	ErrNotConnected = errors.New("Telegram source is not connected")
	ErrUnavailable  = errors.New("Telegram source is no longer available to this account")
	ErrInvalidPage  = errors.New("invalid Telegram media page request")
)

type SourceInfo struct {
	PeerKind            string `json:"peer_kind"`
	PeerID              int64  `json:"peer_id"`
	ChannelID           int64  `json:"channel_id"`
	Title               string `json:"title"`
	Username            string `json:"username,omitempty"`
	Connected           bool   `json:"connected"`
	Protected           bool   `json:"protected"`
	Available           bool   `json:"available"`
	AccountID           int64  `json:"account_id"`
	Generation          string `json:"generation,omitempty"`
	CandidatesTruncated bool   `json:"candidates_truncated,omitempty"`

	accessHash int64 // the stored peer hash, for lookups; never leaves the backend
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
	Protected   bool   `json:"protected"`
	BlockReason string `json:"block_reason,omitempty"`
	TelegramURL string `json:"telegram_url"`
}

type MediaPage struct {
	PeerKind     string      `json:"peer_kind"`
	PeerID       int64       `json:"peer_id"`
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
	self  func(context.Context) (int64, error)
	mu    sync.Mutex // serializes connection generation and media publication
	retry tgclient.FloodWaitRetryPolicy

	// The last lookup of each channel, which older pages reuse instead of
	// asking Telegram about the channel itself again.
	lookupsMu sync.Mutex
	lookups   map[[2]int64]lookedUp // keyed by account and channel

	// The latest dialog walk, for photos of channels not added yet. A walk is
	// far too heavy to repeat per avatar.
	peersMu  sync.Mutex
	peers    map[int64]tgclient.JoinedBroadcastChannel
	peersFor int64 // the account the walk was for
	walkedAt time.Time

	// The latest picker result supplies access hashes for a single-peer check
	// during connection, avoiding another full dialog walk while it is open.
	candidatesMu  sync.Mutex
	candidates    map[sourceKey]tgclient.SourcePeer
	candidatesFor int64
}

type lookedUp struct {
	channel tgclient.JoinedBroadcastChannel
	at      time.Time
}

// channelTTL is how long a lookup serves the older pages of a channel. A first
// page, a search and every open look the channel up afresh, so a channel that
// turns on protection shows it on the next load and never streams after.
const channelTTL = time.Minute

// NewService takes self, which names the signed-in account. The engine passes
// its cached ActorID, so nothing here asks Telegram who the user is.
func NewService(db *sql.DB, tg tgclient.Client, mediaService *media.Service, self func(context.Context) (int64, error)) (*Service, error) {
	if db == nil || tg == nil || mediaService == nil || self == nil {
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
	if err := migrateMediaSources(db); err != nil {
		return nil, err
	}
	return &Service{db: db, tg: tg, media: mediaService, self: self, retry: tgclient.FloodWaitRetryPolicy{
		MaxRetries: 2, MaxWait: 30 * time.Second, MaxTotalWait: time.Minute,
		MaxTransientRetries: 2, TransientBackoff: 500 * time.Millisecond, MaxTransientBackoff: 2 * time.Second,
	}}, nil
}

func (s *Service) telegram(ctx context.Context, call func() error) error {
	return s.retry.Do(ctx, call)
}

func (s *Service) account(ctx context.Context) (int64, error) {
	id, err := s.self(ctx)
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
	err := s.db.QueryRowContext(ctx, `SELECT title, username, protected, generation, access_hash
		FROM connected_channel_sources WHERE account_id=? AND channel_id=?`, accountID, channelID).
		Scan(&source.Title, &source.Username, &protected, &source.Generation, &source.accessHash)
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
	drives, err := s.drives()
	if err != nil {
		return nil, err
	}
	out := make([]SourceInfo, 0, len(channels))
	for _, channel := range channels {
		if channel.ID <= 0 || channel.AccessHash == 0 || channel.Restricted || drives[channel.ID] {
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

// drives returns the channels this device keeps as TDrive drives. A drive's
// posts are TDrive's own storage: headers, file parts and encrypted bodies,
// which would list as noise and fail to play, so a drive is never a source.
func (s *Service) drives() (map[int64]bool, error) {
	channels, err := projection.ListChannels(s.db)
	if err != nil {
		return nil, fmt.Errorf("channel source: list drives: %w", err)
	}
	ids := make(map[int64]bool, len(channels))
	for _, channel := range channels {
		ids[channel.ChannelID] = true
	}
	return ids, nil
}

func (s *Service) ListConnected(ctx context.Context) ([]SourceInfo, error) {
	accountID, err := s.account(ctx)
	if err != nil {
		return nil, err
	}
	// Read before the rows below hold the only connection.
	drives, err := s.drives()
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
		// Added before it became a drive.
		if drives[source.ChannelID] {
			continue
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
	drive, err := projection.ChannelExists(s.db, channelID)
	if err != nil {
		return SourceInfo{}, err
	}
	if drive {
		return SourceInfo{}, ErrUnavailable
	}
	// Picked from the list the dialog walk just produced, so normally this
	// costs no request at all.
	channel, ok, err := s.joined(ctx, accountID, channelID)
	if err != nil {
		return SourceInfo{}, err
	}
	if !ok || channel.Restricted {
		return SourceInfo{}, ErrUnavailable
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

func (s *Service) current(ctx context.Context, channelID int64, fresh bool) (SourceInfo, tgclient.JoinedBroadcastChannel, error) {
	accountID, err := s.account(ctx)
	if err != nil {
		return SourceInfo{}, tgclient.JoinedBroadcastChannel{}, err
	}
	source, err := s.connected(ctx, accountID, channelID)
	if err != nil {
		return SourceInfo{}, tgclient.JoinedBroadcastChannel{}, err
	}
	peer := tgclient.InputPeer{ChannelID: channelID, AccessHash: source.accessHash}
	if peer.AccessHash == 0 {
		// Added before access hashes were kept: the dialog walk knows it, and
		// the refresh below stores it so this happens once.
		joined, ok, err := s.joined(ctx, accountID, channelID)
		if err != nil {
			return SourceInfo{}, tgclient.JoinedBroadcastChannel{}, err
		}
		if !ok {
			return SourceInfo{}, tgclient.JoinedBroadcastChannel{}, ErrUnavailable
		}
		peer.AccessHash = joined.AccessHash
	}
	channel, err := s.lookup(ctx, accountID, peer, fresh)
	if err != nil {
		return SourceInfo{}, tgclient.JoinedBroadcastChannel{}, err
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

// lookup asks Telegram for one channel alone, or, unless fresh is set, answers
// from a lookup made within channelTTL.
func (s *Service) lookup(ctx context.Context, accountID int64, peer tgclient.InputPeer, fresh bool) (tgclient.JoinedBroadcastChannel, error) {
	key := [2]int64{accountID, peer.ChannelID}
	s.lookupsMu.Lock()
	recent, ok := s.lookups[key]
	s.lookupsMu.Unlock()
	if !fresh && ok && time.Since(recent.at) < channelTTL {
		return recent.channel, nil
	}
	var channel tgclient.JoinedBroadcastChannel
	err := s.telegram(ctx, func() error {
		var callErr error
		channel, callErr = s.tg.GetBroadcastChannel(ctx, peer)
		return callErr
	})
	if errors.Is(err, tgclient.ErrChannelUnavailable) {
		return tgclient.JoinedBroadcastChannel{}, ErrUnavailable
	}
	if err != nil {
		return tgclient.JoinedBroadcastChannel{}, fmt.Errorf("channel source: look up channel: %w", err)
	}
	// Telegram withholds it here, so official clients would not show it either.
	if channel.Restricted {
		return tgclient.JoinedBroadcastChannel{}, ErrUnavailable
	}
	s.lookupsMu.Lock()
	if s.lookups == nil {
		s.lookups = make(map[[2]int64]lookedUp)
	}
	s.lookups[key] = lookedUp{channel: channel, at: time.Now()}
	s.lookupsMu.Unlock()
	return channel, nil
}

func telegramURL(channel tgclient.JoinedBroadcastChannel, msgID int64) string {
	if channel.Kind == tgclient.PeerUser || channel.Kind == tgclient.PeerBot {
		if username := strings.TrimPrefix(channel.Username, "@"); username != "" {
			return "https://t.me/" + url.PathEscape(username)
		}
		return ""
	}
	if channel.Kind != "" && channel.Kind != tgclient.PeerChannel && channel.Kind != tgclient.PeerSupergroup {
		return ""
	}
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
	if name == "" {
		name = tgclient.MediaName(message)
	}
	kind := media.ExternalKind(message)
	item := MediaItem{MsgID: message.MsgID, Date: message.Date, Name: name, Size: message.MediaSize,
		Duration: int64(math.Round(message.Duration)), MimeType: message.MimeType, Kind: string(kind), Caption: message.Text,
		TelegramURL: telegramURL(channel, message.MsgID), Protected: channel.Protected || message.NoForwards}
	switch {
	case message.Restricted:
		item.BlockReason = "restricted"
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

// Documents includes both supported document viewers and attachments that
// remain available through Telegram when TDrive cannot preview their format.
func validMediaKind(kind string) bool {
	switch kind {
	case "all", "video", "audio", "image", "document":
		return true
	default:
		return false
	}
}

func matchesMediaKind(item MediaItem, kind string) bool {
	if kind == "all" || item.Kind == kind {
		return true
	}
	return kind == "document" && (item.Kind == string(media.StreamKindPDF) || item.Kind == string(media.StreamKindText) || item.Kind == string(media.StreamKindUnknown))
}

func (s *Service) Page(ctx context.Context, channelID, offsetID int64, limit int, search, kind string) (MediaPage, error) {
	if channelID <= 0 || offsetID < 0 || limit < 1 || limit > 100 || len(search) > 120 || !validMediaKind(kind) {
		return MediaPage{}, ErrInvalidPage
	}
	source, channel, err := s.current(ctx, channelID, offsetID == 0)
	if err != nil {
		return MediaPage{}, err
	}
	peer := tgclient.InputPeer{ChannelID: channelID, AccessHash: channel.AccessHash}
	search = strings.TrimSpace(search)
	page := MediaPage{ChannelID: channelID, AccountID: source.AccountID,
		Generation: source.Generation, Items: make([]MediaItem, 0, limit)}
	// History includes text and unsupported media. Read Telegram's largest
	// batch, at most four times, so a run of ordinary posts does not bury
	// older playable items and a sparse channel is not a request per forty.
	const batchSize = 100
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
		// Only an empty batch proves the start of the channel. Telegram leaves
		// deleted and withheld posts out, so a short one can have more behind it.
		page.HasMore = len(messages) > 0
		for index, message := range messages {
			if message.MsgID <= 0 {
				continue
			}
			if page.NextOffsetID == 0 || message.MsgID < page.NextOffsetID {
				page.NextOffsetID = message.MsgID
			}
			item, ok := mediaItem(channel, message)
			if ok && matchesMediaKind(item, kind) {
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
	source, channel, err := s.current(ctx, channelID, true)
	if err != nil {
		return media.OpenResult{}, err
	}
	if source.AccountID != expectedAccountID || source.Generation != expectedGeneration {
		return media.OpenResult{}, ErrNotConnected
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
