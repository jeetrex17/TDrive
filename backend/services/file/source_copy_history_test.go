package file

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestSourceCopyHistoryKeepsPendingAndNewestFinishedInOrder(t *testing.T) {
	svc, db, _, _ := newTestService(t)
	ctx := t.Context()
	if err := svc.ensureSourceCopyJournal(ctx); err != nil {
		t.Fatal(err)
	}
	// Exercise an existing journal from before batches acquired their own ID.
	if _, err := db.ExecContext(ctx, `
		DROP INDEX source_copy_jobs_by_batch;
		DROP INDEX IF EXISTS source_copy_jobs_pending_by_scope;
		DROP INDEX IF EXISTS source_copy_jobs_finished_by_scope;
		CREATE INDEX IF NOT EXISTS source_copy_jobs_by_scope
			ON source_copy_jobs (account_id, channel_id, status, started_at);
		ALTER TABLE source_copy_jobs DROP COLUMN batch_id;
	`); err != nil {
		t.Fatal(err)
	}
	insert := func(id string, account, channel int64, status string) {
		t.Helper()
		_, err := db.ExecContext(ctx, `
			INSERT INTO source_copy_jobs (
				id, account_id, channel_id, peer_kind, peer_id, generation,
				message_id, identity, parent_id, started_at, name, status
			) VALUES (?, ?, ?, 'channel', 9, 'source', 1, 'document', '', 1, ?, ?)
		`, id, account, channel, id, status)
		if err != nil {
			t.Fatal(err)
		}
	}
	insert("old pending", 7, personalChannelID, sourceCopyPending)
	for index := range 105 {
		if index == 60 {
			insert("recent pending", 7, personalChannelID, sourceCopyPending)
		}
		insert(fmt.Sprintf("finished %d", index), 7, personalChannelID, sourceCopyFailed)
	}
	insert("other account", 8, personalChannelID, sourceCopyPending)
	insert("other drive", 7, sharedChannelID, sourceCopyFailed)

	results, err := svc.listSourceCopyJobs(ctx, personalChannelID, 7)
	if err != nil {
		t.Fatal(err)
	}
	var ids, expected []string
	for _, result := range results {
		ids = append(ids, result.ID)
	}
	for index := 104; index >= 5; index-- {
		expected = append(expected, fmt.Sprintf("finished %d", index))
		if index == 60 {
			expected = append(expected, "recent pending")
		}
	}
	expected = append(expected, "old pending")
	if !slices.Equal(ids, expected) {
		t.Fatalf("copy history = %v, want %v", ids, expected)
	}
	job, err := svc.loadSourceCopyJob(ctx, personalChannelID, 7, "old pending")
	if err != nil || job.BatchID != "" || job.Result.Status != sourceCopyPending {
		t.Fatalf("legacy job = %+v, %v", job, err)
	}

	rows, err := db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+sourceCopyHistoryQuery, personalChannelID, 7, personalChannelID, 7)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	plan := strings.Join(details, "\n")
	for _, index := range []string{"source_copy_jobs_pending_by_scope", "source_copy_jobs_finished_by_scope"} {
		if !strings.Contains(plan, index) {
			t.Errorf("copy history did not use %s:\n%s", index, plan)
		}
	}
}
