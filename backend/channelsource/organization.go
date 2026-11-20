package channelsource

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	ErrSourceGroupName      = errors.New("enter a group name of up to 80 characters")
	ErrSourceGroupNameInUse = errors.New("a source group already has that name")
	ErrSourceGroupMissing   = errors.New("this source group no longer exists")
)

type SourceGroup struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type SourceGroupAssignment struct {
	PeerKind   string `json:"peer_kind"`
	PeerID     int64  `json:"peer_id"`
	Generation string `json:"generation"`
	GroupID    string `json:"group_id"`
}

type SourceOrganization struct {
	Groups      []SourceGroup           `json:"groups"`
	Assignments []SourceGroupAssignment `json:"assignments"`
}

func ensureSourceOrganizationSchema(db *sql.DB) error {
	for _, query := range []string{
		`CREATE TABLE IF NOT EXISTS media_source_groups (
			account_id INTEGER NOT NULL, id TEXT NOT NULL, name TEXT NOT NULL, name_key TEXT NOT NULL,
			PRIMARY KEY(account_id, id), UNIQUE(account_id, name_key)
		)`,
		`CREATE TABLE IF NOT EXISTS media_source_group_members (
			account_id INTEGER NOT NULL, peer_kind TEXT NOT NULL, peer_id INTEGER NOT NULL, group_id TEXT NOT NULL,
			PRIMARY KEY(account_id, peer_kind, peer_id),
			FOREIGN KEY(account_id, group_id) REFERENCES media_source_groups(account_id, id) ON DELETE CASCADE,
			FOREIGN KEY(account_id, peer_kind, peer_id)
				REFERENCES connected_media_sources(account_id, peer_kind, peer_id) ON DELETE CASCADE
		)`,
	} {
		if _, err := db.Exec(query); err != nil {
			return fmt.Errorf("media source: create organization tables: %w", err)
		}
	}
	return nil
}

func (s *Service) ListSourceGroups(ctx context.Context, expectedAccountID int64) (SourceOrganization, error) {
	return s.sourceOrganization(ctx, expectedAccountID, nil)
}

// SaveSourceGroup creates a group when groupID is empty and renames it otherwise.
func (s *Service) SaveSourceGroup(ctx context.Context, expectedAccountID int64, groupID, name string) (SourceOrganization, error) {
	name = strings.TrimSpace(name)
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 80 || strings.ContainsFunc(name, unicode.IsControl) {
		return SourceOrganization{}, ErrSourceGroupName
	}
	return s.sourceOrganization(ctx, expectedAccountID, func(tx *sql.Tx) error {
		return saveSourceGroup(ctx, tx, expectedAccountID, groupID, name)
	})
}

