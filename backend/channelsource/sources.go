package channelsource

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"unicode"

	"TDrive/backend/media"
	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

var ErrInvalidPublicLink = errors.New("enter a public channel @username or t.me link")
var ErrPublicChannelUnavailable = errors.New("This link is not a public channel you can access. Check the link or choose a chat from the list")

// Telegram lists at most 2,000 dialogs, then may append Saved Messages.
const maxCachedCandidates = 2001

// migrateMediaSources preserves the channel connections created before peers
// had a kind. The marker prevents a later disconnect from reviving a legacy row.
func migrateMediaSources(db *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS connected_media_sources (
			account_id INTEGER NOT NULL, peer_kind TEXT NOT NULL, peer_id INTEGER NOT NULL,
			title TEXT NOT NULL, username TEXT NOT NULL DEFAULT '', protected INTEGER NOT NULL DEFAULT 0,
			generation TEXT NOT NULL, access_hash INTEGER NOT NULL DEFAULT 0,
			photo_id INTEGER NOT NULL DEFAULT 0, photo BLOB, public_resolved INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY(account_id, peer_kind, peer_id))`,
		`CREATE TABLE IF NOT EXISTS media_source_migrations (name TEXT PRIMARY KEY)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return fmt.Errorf("media source: create metadata: %w", err)
		}
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("media source: begin migration: %w", err)
	}
	defer tx.Rollback()
	var marker string
	err = tx.QueryRow(`SELECT name FROM media_source_migrations WHERE name='channel-v1'`).Scan(&marker)
	if err == nil {
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("media source: inspect migration: %w", err)
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO connected_media_sources
		(account_id, peer_kind, peer_id, title, username, protected, generation, access_hash, photo_id, photo)
		SELECT account_id, 'channel', channel_id, title, username, protected, generation, access_hash, photo_id, photo
		FROM connected_channel_sources`); err != nil {
		return fmt.Errorf("media source: copy channel connections: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO media_source_migrations(name) VALUES('channel-v1')`); err != nil {
		return fmt.Errorf("media source: record migration: %w", err)
	}
	return tx.Commit()
}

func sourceInfo(accountID int64, peer tgclient.SourcePeer) SourceInfo {
	info := SourceInfo{PeerKind: string(peer.Kind), PeerID: peer.ID, Title: peer.Title,
		Username: peer.Username, Protected: peer.Protected, Available: true, AccountID: accountID,
		CandidatesTruncated: peer.Truncated, accessHash: peer.AccessHash}
	if peer.Kind == tgclient.PeerChannel {
		info.ChannelID = peer.ID
	}
	return info
}

func validSource(kind string, id int64) bool {
	return tgclient.PeerKind(kind).Valid() && id > 0
}

func (s *Service) storedSource(ctx context.Context, accountID int64, kind string, id int64) (SourceInfo, bool, error) {
	var info SourceInfo
	var protected, public int
	err := s.db.QueryRowContext(ctx, `SELECT title, username, protected, generation, access_hash, public_resolved
		FROM connected_media_sources WHERE account_id=? AND peer_kind=? AND peer_id=?`, accountID, kind, id).
		Scan(&info.Title, &info.Username, &protected, &info.Generation, &info.accessHash, &public)
	if errors.Is(err, sql.ErrNoRows) {
		return SourceInfo{}, false, ErrNotConnected
	}
	if err != nil {
		return SourceInfo{}, false, fmt.Errorf("media source: read connection: %w", err)
	}
	info.PeerKind, info.PeerID, info.AccountID, info.Connected = kind, id, accountID, true
	if kind == string(tgclient.PeerChannel) {
		info.ChannelID = id
	}
	info.Protected = protected != 0
	return info, public != 0, nil
}

