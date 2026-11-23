package file

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"TDrive/backend/tgclient"
)

func TestFolderImportRetriesOnlyFailedFilesInOriginalFolders(t *testing.T) {
	svc, db, fake, _ := newTestService(t)
	svc.CreateFolder = testFolderCreator(db)
	svc.FloodWaitRetry = instantRetryPolicy()
	svc.TG = &batchFailureClient{Fake: fake, name: "retry.txt", err: tgclient.ErrInjectedTransport}
	root := filepath.Join(t.TempDir(), "Photos")
	retryPath := filepath.Join(root, "Trip", "retry.txt")
	mkfile(t, retryPath, "original bytes")
	mkfile(t, filepath.Join(root, "Other", "uploaded.txt"), "already accepted")

	retryFiles, err := svc.RunImportWithRetry(t.Context(), personalChannelID, []string{root}, "", false, false, "")
	if !errors.Is(err, tgclient.ErrInjectedTransport) || len(retryFiles) != 1 {
		t.Fatalf("initial report = %+v, error = %v, want one retryable failure", retryFiles, err)
	}
	photos := folderIDByName(t, db, personalChannelID, "", "Photos")
	trip := folderIDByName(t, db, personalChannelID, photos, "Trip")
	item := retryFiles[0]
	if item.Path != retryPath || item.ParentID != trip || item.RelativePath != "Photos/Trip/retry.txt" ||
		item.Size != int64(len("original bytes")) || item.ModifiedAt == "" || item.Code != "temporary_failure" {
		t.Fatalf("retry file = %+v, want original nested path, destination, and source metadata", item)
	}

	svc.TG = fake
	files, failures, err := svc.RetryImportedFiles(t.Context(), personalChannelID, retryFiles, false)
	if err != nil || len(files) != 1 || len(failures) != 0 || len(fake.SentFiles()) != 2 {
		t.Fatalf("retry files = %+v, failures = %+v, error = %v, total accepted = %d", files, failures, err, len(fake.SentFiles()))
	}
	if got := fileParentID(t, db, personalChannelID, "retry.txt"); got != trip {
		t.Fatalf("retry destination = %q, want %q", got, trip)
	}
	var downloaded bytes.Buffer
	sent := fake.SentFiles()[1]
	if err := fake.DownloadFile(t.Context(), sent.Peer, sent.MsgID, &downloaded, nil); err != nil || downloaded.String() != "original bytes" {
		t.Fatalf("retry bytes = %q, error = %v", downloaded.String(), err)
	}
}

func TestFolderImportReportsWindowThatFailedBeforeSending(t *testing.T) {
	svc, db, fake, _ := newTestService(t)
	svc.CreateFolder = testFolderCreator(db)
	svc.ActorID = func(context.Context) (int64, error) { return 0, errors.New("actor unavailable") }
	events := &importTestEventRecorder{}
	svc.Events = events
	root := filepath.Join(t.TempDir(), "Photos")
	mkfile(t, filepath.Join(root, "first.txt"), "one")
	mkfile(t, filepath.Join(root, "Trip", "second.txt"), "two")

	sources, err := svc.RunImportWithRetry(t.Context(), personalChannelID, []string{root}, "", false, false, "")
	if err != nil || len(fake.SentFiles()) != 0 || len(sources) != 2 {
		t.Fatalf("error=%v sent=%d retry=%+v, want both sources from a preflight failure", err, len(fake.SentFiles()), sources)
	}
	photos := folderIDByName(t, db, personalChannelID, "", "Photos")
	trip := folderIDByName(t, db, personalChannelID, photos, "Trip")
	wantParents := map[string]string{"Photos/first.txt": photos, "Photos/Trip/second.txt": trip}
	for _, source := range sources {
		if wantParents[source.RelativePath] != source.ParentID || source.Code != "not_sent" {
			t.Fatalf("retry source = %+v, want original parent and not_sent", source)
		}
		delete(wantParents, source.RelativePath)
	}
	payload, ok := events.lastPayload("import_upload_progress")
	if !ok || payload["failed"] != 2 {
		t.Fatalf("last progress event = %+v, want both failures recorded before completion", payload)
	}
}

