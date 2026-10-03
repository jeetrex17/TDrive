package file

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

// bigBody returns deterministic, non-repeating-ish bytes of length n so a
// reassembly mistake (wrong order, dropped/duplicated part) shows up.
func bigBody(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte((i*31 + 7) % 251)
	}
	return b
}

func TestMultipartRoundTripPlain(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	svc.MaxUploadBytes = 1000 // force splitting above 1000 stored bytes

	body := bigBody(3503) // -> 4 parts (1000,1000,1000,503)
	path := writeTempNamedFile(t, "movie.bin", body)
	files, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, false)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("uploaded files = %d, want 1", len(files))
	}

	parts, err := projection.MultipartParts(db, personalChannelID, int64(files[0].MsgID))
	if err != nil {
		t.Fatalf("MultipartParts: %v", err)
	}
	if len(parts) != 4 {
		t.Fatalf("parts = %d, want 4", len(parts))
	}

	// Each part's Telegram attachment should show the original filename
	// (suffixed for order, since 4 messages share it), not "part-00000".
	sent := fakeTG.SentFiles()
	if len(sent) != 4 {
		t.Fatalf("sent files = %+v, want 4", sent)
	}
	for i, want := range []string{"movie.bin.part0", "movie.bin.part1", "movie.bin.part2", "movie.bin.part3"} {
		if sent[i].Name != want {
			t.Fatalf("part %d attachment name = %q, want %q", i, sent[i].Name, want)
		}
	}

	savePath := filepath.Join(t.TempDir(), "out.bin")
	result := svc.Download(context.Background(), personalChannelID, files[0].MsgID, files[0].MsgID, func(string) (string, error) {
		return savePath, nil
	})
	if result.Status != "success" {
		t.Fatalf("download = %+v", result)
	}
	got, err := os.ReadFile(savePath)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("round-trip mismatch: got %d bytes, want %d", len(got), len(body))
	}
}

func restartedUploadService(previous *Service) *Service {
	return &Service{
		DB: previous.DB, TG: previous.TG, Peers: previous.Peers,
		ActorID: previous.ActorID, Now: previous.Now,
		CacheNamespace: previous.CacheNamespace, MaxUploadBytes: previous.MaxUploadBytes,
	}
}

func TestMultipartResumeReconcilesAcceptedPartAfterRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "upload.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := projection.MigratePersonalChannel(db, personalChannelID); err != nil {
		t.Fatal(err)
	}
	fakeTG := tgclient.NewFake(7)
	peer := tgclient.InputPeer{ChannelID: personalChannelID, AccessHash: 99}
	fakeTG.SeedChannel(peer, "Personal")
	svc := &Service{DB: db, TG: fakeTG, Peers: testPeerResolver{peer: peer},
		ActorID: func(context.Context) (int64, error) { return 7, nil }}
	svc.MaxUploadBytes = 1000
	path := writeTempNamedFile(t, "movie.bin", bigBody(2500))
	svc.afterVisiblePartSend = func(index int, _ int64) {
		if index == 1 {
			panic("simulated crash before part projection")
		}
	}
	if _, err := svc.Upload(t.Context(), personalChannelID, []string{path}, []string{""}, false); err == nil {
		t.Fatal("upload unexpectedly survived injected crash")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	reopened.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = reopened.Close() })
	restarted := restartedUploadService(svc)
	restarted.DB = reopened
	jobs, err := restarted.ListResumableUploads(t.Context(), personalChannelID)
	if err != nil || len(jobs) != 1 || jobs[0].Status != resumePaused || jobs[0].ConfirmedBytes != 1000 {
		t.Fatalf("recovered jobs = %+v, err %v", jobs, err)
	}
	meta, err := restarted.ResumeUpload(t.Context(), personalChannelID, jobs[0].JobID, "")
	if err != nil || meta.MsgID == 0 {
		t.Fatalf("resume metadata = %+v, err %v", meta, err)
	}
	if got := len(fakeTG.SentFiles()); got != 3 {
		t.Fatalf("Telegram part sends = %d, want exactly 3", got)
	}
	remaining, err := restarted.ListResumableUploads(t.Context(), personalChannelID)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("unfinished jobs after resume = %+v, err %v", remaining, err)
	}
}