func (s *Service) ListSourceCandidates(ctx context.Context) ([]SourceInfo, error) {
	accountID, err := s.account(ctx)
	if err != nil {
		return nil, err
	}
	var peers []tgclient.SourcePeer
	if err := s.telegram(ctx, func() error { var callErr error; peers, callErr = s.tg.ListMediaSourcePeers(ctx); return callErr }); err != nil {
		return nil, fmt.Errorf("media source: list dialogs: %w", err)
	}
	drives, err := s.drives()
	if err != nil {
		return nil, err
	}
	connected, err := s.connectedSourceMap(ctx, accountID)
	if err != nil {
		return nil, err
	}
	out := make([]SourceInfo, 0, len(peers))
	for _, peer := range peers {
		if !peer.Kind.Valid() || peer.ID <= 0 || peer.Restricted ||
			(peer.Kind != tgclient.PeerGroup && peer.Kind != tgclient.PeerSelf && peer.AccessHash == 0) ||
			(peer.Kind == tgclient.PeerChannel && drives[peer.ID]) {
			continue
		}
		info := sourceInfo(accountID, peer)
		if old, ok := connected[sourceKey{kind: info.PeerKind, id: info.PeerID}]; ok {
			info.Connected, info.Generation = true, old.Generation
		}
		out = append(out, info)
	}
	sortSources(out)
	if current, err := s.account(ctx); err != nil || current != accountID {
		return nil, ErrUnavailable
	}
	s.rememberCandidates(accountID, peers, out)
	return out, nil
}

// Only peers actually shown by the picker can be used for a fast connection.
// The bounded snapshot lasts for this service's account session. Connection
// still verifies current access with Telegram before persisting a source.
func (s *Service) rememberCandidates(accountID int64, peers []tgclient.SourcePeer, shown []SourceInfo) {
	candidates := make(map[sourceKey]tgclient.SourcePeer, min(len(shown), maxCachedCandidates))
	allowed := make(map[sourceKey]struct{}, min(len(shown), maxCachedCandidates))
	for _, info := range shown {
		if len(allowed) == maxCachedCandidates {
			break
		}
		allowed[sourceKey{kind: info.PeerKind, id: info.PeerID}] = struct{}{}
	}
	for _, peer := range peers {
		key := sourceKey{kind: string(peer.Kind), id: peer.ID}
		if _, ok := allowed[key]; ok && len(candidates) < maxCachedCandidates {
			candidates[key] = peer
		}
	}
	s.candidatesMu.Lock()
	s.candidates, s.candidatesFor = candidates, accountID
	s.candidatesMu.Unlock()
}

func (s *Service) rememberResolvedCandidate(accountID int64, peer tgclient.SourcePeer) {
	s.candidatesMu.Lock()
	defer s.candidatesMu.Unlock()
	if s.candidatesFor != accountID {
		s.candidates = make(map[sourceKey]tgclient.SourcePeer)
		s.candidatesFor = accountID
	}
	key := sourceKey{kind: string(peer.Kind), id: peer.ID}
	if _, ok := s.candidates[key]; ok || len(s.candidates) < maxCachedCandidates {
		s.candidates[key] = peer
	}
}

func (s *Service) recentCandidate(accountID int64, kind string, id int64) (tgclient.SourcePeer, bool) {
	s.candidatesMu.Lock()
	defer s.candidatesMu.Unlock()
	if s.candidatesFor != accountID {
		clear(s.candidates)
		return tgclient.SourcePeer{}, false
	}
	peer, ok := s.candidates[sourceKey{kind: kind, id: id}]
	return peer, ok
}

type sourceKey struct {
	kind string
	id   int64
}