func TestFolderImportDoesNotRetryUnknownAcceptance(t *testing.T) {
	svc, db, fake, _ := newTestService(t)
	svc.CreateFolder = testFolderCreator(db)
	svc.FloodWaitRetry = instantRetryPolicy()
	svc.TG = &batchFailureClient{Fake: fake, name: "uncertain.txt", accept: true, err: errors.Join(tgclient.ErrSendOutcomeUnknown, tgclient.ErrInjectedTransport)}
	root := filepath.Join(t.TempDir(), "Photos")
	mkfile(t, filepath.Join(root, "uncertain.txt"), "accepted bytes")

	retryFiles, err := svc.RunImportWithRetry(t.Context(), personalChannelID, []string{root}, "", false, false, "")
	if !errors.Is(err, tgclient.ErrSendOutcomeUnknown) || len(retryFiles) != 0 || len(fake.SentFiles()) != 1 {
		t.Fatalf("unknown acceptance = %+v, error = %v, accepted = %d", retryFiles, err, len(fake.SentFiles()))
	}
}

func TestImportRetryDoesNotRetainExtractedSources(t *testing.T) {
	svc, db, fake, _ := newTestService(t)
	svc.CreateFolder = testFolderCreator(db)
	svc.FloodWaitRetry = instantRetryPolicy()
	svc.TG = &batchFailureClient{Fake: fake, name: "archived.txt", err: tgclient.ErrInjectedTransport}
	archive := buildZip(t, map[string]string{"nested/archived.txt": "unpacked temporarily"}, nil, nil)

	sources, err := svc.RunImportWithRetry(t.Context(), personalChannelID, []string{archive}, "", false, true, "")
	if !errors.Is(err, tgclient.ErrInjectedTransport) || len(sources) != 0 {
		t.Fatalf("extracted sources = %+v, error = %v, want no retained temporary files", sources, err)
	}
}

func TestRetryImportedFilesRejectsReplacedSourcesAndKeepsIndices(t *testing.T) {
	svc, _, fake, _ := newTestService(t)
	root := t.TempDir()
	stamp := time.Unix(1_700_000_000, 0).UTC()
	retryFiles := make([]ImportRetryFile, 0, 3)
	for _, name := range []string{"changed.txt", "ok.txt", "missing.txt"} {
		sourcePath := filepath.Join(root, name)
		mkfile(t, sourcePath, "original")
		if err := os.Chtimes(sourcePath, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		retryFiles = append(retryFiles, ImportRetryFile{Path: sourcePath, Size: 8, ModifiedAt: "2023-11-14T22:13:20Z"})
	}
	changed := filepath.Join(root, "changed.txt")
	mkfile(t, changed, "modified")
	later := stamp.Add(time.Hour)
	if err := os.Chtimes(changed, later, later); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "missing.txt")); err != nil {
		t.Fatal(err)
	}

	files, failures, err := svc.RetryImportedFiles(t.Context(), personalChannelID, retryFiles, false)
	if err == nil || len(files) != 1 || len(failures) != 2 || len(fake.SentFiles()) != 1 {
		t.Fatalf("retry files = %+v, failures = %+v, error = %v, accepted = %d", files, failures, err, len(fake.SentFiles()))
	}
	if files[0].Name != "ok.txt" {
		t.Fatalf("retried %q, want only ok.txt", files[0].Name)
	}
	for i, code := range []string{"source_changed", "source_missing"} {
		if failure := failures[i]; failure.Index != i*2 || failure.Code != code || failure.Retryable {
			t.Fatalf("failure %d = %+v, want original index and code %q", i, failure, code)
		}
	}
}

