package photobackup

import (
	"fmt"
	"testing"
	"time"
)

// BenchmarkClaimAndRequeue isolates ledger coordination from encryption and
// network. Each operation includes the atomic claim plus returning the same job
// to pending, keeping queue size constant across b.Loop iterations. It cannot
// establish end-to-end upload throughput or physical-device performance.
func BenchmarkClaimAndRequeue(b *testing.B) {
	for _, count := range []int{1, 10000} {
		b.Run(fmt.Sprintf("jobs_%d", count), func(b *testing.B) {
			now := time.Unix(100, 0)
			e, scope := testEngine(b, &now)
			e.db.SetMaxOpenConns(1)
			if _, err := e.db.ExecContext(b.Context(), `PRAGMA journal_mode=WAL; PRAGMA synchronous=NORMAL`); err != nil {
				b.Fatal(err)
			}
			configure(b, e, scope)
			assets := make([]Asset, count)
			for i := range count {
				id := fmt.Sprintf("%d", i)
				assets[i] = Asset{ID: id, Version: "v", Path: "/" + id + ".jpg", Name: id + ".jpg", ModifiedAt: now}
			}
			for start := 0; start < count; start += 128 {
				page := assets[start:min(start+128, count)]
				if n, err := e.EnqueuePage(b.Context(), scope, "camera", page); err != nil || n != len(page) {
					b.Fatalf("enqueue=%d err=%v", n, err)
				}
			}
			settings, err := e.GetSettings(b.Context(), scope)
			if err != nil {
				b.Fatal(err)
			}
			if testing.Verbose() {
				rows, err := e.db.QueryContext(b.Context(), `EXPLAIN QUERY PLAN `+claimCandidateSQL, scope.AccountID, scope.DriveID, now.UnixNano(), true, true, scope.AccountID, scope.DriveID, now.UnixNano(), true, true)
				if err != nil {
					b.Fatal(err)
				}
				for rows.Next() {
					var id, parent, unused int
					var detail string
					if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
						b.Fatal(err)
					}
					b.Log(detail)
				}
				if err := rows.Err(); err != nil {
					b.Fatal(err)
				}
				if err := rows.Close(); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			for b.Loop() {
				job, claimed, err := e.claimNext(b.Context(), scope, settings)
				if err != nil || !claimed {
					b.Fatalf("claimed=%v err=%v", claimed, err)
				}
				if _, err := e.db.ExecContext(b.Context(), `UPDATE photo_backup_jobs SET status='pending' WHERE account_id=? AND drive_id=? AND source_id=? AND asset_id=? AND version=? AND resource_id=?`, scope.AccountID, scope.DriveID, job.source.ID, job.asset.ID, job.asset.Version, job.asset.ResourceID); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// Candidate selection isolates the ordering tradeoff from SQLite write/flush
// noise. A ready item behind a large ineligible prefix is intentionally included.
func BenchmarkClaimCandidate(b *testing.B) {
	const legacy = `SELECT j.rowid FROM photo_backup_jobs j INDEXED BY photo_backup_jobs_ready JOIN photo_backup_sources s USING(account_id,drive_id,source_id) WHERE j.account_id=? AND j.drive_id=? AND s.enabled=1 AND j.status IN ('pending','error') AND j.next_attempt_at<=? AND ((j.media_type='photo' AND ?) OR (j.media_type='video' AND ?)) ORDER BY j.created_at,j.rowid LIMIT 1`
	for _, prefix := range []string{"ready", "backoff", "media", "source"} {
		b.Run(prefix, func(b *testing.B) {
			now := time.Unix(100, 0)
			e, scope := testEngine(b, &now)
			e.db.SetMaxOpenConns(1)
			configure(b, e, scope)
			if err := e.UpsertSource(b.Context(), Source{Scope: scope, ID: "disabled", Kind: "folder", Root: "/disabled"}); err != nil {
				b.Fatal(err)
			}
			tx, err := e.db.BeginTx(b.Context(), nil)
			if err != nil {
				b.Fatal(err)
			}
			defer tx.Rollback()
			for i := range 10001 {
				status, source, media := "pending", "camera", "photo"
				var next int64
				if i < 10000 {
					switch prefix {
					case "backoff":
						status, next = "error", now.Add(time.Hour).UnixNano()
					case "media":
						media = "video"
					case "source":
						source = "disabled"
					}
				}
				if _, err := tx.ExecContext(b.Context(), `INSERT INTO photo_backup_jobs(account_id,drive_id,source_id,asset_id,version,path,name,media_type,modified_at,size,status,next_attempt_at,created_at,updated_at) VALUES(?, ?, ?, ?, 'v', '/a.jpg', 'a.jpg', ?, 0, 1, ?, ?, ?, 0)`, scope.AccountID, scope.DriveID, source, fmt.Sprint(i), media, status, next, i); err != nil {
					b.Fatal(err)
				}
			}
			if err := tx.Commit(); err != nil {
				b.Fatal(err)
			}
			for _, query := range []struct{ name, sql string }{{"legacy", legacy}, {"fifo", claimCandidateSQL}} {
				b.Run(query.name, func(b *testing.B) {
					args := []any{scope.AccountID, scope.DriveID, now.UnixNano(), true, false}
					if query.name == "fifo" {
						args = append(args, args...)
					}
					b.ReportAllocs()
					for b.Loop() {
						var rowid int64
						if err := e.db.QueryRowContext(b.Context(), query.sql, args...).Scan(&rowid); err != nil {
							b.Fatal(err)
						}
						want := int64(10001)
						if prefix == "ready" {
							want = 1
						}
						if rowid != want {
							b.Fatalf("rowid=%d want=%d", rowid, want)
						}
					}
				})
			}
		})
	}
}
