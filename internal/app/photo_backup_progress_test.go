package app

import (
	"TDrive/backend/photobackup"
	fileservice "TDrive/backend/services/file"
	"context"
	"testing"
)

func TestBackupProgressIsScopedAndRejectsOldCallbacks(t *testing.T) {
	a := &App{}
	scope := photobackup.Scope{AccountID: "one", DriveID: 1}
	update, finish := a.beginPhotoBackupProgress(context.Background(), scope, "first.jpg", 100)
	update(fileservice.BackupUploadProgress{Name: "first.jpg", BytesTotal: 100, Percent: 25})
	got := a.withPhotoBackupProgress(PhotoBackupState{}, scope)
	if got.Status.CurrentFile != "first.jpg" || got.Status.CurrentFilePercent != 25 || got.Status.CurrentFileBytesDone != 25 {
		t.Fatalf("progress = %+v", got.Status)
	}
	if other := a.withPhotoBackupProgress(PhotoBackupState{}, photobackup.Scope{AccountID: "two", DriveID: 1}); other.Status.CurrentFile != "" {
		t.Fatal("cross-account progress leak")
	}
	next, _ := a.beginPhotoBackupProgress(context.Background(), scope, "second.jpg", 200)
	next(fileservice.BackupUploadProgress{BytesTotal: 200, Percent: 50})
	update(fileservice.BackupUploadProgress{BytesTotal: 100, Percent: 100})
	finish()
	if got = a.withPhotoBackupProgress(PhotoBackupState{}, scope); got.Status.CurrentFile != "second.jpg" || got.Status.CurrentFilePercent != 50 {
		t.Fatalf("stale callback replaced progress: %+v", got.Status)
	}
}

func TestBackupProgressCancellationAndCompletionClearCurrentFile(t *testing.T) {
	a := &App{}
	scope := photobackup.Scope{AccountID: "one", DriveID: 1}
	ctx, cancel := context.WithCancel(context.Background())
	update, finish := a.beginPhotoBackupProgress(ctx, scope, "photo.jpg", 100)
	cancel()
	update(fileservice.BackupUploadProgress{BytesTotal: 100, Percent: 80})
	if got := a.withPhotoBackupProgress(PhotoBackupState{}, scope); got.Status.CurrentFilePercent != 0 {
		t.Fatal("accepted cancelled progress")
	}
	finish()
	if got := a.withPhotoBackupProgress(PhotoBackupState{}, scope); got.Status.CurrentFile != "" {
		t.Fatal("completed upload remains current")
	}
}

func TestBackupProgressClampsTransportValues(t *testing.T) {
	a := &App{}
	scope := photobackup.Scope{AccountID: "one", DriveID: 1}
	update, finish := a.beginPhotoBackupProgress(context.Background(), scope, "photo.jpg", 100)
	update(fileservice.BackupUploadProgress{BytesTotal: 100, Percent: 120})
	update(fileservice.BackupUploadProgress{BytesTotal: 100, Percent: 20})
	got := a.withPhotoBackupProgress(PhotoBackupState{}, scope)
	if got.Status.CurrentFilePercent != 100 || got.Status.CurrentFileBytesDone != 100 {
		t.Fatalf("invalid progress: %+v", got.Status)
	}
	finish()
	update(fileservice.BackupUploadProgress{BytesTotal: 100, Percent: 100})
	if got := a.withPhotoBackupProgress(PhotoBackupState{}, scope); got.Status.CurrentFile != "" {
		t.Fatal("finished callback became active")
	}
}

func TestBackupProgressAggregatesBothActiveFiles(t *testing.T) {
	a := &App{}
	scope := photobackup.Scope{AccountID: "one", DriveID: 1}
	first, finishFirst := a.beginPhotoBackupProgress(t.Context(), scope, "first.jpg", 100)
	second, finishSecond := a.beginPhotoBackupProgress(t.Context(), scope, "second.jpg", 300)
	first(fileservice.BackupUploadProgress{BytesTotal: 100, Percent: 100})
	second(fileservice.BackupUploadProgress{BytesTotal: 300, Percent: 0})
	got := a.withPhotoBackupProgress(PhotoBackupState{}, scope)
	if got.Status.CurrentFile != "2 files" || got.Status.CurrentFileBytesTotal != 400 || got.Status.CurrentFileBytesDone != 100 || got.Status.CurrentFilePercent != 25 {
		t.Fatalf("aggregate progress = %+v", got.Status)
	}
	finishFirst()
	first(fileservice.BackupUploadProgress{BytesTotal: 900, Percent: 100})
	second(fileservice.BackupUploadProgress{BytesTotal: 300, Percent: 50})
	got = a.withPhotoBackupProgress(PhotoBackupState{}, scope)
	if got.Status.CurrentFile != "second.jpg" || got.Status.CurrentFileBytesDone != 150 {
		t.Fatalf("remaining progress = %+v", got.Status)
	}
	finishSecond()
}

func TestBackupProgressOldScopeCannotClearNewScope(t *testing.T) {
	a := &App{}
	oldScope := photobackup.Scope{AccountID: "old", DriveID: 1}
	newScope := photobackup.Scope{AccountID: "new", DriveID: 2}
	oldUpdate, oldFinish := a.beginPhotoBackupProgress(t.Context(), oldScope, "old.jpg", 100)
	update, finish := a.beginPhotoBackupProgress(t.Context(), newScope, "new.jpg", 200)
	defer finish()
	oldUpdate(fileservice.BackupUploadProgress{BytesTotal: 100, Percent: 100})
	oldFinish()
	update(fileservice.BackupUploadProgress{BytesTotal: 200, Percent: 50})
	if got := a.withPhotoBackupProgress(PhotoBackupState{}, newScope); got.Status.CurrentFile != "new.jpg" || got.Status.CurrentFileBytesDone != 100 {
		t.Fatalf("old scope corrupted progress = %+v", got.Status)
	}
}