func TestRetryImportedFilesKeepsOriginalSkipPolicy(t *testing.T) {
	svc, db, fake, _ := newTestService(t)
	svc.CreateFolder = testFolderCreator(db)
	svc.FloodWaitRetry = instantRetryPolicy()
	svc.TG = &batchFailureClient{Fake: fake, name: "same.txt", err: tgclient.ErrInjectedTransport}
	root := filepath.Join(t.TempDir(), "Photos")
	source := filepath.Join(root, "Trip", "same.txt")
	mkfile(t, source, "retry bytes")
	retryFiles, err := svc.RunImportWithRetry(t.Context(), personalChannelID, []string{root}, "", false, false, "skip")
	if !errors.Is(err, tgclient.ErrInjectedTransport) || len(retryFiles) != 1 || !retryFiles[0].SkipExisting {
		t.Fatalf("initial report = %+v, error = %v, want one failure with original skip policy", retryFiles, err)
	}
	svc.TG = fake
	original := writeTempNamedFile(t, "same.txt", []byte("existing remote file"))
	if _, err := svc.Upload(t.Context(), personalChannelID, []string{original}, []string{retryFiles[0].ParentID}, false); err != nil {
		t.Fatalf("upload colliding file into destination: %v", err)
	}

	files, failures, err := svc.RetryImportedFiles(t.Context(), personalChannelID, retryFiles, false)
	if err == nil || len(files) != 0 || len(failures) != 1 || len(fake.SentFiles()) != 1 {
		t.Fatalf("retry files = %+v, failures = %+v, error = %v, accepted = %d", files, failures, err, len(fake.SentFiles()))
	}
	if failure := failures[0]; failure.Code != "name_conflict" || failure.Retryable {
		t.Fatalf("conflict = %+v, want skipped collision", failure)
	}
}

func TestRetryImportedFilesCancelsAnInputWaitingInTheNextWindow(t *testing.T) {
	svc, _, fake, _ := newTestService(t)
	entered, release := make(chan struct{}, 2), make(chan struct{})
	svc.TG = &countingClient{Fake: fake, entered: entered, release: release}
	sources := retrySkipCandidates(t)

	type result struct {
		failures []UploadFailure
		err      error
	}
	ctx, cancel := context.WithCancel(t.Context())
	finished := make(chan struct{})
	var outcome result
	go func() {
		defer close(finished)
		_, outcome.failures, outcome.err = svc.RetryImportedFiles(ctx, personalChannelID, sources, false)
	}()
	defer func() {
		cancel()
		<-finished
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first window did not start")
	}
	canceled := svc.CancelUpload(1)
	close(release)
	<-finished

	if !canceled || outcome.err != nil || len(outcome.failures) != 1 || len(fake.SentFiles()) != 1 {
		t.Fatalf("canceled=%v, failures=%+v, error=%v, accepted=%d", canceled, outcome.failures, outcome.err, len(fake.SentFiles()))
	}
	if failure := outcome.failures[0]; failure.Index != 1 || failure.Code != "canceled" || failure.Retryable {
		t.Fatalf("failure = %+v, want canceled later input", failure)
	}
}

func TestRetryImportedFilesDoesNotSendCollidingFileAfterUnknownAcceptance(t *testing.T) {
	svc, _, fake, _ := newTestService(t)
	svc.FloodWaitRetry = instantRetryPolicy()
	svc.TG = &batchFailureClient{Fake: fake, name: "same.txt", accept: true, err: errors.Join(tgclient.ErrSendOutcomeUnknown, tgclient.ErrInjectedTransport)}

	_, failures, err := svc.RetryImportedFiles(t.Context(), personalChannelID, retrySkipCandidates(t), false)
	if !errors.Is(err, tgclient.ErrSendOutcomeUnknown) || len(failures) != 2 || len(fake.SentFiles()) != 1 {
		t.Fatalf("failures=%+v, error=%v, accepted=%d", failures, err, len(fake.SentFiles()))
	}
	if failure := failures[1]; failure.Index != 1 || failure.Retryable {
		t.Fatalf("later colliding failure = %+v, must wait for sync", failure)
	}
}

