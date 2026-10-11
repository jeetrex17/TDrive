package file

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

type throttledManifestClient struct {
	*tgclient.Fake
	randomIDs []int64
}

func (c *throttledManifestClient) SendControlWithRandomID(ctx context.Context, peer tgclient.InputPeer, text string, silent bool, randomID int64) (int64, error) {
	c.randomIDs = append(c.randomIDs, randomID)
	if len(c.randomIDs) == 1 {
		c.InjectFloodWaits(5)
	}
	return c.Fake.SendControlWithRandomID(ctx, peer, text, silent, randomID)
}

func TestMultipartManifestWaitsThroughRepeatedThrottling(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	svc.MaxUploadBytes = 1000
	client := &throttledManifestClient{Fake: fakeTG}
	svc.TG = client
	body := bigBody(2500)
	path := writeTempNamedFile(t, "movie.bin", body)
	files, err := svc.Upload(t.Context(), personalChannelID, []string{path}, []string{""}, false)
	if err != nil || len(files) != 1 {
		t.Fatalf("upload = %+v, %v; want one file after manifest cooldowns", files, err)
	}
	if len(client.randomIDs) != 6 {
		t.Fatalf("manifest attempts = %d, want six", len(client.randomIDs))
	}
	for _, id := range client.randomIDs {
		if id <= 0 || id != client.randomIDs[0] {
			t.Fatalf("manifest random ids = %v, want one stable positive identity", client.randomIDs)
		}
	}
	if len(fakeTG.SentFiles()) != 3 || len(fakeTG.SentControls()) != 1 || len(fakeTG.DeletedBatches()) != 0 {
		t.Fatalf("accepted parts/manifests/deletes = %d/%d/%d, want 3/1/0", len(fakeTG.SentFiles()), len(fakeTG.SentControls()), len(fakeTG.DeletedBatches()))
	}
	parts, err := projection.MultipartParts(db, personalChannelID, int64(files[0].MsgID))
	if err != nil || len(parts) != 3 {
		t.Fatalf("projected parts = %+v, %v; want three committed parts", parts, err)
	}
	var downloaded bytes.Buffer
	for _, part := range parts {
		if err := fakeTG.DownloadFile(t.Context(), fakeTG.SentFiles()[0].Peer, part.MsgID, &downloaded, nil); err != nil {
			t.Fatalf("download accepted part: %v", err)
		}
	}
	if !bytes.Equal(downloaded.Bytes(), body) {
		t.Fatal("committed multipart bytes differ after manifest retries")
	}
}

func TestHiddenUploadWaitsThroughRepeatedThrottling(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	fakeTG.InjectFloodWaits(5)
	body := []byte("hidden content survives repeated Telegram throttling")
	remote, err := svc.UploadHidden(t.Context(), personalChannelID, plaintextHiddenRequest("throttled-hidden", "hidden.txt", int64(len(body))), bytes.NewReader(body))
	if err != nil || len(remote.MessageIDs) != 1 {
		t.Fatalf("hidden upload = %+v, %v; want one accepted hidden body", remote, err)
	}
	var downloaded bytes.Buffer
	if err := fakeTG.DownloadFile(t.Context(), fakeTG.SentFiles()[0].Peer, remote.MessageIDs[0], &downloaded, nil); err != nil {
		t.Fatalf("download accepted body: %v", err)
	}
	if !bytes.Equal(downloaded.Bytes(), body) || len(fakeTG.SentFiles()) != 1 {
		t.Fatal("hidden retries did not retain exactly one complete body")
	}
}

type hiddenUnknownThenThrottledClient struct {
	*tgclient.Fake
	calls int
}

func (c *hiddenUnknownThenThrottledClient) SendFileWithRandomID(ctx context.Context, peer tgclient.InputPeer, source io.Reader, name, caption string, size int64, progress func(int64, int64), randomID int64) (tgclient.SendFileResult, error) {
	c.calls++
	if c.calls == 1 {
		if _, err := c.Fake.SendFileWithRandomID(ctx, peer, source, name, caption, size, progress, randomID); err != nil {
			return tgclient.SendFileResult{}, err
		}
		return tgclient.SendFileResult{}, errors.Join(tgclient.ErrSendOutcomeUnknown, tgclient.ErrInjectedTransport)
	}
	return tgclient.SendFileResult{}, tgclient.NewFloodWaitError(time.Hour)
}

func TestHiddenUploadPreservesUnknownOutcomeWhenCooldownCanceled(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	client := &hiddenUnknownThenThrottledClient{Fake: fakeTG}
	svc.TG = client
	policy := tgclient.DefaultUploadFloodWaitRetryPolicy()
	policy.Sleep = func(_ context.Context, wait time.Duration) error {
		if wait == time.Hour {
			return context.Canceled
		}
		return nil
	}
	svc.FloodWaitRetry = policy
	body := []byte("accepted body with lost receipt")
	_, err := svc.UploadHidden(t.Context(), personalChannelID, plaintextHiddenRequest("hidden-unknown", "body.txt", int64(len(body))), bytes.NewReader(body))
	if !errors.Is(err, context.Canceled) || !errors.Is(err, tgclient.ErrSendOutcomeUnknown) {
		t.Fatalf("hidden upload = %v, want cancellation preserving unknown acceptance", err)
	}
	if client.calls != 2 || len(fakeTG.SentFiles()) != 1 || len(fakeTG.DeletedBatches()) != 0 {
		t.Fatal("uncertain accepted hidden body was resent or discarded after cancellation")
	}
}
