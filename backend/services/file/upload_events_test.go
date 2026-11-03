package file

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func importPayloads(t *testing.T, events *importTestEventRecorder) []map[string]any {
	t.Helper()
	events.mu.Lock()
	records := append([]importEventRecord(nil), events.records...)
	events.mu.Unlock()
	payloads := make([]map[string]any, 0, len(records))
	for index, record := range records {
		if record.name != "import_upload_progress" || len(record.args) != 1 {
			t.Fatalf("record %d = %+v, want one aggregate progress payload", index, record)
		}
		payload, ok := record.args[0].(map[string]any)
		if !ok {
			t.Fatalf("record %d payload = %T, want map", index, record.args[0])
		}
		payloads = append(payloads, payload)
	}
	return payloads
}

// The aggregate is what a row runs on, but it cannot say which file is actually
// moving -- so the payload names the ones in flight, and no more of them than a
// row can show.
func TestImportUploadObserverNamesInFlightFilesUpToTheDisplayBound(t *testing.T) {
	events := &importTestEventRecorder{}
	observer := newImportUploadObserver(&Service{Events: events}, 100)
	// Files starting together are one change of who is moving, so the payload
	// that names them all is the one after the rate limit lets go.
	clock := time.Unix(0, 0)
	observer.now = func() time.Time { return clock }
	for id := range maxUploadConcurrency {
		observer.Started(id, "clip-"+string(rune('a'+id))+".mov", 1_000, "")
	}
	clock = clock.Add(minImportUploadActiveInterval)
	observer.Progress(0, 50)

	payloads := importPayloads(t, events)
	last := payloads[len(payloads)-1]
	active, ok := last["active"].([]map[string]any)
	if !ok {
		t.Fatalf("active = %T, want a list of files", last["active"])
	}
	if len(active) != maxImportUploadActiveFiles {
		t.Fatalf("named files = %d, want the display bound %d", len(active), maxImportUploadActiveFiles)
	}
	if count, _ := last["activeCount"].(int); count != maxUploadConcurrency {
		t.Fatalf("activeCount = %v, want every file in flight %d", last["activeCount"], maxUploadConcurrency)
	}
	// Ids are handed out in start order, so the list is the oldest files first
	// and a file keeps its place while the ones around it come and go.
	for index, file := range active {
		if file["id"] != index {
			t.Fatalf("active[%d] id = %v, want start order", index, file["id"])
		}
		if file["name"] == "" {
			t.Fatalf("active[%d] has no name", index)
		}
	}
	// Half of one 1,000-byte file has gone.
	if bytes, _ := last["bytes"].(int64); bytes != 500 {
		t.Fatalf("bytes = %v, want 500 from the one file that has moved", last["bytes"])
	}
}

// A completed file's whole size counts; a failed one's does not, because those
// bytes are not on Telegram and the number is meant to be what actually went.
func TestImportUploadObserverCountsOnlyDeliveredBytes(t *testing.T) {
	events := &importTestEventRecorder{}
	observer := newImportUploadObserver(&Service{Events: events}, 2)
	observer.Started(0, "kept.bin", 400, "")
	observer.Started(1, "lost.bin", 600, "")
	observer.Progress(1, 50)
	observer.Completed(0, "kept.bin")
	observer.Failed(1, "lost.bin", errors.New("flood wait"))

	payloads := importPayloads(t, events)
	last := payloads[len(payloads)-1]
	if bytes, _ := last["bytes"].(int64); bytes != 400 {
		t.Fatalf("bytes = %v, want only the delivered file's 400", last["bytes"])
	}
	if count, _ := last["activeCount"].(int); count != 0 {
		t.Fatalf("activeCount = %v, want nothing left in flight", last["activeCount"])
	}
	if done, failed, canceled, reasons := observer.Summary(); done != 1 || failed != 1 || canceled != 0 || len(reasons) != 1 {
		t.Fatalf("summary = (%d, %d, %d, %v), want one done, one failed and one reason", done, failed, canceled, reasons)
	}
}

