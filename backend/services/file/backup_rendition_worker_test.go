package file

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

func TestBackupRenditionWorkerBoundsSnapshotsAndClearsOnCancel(t *testing.T) {
	started := make(chan struct{})
	w := newBackupRenditionWorker(t.Context(), personalChannelID, func(ctx context.Context, _ projection.File, _ []byte) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}, func(context.Context) error { return nil })
	t.Cleanup(w.Close)
	active := bytes.Repeat([]byte{7}, 20<<20)
	if !w.enqueue(projection.File{ChannelID: personalChannelID, MsgID: 1}, active) {
		t.Fatal("first snapshot rejected")
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("preparation did not start")
	}
	queued := bytes.Repeat([]byte{8}, 10<<20)
	if !w.enqueue(projection.File{ChannelID: personalChannelID, MsgID: 2}, queued) {
		t.Fatal("snapshot within remaining byte budget rejected")
	}
	if w.enqueue(projection.File{ChannelID: personalChannelID, MsgID: 3}, []byte{9}) {
		t.Fatal("accepted snapshot beyond aggregate byte budget")
	}
	if w.enqueue(projection.File{ChannelID: personalChannelID + 1, MsgID: 4}, []byte{9}) {
		t.Fatal("accepted another channel")
	}
	w.Close()
	for _, snapshot := range [][]byte{active, queued} {
		if !bytes.Equal(snapshot, make([]byte, len(snapshot))) {
			t.Fatal("owned plaintext survived worker shutdown")
		}
	}
	if w.enqueue(projection.File{ChannelID: personalChannelID, MsgID: 5}, []byte{9}) {
		t.Fatal("accepted snapshot after shutdown")
	}
}

type blockedBackupRenditionClient struct {
	*tgclient.Fake
	started chan struct{}
	once    sync.Once
}

func (c *blockedBackupRenditionClient) SendFileWithRandomID(ctx context.Context, peer tgclient.InputPeer, source io.Reader, name, caption string, size int64, progress func(int64, int64), randomID int64) (tgclient.SendFileResult, error) {
	if name == "rendition.bin" {
		c.once.Do(func() { close(c.started) })
		<-ctx.Done()
		return tgclient.SendFileResult{}, ctx.Err()
	}
	return c.Fake.SendFileWithRandomID(ctx, peer, source, name, caption, size, progress, randomID)
}

func TestBackupOriginalReturnsBeforePreviewAndEncryptedOutboxResumes(t *testing.T) {
	svc, db, fake, _ := newTestService(t)
	configureEncryptedUpload(t, svc, bytes.Repeat([]byte{7}, 32))
	svc.MaxConcurrentUploads = 2
	blocked := &blockedBackupRenditionClient{Fake: fake, started: make(chan struct{})}
	svc.TG = blocked
	w := svc.NewBackupRenditionWorker(t.Context(), personalChannelID)
	t.Cleanup(w.Close)
	path := writeTempNamedFile(t, "photo.jpg", tinyRenditionJPEG(t))
	meta, err := svc.UploadBackupWithRenditions(t.Context(), personalChannelID, path, "", true, w)
	if err != nil || meta.MsgID <= 0 {
		t.Fatalf("original = %+v, %v", meta, err)
	}
	select {
	case <-blocked.started:
	case <-time.After(5 * time.Second):
		t.Fatal("preview send did not start")
	}
	// A blocked derivative neither owns the native source nor monopolizes the
	// original's upload slot. Another original can complete before it finishes.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	second, err := svc.UploadBackupWithRenditions(t.Context(), personalChannelID, writeTempNamedFile(t, "next.bin", []byte("next original")), "", true, w)
	if err != nil || second.MsgID <= 0 {
		t.Fatalf("next original = %+v, %v", second, err)
	}
	w.Close()
	ids, err := projection.PendingRenditionIDs(t.Context(), db, personalChannelID, 128)
	if err != nil || len(ids) != 2 {
		t.Fatalf("encrypted outbox = %v, %v", ids, err)
	}
	for _, id := range ids {
		job, err := projection.LoadPendingRendition(t.Context(), db, personalChannelID, id)
		if err != nil {
			t.Fatal(err)
		}
		op, err := projection.Parse(job.Header)
		if err != nil || op.Rendition == nil || !op.Rendition.Encrypted || bytes.HasPrefix(job.Payload, []byte{0xff, 0xd8}) {
			t.Fatal("outbox must contain encrypted derivatives")
		}
	}
	// A fresh run recovers persisted ciphertext even after source/key loss.
	svc.TG = fake
	svc.RequireEncryptionKey = func(bool) ([]byte, error) { return nil, errors.New("vault locked") }
	recovery := svc.NewBackupRenditionWorker(t.Context(), personalChannelID)
	t.Cleanup(recovery.Close)
	recovery.Wait()
	for _, kind := range []string{projection.RenditionThumbnail, projection.RenditionPreview} {
		if _, err := projection.CurrentFileRendition(t.Context(), db, personalChannelID, int64(meta.MsgID), kind); err != nil {
			t.Fatalf("recovered %s: %v", kind, err)
		}
	}
	ids, err = projection.PendingRenditionIDs(t.Context(), db, personalChannelID, 128)
	if err != nil || len(ids) != 0 {
		t.Fatalf("undrained outbox = %v, %v", ids, err)
	}
}

