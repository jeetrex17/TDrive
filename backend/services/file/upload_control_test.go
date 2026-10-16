package file

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"TDrive/backend/tgclient"
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

// The next file in line has passed the pause gate and waits for a free slot.
// A cancel aimed at it then must still skip it, not find nothing to stop.
func TestCancellingAFileWaitingForASlotSkipsIt(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	svc.MaxConcurrentUploads = 1
	paths := []string{writeTempNamedFile(t, "a.txt", []byte("a")), writeTempNamedFile(t, "b.txt", []byte("b"))}
	synctest.Test(t, func(t *testing.T) {
		entered := make(chan struct{}, len(paths))
		release := make(chan struct{})
		svc.TG = &countingClient{Fake: fakeTG, entered: entered, release: release}
		done := startUploadWithFailures(t.Context(), svc, paths)
		<-entered
		synctest.Wait()
		if !svc.CancelUpload(1) {
			t.Fatal("CancelUpload(1) found nothing to stop while b.txt waited for a slot")
		}
		close(release)
		outcome := <-done
		if outcome.err != nil || len(outcome.files) != 1 || outcome.files[0].Name != "a.txt" {
			t.Fatalf("outcome = %+v, want only a.txt uploaded and no batch error", outcome)
		}
		if len(outcome.failures) != 1 || outcome.failures[0].Index != 1 || outcome.failures[0].Code != "canceled" {
			t.Fatalf("failures = %+v, want b.txt reported canceled", outcome.failures)
		}
	})
}

func TestAPauseHoldsAFileThatWasWaitingForASlot(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	svc.MaxConcurrentUploads = 1
	paths := []string{writeTempNamedFile(t, "a.txt", []byte("a")), writeTempNamedFile(t, "b.txt", []byte("b"))}
	synctest.Test(t, func(t *testing.T) {
		entered := make(chan struct{}, len(paths))
		release := make(chan struct{})
		client := &countingClient{Fake: fakeTG, entered: entered, release: release}
		svc.TG = client
		done := startUploadWithFailures(t.Context(), svc, paths)
		<-entered
		synctest.Wait()
		svc.PauseUploads()
		defer svc.ResumeUploads()
		close(release)
		synctest.Wait()
		if calls, _, _ := client.snapshot(); calls != 1 {
			t.Fatalf("send calls = %d once a.txt finished, want b.txt held by the pause", calls)
		}
		svc.ResumeUploads()
		if outcome := <-done; outcome.err != nil || len(outcome.files) != 2 {
			t.Fatalf("outcome = %+v, want both files once resumed", outcome)
		}
	})
}

// unknownOutcomeClient reports a cancelled send as one Telegram may still have
// accepted, as a real client does when the cancel lands mid-request.
type unknownOutcomeClient struct{ *countingClient }

func unknownWhenCanceled(ctx context.Context, err error) error {
	if err != nil && ctx.Err() != nil {
		return fmt.Errorf("%w: send media: %w", tgclient.ErrSendOutcomeUnknown, err)
	}
	return err
}

func (c unknownOutcomeClient) SendFile(ctx context.Context, peer tgclient.InputPeer, r io.Reader, name, caption string, totalSize int64, onProgress func(sent, total int64)) (tgclient.SendFileResult, error) {
	result, err := c.countingClient.SendFile(ctx, peer, r, name, caption, totalSize, onProgress)
	return result, unknownWhenCanceled(ctx, err)
}

func (c unknownOutcomeClient) SendFileWithRandomID(ctx context.Context, peer tgclient.InputPeer, r io.Reader, name, caption string, totalSize int64, onProgress func(sent, total int64), randomID int64) (tgclient.SendFileResult, error) {
	result, err := c.countingClient.SendFileWithRandomID(ctx, peer, r, name, caption, totalSize, onProgress, randomID)
	return result, unknownWhenCanceled(ctx, err)
}

// A cancel that lands while Telegram may already hold the file cannot clear
// it: the file still needs attention and the batch reports the failure.
func TestCancellingAFileWhoseSendOutcomeIsUnknownStillNeedsAttention(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	svc.MaxConcurrentUploads = 2
	paths := []string{writeTempNamedFile(t, "a.txt", []byte("a")), writeTempNamedFile(t, "b.txt", []byte("b"))}
	synctest.Test(t, func(t *testing.T) {
		entered := make(chan struct{}, len(paths))
		release := make(chan struct{})
		svc.TG = unknownOutcomeClient{&countingClient{Fake: fakeTG, entered: entered, release: release}}
		done := startUploadWithFailures(t.Context(), svc, paths)
		<-entered
		<-entered
		synctest.Wait()
		if !svc.CancelUpload(0) {
			t.Fatal("CancelUpload(0) did not find a.txt sending")
		}
		close(release)
		outcome := <-done
		if outcome.err == nil || len(outcome.failures) != 1 || outcome.failures[0].Index != 0 || outcome.failures[0].Code != "outcome_unknown" {
			t.Fatalf("outcome = %+v, want a.txt left needing attention and a batch error", outcome)
		}
	})
}

// Cancelling one file of an import stops only that file. The import carries
// on, finishes done and lists the file canceled rather than failed.
func TestCancellingOneImportFileLetsTheImportFinish(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	svc.CreateFolder = testFolderCreator(db)
	svc.MaxConcurrentUploads = 1
	events := &importTestEventRecorder{}
	svc.Events = events
	root := filepath.Join(t.TempDir(), "Album")
	mkfile(t, filepath.Join(root, "a.jpg"), "a")
	mkfile(t, filepath.Join(root, "b.jpg"), "b")
	synctest.Test(t, func(t *testing.T) {
		entered := make(chan struct{}, 2)
		release := make(chan struct{})
		svc.TG = &countingClient{Fake: fakeTG, entered: entered, release: release}
		done := make(chan error, 1)
		go func() { done <- svc.RunImport(t.Context(), personalChannelID, []string{root}, "", false, false) }()
		<-entered
		synctest.Wait()
		if !svc.CancelUpload(0) {
			t.Fatal("CancelUpload(0) did not find the first file sending")
		}
		close(release)
		if err := <-done; err != nil {
			t.Fatalf("RunImport: %v", err)
		}
	})
	if sent := fakeTG.SentFiles(); len(sent) != 1 {
		t.Fatalf("sent %d files, want the one that was not cancelled", len(sent))
	}
	payload, ok := events.lastPayload("import_complete")
	if !ok || payload["status"] != "done" || payload["uploaded"] != 1 || payload["failed"] != 0 {
		t.Fatalf("import completion = %+v, want done with one file uploaded and none failed", payload)
	}
	var statuses []string
	events.mu.Lock()
	for _, record := range events.records {
		if record.name != "import_upload_progress" || len(record.args) != 1 {
			continue
		}
		payload, _ := record.args[0].(map[string]any)
		settled, _ := payload["settled"].([]map[string]any)
		for _, file := range settled {
			status, _ := file["status"].(string)
			statuses = append(statuses, status)
		}
	}
	events.mu.Unlock()
	slices.Sort(statuses)
	if want := []string{"canceled", "done"}; !slices.Equal(statuses, want) {
		t.Fatalf("settled statuses = %q, want %q", statuses, want)
	}
}