func TestMultipartResumeWaitsForNetwork(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	svc.MaxUploadBytes = 1000
	policy := instantRetryPolicy()
	policy.MaxTransientRetries = 1
	svc.FloodWaitRetry = policy
	path := writeTempNamedFile(t, "movie.bin", bigBody(2500))
	fakeTG.InjectTransientFailures(2)
	if _, err := svc.Upload(t.Context(), personalChannelID, []string{path}, []string{""}, false); err == nil {
		t.Fatal("upload succeeded after transport retry budget was exhausted")
	}
	restarted := restartedUploadService(svc)
	jobs, err := restarted.ListResumableUploads(t.Context(), personalChannelID)
	if err != nil || len(jobs) != 1 || jobs[0].Status != resumeWaitingNetwork {
		t.Fatalf("network-waiting jobs = %+v, err %v", jobs, err)
	}
	if got := len(fakeTG.SentFiles()); got != 0 {
		t.Fatalf("part sends before reconnect = %d, want 0", got)
	}
	restarted.afterVisiblePartSend = func(index int, _ int64) {
		if index != 0 {
			return
		}
		if _, err := restarted.ResumeUpload(t.Context(), personalChannelID, jobs[0].JobID, ""); err == nil {
			t.Fatal("concurrent resume claimed the same upload")
		}
	}
	if _, err := restarted.ResumeUpload(t.Context(), personalChannelID, jobs[0].JobID, ""); err != nil {
		t.Fatalf("resume after reconnect: %v", err)
	}
	if got := len(fakeTG.SentFiles()); got != 3 {
		t.Fatalf("part sends after reconnect = %d, want 3", got)
	}
}

type interruptedPartVerificationClient struct {
	*visibleAcceptThenLoseReceiptClient
}

func (c *interruptedPartVerificationClient) DownloadFile(context.Context, tgclient.InputPeer, int64, io.Writer, func(int64, int64)) error {
	return tgclient.ErrInjectedTransport
}

func TestMultipartResumeReconcilesAcceptedPartAfterNetworkLoss(t *testing.T) {
	for _, verifyInterrupted := range []bool{false, true} {
		t.Run(fmt.Sprintf("verification_interrupted=%v", verifyInterrupted), func(t *testing.T) {
			svc, _, fakeTG, _ := newTestService(t)
			svc.MaxUploadBytes = 1000
			policy := instantRetryPolicy()
			policy.MaxTransientRetries = 0
			if verifyInterrupted {
				policy.MaxTransientRetries = 1
			}
			svc.FloodWaitRetry = policy
			client := &visibleAcceptThenLoseReceiptClient{Fake: fakeTG, fileFailAt: 1}
			if verifyInterrupted {
				svc.TG = &interruptedPartVerificationClient{client}
			} else {
				svc.TG = client
			}
			path := writeTempNamedFile(t, "movie.bin", bigBody(2500))
			if _, err := svc.Upload(t.Context(), personalChannelID, []string{path}, []string{""}, false); err == nil {
				t.Fatal("lost receipt was reported as a completed upload")
			}
			restarted := restartedUploadService(svc)
			restarted.TG = fakeTG
			jobs, err := restarted.ListResumableUploads(t.Context(), personalChannelID)
			if err != nil || len(jobs) != 1 || jobs[0].Status != resumeWaitingNetwork {
				t.Fatalf("network-waiting jobs = %+v, err %v", jobs, err)
			}
			if _, err := restarted.ResumeUpload(t.Context(), personalChannelID, jobs[0].JobID, ""); err != nil {
				t.Fatalf("resume accepted part: %v", err)
			}
			if got := len(fakeTG.SentFiles()); got != 3 {
				t.Fatalf("part sends after reconnect = %d, want exactly 3", got)
			}
		})
	}
}