// Naming the files in flight must not turn a ten-thousand-file import into
// twenty thousand events: the percentage is worth one event per whole point,
// and a change of which files are moving is worth one per interval.
func TestImportUploadObserverStaysBoundedWhenEveryFileChurns(t *testing.T) {
	const total = 10_000
	const perFile = time.Millisecond

	events := &importTestEventRecorder{}
	observer := newImportUploadObserver(&Service{Events: events}, total)
	clock := time.Unix(0, 0)
	observer.now = func() time.Time { return clock }
	observer.EmitInitial()

	for id := range total {
		clock = clock.Add(perFile)
		observer.Started(id, "photo.jpg", 1_000, "")
		observer.Progress(id, 100)
		observer.Completed(id, "photo.jpg")
	}

	elapsed := time.Duration(total) * perFile
	// One per whole percentage point, plus the rate-limited churn events, plus
	// the initial one. Far below the two events per file a naive observer sends.
	bound := 101 + int(elapsed/minImportUploadActiveInterval) + 1
	payloads := importPayloads(t, events)
	if len(payloads) > bound {
		t.Fatalf("events = %d for %d files, want at most %d", len(payloads), total, bound)
	}
	if len(observer.active) != 0 {
		t.Fatalf("retained in-flight files = %d, want none once the import is done", len(observer.active))
	}
	if len(observer.failureReasons) > maxImportUploadFailureReasons {
		t.Fatalf("retained failure reasons = %d, want at most %d", len(observer.failureReasons), maxImportUploadFailureReasons)
	}
	for index, payload := range payloads {
		active, ok := payload["active"].([]map[string]any)
		if !ok || len(active) > maxImportUploadActiveFiles {
			t.Fatalf("payload %d named %v files, want at most %d", index, payload["active"], maxImportUploadActiveFiles)
		}
	}
	if progress, _ := payloads[len(payloads)-1]["progress"].(float64); progress != 100 {
		t.Fatalf("final progress = %v, want 100", progress)
	}
}

// Activity lists every imported file with its outcome. Each one is reported once,
// inside the payloads the aggregate row already receives, so per-file detail
// costs no event per file.
func TestImportUploadObserverReportsEachSettledFileOnce(t *testing.T) {
	const total = 500
	events := &importTestEventRecorder{}
	observer := newImportUploadObserver(&Service{Events: events}, total)
	clock := time.Unix(0, 0)
	observer.now = func() time.Time { return clock }
	for id := range total {
		clock = clock.Add(time.Millisecond)
		name := fmt.Sprintf("file-%d.jpg", id)
		observer.Started(id, name, 10, "")
		if id == 7 {
			observer.Failed(id, name, errors.New("rpc error code 400: FILE_REFERENCE_EXPIRED"))
			continue
		}
		observer.Completed(id, name)
	}
	observer.Flush()

	payloads := importPayloads(t, events)
	if bound := 101 + int(time.Duration(total)*time.Millisecond/minImportUploadActiveInterval) + 2; len(payloads) > bound {
		t.Fatalf("events = %d for %d files, want at most %d", len(payloads), total, bound)
	}
	seen := map[int]map[string]any{}
	for _, payload := range payloads {
		settled, _ := payload["settled"].([]map[string]any)
		for _, file := range settled {
			id, _ := file["id"].(int)
			if _, duplicate := seen[id]; duplicate {
				t.Fatalf("file %d reported twice", id)
			}
			seen[id] = file
		}
	}
	if len(seen) != total {
		t.Fatalf("settled files = %d, want %d", len(seen), total)
	}
	if failed := seen[7]; failed["status"] != "failed" || failed["name"] != "file-7.jpg" || failed["message"] == nil {
		t.Fatalf("failed file = %+v, want its name, status and reason", failed)
	}
	if done := seen[8]; done["status"] != "done" || done["size"] != int64(10) {
		t.Fatalf("completed file = %+v, want done with its size", done)
	}
	if len(observer.settled) != 0 {
		t.Fatalf("observer still holds %d settled files after flushing", len(observer.settled))
	}
}