func TestBackupRenditionWorkerPreparesWhileSenderBlocked(t *testing.T) {
	sending := make(chan struct{})
	release := make(chan struct{})
	prepared := make(chan int64, 2)
	w := newBackupRenditionWorker(t.Context(), personalChannelID, func(_ context.Context, source projection.File, _ []byte) error {
		prepared <- source.MsgID
		return nil
	}, func(ctx context.Context) error {
		select {
		case <-sending:
		default:
			close(sending)
		}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	t.Cleanup(w.Close)
	<-sending
	for id := int64(1); id <= 2; id++ {
		if !w.enqueue(projection.File{ChannelID: personalChannelID, MsgID: id}, []byte{7}) {
			t.Fatal("snapshot rejected")
		}
		select {
		case got := <-prepared:
			if got != id {
				t.Fatalf("prepared %d, want %d", got, id)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("sender blocked preparation")
		}
	}
	close(release)
	w.Wait()
}

func TestBackupRenditionBackpressureDoesNotDropEligibleSnapshots(t *testing.T) {
	allow := make(chan struct{}, 3)
	prepared := make(chan int64, 3)
	w := newBackupRenditionWorker(t.Context(), personalChannelID, func(ctx context.Context, source projection.File, _ []byte) error {
		select {
		case <-allow:
			prepared <- source.MsgID
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}, func(context.Context) error { return nil })
	t.Cleanup(w.Close)
	snapshots := [][]byte{{1}, {2}, {3}}
	for id := int64(1); id <= 2; id++ {
		if !w.enqueue(projection.File{ChannelID: personalChannelID, MsgID: id}, snapshots[id-1]) {
			t.Fatal("initial snapshot rejected")
		}
	}
	accepted := make(chan bool, 1)
	go func() {
		reservation, err := w.reserve(t.Context(), personalChannelID, len(snapshots[2]))
		if err != nil {
			accepted <- false
			return
		}
		defer reservation.release()
		accepted <- reservation.handoff(projection.File{ChannelID: personalChannelID, MsgID: 3}, snapshots[2])
	}()
	for range 3 {
		allow <- struct{}{}
	}
	select {
	case ok := <-accepted:
		if !ok {
			t.Fatal("third snapshot dropped when preparation budget freed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("admission did not wake after preparation released budget")
	}
	w.Wait()
	if len(prepared) != 3 {
		t.Fatalf("prepared %d originals, want three", len(prepared))
	}
	for _, snapshot := range snapshots {
		if snapshot[0] != 0 {
			t.Fatal("completed worker retained plaintext")
		}
	}
}

func TestBackupReservationWakesForCancellationCloseAndReleasedCapacity(t *testing.T) {
	for _, event := range []string{"caller_cancel", "worker_close", "capacity_released"} {
		t.Run(event, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := newBackupRenditionWorker(t.Context(), personalChannelID, func(context.Context, projection.File, []byte) error {
					return nil
				}, func(context.Context) error { return nil })
				defer w.Close()
				occupied, err := w.reserve(t.Context(), personalChannelID, backupRenditionSnapshotBytes)
				if err != nil {
					t.Fatal(err)
				}
				defer occupied.release()
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				type result struct {
					reservation *backupRenditionReservation
					err         error
				}
				ready := make(chan result, 1)
				go func() {
					r, err := w.reserve(ctx, personalChannelID, 1)
					ready <- result{reservation: r, err: err}
				}()
				// All goroutines are durably blocked here: the second producer
				// really reached the full budget, not merely a canceled preflight.
				synctest.Wait()
				select {
				case <-ready:
					t.Fatal("reservation bypassed occupied byte budget")
				default:
				}
				switch event {
				case "caller_cancel":
					cancel()
				case "worker_close":
					w.Close()
				case "capacity_released":
					occupied.release()
				}
				synctest.Wait()
				got := <-ready
				if event == "capacity_released" {
					if got.err != nil || got.reservation == nil {
						t.Fatalf("released capacity was not reusable: %v", got.err)
					}
					got.reservation.release()
				} else if !errors.Is(got.err, context.Canceled) || got.reservation != nil {
					t.Fatalf("stopped reservation = %+v", got)
				}
				occupied.release()
				w.mu.Lock()
				used, count := w.bytes, w.count
				w.mu.Unlock()
				if used != 0 || count != 0 {
					t.Fatalf("reservation leak after %s: bytes=%d, count=%d", event, used, count)
				}
			})
		})
	}
}

func TestBackupReservationRejectsChangedSourceAndReleasesBudget(t *testing.T) {
	w := newBackupRenditionWorker(t.Context(), personalChannelID, func(context.Context, projection.File, []byte) error {
		return nil
	}, func(context.Context) error { return nil })
	defer w.Close()
	for _, invalid := range []struct {
		channel int64
		size    int
	}{{personalChannelID + 1, 1}, {personalChannelID, 0}, {personalChannelID, backupRenditionSnapshotBytes + 1}} {
		if r, err := w.reserve(t.Context(), invalid.channel, invalid.size); err == nil || r != nil {
			t.Fatalf("invalid reservation admitted: %+v", invalid)
		}
	}
	r, err := w.reserve(t.Context(), personalChannelID, 3)
	if err != nil {
		t.Fatal(err)
	}
	if r.handoff(projection.File{ChannelID: personalChannelID, MsgID: 1}, []byte{1, 2}) {
		t.Fatal("changed snapshot size accepted")
	}
	if r.handoff(projection.File{ChannelID: personalChannelID + 1, MsgID: 1}, []byte{1, 2, 3}) {
		t.Fatal("another drive accepted the reserved snapshot")
	}
	r.release()
	r.release()
	w.mu.Lock()
	used, count := w.bytes, w.count
	w.mu.Unlock()
	if used != 0 || count != 0 {
		t.Fatalf("rejected source leaked budget: bytes=%d, count=%d", used, count)
	}
	if r.handoff(projection.File{ChannelID: personalChannelID, MsgID: 1}, []byte{1, 2, 3}) {
		t.Fatal("released reservation reused")
	}
	w.Close()
	if r, err := w.reserve(t.Context(), personalChannelID, 1); err == nil || r != nil {
		t.Fatal("closed worker accepted new reservation")
	}
}