func retrySkipCandidates(t *testing.T) []ImportRetryFile {
	t.Helper()
	sources := make([]ImportRetryFile, 0, 2)
	for range 2 {
		name := writeTempNamedFile(t, "same.txt", []byte("original"))
		stamp := time.Unix(1_700_000_000, 0)
		if err := os.Chtimes(name, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		sources = append(sources, ImportRetryFile{Path: name, Size: 8, ModifiedAt: "2023-11-14T22:13:20Z", SkipExisting: true})
	}
	return sources
}

func TestFolderImportDropsEarlierRetryAfterMatchingSendIsUnconfirmed(t *testing.T) {
	svc, db, fake, _ := newTestService(t)
	svc.CreateFolder = testFolderCreator(db)
	svc.FloodWaitRetry = instantRetryPolicy()
	svc.TG = &batchFailureClient{Fake: fake, name: "same.txt", accept: true, err: tgclient.ErrSendOutcomeUnknown}
	first := filepath.Join(t.TempDir(), "Collection")
	second := filepath.Join(t.TempDir(), "Collection")
	unavailable := filepath.Join(first, "SAME.txt")
	mkfile(t, unavailable, "source disappears before upload")
	mkfile(t, filepath.Join(second, "same.txt"), "may already exist on Telegram")
	actorID := svc.ActorID
	var removeErr error
	removeOnce := sync.OnceFunc(func() { removeErr = os.Remove(unavailable) })
	svc.ActorID = func(ctx context.Context) (int64, error) {
		removeOnce()
		if removeErr != nil {
			return 0, removeErr
		}
		return actorID(ctx)
	}

	sources, err := svc.RunImportWithRetry(t.Context(), personalChannelID, []string{first, second}, "", false, false, "skip")
	if !errors.Is(err, tgclient.ErrSendOutcomeUnknown) || len(sources) != 0 || len(fake.SentFiles()) != 1 {
		t.Fatalf("retry sources=%+v, error=%v, accepted=%d", sources, err, len(fake.SentFiles()))
	}
}

type importRetryUnconfirmedClient struct{ *batchFailureClient }

func (c importRetryUnconfirmedClient) SendFileWithRandomID(ctx context.Context, peer tgclient.InputPeer, source io.Reader, name, caption string, size int64, progress func(int64, int64), randomID int64) (tgclient.SendFileResult, error) {
	if name == "SAME.txt" {
		return tgclient.SendFileResult{}, errors.Join(errUploadSource, fs.ErrPermission)
	}
	return c.batchFailureClient.SendFileWithRandomID(ctx, peer, source, name, caption, size, progress, randomID)
}

func TestRetryImportedFilesDropsEarlierRetryAfterMatchingSendIsUnconfirmed(t *testing.T) {
	svc, _, fake, _ := newTestService(t)
	svc.FloodWaitRetry = instantRetryPolicy()
	svc.TG = importRetryUnconfirmedClient{&batchFailureClient{Fake: fake, name: "same.txt", accept: true, err: tgclient.ErrSendOutcomeUnknown}}
	sources := retrySkipCandidates(t)
	upper := filepath.Join(filepath.Dir(sources[0].Path), "SAME.txt")
	if err := os.Rename(sources[0].Path, upper); err != nil {
		t.Fatal(err)
	}
	sources[0].Path = upper

	_, failures, err := svc.RetryImportedFiles(t.Context(), personalChannelID, sources, false)
	if !errors.Is(err, tgclient.ErrSendOutcomeUnknown) || len(failures) != 2 || len(fake.SentFiles()) != 1 {
		t.Fatalf("failures=%+v, error=%v, accepted=%d", failures, err, len(fake.SentFiles()))
	}
	if failure := failures[0]; failure.Index != 0 || failure.Code != "name_unconfirmed" || failure.Retryable {
		t.Fatalf("earlier colliding failure = %+v, must wait for sync", failure)
	}
}