func (s *Service) connectedSourceMap(ctx context.Context, accountID int64) (map[sourceKey]SourceInfo, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT peer_kind, peer_id, title, username, protected, generation
		FROM connected_media_sources WHERE account_id=?`, accountID)
	if err != nil {
		return nil, fmt.Errorf("media source: list connections: %w", err)
	}
	defer rows.Close()
	out := make(map[sourceKey]SourceInfo)
	for rows.Next() {
		var info SourceInfo
		var protected int
		if err := rows.Scan(&info.PeerKind, &info.PeerID, &info.Title, &info.Username, &protected, &info.Generation); err != nil {
			return nil, err
		}
		info.AccountID, info.Connected, info.Protected = accountID, true, protected != 0
		if info.PeerKind == string(tgclient.PeerChannel) {
			info.ChannelID = info.PeerID
		}
		out[sourceKey{info.PeerKind, info.PeerID}] = info
	}
	return out, rows.Err()
}

func sortSources(sources []SourceInfo) {
	slices.SortFunc(sources, func(a, b SourceInfo) int {
		if n := strings.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title)); n != 0 {
			return n
		}
		if n := strings.Compare(a.PeerKind, b.PeerKind); n != 0 {
			return n
		}
		if a.PeerID < b.PeerID {
			return -1
		}
		if a.PeerID > b.PeerID {
			return 1
		}
		return 0
	})
}

func (s *Service) ListAllConnected(ctx context.Context) ([]SourceInfo, error) {
	accountID, err := s.account(ctx)
	if err != nil {
		return nil, err
	}
	drives, err := s.drives()
	if err != nil {
		return nil, err
	}
	connected, err := s.connectedSourceMap(ctx, accountID)
	if err != nil {
		return nil, err
	}
	out := make([]SourceInfo, 0, len(connected))
	for _, info := range connected {
		if info.PeerKind == string(tgclient.PeerChannel) && drives[info.PeerID] {
			continue
		}
		out = append(out, info)
	}
	sortSources(out)
	if current, err := s.account(ctx); err != nil || current != accountID {
		return nil, ErrUnavailable
	}
	return out, nil
}

func parsePublicChannel(input string) (string, error) {
	input = strings.TrimSpace(input)
	if strings.HasPrefix(input, "@") {
		input = input[1:]
	} else if strings.ContainsAny(input, "/:.") {
		if !strings.Contains(input, "://") {
			input = "https://" + input
		}
		parsed, err := url.Parse(input)
		if err != nil || parsed.Scheme != "https" || (parsed.Hostname() != "t.me" && parsed.Hostname() != "www.t.me") ||
			parsed.Port() != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
			return "", ErrInvalidPublicLink
		}
		parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		if len(parts) < 1 || len(parts) > 2 || (len(parts) == 2 && !positiveDecimal(parts[1])) {
			return "", ErrInvalidPublicLink
		}
		input = parts[0]
	}
	if len(input) < 5 || len(input) > 32 || !unicode.IsLetter(rune(input[0])) {
		return "", ErrInvalidPublicLink
	}
	for _, ch := range input {
		if ch > unicode.MaxASCII || !(unicode.IsLetter(ch) || unicode.IsDigit(ch) || ch == '_') {
			return "", ErrInvalidPublicLink
		}
	}
	return input, nil
}

func positiveDecimal(value string) bool {
	if value == "" || len(value) > 10 || value[0] == '0' {
		return false
	}
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func (s *Service) ResolvePublicSource(ctx context.Context, input string) (SourceInfo, error) {
	username, err := parsePublicChannel(input)
	if err != nil {
		return SourceInfo{}, err
	}
	accountID, err := s.account(ctx)
	if err != nil {
		return SourceInfo{}, err
	}
	var peer tgclient.SourcePeer
	if err := s.telegram(ctx, func() error {
		var callErr error
		peer, callErr = s.tg.ResolvePublicChannel(ctx, username)
		return callErr
	}); err != nil {
		if errors.Is(err, tgclient.ErrChannelUnavailable) {
			return SourceInfo{}, ErrPublicChannelUnavailable
		}
		return SourceInfo{}, fmt.Errorf("media source: resolve public channel: %w", err)
	}
	if peer.ID <= 0 || peer.AccessHash == 0 || peer.Kind != tgclient.PeerChannel || peer.Restricted {
		return SourceInfo{}, ErrPublicChannelUnavailable
	}
	drive, err := projection.ChannelExists(s.db, peer.ID)
	if err != nil {
		return SourceInfo{}, err
	}
	if drive {
		return SourceInfo{}, ErrUnavailable
	}
	info := sourceInfo(accountID, peer)
	stored, _, err := s.storedSource(ctx, accountID, info.PeerKind, info.PeerID)
	if err == nil {
		info.Connected, info.Generation = true, stored.Generation
	} else if !errors.Is(err, ErrNotConnected) {
		return SourceInfo{}, err
	}
	if current, err := s.account(ctx); err != nil || current != accountID {
		return SourceInfo{}, ErrUnavailable
	}
	s.rememberResolvedCandidate(accountID, peer)
	return info, nil
}

func (s *Service) ConnectSourceWithGate(ctx context.Context, kind string, id int64, username string, expectedAccountID int64, gate func(func() error) error) (SourceInfo, error) {
	if !validSource(kind, id) || expectedAccountID <= 0 || gate == nil {
		return SourceInfo{}, ErrUnavailable
	}
	accountID, err := s.account(ctx)
	if err != nil {
		return SourceInfo{}, err
	}
	if accountID != expectedAccountID {
		return SourceInfo{}, ErrUnavailable
	}
	if kind == string(tgclient.PeerChannel) {
		drive, err := projection.ChannelExists(s.db, id)
		if err != nil {
			return SourceInfo{}, err
		}
		if drive {
			return SourceInfo{}, ErrUnavailable
		}
	}
	var peer tgclient.SourcePeer
	public := false
	if username != "" && kind == string(tgclient.PeerChannel) {
		parsed, err := parsePublicChannel(username)
		if err != nil {
			return SourceInfo{}, err
		}
		public = true
		err = s.telegram(ctx, func() error {
			var callErr error
			peer, callErr = s.tg.ResolvePublicChannel(ctx, parsed)
			return callErr
		})
		if err != nil {
			if errors.Is(err, tgclient.ErrChannelUnavailable) {
				return SourceInfo{}, ErrPublicChannelUnavailable
			}
			return SourceInfo{}, fmt.Errorf("media source: resolve public channel: %w", err)
		}
	} else {
		if candidate, ok := s.recentCandidate(accountID, kind, id); ok {
			// A single read verifies current access and peer kind. The picker
			// snapshot is only a source for the access hash, never authority.
			err = s.telegram(ctx, func() error {
				var callErr error
				peer, callErr = s.tg.GetMediaSourcePeer(ctx, tgclient.InputPeer{
					Kind: candidate.Kind, ChannelID: id, AccessHash: candidate.AccessHash})
				return callErr
			})
			if errors.Is(err, tgclient.ErrChannelUnavailable) {
				return SourceInfo{}, ErrUnavailable
			}
			if err != nil {
				return SourceInfo{}, fmt.Errorf("media source: check dialog access: %w", err)
			}
		} else {
			var peers []tgclient.SourcePeer
			err = s.telegram(ctx, func() error { var callErr error; peers, callErr = s.tg.ListMediaSourcePeers(ctx); return callErr })
			if err != nil {
				return SourceInfo{}, fmt.Errorf("media source: find dialog: %w", err)
			}
			for _, candidate := range peers {
				if string(candidate.Kind) == kind && candidate.ID == id {
					peer = candidate
					break
				}
			}
		}
	}
	if peer.ID != id || string(peer.Kind) != kind || peer.Restricted ||
		(kind != string(tgclient.PeerGroup) && kind != string(tgclient.PeerSelf) && peer.AccessHash == 0) {
		if public && peer.ID == id {
			return SourceInfo{}, ErrPublicChannelUnavailable
		}
		return SourceInfo{}, ErrUnavailable
	}
	if now, err := s.account(ctx); err != nil || now != accountID {
		return SourceInfo{}, ErrUnavailable
	}
	generation, err := newGeneration()
	if err != nil {
		return SourceInfo{}, fmt.Errorf("media source: generate connection: %w", err)
	}
	info := sourceInfo(accountID, peer)
	err = gate(func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		if current, err := s.account(ctx); err != nil || current != accountID {
			return ErrUnavailable
		}
		if existing, _, err := s.storedSource(ctx, accountID, kind, id); err == nil {
			info = existing
			info.Available = true
			return nil
		} else if !errors.Is(err, ErrNotConnected) {
			return err
		}
		_, err := s.db.ExecContext(ctx, `INSERT INTO connected_media_sources
			(account_id, peer_kind, peer_id, title, username, protected, generation, access_hash, photo_id, public_resolved)
			VALUES(?,?,?,?,?,?,?,?,?,?)`, accountID, kind, id, peer.Title, peer.Username, peer.Protected,
			generation, peer.AccessHash, peer.PhotoID, public)
		if err != nil {
			return fmt.Errorf("media source: connect: %w", err)
		}
		info.Connected, info.Generation = true, generation
		return nil
	})
	return info, err
}

func (s *Service) DisconnectSourceWithGate(ctx context.Context, kind string, id, expectedAccountID int64, generation string, gate func(func() error) error) ([]string, error) {
	if !validSource(kind, id) || expectedAccountID <= 0 || generation == "" || gate == nil {
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
		stored, _, err := s.storedSource(ctx, accountID, kind, id)
		if err != nil || stored.Generation != generation {
			return ErrNotConnected
		}
		result, err := s.db.ExecContext(ctx, `DELETE FROM connected_media_sources WHERE account_id=? AND peer_kind=? AND peer_id=? AND generation=?`, accountID, kind, id, generation)
		if err != nil {
			return fmt.Errorf("media source: disconnect: %w", err)
		}
		if rows, err := result.RowsAffected(); err != nil || rows != 1 {
			return ErrNotConnected
		}
		tokens = s.media.CloseSourceSessions(accountID, kind, id)
		return nil
	})
	return tokens, err
}

func (s *Service) currentSource(ctx context.Context, kind string, id int64) (SourceInfo, bool, tgclient.SourcePeer, error) {
	accountID, err := s.account(ctx)
	if err != nil {
		return SourceInfo{}, false, tgclient.SourcePeer{}, err
	}
	stored, public, err := s.storedSource(ctx, accountID, kind, id)
	if err != nil {
		return SourceInfo{}, false, tgclient.SourcePeer{}, err
	}
	if stored.accessHash == 0 && kind != string(tgclient.PeerGroup) && kind != string(tgclient.PeerSelf) {
		var peers []tgclient.SourcePeer
		err := s.telegram(ctx, func() error { var callErr error; peers, callErr = s.tg.ListMediaSourcePeers(ctx); return callErr })
		if err != nil {
			return SourceInfo{}, false, tgclient.SourcePeer{}, fmt.Errorf("media source: restore peer access: %w", err)
		}
		for _, candidate := range peers {
			if string(candidate.Kind) == kind && candidate.ID == id {
				stored.accessHash = candidate.AccessHash
				break
			}
		}
		if stored.accessHash == 0 {
			return SourceInfo{}, false, tgclient.SourcePeer{}, ErrUnavailable
		}
	}
	peer := tgclient.InputPeer{Kind: tgclient.PeerKind(kind), ChannelID: id, AccessHash: stored.accessHash}
	var current tgclient.SourcePeer
	err = s.telegram(ctx, func() error { var callErr error; current, callErr = s.tg.GetMediaSourcePeer(ctx, peer); return callErr })
	if errors.Is(err, tgclient.ErrChannelUnavailable) {
		return SourceInfo{}, false, tgclient.SourcePeer{}, ErrUnavailable
	}
	if err != nil {
		return SourceInfo{}, false, tgclient.SourcePeer{}, fmt.Errorf("media source: check access: %w", err)
	}
	if current.Restricted || current.ID != id || string(current.Kind) != kind {
		return SourceInfo{}, false, tgclient.SourcePeer{}, ErrUnavailable
	}
	if current.AccessHash == 0 && kind != string(tgclient.PeerGroup) && kind != string(tgclient.PeerSelf) {
		current.AccessHash = stored.accessHash
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE connected_media_sources SET title=?, username=?, protected=?, access_hash=?,
		photo=CASE WHEN photo_id=? THEN photo ELSE NULL END, photo_id=?
		WHERE account_id=? AND peer_kind=? AND peer_id=? AND generation=?`,
		current.Title, current.Username, current.Protected, current.AccessHash, current.PhotoID, current.PhotoID,
		accountID, kind, id, stored.Generation); err != nil {
		return SourceInfo{}, false, tgclient.SourcePeer{}, fmt.Errorf("media source: refresh metadata: %w", err)
	}
	stored.Title, stored.Username, stored.Protected, stored.Available = current.Title, current.Username, current.Protected, true
	return stored, public, current, nil
}