func TestMultipartManualPauseStaysPaused(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	svc.MaxUploadBytes = 1000
	path := writeTempNamedFile(t, "movie.bin", bigBody(2500))
	svc.afterVisiblePartSend = func(index int, _ int64) {
		if index != 0 {
			return
		}
		jobs, err := svc.ListResumableUploads(t.Context(), personalChannelID)
		if err != nil || len(jobs) != 1 || !svc.PauseUpload(personalChannelID, jobs[0].JobID) {
			t.Fatalf("pause active upload: jobs=%+v, err=%v", jobs, err)
		}
	}
	if _, err := svc.Upload(t.Context(), personalChannelID, []string{path}, []string{""}, false); err == nil {
		t.Fatal("paused upload reported success")
	}
	jobs, err := svc.ListResumableUploads(t.Context(), personalChannelID)
	if err != nil || len(jobs) != 1 || jobs[0].Status != resumePaused {
		t.Fatalf("paused jobs = %+v, err %v", jobs, err)
	}
	if got := len(fakeTG.SentFiles()); got != 1 {
		t.Fatalf("part sends before explicit resume = %d, want 1", got)
	}
	restarted := restartedUploadService(svc)
	if _, err := restarted.ResumeUpload(t.Context(), personalChannelID, jobs[0].JobID, ""); err != nil {
		t.Fatalf("explicit resume after pause: %v", err)
	}
	if got := len(fakeTG.SentFiles()); got != 3 {
		t.Fatalf("part sends after pause and resume = %d, want exactly 3", got)
	}
}

