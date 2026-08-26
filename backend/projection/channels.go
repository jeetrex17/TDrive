package projection

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
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
	updateJoinedAt := 1
	if joined == 0 {
		joined = time.Now().Unix()
		updateJoinedAt = 0
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
		  title = excluded.title,
		  joined_at = CASE WHEN ? = 1 THEN excluded.joined_at ELSE channels.joined_at END
	`, c.ChannelID, c.AccessHash, c.Title, c.Kind, nullable(c.InviteLink), joined, personalDone, updateJoinedAt)
	if err != nil {
		slog.Error("projection: insert channel failed", "channel_id", c.ChannelID, "kind", c.Kind, "error", err)
		return fmt.Errorf("projection: insert channel: %w", err)
	}
	slog.Info("projection: channel inserted", "channel_id", c.ChannelID, "kind", c.Kind)
	return nil
}

// DeleteChannel removes the channel row plus every projection, encryption,
// replay, and cleanup record scoped to it.
//
// Used by LeaveSharedDrive. Wraps the cascade in a single transaction so a
// crash mid-delete leaves the channel intact rather than half-deleted.
func DeleteChannel(db *sql.DB, channelID int64) error {
	if channelID == 0 {
		return fmt.Errorf("projection: delete channel: id is zero")
	}
	slog.Warn("projection: deleting channel and all scoped data", "channel_id", channelID)
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("projection: delete channel begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, q := range []string{
		`DELETE FROM dirents WHERE channel_id = ?`,
		`DELETE FROM file_revisions WHERE channel_id = ?`,
		`DELETE FROM projection_operations WHERE channel_id = ?`,
		`DELETE FROM trash_entries WHERE channel_id = ?`,
		`DELETE FROM files WHERE channel_id = ?`,
		`DELETE FROM file_parts WHERE channel_id = ?`,
		`DELETE FROM pending_part_cleanup WHERE channel_id = ?`,
		`DELETE FROM folders WHERE channel_id = ?`,
		`DELETE FROM replay_log_rejects WHERE channel_id = ?`,
		`DELETE FROM replay_log WHERE channel_id = ?`,
		`DELETE FROM replay_log_tamper WHERE channel_id = ?`,
		`DELETE FROM backfill_progress WHERE channel_id = ?`,
		`DELETE FROM encryption WHERE channel_id = ?`,
		`DELETE FROM channels WHERE channel_id = ?`,
	} {
		if _, err := tx.Exec(q, channelID); err != nil {
			return fmt.Errorf("projection: delete channel %q: %w", q, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	slog.Info("projection: channel deleted", "channel_id", channelID)
	return nil
}

// ChannelExists reports whether a channel row is already registered. It also
// handles a database that has not created the projection schema yet, which is
// the normal state before first-run personal-drive recovery.
func ChannelExists(db *sql.DB, channelID int64) (bool, error) {
	if db == nil {
		return false, fmt.Errorf("projection: channel exists: db is nil")
	}
	if channelID <= 0 {
		return false, fmt.Errorf("projection: channel exists: invalid id %d", channelID)
	}
	var tableCount int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'table' AND name = 'channels'
	`).Scan(&tableCount); err != nil {
		return false, fmt.Errorf("projection: inspect channels table: %w", err)
	}
	if tableCount == 0 {
		return false, nil
	}

	var exists int
	err := db.QueryRow(`SELECT 1 FROM channels WHERE channel_id = ?`, channelID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("projection: check channel %d: %w", channelID, err)
	}
	return true, nil
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
	slog.Debug("projection: channel access hash refreshed", "channel_id", channelID)
	return nil
}

// MarkPersonalBackfillDone records that a personal channel already has an
// authoritative local projection. Recovery uses this after a full history
// scan so the legacy migration backfill cannot write duplicate metadata into
// an existing Telegram channel.
func MarkPersonalBackfillDone(db *sql.DB, channelID int64) error {
	if channelID == 0 {
		return fmt.Errorf("projection: mark personal backfill done: id is zero")
	}
	result, err := db.Exec(`UPDATE channels SET personal_backfill_done = 1 WHERE channel_id = ?`, channelID)
	if err != nil {
		return fmt.Errorf("projection: mark personal backfill done: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("projection: mark personal backfill done rows affected: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("projection: mark personal backfill done: channel %d not found", channelID)
	}
	return nil
}

func nullable(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
