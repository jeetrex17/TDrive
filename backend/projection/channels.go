package projection

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// InsertChannel adds a row to the channels table. Used by:
//   - personal-channel migration (kind='personal')
//   - CreateSharedDrive (kind='shared', personal_backfill_done=1 since
//     a freshly-created shared channel has nothing to backfill)
//   - JoinSharedDrive (kind='shared', personal_backfill_done=1 same reason)
//
// Idempotent on conflict (channel already known): updates access_hash and
// title in case they changed, leaves other fields alone.
func InsertChannel(db *sql.DB, c Channel) error {
	if c.ChannelID == 0 {
		return fmt.Errorf("projection: insert channel: id is zero")
	}
	if c.Kind != KindPersonal && c.Kind != KindShared {
		return fmt.Errorf("projection: insert channel: invalid kind %q", c.Kind)
	}
	joined := c.JoinedAt
	if joined == 0 {
		joined = time.Now().Unix()
	}
	personalDone := 0
	if c.PersonalBackfillDone {
		personalDone = 1
	}
	_, err := db.Exec(`
		INSERT INTO channels
		  (channel_id, access_hash, title, kind, invite_link, joined_at, personal_backfill_done)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(channel_id) DO UPDATE SET
		  access_hash = excluded.access_hash,
		  title = excluded.title
	`, c.ChannelID, c.AccessHash, c.Title, c.Kind, nullable(c.InviteLink), joined, personalDone)
	if err != nil {
		return fmt.Errorf("projection: insert channel: %w", err)
	}
	return nil
}

// DeleteChannel removes the channel row plus everything scoped to it:
// replay_log, replay_log_tamper, folders, files, file_parts,
// pending_part_cleanup, backfill_progress.
//
// Used by LeaveSharedDrive. Wraps the cascade in a single transaction so a
// crash mid-delete leaves the channel intact rather than half-deleted.
func DeleteChannel(db *sql.DB, channelID int64) error {
	if channelID == 0 {
		return fmt.Errorf("projection: delete channel: id is zero")
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("projection: delete channel begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, q := range []string{
		`DELETE FROM files WHERE channel_id = ?`,
		`DELETE FROM file_parts WHERE channel_id = ?`,
		`DELETE FROM pending_part_cleanup WHERE channel_id = ?`,
		`DELETE FROM folders WHERE channel_id = ?`,
		`DELETE FROM replay_log WHERE channel_id = ?`,
		`DELETE FROM replay_log_tamper WHERE channel_id = ?`,
		`DELETE FROM backfill_progress WHERE channel_id = ?`,
		`DELETE FROM channels WHERE channel_id = ?`,
	} {
		if _, err := tx.Exec(q, channelID); err != nil {
			return fmt.Errorf("projection: delete channel %q: %w", q, err)
		}
	}
	return tx.Commit()
}

// ListChannels returns all known channels. Personal first, then shared
// ordered by joined_at ascending (oldest at top, matches typical sidebar
// expectations).
func ListChannels(db *sql.DB) ([]Channel, error) {
	rows, err := db.Query(`
		SELECT channel_id, access_hash, title, kind, COALESCE(invite_link, ''), joined_at,
		       last_synced_msg, last_viewed_msg, has_unseen_content,
		       initial_sync_done, personal_backfill_done
		FROM channels
		ORDER BY CASE kind WHEN ? THEN 0 ELSE 1 END, joined_at ASC, channel_id ASC
	`, KindPersonal)
	if err != nil {
		return nil, fmt.Errorf("projection: list channels: %w", err)
	}
	defer rows.Close()

	var out []Channel
	for rows.Next() {
		var (
			c                                                Channel
			hasUnseen, initialSyncDone, personalBackfillDone int
		)
		if err := rows.Scan(
			&c.ChannelID, &c.AccessHash, &c.Title, &c.Kind, &c.InviteLink, &c.JoinedAt,
			&c.LastSyncedMsg, &c.LastViewedMsg, &hasUnseen,
			&initialSyncDone, &personalBackfillDone,
		); err != nil {
			return nil, fmt.Errorf("projection: scan channel: %w", err)
		}
		c.HasUnseenContent = hasUnseen != 0
		c.InitialSyncDone = initialSyncDone != 0
		c.PersonalBackfillDone = personalBackfillDone != 0
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetChannel returns the row matching channelID, or sql.ErrNoRows wrapped.
func GetChannel(db *sql.DB, channelID int64) (Channel, error) {
	var (
		c                                                Channel
		hasUnseen, initialSyncDone, personalBackfillDone int
	)
	err := db.QueryRow(`
		SELECT channel_id, access_hash, title, kind, COALESCE(invite_link, ''), joined_at,
		       last_synced_msg, last_viewed_msg, has_unseen_content,
		       initial_sync_done, personal_backfill_done
		FROM channels WHERE channel_id = ?
	`, channelID).Scan(
		&c.ChannelID, &c.AccessHash, &c.Title, &c.Kind, &c.InviteLink, &c.JoinedAt,
		&c.LastSyncedMsg, &c.LastViewedMsg, &hasUnseen,
		&initialSyncDone, &personalBackfillDone,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Channel{}, fmt.Errorf("projection: channel %d not found", channelID)
	}
	if err != nil {
		return Channel{}, err
	}
	c.HasUnseenContent = hasUnseen != 0
	c.InitialSyncDone = initialSyncDone != 0
	c.PersonalBackfillDone = personalBackfillDone != 0
	return c, nil
}

// UpdateInviteLink caches the most recently exported invite link. Invite
// links are NEVER trusted as durable — every "Share" click should call
// MessagesExportChatInvite and refresh this cache.
func UpdateInviteLink(db *sql.DB, channelID int64, link string) error {
	_, err := db.Exec(`UPDATE channels SET invite_link = ? WHERE channel_id = ?`, nullable(link), channelID)
	if err != nil {
		return fmt.Errorf("projection: update invite link: %w", err)
	}
	return nil
}

// UpdateAccessHash refreshes the cached access_hash. Telegram occasionally
// rotates these; sync paths should call this when ResolveDriveChannel
// returns a different value than the one stored.
func UpdateAccessHash(db *sql.DB, channelID, accessHash int64) error {
	_, err := db.Exec(`UPDATE channels SET access_hash = ? WHERE channel_id = ?`, accessHash, channelID)
	if err != nil {
		return fmt.Errorf("projection: update access hash: %w", err)
	}
	return nil
}

func nullable(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