func TestMultipartResumeRejectsChangedSource(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	svc.MaxUploadBytes = 1000
	path := writeTempNamedFile(t, "movie.bin", bigBody(2500))
	svc.afterVisiblePartSend = func(index int, _ int64) {
		if index == 0 {
			panic("simulated crash")
		}
	}
	_, _ = svc.Upload(t.Context(), personalChannelID, []string{path}, []string{""}, false)
	restarted := restartedUploadService(svc)
	jobs, err := restarted.ListResumableUploads(t.Context(), personalChannelID)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("list jobs = %+v, err %v", jobs, err)
	}
	changed := bigBody(2500)
	changed[1500] ^= 0xff
	if err := os.WriteFile(path, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.ResumeUpload(t.Context(), personalChannelID, jobs[0].JobID, ""); err == nil {
		t.Fatal("changed source was accepted")
	}
	jobs, err = restarted.ListResumableUploads(t.Context(), personalChannelID)
	if err != nil || len(jobs) != 1 || jobs[0].Status != resumeNeedsFile {
		t.Fatalf("changed-source job = %+v, err %v", jobs, err)
	}
	if got := len(fakeTG.SentFiles()); got != 1 {
		t.Fatalf("Telegram part sends after source mutation = %d, want 1", got)
	}
	// A deleted remote checkpoint must never lead to a manifest, and cleanup
	// can finish even when the remote message is already gone.
	partID := fakeTG.SentFiles()[0].MsgID
	if err := fakeTG.DeleteMessages(t.Context(), tgclient.InputPeer{ChannelID: personalChannelID, AccessHash: 99}, []int64{partID}); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.ResumeUpload(t.Context(), personalChannelID, jobs[0].JobID, ""); err == nil {
		t.Fatal("missing remote checkpoint was accepted")
	}
	if _, err := db.Exec(`UPDATE resumable_uploads SET status = ? WHERE job_id = ?`, resumeCanceling, jobs[0].JobID); err != nil {
		t.Fatal(err)
	}
	otherID, err := fakeTG.SendControl(t.Context(), tgclient.InputPeer{ChannelID: personalChannelID, AccessHash: 99}, "unrelated", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE file_parts SET msg_id = ? WHERE upload_uuid = ?`, otherID, jobs[0].JobID); err != nil {
		t.Fatal(err)
	}
	if err := restarted.CancelResumableUpload(t.Context(), personalChannelID, jobs[0].JobID); err == nil {
		t.Fatal("tampered receipt could delete unrelated message")
	}
	if _, err := db.Exec(`UPDATE file_parts SET msg_id = ? WHERE upload_uuid = ?`, partID, jobs[0].JobID); err != nil {
		t.Fatal(err)
	}
	if err := restarted.CancelResumableUpload(t.Context(), personalChannelID, jobs[0].JobID); err != nil {
		t.Fatalf("retry cleanup after partial deletion: %v", err)
	}
}

type changingSourceClient struct {
	*tgclient.Fake
	path     string
	original []byte
	changed  bool
}

func (c *changingSourceClient) SendFileWithRandomID(ctx context.Context, peer tgclient.InputPeer, r io.Reader, name, caption string, size int64, progress func(int64, int64), randomID int64) (tgclient.SendFileResult, error) {
	if !c.changed {
		c.changed = true
		mutated := append([]byte(nil), c.original...)
		mutated[100] ^= 0xff
		if err := os.WriteFile(c.path, mutated, 0o600); err != nil {
			return tgclient.SendFileResult{}, err
		}
		defer func() { _ = os.WriteFile(c.path, c.original, 0o600) }()
	}
	return c.Fake.SendFileWithRandomID(ctx, peer, r, name, caption, size, progress, randomID)
}

func TestMultipartResumeInvalidatesBytesChangedDuringSend(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	svc.MaxUploadBytes = 1000
	original := bigBody(2500)
	path := writeTempNamedFile(t, "movie.bin", original)
	svc.TG = &changingSourceClient{Fake: fakeTG, path: path, original: original}
	if _, err := svc.Upload(t.Context(), personalChannelID, []string{path}, []string{""}, false); err == nil {
		t.Fatal("upload with mutated sent bytes succeeded")
	}
	jobs, err := svc.ListResumableUploads(t.Context(), personalChannelID)
	if err != nil || len(jobs) != 1 || jobs[0].Status != resumeRestartRequired {
		t.Fatalf("invalidated upload = %+v, err %v", jobs, err)
	}
	if _, err := svc.ResumeUpload(t.Context(), personalChannelID, jobs[0].JobID, ""); err == nil {
		t.Fatal("invalidated upload reused corrupted remote bytes")
	}
	if got := len(fakeTG.SentControls()); got != 0 {
		t.Fatalf("manifest sends = %d, want 0", got)
	}
	if err := svc.CancelResumableUpload(t.Context(), personalChannelID, jobs[0].JobID); err != nil {
		t.Fatalf("discard invalidated upload: %v", err)
	}
	if got := len(fakeTG.SentFiles()); got != 1 {
		t.Fatalf("part sends = %d, want 1", got)
	}
}

type acceptedBadThenRetriedClient struct {
	*tgclient.Fake
	path     string
	original []byte
	calls    int
}

func (c *acceptedBadThenRetriedClient) SendFileWithRandomID(ctx context.Context, peer tgclient.InputPeer, r io.Reader, name, caption string, size int64, progress func(int64, int64), randomID int64) (tgclient.SendFileResult, error) {
	c.calls++
	if c.calls == 1 {
		bad := append([]byte(nil), c.original...)
		bad[100] ^= 0xff
		if err := os.WriteFile(c.path, bad, 0o600); err != nil {
			return tgclient.SendFileResult{}, err
		}
		defer func() { _ = os.WriteFile(c.path, c.original, 0o600) }()
		if _, err := c.Fake.SendFileWithRandomID(ctx, peer, r, name, caption, size, progress, randomID); err != nil {
			return tgclient.SendFileResult{}, err
		}
		return tgclient.SendFileResult{}, errors.Join(tgclient.ErrSendOutcomeUnknown, tgclient.ErrInjectedTransport)
	}
	// The retry consumes the correct source but Telegram returns the message
	// accepted on attempt one for this stable random ID.
	if _, err := io.Copy(io.Discard, r); err != nil {
		return tgclient.SendFileResult{}, err
	}
	return c.Fake.SendFileWithRandomID(ctx, peer, nil, name, caption, size, progress, randomID)
}

func TestMultipartRetryVerifiesAcceptedBytesNotLastAttempt(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	svc.MaxUploadBytes = 1000
	svc.FloodWaitRetry = instantRetryPolicy()
	original := bigBody(2500)
	path := writeTempNamedFile(t, "movie.bin", original)
	client := &acceptedBadThenRetriedClient{Fake: fakeTG, path: path, original: original}
	svc.TG = client
	if _, err := svc.Upload(t.Context(), personalChannelID, []string{path}, []string{""}, false); err == nil {
		t.Fatal("accepted wrong bytes were trusted after good retry")
	}
	if client.calls < 2 {
		t.Fatalf("send calls = %d, want a retry", client.calls)
	}
	jobs, err := svc.ListResumableUploads(t.Context(), personalChannelID)
	if err != nil || len(jobs) != 1 || jobs[0].Status != resumeRestartRequired {
		t.Fatalf("job after bad accepted retry = %+v, err %v", jobs, err)
	}
	if got := len(fakeTG.SentControls()); got != 0 {
		t.Fatalf("manifest sends = %d, want 0", got)
	}
	// A stray manifest must block deletion without making the invalidated
	// multipart file visible as a side effect of cleanup reconciliation.
	job, err := svc.loadUploadJob(t.Context(), jobs[0].JobID)
	if err != nil {
		t.Fatal(err)
	}
	peer := tgclient.InputPeer{ChannelID: personalChannelID, AccessHash: 99}
	plan := uploadPartPlan{partSize: job.PartSize, partCount: job.PartCount}
	for i := 1; i < job.PartCount; i++ {
		offset, length, err := plan.window(job.Size, i)
		if err != nil {
			t.Fatal(err)
		}
		op := projection.Op{Type: projection.OpFilePart, UploadUUID: job.ID, PartIndex: i, FileSize: length}
		randomID, err := tgclient.StableRandomID(job.ID, fmt.Sprintf("part:%d", i))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fakeTG.SendFileWithRandomID(t.Context(), peer, bytes.NewReader(original[offset:offset+length]),
			partAttachmentName(job.Name, i, job.PartCount), projection.Format(op), length, nil, randomID); err != nil {
			t.Fatal(err)
		}
	}
	manifestID, err := fakeTG.SendControl(t.Context(), peer, projection.Format(uploadJobManifest(job)), true)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.CancelResumableUpload(t.Context(), personalChannelID, job.ID); err == nil {
		t.Fatal("published manifest did not block cleanup")
	}
	if projection.FileExists(db, personalChannelID, manifestID) {
		t.Fatal("invalidated manifest became visible during cleanup check")
	}
}

type anonymousUploadHistoryClient struct {
	*tgclient.Fake
	outgoing bool
}

func (c *anonymousUploadHistoryClient) GetHistory(ctx context.Context, peer tgclient.InputPeer, minID, offsetID int64, limit int) ([]tgclient.HistoryMessage, error) {
	page, err := c.Fake.GetHistory(ctx, peer, minID, offsetID, limit)
	if err != nil {
		return nil, err
	}
	for i := range page {
		if page[i].HasMedia {
			page[i].FromID = 0
			page[i].Outgoing = c.outgoing
		}
	}
	return page, nil
}

func TestMultipartCancelChecksAnonymousSenderOwnership(t *testing.T) {
	for _, outgoing := range []bool{false, true} {
		t.Run(fmt.Sprint("outgoing=", outgoing), func(t *testing.T) {
			svc, _, fakeTG, _ := newTestService(t)
			svc.MaxUploadBytes = 1000
			path := writeTempNamedFile(t, "movie.bin", bigBody(2500))
			svc.afterVisiblePartSend = func(index int, _ int64) {
				if index == 0 {
					panic("simulated crash")
				}
			}
			_, _ = svc.Upload(t.Context(), personalChannelID, []string{path}, []string{""}, false)
			restarted := restartedUploadService(svc)
			restarted.TG = &anonymousUploadHistoryClient{Fake: fakeTG, outgoing: outgoing}
			jobs, err := restarted.ListResumableUploads(t.Context(), personalChannelID)
			if err != nil || len(jobs) != 1 {
				t.Fatalf("jobs = %+v, err %v", jobs, err)
			}
			err = restarted.CancelResumableUpload(t.Context(), personalChannelID, jobs[0].JobID)
			if outgoing && err != nil {
				t.Fatalf("own anonymous part was not canceled: %v", err)
			}
			if !outgoing && err == nil {
				t.Fatal("unowned anonymous part was deleted")
			}
		})
	}
}

type uncertainManifestClient struct {
	*tgclient.Fake
	accepted bool
	canceled bool
}

func (c *uncertainManifestClient) SendControlWithRandomID(ctx context.Context, peer tgclient.InputPeer, text string, silent bool, randomID int64) (int64, error) {
	if c.accepted {
		_, _ = c.Fake.SendControlWithRandomID(ctx, peer, text, silent, randomID)
	}
	if c.canceled {
		return 0, context.Canceled
	}
	return 0, fmt.Errorf("%w: receipt lost", tgclient.ErrSendOutcomeUnknown)
}

func TestMultipartResumeReconcilesOrRetriesManifest(t *testing.T) {
	for _, test := range []struct {
		accepted bool
		canceled bool
	}{{false, false}, {true, false}, {true, true}} {
		t.Run(fmt.Sprintf("accepted=%v/canceled=%v", test.accepted, test.canceled), func(t *testing.T) {
			svc, _, fakeTG, _ := newTestService(t)
			svc.MaxUploadBytes = 1000
			svc.TG = &uncertainManifestClient{Fake: fakeTG, accepted: test.accepted, canceled: test.canceled}
			path := writeTempNamedFile(t, "movie.bin", bigBody(2500))
			if _, err := svc.Upload(t.Context(), personalChannelID, []string{path}, []string{""}, false); err == nil {
				t.Fatal("unknown manifest result reported success")
			}
			restarted := restartedUploadService(svc)
			restarted.TG = fakeTG
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			jobs, err := restarted.ListResumableUploads(t.Context(), personalChannelID)
			if err != nil || len(jobs) != 1 || jobs[0].Status != resumeManifestUncertain {
				t.Fatalf("uncertain job = %+v, err %v", jobs, err)
			}
			if !test.accepted {
				fakeTG.InjectReadFloodWaits(1)
				if _, err := restarted.ResumeUpload(t.Context(), personalChannelID, jobs[0].JobID, ""); err == nil {
					t.Fatal("failed history check was accepted")
				}
				jobs, err = restarted.ListResumableUploads(t.Context(), personalChannelID)
				if err != nil || len(jobs) != 1 || jobs[0].Status != resumeManifestUncertain {
					t.Fatalf("manifest uncertainty downgraded after history error: %+v, %v", jobs, err)
				}
			}
			_, err = restarted.ResumeUpload(t.Context(), personalChannelID, jobs[0].JobID, "")
			if err != nil {
				t.Fatalf("manifest not resolved without source: %v", err)
			}
			wantControls := 1
			if got := len(fakeTG.SentControls()); got != wantControls {
				t.Fatalf("manifest sends = %d, want %d", got, wantControls)
			}
		})
	}
}

func TestMultipartRoundTripEncrypted(t *testing.T) {
	svc, db, _, _ := newTestService(t)
	svc.MaxUploadBytes = 1000
	masterKey := bytes.Repeat([]byte{5}, 32)
	wireEncryption(svc, masterKey)

	body := bigBody(5000) // ciphertext ~5066 -> 6 parts
	path := writeTempNamedFile(t, "secret.bin", body)
	files, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, true)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}

	parts, err := projection.MultipartParts(db, personalChannelID, int64(files[0].MsgID))
	if err != nil {
		t.Fatalf("MultipartParts: %v", err)
	}
	if len(parts) < 2 {
		t.Fatalf("encrypted parts = %d, want >= 2", len(parts))
	}

	var downloadKey []byte
	svc.RequireEncryptionKey = func(encrypted bool) ([]byte, error) {
		if !encrypted {
			return nil, nil
		}
		downloadKey = append([]byte(nil), masterKey...)
		return downloadKey, nil
	}
	savePath := filepath.Join(t.TempDir(), "secret.out")
	result := svc.Download(context.Background(), personalChannelID, files[0].MsgID, files[0].MsgID, func(string) (string, error) {
		return savePath, nil
	})
	if result.Status != "success" {
		t.Fatalf("download = %+v", result)
	}
	got, err := os.ReadFile(savePath)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("encrypted round-trip mismatch: got %d bytes, want %d", len(got), len(body))
	}
	assertKeyZeroed(t, downloadKey)
}

func TestMultipartEncryptedUploadCopiesClearsAndJoinsProducerKey(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	svc.MaxUploadBytes = 32

	uploadKey := bytes.Repeat([]byte{0x4d}, 32)
	svc.MasterKeyForUpload = func(channelID int64, wantEncrypted bool) ([]byte, error) {
		if !wantEncrypted {
			return nil, nil
		}
		return uploadKey, nil
	}

	producerStarted := make(chan struct{})
	releaseProducer := make(chan struct{})
	producerErr := errors.New("test producer stopped")
	var (
		producerKey       []byte
		sharesCallerKey   bool
		producerKeyActive bool
	)
	svc.encryptStream = func(_ io.Reader, _ io.Writer, key []byte, _ int64) error {
		producerKey = key
		sharesCallerKey = len(key) > 0 && len(uploadKey) > 0 && &key[0] == &uploadKey[0]
		producerKeyActive = bytes.Equal(key, bytes.Repeat([]byte{0x4d}, 32))
		close(producerStarted)
		<-releaseProducer
		return producerErr
	}

	// The send fails without draining the pipe. uploadMultipart must close the
	// reader and still join the blocked producer before its caller can wipe the
	// caller-owned key and return.
	fakeTG.FailNextSend()
	path := writeTempNamedFile(t, "joined.bin", bigBody(64))
	done := make(chan error, 1)
	go func() {
		_, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, true)
		done <- err
	}()

	select {
	case <-producerStarted:
	case <-time.After(2 * time.Second):
		close(releaseProducer)
		t.Fatal("encrypted multipart producer did not start")
	}

	select {
	case err := <-done:
		close(releaseProducer)
		t.Fatalf("upload returned before its encryption producer stopped: %v", err)
	case <-time.After(150 * time.Millisecond):
	}

	if sharesCallerKey {
		close(releaseProducer)
		t.Fatal("multipart encryption producer received the caller-owned key buffer")
	}
	if !producerKeyActive {
		close(releaseProducer)
		t.Fatal("multipart encryption producer did not receive an active key copy")
	}
	if !bytes.Equal(uploadKey, bytes.Repeat([]byte{0x4d}, 32)) {
		close(releaseProducer)
		t.Fatal("caller-owned key was cleared while uploadMultipart was still using its producer")
	}

	close(releaseProducer)
	if err := <-done; err == nil {
		t.Fatal("upload unexpectedly succeeded after injected send failure")
	}
	assertKeyZeroed(t, producerKey)
	assertKeyZeroed(t, uploadKey)
}

func TestMultipartDeleteKeepsPartsRestorable(t *testing.T) {
	svc, db, _, _ := newTestService(t)
	svc.MaxUploadBytes = 1000

	body := bigBody(2500) // 3 parts
	path := writeTempNamedFile(t, "a.bin", body)
	files, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, false)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	manifestMsgID := int64(files[0].MsgID)
	if parts, _ := projection.MultipartParts(db, personalChannelID, manifestMsgID); len(parts) != 3 {
		t.Fatalf("parts = %d, want 3", len(parts))
	}

	if err := svc.Delete(context.Background(), personalChannelID, files[0].MsgID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// The file is hidden...
	var tombstoned int
	if err := db.QueryRow(`SELECT tombstoned FROM files WHERE channel_id = ? AND msg_id = ?`, personalChannelID, manifestMsgID).Scan(&tombstoned); err != nil {
		t.Fatalf("read file row: %v", err)
	}
	if tombstoned != 1 {
		t.Fatalf("tombstoned = %d, want 1", tombstoned)
	}
	// ...but every part body and its pointer survive, because the file is
	// still restorable until it is purged.
	if left, _ := projection.MultipartParts(db, personalChannelID, manifestMsgID); len(left) != 3 {
		t.Fatalf("file_parts after delete = %d, want 3", len(left))
	}
	orphans, err := projection.OrphanPartMessages(db, personalChannelID)
	if err != nil {
		t.Fatalf("orphan parts: %v", err)
	}
	if len(orphans) != 0 {
		t.Fatalf("orphan sweep would delete restorable bodies: %v", orphans)
	}
}
