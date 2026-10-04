package app

import (
	"context"
	"errors"
	"testing"
)

func TestDownloadRejectsInvalidBoundIDsBeforeServiceLookup(t *testing.T) {
	app := &App{}

	for _, tc := range []struct {
		name      string
		channelID int64
		messageID int
		lookupID  int
	}{
		{name: "zero channel", channelID: 0, messageID: 1, lookupID: 1},
		{name: "negative channel", channelID: -1, messageID: 1, lookupID: 1},
		{name: "zero message", channelID: 1, messageID: 0, lookupID: 1},
		{name: "zero lookup", channelID: 1, messageID: 1, lookupID: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := app.DownloadFile(tc.channelID, tc.messageID, tc.lookupID, "file:1:1")
			if result.Result.OK {
				t.Fatal("invalid download request unexpectedly succeeded")
			}
			if result.Result.Error == nil || result.Result.Error.Code != OperationCodeFailed {
				t.Fatalf("expected validation error, got %#v", result.Result.Error)
			}
		})
	}

	result := app.DownloadFolder(0, "d:folder", "folder:0:d:folder")
	if result.Result.OK || result.Result.Error == nil || result.Result.Error.Code != OperationCodeFailed {
		t.Fatalf("expected invalid folder channel to fail validation, got %#v", result.Result.Error)
	}
}

func TestDownloadRejectsInvalidRequestIdentityBeforeServiceLookup(t *testing.T) {
	app := &App{}

	for _, tc := range []struct {
		name      string
		requestID string
	}{
		{name: "empty", requestID: ""},
		{name: "control character", requestID: "file:1:1\n2"},
		{name: "too long", requestID: string(make([]byte, maxFrontendDownloadRequestLen+1))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := app.DownloadFile(1, 1, 1, tc.requestID)
			if result.Result.OK {
				t.Fatal("invalid download request identity unexpectedly succeeded")
			}
		})
	}

	result := app.DownloadFolder(1, "   ", "folder:1:space")
	if result.Result.OK {
		t.Fatal("blank folder id unexpectedly succeeded")
	}
}

func TestResumableDownloadRejectsInvalidJobIDsBeforeServiceLookup(t *testing.T) {
	app := &App{}
	if got := app.ListResumableDownloads(0); got.Result.OK {
		t.Fatal("accepted invalid drive for job listing")
	}
	if got := app.ResumeDownload(1, "", "file:1:1"); got.Result.OK {
		t.Fatal("accepted empty resume job ID")
	}
	if got := app.PauseResumableDownload(1, "job\nother"); got.OK {
		t.Fatal("accepted invalid pause job ID")
	}
	if got := app.DiscardResumableDownload(0, "job"); got.OK {
		t.Fatal("accepted invalid discard drive")
	}
}

func TestDownloadSlotRejectsConcurrentStartUntilPreviousRunEnds(t *testing.T) {
	app := &App{ctx: t.Context()}
	activeCtx, finishActive, err := app.transfers.beginDownload(app.ctx)
	if err != nil {
		t.Fatalf("start first download: %v", err)
	}
	t.Cleanup(finishActive)
	if _, _, err := app.transfers.beginDownload(app.ctx); !errors.Is(err, errDownloadBusy) {
		t.Fatalf("concurrent start error = %v, want download busy", err)
	}
	if activeCtx.Err() != nil {
		t.Fatalf("busy rejection canceled active download: %v", activeCtx.Err())
	}
	app.CancelDownload()
	if activeCtx.Err() != context.Canceled {
		t.Fatalf("active download context = %v, want canceled", activeCtx.Err())
	}
	if _, _, err := app.transfers.beginDownload(app.ctx); !errors.Is(err, errDownloadBusy) {
		t.Fatalf("start before canceled run exits = %v, want download busy", err)
	}
	finishActive()
	newCtx, finishNew, err := app.transfers.beginDownload(app.ctx)
	if err != nil || newCtx.Err() != nil {
		t.Fatalf("start after previous run ended: context=%v, error=%v", newCtx, err)
	}
	finishNew()
	if app.transfers.active[downloadTransfer] != nil {
		t.Fatal("finished download left an active cancel handle")
	}
}