func (s *Service) PageSource(ctx context.Context, kind string, id, offsetID int64, limit int, search, mediaKind string) (MediaPage, error) {
	if !validSource(kind, id) || offsetID < 0 || limit < 1 || limit > 100 || len(search) > 120 ||
		!validMediaKind(mediaKind) {
		return MediaPage{}, ErrInvalidPage
	}
	stored, _, source, err := s.currentSource(ctx, kind, id)
	if err != nil {
		return MediaPage{}, err
	}
	peer := tgclient.InputPeer{Kind: source.Kind, ChannelID: id, AccessHash: source.AccessHash}
	search = strings.TrimSpace(search)
	page := MediaPage{PeerKind: kind, PeerID: id, AccountID: stored.AccountID, Generation: stored.Generation,
		Items: make([]MediaItem, 0, limit)}
	if kind == string(tgclient.PeerChannel) {
		page.ChannelID = id
	}
	cursor := offsetID
	const batchSize = 100
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
			return MediaPage{}, fmt.Errorf("media source: page media: %w", err)
		}
		page.HasMore = len(messages) > 0
		for _, message := range messages {
			if message.MsgID <= 0 {
				continue
			}
			if page.NextOffsetID == 0 || message.MsgID < page.NextOffsetID {
				page.NextOffsetID = message.MsgID
			}
			item, ok := mediaItem(source, message)
			if ok && matchesMediaKind(item, mediaKind) {
				page.Items = append(page.Items, item)
			}
			if len(page.Items) >= limit {
				break
			}
		}
		if len(page.Items) >= limit || !page.HasMore || page.NextOffsetID <= 0 || page.NextOffsetID == cursor {
			break
		}
		cursor = page.NextOffsetID
	}
	current, _, err := s.storedSource(ctx, stored.AccountID, kind, id)
	if err != nil || current.Generation != stored.Generation {
		return MediaPage{}, ErrNotConnected
	}
	if accountID, err := s.account(ctx); err != nil || accountID != stored.AccountID {
		return MediaPage{}, ErrUnavailable
	}
	return page, nil
}

