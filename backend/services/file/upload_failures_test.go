package file

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"testing"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

type batchFailureClient struct {
	*tgclient.Fake
	name   string
	err    error
	accept bool
}

func (c *batchFailureClient) SendFileWithRandomID(ctx context.Context, peer tgclient.InputPeer, source io.Reader, name, caption string, size int64, progress func(int64, int64), randomID int64) (tgclient.SendFileResult, error) {
	if name != c.name {
		return c.Fake.SendFileWithRandomID(ctx, peer, source, name, caption, size, progress, randomID)
	}
	if c.accept {
		if _, err := c.Fake.SendFileWithRandomID(ctx, peer, source, name, caption, size, progress, randomID); err != nil {
			return tgclient.SendFileResult{}, err
		}
	}
	return tgclient.SendFileResult{}, c.err
}

func TestUploadWithFailuresPartialBatch(t *testing.T) {
	svc, db, fake, _ := newTestService(t)
	svc.FloodWaitRetry = instantRetryPolicy()
	svc.TG = &batchFailureClient{Fake: fake, name: "failed.txt", err: tgclient.ErrInjectedTransport}
	paths := []string{writeTempNamedFile(t, "same.txt", []byte("first")), writeTempNamedFile(t, "failed.txt", []byte("failed")), writeTempNamedFile(t, "same.txt", []byte("last"))}
	files, failures, err := svc.UploadWithFailures(t.Context(), personalChannelID, paths, []string{"", "", ""}, false)
	if err == nil || len(files) != 2 || len(failures) != 1 {
		t.Fatalf("files=%v failures=%v error=%v, want two successes and one failure", files, failures, err)
	}
	if f := failures[0]; f.Index != 1 || f.Name != "failed.txt" || !f.Retryable || f.Code != "temporary_failure" {
		t.Fatalf("failure = %+v, want retryable second input", f)
	}
	for _, file := range files {
		if !projection.FileExists(db, personalChannelID, int64(file.MsgID)) {
			t.Fatalf("accepted file %d missing projection", file.MsgID)
		}
	}
	svc.TG = fake
	_, retryFailures, err := svc.UploadWithFailures(t.Context(), personalChannelID, []string{paths[failures[0].Index]}, []string{""}, false)
	if err != nil || len(retryFailures) != 0 || len(fake.SentFiles()) != 3 {
		t.Fatalf("retry failures=%v error=%v accepted=%d, want one additional accepted file", retryFailures, err, len(fake.SentFiles()))
	}
}

func TestUploadWithFailuresUnknownAcceptanceCannotRestart(t *testing.T) {
	svc, _, fake, _ := newTestService(t)
	svc.FloodWaitRetry = instantRetryPolicy()
	svc.TG = &batchFailureClient{Fake: fake, name: "lost.txt", accept: true, err: errors.Join(tgclient.ErrSendOutcomeUnknown, tgclient.ErrInjectedTransport)}
	_, failures, err := svc.UploadWithFailures(t.Context(), personalChannelID, []string{writeTempNamedFile(t, "lost.txt", []byte("accepted"))}, []string{""}, false)
	if !errors.Is(err, tgclient.ErrSendOutcomeUnknown) || len(failures) != 1 || failures[0].Retryable || failures[0].Code != "outcome_unknown" || len(fake.SentFiles()) != 1 {
		t.Fatalf("failures=%v error=%v accepted=%d, want one uncertain accepted file excluded from retry", failures, err, len(fake.SentFiles()))
	}
}

