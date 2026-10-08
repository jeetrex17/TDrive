package file

import (
	"bytes"
	"testing"
	"time"
)

// BenchmarkBackupPreviewPipeline compares identical batches of two tiny JPEGs
// with real encryption, SQLite and a zero-latency offline Telegram fake. This
// measures scheduling/CPU overhead, not network throughput or device decoding.
// Both variants must finish all four derivatives, so deferring/skipping work
// cannot manufacture a speedup. Use fixed iterations for bounded fake history:
// go test ./backend/services/file -run '^$' -bench '^BenchmarkBackupPreviewPipeline$' -benchtime=20x -count=5 -benchmem
func BenchmarkBackupPreviewPipeline(b *testing.B) {
	for _, queued := range []bool{false, true} {
		b.Run(map[bool]string{false: "synchronous", true: "queued"}[queued], func(b *testing.B) {
			svc, db, _, _ := newTestService(b)
			configureEncryptedUpload(b, svc, bytes.Repeat([]byte{7}, 32))
			path := writeTempNamedFile(b, "photo.jpg", tinyRenditionJPEG(b))
			var originals, drain time.Duration
			b.ReportAllocs()
			for b.Loop() {
				ctx := b.Context()
				var worker *BackupRenditionWorker
				started := time.Now()
				if queued {
					worker = svc.NewBackupRenditionWorker(ctx, personalChannelID)
				}
				for range 2 {
					var err error
					if queued {
						_, err = svc.UploadBackupWithRenditions(ctx, personalChannelID, path, "", true, worker)
					} else {
						_, err = svc.UploadBackup(ctx, personalChannelID, path, "", true)
					}
					if err != nil {
						if worker != nil {
							worker.Close()
						}
						b.Fatal(err)
					}
				}
				originals += time.Since(started)
				started = time.Now()
				if worker != nil {
					worker.Wait()
					worker.Close()
				}
				drain += time.Since(started)
			}
			var count int
			if err := db.QueryRow(`SELECT COUNT(*) FROM file_renditions`).Scan(&count); err != nil || count != b.N*4 {
				b.Fatalf("derivatives = %d, want %d; err=%v", count, b.N*4, err)
			}
			b.ReportMetric(float64(originals.Nanoseconds())/float64(b.N), "originals-ns/batch")
			b.ReportMetric(float64(drain.Nanoseconds())/float64(b.N), "drain-ns/batch")
		})
	}
}
