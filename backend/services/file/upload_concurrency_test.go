package file

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
)

func TestConcurrentMultipartUploadsRecoverIndependentlyAfterSharedFailures(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	svc.MaxUploadBytes = 1000
	svc.MaxConcurrentUploads = 3
	svc.FloodWaitRetry = instantRetryPolicy()

	expected := map[string][]byte{
		"alpha.bin":   bytes.Repeat([]byte("a"), 2501),
		"bravo.bin":   bytes.Repeat([]byte("b"), 2502),
		"charlie.bin": bytes.Repeat([]byte("c"), 2503),
	}
	paths := make([]string, 0, len(expected))
	parents := make([]string, 0, len(expected))
	for name, body := range expected {
		paths = append(paths, writeTempNamedFile(t, name, body))
		parents = append(parents, "")
	}
	// Model one shared connection loss surfacing through several in-flight
	// sends. Each logical upload must keep its own retry and multipart state.
	fakeTG.InjectTransientFailures(len(expected))

	files, err := svc.Upload(context.Background(), personalChannelID, paths, parents, false)
	if err != nil {
		t.Fatalf("concurrent multipart upload: %v", err)
	}
	if len(files) != len(expected) {
		t.Fatalf("uploaded files = %d, want %d", len(files), len(expected))
	}
	for _, file := range files {
		want, ok := expected[file.Name]
		if !ok {
			t.Fatalf("unexpected uploaded file %q", file.Name)
		}
		savePath := filepath.Join(t.TempDir(), file.Name)
		result := svc.Download(context.Background(), personalChannelID, file.MsgID, file.MsgID, func(string) (string, error) {
			return savePath, nil
		})
		if result.Status != "success" {
			t.Fatalf("download %q = %+v", file.Name, result)
		}
		got, err := os.ReadFile(savePath)
		if err != nil {
			t.Fatalf("read %q: %v", file.Name, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("round trip %q = %d bytes, want %d", file.Name, len(got), len(want))
		}
	}
}

func TestConcurrentUploadCallsShareConfiguredLimiter(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	svc.MaxConcurrentUploads = 1

	pathA := writeTempNamedFile(t, "a.txt", []byte("alpha"))
	pathB := writeTempNamedFile(t, "b.txt", []byte("bravo"))
	synctest.Test(t, func(t *testing.T) {
		entered := make(chan struct{}, 2)
		release := make(chan struct{})
		client := &countingClient{Fake: fakeTG, entered: entered, release: release}
		svc.TG = client
		errCh := make(chan error, 2)

		go func() {
			_, err := svc.Upload(context.Background(), personalChannelID, []string{pathA}, []string{""}, false)
			errCh <- err
		}()
		<-entered
		go func() {
			_, err := svc.Upload(context.Background(), personalChannelID, []string{pathB}, []string{""}, false)
			errCh <- err
		}()

		synctest.Wait()
		if calls, inFlight, maxSeen := client.snapshot(); calls != 1 || inFlight != 1 || maxSeen != 1 {
			t.Fatalf("while first upload is blocked calls/in-flight/max = %d/%d/%d, want 1/1/1", calls, inFlight, maxSeen)
		}

		close(release)
		synctest.Wait()
		for i := 0; i < 2; i++ {
			if err := <-errCh; err != nil {
				t.Fatalf("upload %d: %v", i, err)
			}
		}
		if calls, inFlight, maxSeen := client.snapshot(); calls != 2 || inFlight != 0 || maxSeen != 1 {
			t.Fatalf("final calls/in-flight/max = %d/%d/%d, want 2/0/1", calls, inFlight, maxSeen)
		}
	})
}

func TestUploadLimiterHonorsCancellationWhileQueued(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	svc.MaxConcurrentUploads = 1
	pathA := writeTempNamedFile(t, "active.txt", []byte("active"))
	pathB := writeTempNamedFile(t, "queued.txt", []byte("queued"))

	synctest.Test(t, func(t *testing.T) {
		entered := make(chan struct{}, 2)
		release := make(chan struct{})
		client := &countingClient{Fake: fakeTG, entered: entered, release: release}
		svc.TG = client
		firstErr := make(chan error, 1)
		go func() {
			_, err := svc.Upload(context.Background(), personalChannelID, []string{pathA}, []string{""}, false)
			firstErr <- err
		}()
		<-entered

		queuedCtx, cancelQueued := context.WithCancel(context.Background())
		queuedErr := make(chan error, 1)
		go func() {
			_, err := svc.Upload(queuedCtx, personalChannelID, []string{pathB}, []string{""}, false)
			queuedErr <- err
		}()
		cancelQueued()
		synctest.Wait()
		if err := <-queuedErr; !errors.Is(err, context.Canceled) {
			t.Fatalf("queued upload error = %v, want context canceled", err)
		}
		if calls, inFlight, maxSeen := client.snapshot(); calls != 1 || inFlight != 1 || maxSeen != 1 {
			t.Fatalf("after queued cancellation calls/in-flight/max = %d/%d/%d, want 1/1/1", calls, inFlight, maxSeen)
		}

		close(release)
		synctest.Wait()
		if err := <-firstErr; err != nil {
			t.Fatalf("active upload: %v", err)
		}
	})
}

func TestVisibleAndHiddenUploadsShareConfiguredLimiter(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	svc.MaxConcurrentUploads = 1

	visiblePath := writeTempNamedFile(t, "visible.txt", []byte("visible"))
	hiddenBody := []byte("hidden")
	synctest.Test(t, func(t *testing.T) {
		entered := make(chan struct{}, 2)
		release := make(chan struct{})
		client := &countingClient{Fake: fakeTG, entered: entered, release: release}
		svc.TG = client
		errCh := make(chan error, 2)

		go func() {
			_, err := svc.Upload(context.Background(), personalChannelID, []string{visiblePath}, []string{""}, false)
			errCh <- err
		}()
		<-entered
		go func() {
			_, err := svc.UploadHidden(
				context.Background(),
				personalChannelID,
				HiddenUploadRequest{
					OperationID:   "global-upload-limit-hidden",
					Name:          "hidden.txt",
					StoredSize:    int64(len(hiddenBody)),
					PlaintextSize: int64(len(hiddenBody)),
				},
				bytes.NewReader(hiddenBody),
			)
			errCh <- err
		}()

		synctest.Wait()
		if calls, inFlight, maxSeen := client.snapshot(); calls != 1 || inFlight != 1 || maxSeen != 1 {
			t.Fatalf("while visible upload is blocked calls/in-flight/max = %d/%d/%d, want 1/1/1", calls, inFlight, maxSeen)
		}

		close(release)
		synctest.Wait()
		for i := 0; i < 2; i++ {
			if err := <-errCh; err != nil {
				t.Fatalf("upload %d: %v", i, err)
			}
		}
		if calls, inFlight, maxSeen := client.snapshot(); calls != 2 || inFlight != 0 || maxSeen != 1 {
			t.Fatalf("final calls/in-flight/max = %d/%d/%d, want 2/0/1", calls, inFlight, maxSeen)
		}
	})
}

func TestUploadConcurrencyClamp(t *testing.T) {
	for _, tc := range []struct {
		configured int
		want       int
	}{
		{configured: 0, want: 3},
		{configured: -2, want: 3},
		{configured: 1, want: 1},
		{configured: 5, want: 5},
		{configured: 99, want: 8},
	} {
		svc := &Service{MaxConcurrentUploads: tc.configured}
		if got := svc.uploadConcurrency(); got != tc.want {
			t.Fatalf("uploadConcurrency(%d) = %d, want %d", tc.configured, got, tc.want)
		}
	}
}
