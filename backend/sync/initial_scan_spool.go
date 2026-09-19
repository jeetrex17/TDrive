package sync

import (
	"database/sql"
	"fmt"

	"TDrive/backend/tgclient"
)

// The initial-scan spool is scratch storage private to a full history scan: a
// place to park fetched pages between the network pass and the single
// transaction that projects them. It exists because those two things cannot
// share a transaction (see applyInitialHistoryPlan) and cannot share memory
// either — a million-message drive is an explicit target of this codebase, and
// holding its parsed ops at once costs hundreds of megabytes, which on the
// mobile builds is a kill rather than a slowdown.
//
// It deliberately lives outside the projection schema. Rows left behind by an
// interrupted scan must be invisible to everything else, and in particular to
// projection.ChannelIsEmpty, whose replay_log/folders/files probe is what
// guards InitialSyncEmptyChannel and caption-less adoption. Spooled rows trip
// none of those, so an abandoned scan still leaves no state behind.
//
// Rows are the inputs to ParseHistoryPageWithOptions rather than parsed ops,
// so the apply pass runs the identical parse over identical data and no
// encode/decode round trip can alter an op on its way to the projection.

// initialScanSpoolBatch bounds the rows held between each read and apply
// phase, matching the replay batching in projection.RebuildProjectionTx: the
// read is drained and closed before applying so SQLite can execute the
// projection writes inside the same transaction.
const initialScanSpoolBatch = 256

func ensureInitialScanSpool(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS initial_scan_spool (
			channel_id    INTEGER NOT NULL,
			msg_id        INTEGER NOT NULL,
			from_id       INTEGER NOT NULL,
			date          INTEGER NOT NULL,
			text          TEXT    NOT NULL,
			has_media     INTEGER NOT NULL,
			media_size    INTEGER NOT NULL,
			document_name TEXT    NOT NULL,
			PRIMARY KEY (channel_id, msg_id)
		)
	`)
	if err != nil {
		return fmt.Errorf("sync: create initial-scan spool: %w", err)
	}
	return nil
}

func clearInitialScanSpool(db *sql.DB, channelID int64) error {
	if _, err := db.Exec(`DELETE FROM initial_scan_spool WHERE channel_id = ?`, channelID); err != nil {
		return fmt.Errorf("sync: clear initial-scan spool: %w", err)
	}
	return nil
}

// spoolHistoryPage parks one fetched page. The whole page lands in a single
// short transaction so a crash can never split a page, and the primary key
// makes a re-fetched page overwrite rather than collide.
func spoolHistoryPage(db *sql.DB, channelID int64, page []tgclient.HistoryMessage) error {
	if len(page) == 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("sync: begin initial-scan spool write: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare(`
		INSERT OR REPLACE INTO initial_scan_spool
			(channel_id, msg_id, from_id, date, text, has_media, media_size, document_name)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("sync: prepare initial-scan spool write: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, m := range page {
		if _, err := stmt.Exec(channelID, m.MsgID, m.FromID, m.Date, m.Text, m.HasMedia, m.MediaSize, m.DocumentName); err != nil {
			return fmt.Errorf("sync: spool msg=%d: %w", m.MsgID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sync: commit initial-scan spool write: %w", err)
	}
	return nil
}

// eachSpooledBatch replays the spool in ascending message order, handing each
// batch to apply. Keyset pagination on the primary key gives that order
// without a sort and without a cursor held open across the applies.
func eachSpooledBatch(tx *sql.Tx, channelID int64, apply func([]tgclient.HistoryMessage) error) error {
	lastMsgID := int64(-1 << 63)
	batch := make([]tgclient.HistoryMessage, 0, initialScanSpoolBatch)
	for {
		rows, err := tx.Query(`
			SELECT msg_id, from_id, date, text, has_media, media_size, document_name
			FROM initial_scan_spool
			WHERE channel_id = ? AND msg_id > ?
			ORDER BY msg_id ASC
			LIMIT ?
		`, channelID, lastMsgID, initialScanSpoolBatch)
		if err != nil {
			return fmt.Errorf("sync: scan initial-scan spool: %w", err)
		}
		batch = batch[:0]
		for rows.Next() {
			var m tgclient.HistoryMessage
			if err := rows.Scan(&m.MsgID, &m.FromID, &m.Date, &m.Text, &m.HasMedia, &m.MediaSize, &m.DocumentName); err != nil {
				_ = rows.Close()
				return fmt.Errorf("sync: read initial-scan spool row: %w", err)
			}
			batch = append(batch, m)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return fmt.Errorf("sync: read initial-scan spool: %w", err)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("sync: close initial-scan spool scan: %w", err)
		}
		if len(batch) == 0 {
			return nil
		}
		if err := apply(batch); err != nil {
			return err
		}
		lastMsgID = batch[len(batch)-1].MsgID
	}
}