func (s *Service) OpenSourceWithGate(ctx context.Context, kind string, id, msgID, expectedAccountID int64, generation string, gate func(func() error) error) (media.OpenResult, error) {
	if !validSource(kind, id) || msgID <= 0 || expectedAccountID <= 0 || generation == "" || gate == nil {
		return media.OpenResult{}, ErrUnavailable
	}
	stored, public, source, err := s.currentSource(ctx, kind, id)
	if err != nil {
		return media.OpenResult{}, err
	}
	if stored.AccountID != expectedAccountID || stored.Generation != generation {
		return media.OpenResult{}, ErrNotConnected
	}
	peer := tgclient.InputPeer{Kind: source.Kind, ChannelID: id, AccessHash: source.AccessHash}
	var message tgclient.HistoryMessage
	err = s.telegram(ctx, func() error {
		var callErr error
		message, callErr = s.tg.GetChannelMessage(ctx, peer, msgID)
		return callErr
	})
	if err != nil {
		return media.OpenResult{}, fmt.Errorf("media source: fetch media: %w", err)
	}
	item, ok := mediaItem(source, message)
	if !ok || !item.Streamable {
		return media.OpenResult{}, media.ErrExternalRestricted
	}
	return s.media.OpenExternal(ctx, media.ExternalMedia{Peer: peer, Client: s.tg, Message: message,
		AccountID: stored.AccountID, Generation: stored.Generation, Protected: source.Protected, ProbeAccess: public,
		Validate: func(ctx context.Context) error {
			accountID, err := s.account(ctx)
			if err != nil || accountID != stored.AccountID {
				return ErrUnavailable
			}
			current, _, err := s.storedSource(ctx, stored.AccountID, kind, id)
			if err != nil || current.Generation != stored.Generation {
				return ErrNotConnected
			}
			return nil
		}}, func(add func() error) error {
		if accountID, err := s.account(ctx); err != nil || accountID != stored.AccountID {
			return ErrUnavailable
		}
		return gate(func() error {
			s.mu.Lock()
			defer s.mu.Unlock()
			current, _, err := s.storedSource(ctx, stored.AccountID, kind, id)
			if err != nil || current.Generation != stored.Generation {
				return ErrNotConnected
			}
			return add()
		})
	})
}

