package sync

import (
	"context"
	"strconv"
	"testing"

	"TDrive/backend/projection"
)

// A scan reports an unknown total while counting, then a determinate total
// once the plan is known, and finishes with done == total so a progress bar
// always lands on full.
func TestScanReportsCountingThenDeterminateApplyProgress(t *testing.T) {
	for _, tc := range []struct {
		name string
		scan func(*Engine) error
	}{
		{"incremental", func(e *Engine) error { return e.Incremental(context.Background(), testChan) }},
		{"authoritative", func(e *Engine) error { return e.EnsureAuthoritative(context.Background(), testChan) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, tg, eng := newSyncEnv(t)
			const pages = 3
			for i := 0; i < pages*defaultPageSize; i++ {
				sendOp(t, tg, projection.Op{
					Type: projection.OpMkdir, Obj: projection.FolderIDPrefix + strconv.Itoa(i),
					Parent: projection.RootParent, Name: "F" + strconv.Itoa(i),
				})
			}

			var updates []Progress
			eng.OnProgress = func(p Progress) { updates = append(updates, p) }
			if err := tc.scan(eng); err != nil {
				t.Fatalf("scan: %v", err)
			}

			var counting, applying []Progress
			for _, u := range updates {
				if u.ChannelID != testChan {
					t.Fatalf("progress for channel %d, want %d", u.ChannelID, testChan)
				}
				switch u.Phase {
				case ProgressCounting:
					counting = append(counting, u)
				case ProgressApplying:
					applying = append(applying, u)
				}
			}
			if len(counting) == 0 || len(applying) == 0 {
				t.Fatalf("counting=%d applying=%d, want both phases", len(counting), len(applying))
			}
			for _, u := range counting {
				if u.MessagesTotal != 0 || u.PagesTotal != 0 {
					t.Fatalf("counting reported a total it cannot know: %#v", u)
				}
			}
			if got := counting[len(counting)-1].MessagesDone; got != pages*defaultPageSize {
				t.Fatalf("counted %d messages, want %d", got, pages*defaultPageSize)
			}

			last := applying[len(applying)-1]
			if last.PagesDone != last.PagesTotal || last.PagesTotal != pages {
				t.Fatalf("final page progress = %d/%d, want %d/%d", last.PagesDone, last.PagesTotal, pages, pages)
			}
			if last.MessagesDone != last.MessagesTotal || last.MessagesTotal != pages*defaultPageSize {
				t.Fatalf("final message progress = %d/%d, want %d", last.MessagesDone, last.MessagesTotal, pages*defaultPageSize)
			}
			prev := 0
			for _, u := range applying {
				if u.MessagesDone <= prev || u.MessagesDone > u.MessagesTotal {
					t.Fatalf("apply progress not monotonic within total: %#v after %d", u, prev)
				}
				prev = u.MessagesDone
			}
		})
	}
}

// A sync with nothing new must stay silent: the UI should not flash a bar
// for the routine no-op case.
func TestSyncWithoutHistoryReportsNoProgress(t *testing.T) {
	_, _, eng := newSyncEnv(t)
	var updates []Progress
	eng.OnProgress = func(p Progress) { updates = append(updates, p) }

	if err := eng.Incremental(context.Background(), testChan); err != nil {
		t.Fatalf("incremental: %v", err)
	}
	if len(updates) != 0 {
		t.Fatalf("empty sync reported %d progress updates: %#v", len(updates), updates)
	}
}

// A nil hook is the default; scans must not panic without it.
func TestScanWithoutProgressHook(t *testing.T) {
	_, tg, eng := newSyncEnv(t)
	sendOp(t, tg, projection.Op{Type: projection.OpMkdir, Obj: "d:a", Parent: projection.RootParent, Name: "A"})
	if err := eng.Incremental(context.Background(), testChan); err != nil {
		t.Fatalf("incremental without hook: %v", err)
	}
}