func saveSourceGroup(ctx context.Context, tx *sql.Tx, accountID int64, groupID, name string) error {
	key := strings.ToLower(name)
	var duplicate bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM media_source_groups
		WHERE account_id=? AND name_key=? AND id<>?)`, accountID, key, groupID).Scan(&duplicate)
	if err != nil {
		return err
	}
	if duplicate {
		return ErrSourceGroupNameInUse
	}
	if groupID == "" {
		id, err := newGeneration()
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO media_source_groups(account_id, id, name, name_key) VALUES(?, ?, ?, ?)`,
			accountID, id, name, key)
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE media_source_groups SET name=?, name_key=? WHERE account_id=? AND id=?`,
		name, key, accountID, groupID)
	return requireSourceGroupAffected(result, err)
}

func (s *Service) DeleteSourceGroup(ctx context.Context, expectedAccountID int64, groupID string) (SourceOrganization, error) {
	return s.sourceOrganization(ctx, expectedAccountID, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `DELETE FROM media_source_groups WHERE account_id=? AND id=?`, expectedAccountID, groupID)
		return requireSourceGroupAffected(result, err)
	})
}

func requireSourceGroupAffected(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrSourceGroupMissing
	}
	return nil
}

// MoveSourceToGroup assigns the current typed connection to one local group.
// An empty group ID returns the source to the ungrouped section.
func (s *Service) MoveSourceToGroup(ctx context.Context, kind string, peerID, expectedAccountID int64, generation, groupID string) (SourceOrganization, error) {
	if !validSource(kind, peerID) || generation == "" {
		return SourceOrganization{}, ErrNotConnected
	}
	return s.sourceOrganization(ctx, expectedAccountID, func(tx *sql.Tx) error {
		return moveSourceToGroup(ctx, tx, expectedAccountID, kind, peerID, generation, groupID)
	})
}

func moveSourceToGroup(ctx context.Context, tx *sql.Tx, accountID int64, kind string, peerID int64, generation, groupID string) error {
	var exists bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM connected_media_sources
		WHERE account_id=? AND peer_kind=? AND peer_id=? AND generation=?)`,
		accountID, kind, peerID, generation).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return ErrNotConnected
	}
	if groupID == "" {
		_, err := tx.ExecContext(ctx, `DELETE FROM media_source_group_members WHERE account_id=? AND peer_kind=? AND peer_id=?`,
			accountID, kind, peerID)
		return err
	}
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM media_source_groups WHERE account_id=? AND id=?)`,
		accountID, groupID).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return ErrSourceGroupMissing
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO media_source_group_members(account_id, peer_kind, peer_id, group_id)
		VALUES(?, ?, ?, ?) ON CONFLICT(account_id, peer_kind, peer_id) DO UPDATE SET group_id=excluded.group_id`,
		accountID, kind, peerID, groupID)
	return err
}

func (s *Service) sourceOrganization(ctx context.Context, expectedAccountID int64, change func(*sql.Tx) error) (SourceOrganization, error) {
	accountID, err := s.account(ctx)
	if err != nil {
		return SourceOrganization{}, err
	}
	if accountID != expectedAccountID {
		return SourceOrganization{}, ErrUnavailable
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SourceOrganization{}, fmt.Errorf("media source: begin organization: %w", err)
	}
	defer tx.Rollback()
	if change != nil {
		if err := change(tx); err != nil {
			return SourceOrganization{}, fmt.Errorf("media source: update organization: %w", err)
		}
	}
	groups, err := listSourceGroups(ctx, tx, accountID)
	if err != nil {
		return SourceOrganization{}, fmt.Errorf("media source: list groups: %w", err)
	}
	assignments, err := listSourceGroupAssignments(ctx, tx, accountID)
	if err != nil {
		return SourceOrganization{}, fmt.Errorf("media source: list assignments: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return SourceOrganization{}, fmt.Errorf("media source: commit organization: %w", err)
	}
	return SourceOrganization{Groups: groups, Assignments: assignments}, nil
}

func listSourceGroups(ctx context.Context, tx *sql.Tx, accountID int64) ([]SourceGroup, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, name FROM media_source_groups WHERE account_id=? ORDER BY name_key, id`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := []SourceGroup{}
	for rows.Next() {
		var group SourceGroup
		if err := rows.Scan(&group.ID, &group.Name); err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	return groups, rows.Err()
}

func listSourceGroupAssignments(ctx context.Context, tx *sql.Tx, accountID int64) ([]SourceGroupAssignment, error) {
	rows, err := tx.QueryContext(ctx, `SELECT members.peer_kind, members.peer_id, sources.generation, members.group_id
		FROM media_source_group_members AS members JOIN connected_media_sources AS sources
		USING(account_id, peer_kind, peer_id) WHERE members.account_id=?`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	assignments := []SourceGroupAssignment{}
	for rows.Next() {
		var assignment SourceGroupAssignment
		if err := rows.Scan(&assignment.PeerKind, &assignment.PeerID, &assignment.Generation, &assignment.GroupID); err != nil {
			return nil, err
		}
		assignments = append(assignments, assignment)
	}
	return assignments, rows.Err()
}
