package tgclient

import (
	"errors"
	"testing"

	"github.com/gotd/td/tgerr"
)

func TestIsFileMigrateRecognizesOnlyTheRedirect(t *testing.T) {
	t.Parallel()

	if !isFileMigrate(tgerr.New(303, "FILE_MIGRATE_4")) {
		t.Fatal("FILE_MIGRATE_4 was not recognized as a redirect")
	}
	for _, err := range []error{
		tgerr.New(420, "FLOOD_WAIT_30"),
		tgerr.New(400, "FILE_REFERENCE_EXPIRED"),
		errors.New("engine forcibly closed"),
		nil,
	} {
		if isFileMigrate(err) {
			t.Fatalf("%v was mistaken for a redirect", err)
		}
	}
}

// The limiter budget follows the pool so every socket stays busy while a
// seek can still land on all of them at once.
func TestGetFileBudgetTracksPoolSize(t *testing.T) {
	t.Parallel()

	if MaxConcurrentGetFile != 3*MediaPoolSize || PlaybackGetFileReserve != MediaPoolSize {
		t.Fatalf("budget = %d/%d for a pool of %d", MaxConcurrentGetFile, PlaybackGetFileReserve, MediaPoolSize)
	}
	if MaxConcurrentBackgroundGetFile < 2*DefaultDownloadThreads {
		t.Fatalf("background pool %d cannot fit two downloads of %d threads", MaxConcurrentBackgroundGetFile, DefaultDownloadThreads)
	}
}
