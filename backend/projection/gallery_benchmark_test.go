package projection

import (
	"context"
	"database/sql"
	"testing"
)

// Run with -bench=BenchmarkGallery100K -benchmem. A deep page uses the sparse
// anchor directly, so its query cost does not grow with the preceding 99k rows.
func BenchmarkGallery100K(b *testing.B) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := MigratePersonalChannel(db, testChan); err != nil {
		b.Fatal(err)
	}
	seedGalleryFiles(b, db, testChan, 100000)
	timeline, err := MediaTimeline(context.Background(), db, testChan)
	if err != nil {
		b.Fatal(err)
	}
	if timeline.TotalCount != 100000 || len(timeline.Anchors) != 782 {
		b.Fatalf("invalid fixture count=%d anchors=%d", timeline.TotalCount, len(timeline.Anchors))
	}
	b.Run("Timeline", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := MediaTimeline(context.Background(), db, testChan); err != nil {
				b.Fatal(err)
			}
		}
	})
	for _, test := range []struct {
		name   string
		anchor int
	}{{"FirstPage", 0}, {"DeepPage", 780}} {
		b.Run(test.name, func(b *testing.B) {
			cursor := timeline.Anchors[test.anchor].Cursor
			b.ReportAllocs()
			for b.Loop() {
				page, err := MediaPage(context.Background(), db, testChan, cursor, 128)
				if err != nil || len(page.Items) != 128 {
					b.Fatalf("page count=%d error=%v", len(page.Items), err)
				}
			}
		})
	}
	b.Run("NeighborsDeep", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := MediaNeighbors(context.Background(), db, testChan, 100, 2, 2, timeline.Generation); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("LocateDeep", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := LocateMedia(context.Background(), db, testChan, 100, timeline.Generation); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkGalleryMillionMixed models a library where non-media files are
// interleaved with images. Run setup and each operation once with:
//
//	go test ./backend/projection -run '^$' -bench BenchmarkGalleryMillionMixed -benchtime=1x -benchmem
func BenchmarkGalleryMillionMixed(b *testing.B) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := MigratePersonalChannel(db, testChan); err != nil {
		b.Fatal(err)
	}
	seedMillionGalleryRows(b, db, testChan, 1_000_000)
	originalCache := mediaTimelineCache
	mediaTimelineCache = newGalleryTimelineCache(galleryTimelineCacheEntries)
	b.Cleanup(func() { mediaTimelineCache = originalCache })
	timeline, err := MediaTimeline(context.Background(), db, testChan)
	if err != nil {
		b.Fatal(err)
	}
	if timeline.TotalCount != 800_000 || len(timeline.Anchors) != 6_250 {
		b.Fatalf("invalid fixture count=%d anchors=%d", timeline.TotalCount, len(timeline.Anchors))
	}

	b.Run("CachedTimeline", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := MediaTimeline(context.Background(), db, testChan); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("DeepPage", func(b *testing.B) {
		cursor := timeline.Anchors[len(timeline.Anchors)-2].Cursor
		b.ReportAllocs()
		for b.Loop() {
			page, err := MediaPage(context.Background(), db, testChan, cursor, GalleryPageSize)
			if err != nil || len(page.Items) != GalleryPageSize {
				b.Fatalf("page count=%d error=%v", len(page.Items), err)
			}
		}
	})
	b.Run("LocateDeep", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			location, err := LocateMedia(context.Background(), db, testChan, 1, timeline.Generation)
			if err != nil || location.Index != timeline.TotalCount-1 {
				b.Fatalf("location=%+v error=%v", location, err)
			}
		}
	})
}