func TestUploadWithFailuresProjectionPendingCannotRestart(t *testing.T) {
	svc, db, fake, _ := newTestService(t)
	if _, err := db.Exec(`CREATE TRIGGER reject_upload BEFORE INSERT ON files BEGIN SELECT RAISE(ABORT, 'index unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	_, failures, err := svc.UploadWithFailures(t.Context(), personalChannelID, []string{writeTempNamedFile(t, "accepted.txt", []byte("accepted"))}, []string{""}, false)
	if !errors.Is(err, errUploadProjection) || len(failures) != 1 || failures[0].Retryable || failures[0].Code != "projection_pending" || len(fake.SentFiles()) != 1 {
		t.Fatalf("failures=%v error=%v accepted=%d, want accepted file excluded from resend", failures, err, len(fake.SentFiles()))
	}
}

func TestUploadWithFailuresCancellation(t *testing.T) {
	svc, _, fake, _ := newTestService(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, failures, err := svc.UploadWithFailures(ctx, personalChannelID, []string{writeTempNamedFile(t, "cancel.txt", []byte("cancel"))}, []string{""}, false)
	if !errors.Is(err, context.Canceled) || len(failures) != 1 || failures[0].Retryable || failures[0].Code != "canceled" || len(fake.SentFiles()) != 0 {
		t.Fatalf("failures=%v error=%v accepted=%d, want canceled file excluded", failures, err, len(fake.SentFiles()))
	}
}

func TestUploadWithFailuresRetainsAllInputIndices(t *testing.T) {
	svc, _, fake, _ := newTestService(t)
	paths := make([]string, 5)
	parents := make([]string, len(paths))
	for i := range paths {
		paths[i] = filepath.Join(t.TempDir(), "missing.txt")
	}
	_, failures, err := svc.UploadWithFailures(t.Context(), personalChannelID, paths, parents, false)
	if err == nil || len(failures) != len(paths) || len(fake.SentFiles()) != 0 {
		t.Fatalf("failures=%v error=%v, want every missing input and no sends", failures, err)
	}
	for index, failure := range failures {
		if failure.Index != index || failure.Name != "missing.txt" || failure.Code != "source_unavailable" || !failure.Retryable {
			t.Fatalf("input %d = %+v, want ordered safe source failure", index, failure)
		}
	}
}

func TestUploadFailureEligibility(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		code      string
		retryable bool
	}{
		{"internal cancellation", fmt.Errorf("engine forcibly closed: %w", context.Canceled), "temporary_failure", true},
		{"caller cancellation", context.Canceled, "canceled", false},
		{"durable job", errors.Join(errUploadResumable, tgclient.ErrInjectedTransport), "resumable_upload", false},
		{"unknown canceled receipt", errors.Join(tgclient.ErrSendOutcomeUnknown, context.Canceled), "outcome_unknown", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failure := classifyUploadFailure(t.Context(), 1, "file.txt", tc.err)
			if failure.Code != tc.code || failure.Retryable != tc.retryable {
				t.Fatalf("failure=%+v, want code %q retryable %v", failure, tc.code, tc.retryable)
			}
		})
	}
}

func TestUploadWithFailuresKeepsDurableJobRecovery(t *testing.T) {
	svc, db, fake, _ := newTestService(t)
	svc.MaxUploadBytes = 1000
	svc.FloodWaitRetry = instantRetryPolicy()
	fake.InjectTransientFailures(20)
	_, failures, err := svc.UploadWithFailures(t.Context(), personalChannelID, []string{writeTempNamedFile(t, "large.bin", bigBody(1500))}, []string{""}, false)
	if err == nil || len(failures) != 1 || failures[0].Retryable || failures[0].Code != "resumable_upload" {
		t.Fatalf("failures=%v error=%v, want durable recovery excluded from fresh retry", failures, err)
	}
	var jobs int
	if err := db.QueryRow(`SELECT COUNT(*) FROM resumable_uploads`).Scan(&jobs); err != nil || jobs != 1 {
		t.Fatalf("jobs=%d error=%v, want one retained resumable job", jobs, err)
	}
}

func TestUploadWithFailuresPreflightReportsNothingSent(t *testing.T) {
	svc, _, fake, _ := newTestService(t)
	svc.ActorID = func(context.Context) (int64, error) { return 0, errors.New("actor unavailable") }
	_, failures, err := svc.UploadWithFailures(t.Context(), personalChannelID, []string{"first.txt", "second.txt"}, []string{"", ""}, false)
	if err == nil || len(failures) != 2 || len(fake.SentFiles()) != 0 {
		t.Fatalf("failures=%v error=%v, want all preflight outcomes and no sends", failures, err)
	}
	for index, failure := range failures {
		if failure.Index != index || failure.Code != "not_sent" || !failure.Retryable {
			t.Fatalf("preflight failure %+v, want ordered safe not_sent", failure)
		}
	}
}

func TestUploadWithFailuresRefusesEmptyPlaintextBeforeSending(t *testing.T) {
	svc, _, fake, _ := newTestService(t)
	paths := []string{writeTempNamedFile(t, "empty.toc", nil), writeTempNamedFile(t, "notes.txt", []byte("hi"))}
	files, failures, err := svc.UploadWithFailures(t.Context(), personalChannelID, paths, []string{"", ""}, false)
	if err == nil || len(files) != 1 || len(failures) != 1 {
		t.Fatalf("files=%v failures=%v error=%v, want one success and one failure", files, failures, err)
	}
	if f := failures[0]; f.Index != 0 || f.Code != "empty_file" || f.Retryable {
		t.Fatalf("failure = %+v, want a non-retryable empty_file for the first input", f)
	}
	if sent := fake.SentFiles(); len(sent) != 1 {
		t.Fatalf("sent files = %d, want only the non-empty file", len(sent))
	}
}