func (s *Service) SourcePhoto(ctx context.Context, kind string, id, expectedAccountID int64, expectedGeneration string) ([]byte, error) {
	if !validSource(kind, id) || expectedAccountID <= 0 {
		return nil, ErrUnavailable
	}
	accountID, err := s.account(ctx)
	if err != nil {
		return nil, err
	}
	if accountID != expectedAccountID {
		return nil, ErrUnavailable
	}
	var photo []byte
	var photoID int64
	var generation string
	err = s.db.QueryRowContext(ctx, `SELECT photo_id, photo, generation FROM connected_media_sources
		WHERE account_id=? AND peer_kind=? AND peer_id=?`, accountID, kind, id).Scan(&photoID, &photo, &generation)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if expectedGeneration != "" && (errors.Is(err, sql.ErrNoRows) || generation != expectedGeneration) {
		return nil, ErrNotConnected
	}
	if err == nil && len(photo) > 0 {
		if err := s.checkPhotoScope(ctx, accountID, kind, id, expectedGeneration); err != nil {
			return nil, err
		}
		return photo, nil
	}
	stored := err == nil
	var source tgclient.SourcePeer
	if stored {
		_, _, source, err = s.currentSource(ctx, kind, id)
	} else {
		if candidate, ok := s.recentCandidate(accountID, kind, id); ok {
			source = candidate
			err = nil
		} else {
			// An avatar is optional. Rewalking thousands of dialogs for a
			// missing picker result can itself trigger flood wait.
			return nil, s.checkPhotoScope(ctx, accountID, kind, id, expectedGeneration)
		}
	}
	if err != nil {
		return nil, err
	}
	if source.ID != id || source.PhotoID == 0 {
		return nil, s.checkPhotoScope(ctx, accountID, kind, id, expectedGeneration)
	}
	peer := tgclient.InputPeer{Kind: source.Kind, ChannelID: id, AccessHash: source.AccessHash}
	err = s.telegram(ctx, func() error {
		var callErr error
		photo, callErr = s.tg.DownloadChannelPhoto(ctx, peer, source.PhotoID)
		return callErr
	})
	if err != nil {
		return nil, fmt.Errorf("media source: download photo: %w", err)
	}
	if stored {
		_, err = s.db.ExecContext(ctx, `UPDATE connected_media_sources SET photo=? WHERE account_id=? AND peer_kind=? AND peer_id=? AND photo_id=?`,
			photo, accountID, kind, id, source.PhotoID)
		if err != nil {
			return nil, fmt.Errorf("media source: store photo: %w", err)
		}
	}
	if err := s.checkPhotoScope(ctx, accountID, kind, id, expectedGeneration); err != nil {
		return nil, err
	}
	return photo, nil
}

func (s *Service) checkPhotoScope(ctx context.Context, accountID int64, kind string, id int64, generation string) error {
	current, err := s.account(ctx)
	if err != nil || current != accountID {
		return ErrUnavailable
	}
	if generation == "" {
		return nil
	}
	stored, _, err := s.storedSource(ctx, accountID, kind, id)
	if err != nil || stored.Generation != generation {
		return ErrNotConnected
	}
	return nil
}
