package projection

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const (
	PendingJoinStatusPending = "pending"
	PendingJoinStatusError   = "error"
)

func UpsertPendingJoin(db *sql.DB, p PendingJoin) error {
	if db == nil {
		return fmt.Errorf("projection: db is nil")
	}
	if p.InviteHash == "" {
		return fmt.Errorf("projection: pending join hash required")
	}
	if p.InviteLink == "" {
		return fmt.Errorf("projection: pending join link required")
	}
	if p.RequestedAt == 0 {
		p.RequestedAt = time.Now().Unix()
	}
	if p.Status == "" {
		p.Status = PendingJoinStatusPending
	}
	_, err := db.Exec(`
		INSERT INTO pending_joins
		  (invite_hash, invite_link, title, requested_at, last_checked_at, status, last_error)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(invite_hash) DO UPDATE SET
		  invite_link = excluded.invite_link,
		  title = excluded.title,
		  last_checked_at = excluded.last_checked_at,
		  status = excluded.status,
		  last_error = excluded.last_error
	`, p.InviteHash, p.InviteLink, p.Title, p.RequestedAt, p.LastCheckedAt, p.Status, p.LastError)
	if err != nil {
		return fmt.Errorf("projection: upsert pending join: %w", err)
	}
	return nil
}

func ListPendingJoins(db *sql.DB) ([]PendingJoin, error) {
	rows, err := db.Query(`
		SELECT invite_hash, invite_link, title, requested_at, last_checked_at, status, last_error
		FROM pending_joins
		ORDER BY requested_at ASC, invite_hash ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("projection: list pending joins: %w", err)
	}
	defer rows.Close()

	var out []PendingJoin
	for rows.Next() {
		var p PendingJoin
		if err := rows.Scan(&p.InviteHash, &p.InviteLink, &p.Title, &p.RequestedAt, &p.LastCheckedAt, &p.Status, &p.LastError); err != nil {
			return nil, fmt.Errorf("projection: scan pending join: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func GetPendingJoin(db *sql.DB, inviteHash string) (PendingJoin, error) {
	var p PendingJoin
	err := db.QueryRow(`
		SELECT invite_hash, invite_link, title, requested_at, last_checked_at, status, last_error
		FROM pending_joins
		WHERE invite_hash = ?
	`, inviteHash).Scan(&p.InviteHash, &p.InviteLink, &p.Title, &p.RequestedAt, &p.LastCheckedAt, &p.Status, &p.LastError)
	if errors.Is(err, sql.ErrNoRows) {
		return PendingJoin{}, fmt.Errorf("projection: pending join %q not found", inviteHash)
	}
	if err != nil {
		return PendingJoin{}, err
	}
	return p, nil
}

func UpdatePendingJoinCheck(db *sql.DB, inviteHash, status, lastError string) error {
	_, err := db.Exec(`
		UPDATE pending_joins
		SET last_checked_at = ?, status = ?, last_error = ?
		WHERE invite_hash = ?
	`, time.Now().Unix(), status, lastError, inviteHash)
	if err != nil {
		return fmt.Errorf("projection: update pending join check: %w", err)
	}
	return nil
}

func DeletePendingJoin(db *sql.DB, inviteHash string) error {
	_, err := db.Exec(`DELETE FROM pending_joins WHERE invite_hash = ?`, inviteHash)
	if err != nil {
		return fmt.Errorf("projection: delete pending join: %w", err)
	}
	return nil
}
