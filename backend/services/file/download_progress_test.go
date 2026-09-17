package file

import (
	"context"
	"testing"
	"time"
)

func TestDownloadProgressEmitsTheRequestIDFromContext(t *testing.T) {
	recorder := &argumentEventRecorder{}
	svc := &Service{Events: recorder}
	progress := svc.downloadProgress(WithDownloadProgressID(context.Background(), "file:7:42"), 100)

	// downloadProgress intentionally coalesces its first 100ms of updates.
	time.Sleep(110 * time.Millisecond)
	progress(25, 100)

	event, ok := recorder.last("download_progress")
	if !ok || len(event.args) != 2 {
		t.Fatalf("download progress event = %#v, want percent and request id", event)
	}
	if event.args[0] != 25.0 || event.args[1] != "file:7:42" {
		t.Fatalf("download progress args = %#v, want [25 file:7:42]", event.args)
	}
}
