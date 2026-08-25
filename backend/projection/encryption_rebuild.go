package projection

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
)

// ErrEncryptionConfigReplayInvalid means the canonical encryption-policy
// history cannot be trusted. Callers must fail closed rather than interpreting
// a missing derived encryption row as a plaintext drive.
var ErrEncryptionConfigReplayInvalid = errors.New("projection: encryption config replay is invalid")

type encryptionReplayRow struct {
	msgID      int64
	rawHeader  string
	storedHash string
	tampered   bool
}

// RebuildEncryptionConfigFromReplay recreates only the derived encryption row
// from the canonical replay log. It deliberately does not rebuild files or
// folders, so checking mount policy stays cheap and cannot disturb navigation
// state. The returned boolean reports whether encryption-policy history exists.
//
// Every policy header is re-parsed and hash-checked. A recorded edit, corrupt
// header, or invalid vault configuration aborts the transaction and returns a
// stable error that mount callers can convert to a sanitized fail-closed error.
func RebuildEncryptionConfigFromReplay(db *sql.DB, channelID int64) (bool, error) {
	if db == nil || channelID <= 0 {
		return false, fmt.Errorf("%w: database and channel are required", ErrEncryptionConfigReplayInvalid)
	}
	// Logging here deliberately never includes row.rawHeader or the parsed Op:
	// an OpEncConfig header carries the same KDF salt / wrapped master key /
	// key check fields as the encryption table row itself.
	slog.Debug("projection: rebuilding encryption config from replay", "channel_id", channelID)

	tx, err := db.Begin()
	if err != nil {
		return false, fmt.Errorf("%w: begin: %v", ErrEncryptionConfigReplayInvalid, err)
	}
	defer func() { _ = tx.Rollback() }()

	replay, err := readEncryptionReplay(tx, channelID)
	if err != nil {
		return false, err
	}
	if len(replay) == 0 {
		// A derived row without canonical replay is not trustworthy, but it may
		// be recoverable legacy state. Leave it intact and let the policy layer
		// require an authoritative refresh rather than destructively erasing it.
		slog.Debug("projection: no encryption-policy replay history found", "channel_id", channelID)
		return false, nil
	}
	ops := make([]Op, 0, len(replay))
	for _, row := range replay {
		if row.tampered || HashHeader(row.rawHeader) != row.storedHash {
			slog.Error("projection: encryption replay integrity check failed", "channel_id", channelID, "msg_id", row.msgID)
			return false, fmt.Errorf("%w: integrity check failed for msg %d", ErrEncryptionConfigReplayInvalid, row.msgID)
		}
		op, parseErr := Parse(row.rawHeader)
		if parseErr != nil || op.Type != OpEncConfig {
			return false, fmt.Errorf("%w: parse msg %d", ErrEncryptionConfigReplayInvalid, row.msgID)
		}
		ops = append(ops, op)
	}

	if _, err := tx.Exec(`DELETE FROM encryption WHERE channel_id = ?`, channelID); err != nil {
		return false, fmt.Errorf("%w: clear derived policy: %v", ErrEncryptionConfigReplayInvalid, err)
	}
	for i, op := range ops {
		if err := applyEncConfig(tx, channelID, op); err != nil {
			return false, fmt.Errorf("%w: apply msg %d: %v", ErrEncryptionConfigReplayInvalid, replay[i].msgID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("%w: commit: %v", ErrEncryptionConfigReplayInvalid, err)
	}
	slog.Info("projection: encryption config rebuilt from replay", "channel_id", channelID, "ops_replayed", len(ops))
	return len(replay) > 0, nil
}

func readEncryptionReplay(tx *sql.Tx, channelID int64) ([]encryptionReplayRow, error) {
	rows, err := tx.Query(`
		SELECT r.msg_id, r.raw_header, r.first_seen_hash,
		       CASE WHEN t.msg_id IS NULL THEN 0 ELSE 1 END
		FROM replay_log AS r
		LEFT JOIN replay_log_tamper AS t
		  ON t.channel_id = r.channel_id AND t.msg_id = r.msg_id
		WHERE r.channel_id = ? AND r.op_type = ?
		ORDER BY r.msg_id ASC
	`, channelID, string(OpEncConfig))
	if err != nil {
		return nil, fmt.Errorf("%w: scan: %v", ErrEncryptionConfigReplayInvalid, err)
	}
	defer rows.Close()

	var replay []encryptionReplayRow
	for rows.Next() {
		var row encryptionReplayRow
		var tampered int
		if err := rows.Scan(&row.msgID, &row.rawHeader, &row.storedHash, &tampered); err != nil {
			return nil, fmt.Errorf("%w: row: %v", ErrEncryptionConfigReplayInvalid, err)
		}
		row.tampered = tampered != 0
		replay = append(replay, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: rows: %v", ErrEncryptionConfigReplayInvalid, err)
	}
	return replay, nil
}
