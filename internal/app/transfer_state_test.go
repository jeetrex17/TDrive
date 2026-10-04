package app

import (
	"sync"
	"testing"
)

func TestTransferCompletionKeepsReplacementCancellable(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind transferKind
	}{
		{"upload", uploadTransfer},
		{"download", downloadTransfer},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := &App{ctx: t.Context()}
			first, finishFirst := app.transfers.begin(t.Context(), tc.kind)
			second, finishSecond := app.transfers.begin(t.Context(), tc.kind)
			defer finishSecond()
			if first.Err() == nil {
				t.Fatal("replacement did not cancel previous transfer")
			}
			finishFirst()
			if !app.transfers.keepAwake {
				t.Fatal("previous completion released replacement's awake lease")
			}
			if tc.kind == uploadTransfer {
				app.CancelUpload()
			} else {
				app.CancelDownload()
			}
			if second.Err() == nil {
				t.Fatal("previous completion removed replacement's cancellation handle")
			}
			finishSecond()
			if app.transfers.keepAwake {
				t.Fatal("completed transfers retained awake lease")
			}
		})
	}
}

func TestTransferDirectionsHaveIndependentLifetimes(t *testing.T) {
	var state transferState
	upload, finishUpload := state.begin(t.Context(), uploadTransfer)
	defer finishUpload()
	download, finishDownload := state.begin(t.Context(), downloadTransfer)
	defer finishDownload()
	finishUpload()
	if upload.Err() == nil || download.Err() != nil {
		t.Fatalf("upload=%v download=%v; completion must release only its own context", upload.Err(), download.Err())
	}
	if !state.keepAwake {
		t.Fatal("download lost awake lease when upload completed")
	}
	finishDownload()
	if state.keepAwake {
		t.Fatal("final completion retained awake lease")
	}
}

func TestResumeUploadDoesNotReplaceActiveBatch(t *testing.T) {
	var state transferState
	active, finish := state.begin(t.Context(), uploadTransfer)
	defer finish()
	if _, cleanup, err := state.resumeUpload(t.Context()); err == nil {
		cleanup()
		t.Fatal("resume replaced an active upload")
	}
	if active.Err() != nil {
		t.Fatal("rejected resume canceled active upload")
	}
	finish()
	resumed, cleanup, err := state.resumeUpload(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	state.cancel(uploadTransfer)
	if resumed.Err() == nil {
		t.Fatal("resumed upload cannot be canceled")
	}
}

func TestTransferConcurrentCompletionAndCancellation(t *testing.T) {
	var state transferState
	var workers sync.WaitGroup
	for range 32 {
		workers.Go(func() {
			ctx, finish := state.begin(t.Context(), uploadTransfer)
			defer finish()
			state.cancel(uploadTransfer)
			finish()
			if ctx.Err() == nil {
				t.Error("finished transfer context remained live")
			}
		})
	}
	workers.Wait()
	if state.keepAwake {
		t.Fatal("completed workers retained awake lease")
	}
}

func TestPickerSourcesAreConsumedOnceAndReplacedByNewSelection(t *testing.T) {
	var state transferState
	state.rememberPickerSources([]string{"old", "selected"})
	if !state.takePickerSource("selected") || state.takePickerSource("selected") {
		t.Fatal("picker copy must be consumable exactly once")
	}
	state.rememberPickerSources([]string{"new"})
	if state.takePickerSource("old") || !state.takePickerSource("new") {
		t.Fatal("new selection must replace old picker ownership")
	}
}
