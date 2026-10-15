package file

import (
	"context"
	"testing"
	"time"
)

func waitForUploadWindow(t *testing.T, svc *Service) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		svc.uploadCancelMu.Lock()
		open := len(svc.uploadWindows)
		svc.uploadCancelMu.Unlock()
		if open > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("upload batch never opened its window")
}

type uploadOutcome struct {
	files    []Metadata
	failures []UploadFailure
	err      error
}

func startUploadWithFailures(ctx context.Context, svc *Service, paths []string) <-chan uploadOutcome {
	done := make(chan uploadOutcome, 1)
	go func() {
		files, failures, err := svc.UploadWithFailures(ctx, personalChannelID, paths, make([]string, len(paths)), false)
		done <- uploadOutcome{files, failures, err}
	}()
	return done
}

func TestPausedUploadsStartNothingUntilResumedAndSkipACancelledWaitingFile(t *testing.T) {
	svc, _, fake, _ := newTestService(t)
	svc.MaxConcurrentUploads = 1
	paths := []string{writeTempNamedFile(t, "a.txt", []byte("a")), writeTempNamedFile(t, "b.txt", []byte("b")), writeTempNamedFile(t, "c.txt", []byte("c"))}

	svc.PauseUploads()
	if !svc.UploadsPaused() {
		t.Fatal("UploadsPaused = false after PauseUploads")
	}
	done := startUploadWithFailures(t.Context(), svc, paths)
	waitForUploadWindow(t, svc)
	time.Sleep(20 * time.Millisecond)
	if sent := len(fake.SentFiles()); sent != 0 {
		t.Fatalf("sent %d files while paused, want none", sent)
	}
	if !svc.CancelUpload(1) {
		t.Fatal("CancelUpload of a waiting file reported nothing to cancel")
	}
	svc.ResumeUploads()

	outcome := <-done
	if sent := fake.SentFiles(); len(sent) != 2 {
		t.Fatalf("sent %d files, want the two that were not cancelled", len(sent))
	}
	if len(outcome.failures) != 1 || outcome.failures[0].Index != 1 || outcome.failures[0].Code != "canceled" {
		t.Fatalf("failures = %+v, want only the cancelled waiting file", outcome.failures)
	}
	if svc.UploadsPaused() {
		t.Fatal("still paused after ResumeUploads")
	}
}

func TestCancellingAPausedBatchSendsNothing(t *testing.T) {
	svc, _, fake, _ := newTestService(t)
	paths := []string{writeTempNamedFile(t, "a.txt", []byte("a")), writeTempNamedFile(t, "b.txt", []byte("b"))}
	ctx, cancel := context.WithCancel(t.Context())
	svc.PauseUploads()
	defer svc.ResumeUploads()

	done := startUploadWithFailures(ctx, svc, paths)
	waitForUploadWindow(t, svc)
	cancel()
	outcome := <-done
	if len(fake.SentFiles()) != 0 {
		t.Fatal("a paused, cancelled batch sent files")
	}
	if len(outcome.failures) != 2 {
		t.Fatalf("failures = %+v, want both files", outcome.failures)
	}
	for _, failure := range outcome.failures {
		if failure.Code != "canceled" {
			t.Fatalf("failure = %+v, want a cancelled file", failure)
		}
	}
	if svc.CancelUpload(0) {
		t.Fatal("a finished batch still accepted a cancel")
	}
}

// A file Telegram holds is no longer uploading: it leaves the in-flight list,
// counts as moved, and settles once its window is indexed.
func TestImportObserverMovesAcceptedFilesOutOfFlight(t *testing.T) {
	events := &importTestEventRecorder{}
	observer := newImportUploadObserver(&Service{Events: events}, 2)
	observer.EmitInitial()
	observer.Started(0, "a.jpg", 100, "")
	observer.Progress(0, 100)
	observer.Started(1, "b.jpg", 100, "")
	observer.Accepted(0)
	observer.Flush()
	payloads := importPayloads(t, events)
	saving := payloads[len(payloads)-1]
	active, _ := saving["active"].([]map[string]any)
	if accepted, _ := saving["accepted"].([]int); len(accepted) != 1 || accepted[0] != 0 || len(active) != 1 || active[0]["id"] != 1 || saving["saving"] != 1 {
		t.Fatalf("payload = %+v, want a.jpg saving and only b.jpg in flight", saving)
	}

	observer.Completed(0, "a.jpg")
	observer.Flush()
	payloads = importPayloads(t, events)
	last := payloads[len(payloads)-1]
	if last["saving"] != 0 || last["bytes"] != int64(100) {
		t.Fatalf("last payload = %+v, want nothing saving and a.jpg's bytes counted once", last)
	}
}
