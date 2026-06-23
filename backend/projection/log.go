// log.go is the single transactional writer for replay_log + projection state.
//
// Both the local-action path (app.go: emit Telegram op -> ProjectFromOp) and
// the sync engine (read history -> ProjectFromOp per parsed message) MUST go
// through these functions. There is no other legal way to mutate the log or
// the projection.
//
// Tamper detection: each replay_log row stores a sha256 of the raw header that
// was on the wire when we first saw the op. If we ever encounter the same
// (channel_id, msg_id) again with a different hash, the caption was edited
// post-hoc by someone using the regular Telegram client. We record the event
// in replay_log_tamper but keep the original op canonical so already-synced
// clients converge.
package projection

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var ErrChannelNotEmpty = errors.New("projection: channel not empty")

func HashHeader(rawHeader string) string {
	sum := sha256.Sum256([]byte(rawHeader))
	return hex.EncodeToString(sum[:])
}

// ProjectFromOp opens its own transaction and projects one op. Use this from
// app.go local-action paths and from the sync engine's per-message loop.
//
// Returns alreadySeen=true when this (channel_id, msg_id) was already in the
// replay_log. In that case the projection is unchanged. If the hash differs
// from what we stored before, a row is written to replay_log_tamper.
func ProjectFromOp(db *sql.DB, channelID int64, msgID int64, op Op, actorID int64, rawHeader string) (alreadySeen bool, err error) {
	tx, err := db.Begin()
	if err != nil {
		return false, fmt.Errorf("projection: begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	alreadySeen, err = ProjectFromOpTx(tx, channelID, msgID, op, actorID, rawHeader)
	if err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, fmt.Errorf("projection: commit: %w", err)
	}
	return alreadySeen, nil
}

// ProjectFromOpTx is the caller-managed-tx variant. Use this from backfill so
// the replay_log insert + projector apply + backfill_progress checkpoint all
// commit atomically.
func ProjectFromOpTx(tx *sql.Tx, channelID int64, msgID int64, op Op, actorID int64, rawHeader string) (alreadySeen bool, err error) {
	if msgID <= 0 {
		return false, fmt.Errorf("projection: msg id required")
	}

	hash := HashHeader(rawHeader)

	existingHash, ok, err := existingHash(tx, channelID, msgID)
	if err != nil {
		return false, err
	}
	if ok {
		if existingHash != hash {
			if err := recordTamper(tx, channelID, msgID, existingHash, hash); err != nil {
				return false, err
			}
		}
		return true, nil
	}

	payload, err := json.Marshal(op)
	if err != nil {
		return false, fmt.Errorf("projection: marshal op: %w", err)
	}

	_, err = tx.Exec(`
		INSERT INTO replay_log
		  (channel_id, msg_id, op_type, op_payload_json, raw_header, first_seen_hash, actor_user_id, seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`,
		channelID, msgID, string(op.Type), payload,
		rawHeader, hash, actorID, time.Now().Unix())
	if err != nil {
		return false, fmt.Errorf("projection: insert replay_log: %w", err)
	}

	if err := ApplyOp(tx, channelID, msgID, op, actorID); err != nil {
		if isSkippableApplyError(err) {
			if recErr := recordReject(tx, channelID, msgID, err); recErr != nil {
				return false, recErr
			}
			return false, nil
		}
		return false, fmt.Errorf("projection: apply op msg=%d: %w", msgID, err)
	}
	return false, nil
}

func isSkippableApplyError(err error) bool {
	return errors.Is(err, ErrCycleRejected) || errors.Is(err, ErrBadOp)
}

func existingHash(tx *sql.Tx, channelID, msgID int64) (string, bool, error) {
	var h string
	err := tx.QueryRow(`
		SELECT first_seen_hash FROM replay_log
		WHERE channel_id = ? AND msg_id = ?
	`, channelID, msgID).Scan(&h)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("projection: read existing hash: %w", err)
	}
	return h, true, nil
}

func recordTamper(tx *sql.Tx, channelID, msgID int64, oldHash, newHash string) error {
	_, err := tx.Exec(`
		INSERT INTO replay_log_tamper (channel_id, msg_id, old_hash, new_hash, detected_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(channel_id, msg_id) DO UPDATE SET
			new_hash = excluded.new_hash,
			detected_at = excluded.detected_at
	`, channelID, msgID, oldHash, newHash, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("projection: record tamper: %w", err)
	}
	return nil
}

func recordReject(tx *sql.Tx, channelID, msgID int64, applyErr error) error {
	_, err := tx.Exec(`
		INSERT INTO replay_log_rejects (channel_id, msg_id, error, detected_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(channel_id, msg_id) DO UPDATE SET
			error = excluded.error,
			detected_at = excluded.detected_at
	`, channelID, msgID, applyErr.Error(), time.Now().Unix())
	if err != nil {
		return fmt.Errorf("projection: record rejected op: %w", err)
	}
	return nil
}

// ChannelIsEmpty returns true when there are no replay_log rows AND no
// folders/files projection rows for the channel. Precondition guard for
// InitialSyncEmptyChannel.
func ChannelIsEmpty(db *sql.DB, channelID int64) (bool, error) {
	for _, q := range []string{
		`SELECT 1 FROM replay_log WHERE channel_id = ? LIMIT 1`,
		`SELECT 1 FROM folders WHERE channel_id = ? LIMIT 1`,
		`SELECT 1 FROM files WHERE channel_id = ? LIMIT 1`,
	} {
		var tmp int
		err := db.QueryRow(q, channelID).Scan(&tmp)
		if err == nil {
			return false, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
	}
	return true, nil
}
