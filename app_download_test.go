package main

import "testing"

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
