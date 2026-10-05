package channelsource

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"TDrive/backend/tgclient"
)

// peerWalkInterval bounds how often a photo for a channel the last dialog walk
// did not include can cause another walk.
const peerWalkInterval = time.Minute

// addPhotoColumns brings a table created by an earlier build of this feature
// up to the current schema. New tables already have the columns.
func addPhotoColumns(db *sql.DB) error {
	for _, column := range []struct{ name, ddl string }{
		{"access_hash", `ALTER TABLE connected_channel_sources ADD COLUMN access_hash INTEGER NOT NULL DEFAULT 0`},
		{"photo_id", `ALTER TABLE connected_channel_sources ADD COLUMN photo_id INTEGER NOT NULL DEFAULT 0`},
		{"photo", `ALTER TABLE connected_channel_sources ADD COLUMN photo BLOB`},
	} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('connected_channel_sources') WHERE name=?`, column.name).Scan(&count); err != nil {
			return fmt.Errorf("channel source: inspect %s: %w", column.name, err)
		}
		if count != 0 {
			continue
		}
		if _, err := db.Exec(column.ddl); err != nil {
			return fmt.Errorf("channel source: add %s: %w", column.name, err)
		}
	}
	return nil
}

// Photo returns a channel's small profile photo, or nil when it has none. An
// added channel keeps its photo in the database, so it downloads once, and
// again only after the channel changes its picture. A channel not added yet
// downloads on every call; the picker keeps it for the session.
func (s *Service) Photo(ctx context.Context, channelID int64) ([]byte, error) {
	if channelID <= 0 {
		return nil, ErrUnavailable
	}
	accountID, err := s.account(ctx)
	if err != nil {
		return nil, err
	}
	var accessHash, photoID int64
	var photo []byte
	err = s.db.QueryRowContext(ctx, `SELECT access_hash, photo_id, photo FROM connected_channel_sources
		WHERE account_id=? AND channel_id=?`, accountID, channelID).Scan(&accessHash, &photoID, &photo)
	added := err == nil
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return nil, fmt.Errorf("channel source: read photo: %w", err)
	case len(photo) > 0:
		return photo, nil
	}
	// Not added, or added before access hashes were kept: the dialog walk
	// knows the peer and its current photo.
	if accessHash == 0 {
		channel, ok, err := s.joined(ctx, accountID, channelID)
		if err != nil || !ok {
			return nil, err
		}
		accessHash, photoID = channel.AccessHash, channel.PhotoID
		if added {
			if _, err := s.db.ExecContext(ctx, `UPDATE connected_channel_sources SET access_hash=?, photo_id=?
				WHERE account_id=? AND channel_id=? AND access_hash=0`, accessHash, photoID, accountID, channelID); err != nil {
				return nil, fmt.Errorf("channel source: store peer: %w", err)
			}
		}
	}
	if photoID == 0 {
		return nil, nil
	}
	err = s.telegram(ctx, func() error {
		var callErr error
		photo, callErr = s.tg.DownloadChannelPhoto(ctx, tgclient.InputPeer{ChannelID: channelID, AccessHash: accessHash}, photoID)
		return callErr
	})
	if err != nil {
		return nil, fmt.Errorf("channel source: download photo: %w", err)
	}
	if added {
		// Keyed by photo ID too, so a picture replaced meanwhile is not stored
		// under the new ID.
		if _, err := s.db.ExecContext(ctx, `UPDATE connected_channel_sources SET photo=?
			WHERE account_id=? AND channel_id=? AND photo_id=?`, photo, accountID, channelID, photoID); err != nil {
			return nil, fmt.Errorf("channel source: store photo: %w", err)
		}
	}
	return photo, nil
}

// joined finds a channel the account has joined in the latest dialog walk,
// walking again when it is missing and the last walk is over a minute old.
// The lock is held across the walk, so concurrent avatars share one.
func (s *Service) joined(ctx context.Context, accountID, channelID int64) (tgclient.JoinedBroadcastChannel, bool, error) {
	s.peersMu.Lock()
	defer s.peersMu.Unlock()
	if s.peersFor == accountID {
		if channel, ok := s.peers[channelID]; ok {
			return channel, true, nil
		}
		if time.Since(s.walkedAt) < peerWalkInterval {
			return tgclient.JoinedBroadcastChannel{}, false, nil
		}
	}
	if _, err := s.walkLocked(ctx, accountID); err != nil {
		return tgclient.JoinedBroadcastChannel{}, false, err
	}
	channel, ok := s.peers[channelID]
	return channel, ok, nil
}

// walkLocked lists every broadcast channel the account has joined and keeps
// the result for photo lookups. The caller holds peersMu.
func (s *Service) walkLocked(ctx context.Context, accountID int64) ([]tgclient.JoinedBroadcastChannel, error) {
	var channels []tgclient.JoinedBroadcastChannel
	err := s.telegram(ctx, func() error {
		var callErr error
		channels, callErr = s.tg.ListJoinedBroadcastChannels(ctx)
		return callErr
	})
	if err != nil {
		return nil, fmt.Errorf("channel source: list joined channels: %w", err)
	}
	s.peers = make(map[int64]tgclient.JoinedBroadcastChannel, len(channels))
	for _, channel := range channels {
		s.peers[channel.ID] = channel
	}
	s.peersFor, s.walkedAt = accountID, time.Now()
	return channels, nil
}
