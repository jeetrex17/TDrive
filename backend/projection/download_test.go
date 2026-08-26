package projection

import (
	"context"
	"errors"
	"testing"
)

func TestBuildFolderDownloadManifestUsesPortableLiveNamespace(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 1, Op{Type: OpMkdir, Obj: "d:project", Parent: RootParent, Name: "Project"})
	mustOp(t, db, 2, Op{Type: OpMkdir, Obj: "d:empty", Parent: "d:project", Name: "Empty"})
	mustOp(t, db, 3, Op{Type: OpMkdir, Obj: "d:data", Parent: "d:project", Name: "Data"})
	mustOp(t, db, 4, Op{Type: OpMkdir, Obj: "d:outside", Parent: RootParent, Name: "Outside"})
	mustOp(t, db, 10, Op{
		Type: OpFileCommit, ProtocolVersion: 1, OpID: "create-report",
		Parent: "d:data", Name: "report.txt", ContentMsgID: 700,
		ContentHash: "old", FileSize: 8, FileUploadTime: 100,
	})
	mustOp(t, db, 11, Op{
		Type: OpFileUpload, Parent: "d:outside", Name: "outside.txt",
		FileSize: 99, FileUploadTime: 101,
	})
	mustOp(t, db, 12, Op{
		Type: OpFileReplace, ProtocolVersion: 1, OpID: "replace-report",
		Obj: "f:10", ExpectedRevision: 1, ContentMsgID: 777,
		ContentHash: "abc", FileSize: 12, RetainedUntil: 500,
	})

	// Legacy storage columns may contain names that are unsafe on a local
	// filesystem. dirents is the authoritative, portable namespace and must be
	// the only source of names used by a download manifest.
	if _, err := db.Exec(`UPDATE folders SET name='legacy/root' WHERE channel_id=? AND id='d:project'`, testChan); err != nil {
		t.Fatalf("update legacy folder name: %v", err)
	}
	if _, err := db.Exec(`UPDATE files SET name='../legacy.txt' WHERE channel_id=? AND msg_id=10`, testChan); err != nil {
		t.Fatalf("update current file reference: %v", err)
	}
	if _, err := db.Exec(`UPDATE dirents SET display_name='Current.txt', name_key=? WHERE channel_id=? AND object_id='f:10'`, "current.txt", testChan); err != nil {
		t.Fatalf("update portable file name: %v", err)
	}

	manifest, err := BuildFolderDownloadManifestContext(context.Background(), db, testChan, "d:project")
	if err != nil {
		t.Fatalf("BuildFolderDownloadManifestContext: %v", err)
	}
	if manifest.Root.ID != "d:project" || manifest.Root.Name != "Project" {
		t.Fatalf("root = %+v, want portable Project entry", manifest.Root)
	}

	folders := make(map[string]DownloadDirectory, len(manifest.Folders))
	for _, folder := range manifest.Folders {
		folders[folder.ID] = folder
	}
	if len(folders) != 3 {
		t.Fatalf("folders = %+v, want root plus two descendants", manifest.Folders)
	}
	if folders["d:empty"].ParentID != "d:project" || folders["d:data"].Name != "Data" {
		t.Fatalf("descendant folders = %+v", manifest.Folders)
	}
	if _, leaked := folders["d:outside"]; leaked {
		t.Fatal("folder outside requested subtree leaked into manifest")
	}

	if len(manifest.Files) != 1 {
		t.Fatalf("files = %+v, want one subtree file", manifest.Files)
	}
	file := manifest.Files[0]
	if file.LogicalMsgID != 10 || file.ContentMsgID != 777 || file.Revision != 2 || file.Name != "Current.txt" || file.ParentID != "d:data" {
		t.Fatalf("file = %+v, want portable name and current immutable content reference", file)
	}
}

func TestBuildFolderDownloadManifestRejectsInvalidInputAndCorruptNames(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 1, Op{Type: OpMkdir, Obj: "d:project", Parent: RootParent, Name: "Project"})

	tests := []struct {
		name     string
		ctx      context.Context
		folderID string
		wantErr  error
	}{
		{name: "nil context", ctx: nil, folderID: "d:project", wantErr: ErrInvalidContext},
		{name: "empty id", ctx: context.Background(), folderID: "", wantErr: ErrInvalidFolderDownload},
		{name: "not a folder id", ctx: context.Background(), folderID: "project", wantErr: ErrInvalidFolderDownload},
		{name: "missing", ctx: context.Background(), folderID: "d:missing", wantErr: ErrFolderDownloadNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := BuildFolderDownloadManifestContext(tt.ctx, db, testChan, tt.folderID)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := BuildFolderDownloadManifestContext(ctx, db, testChan, "d:project"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled error = %v, want context.Canceled", err)
	}

	if _, err := db.Exec(`UPDATE dirents SET display_name='../escape' WHERE channel_id=? AND object_id='d:project'`, testChan); err != nil {
		t.Fatalf("corrupt portable name: %v", err)
	}
	if _, err := BuildFolderDownloadManifestContext(context.Background(), db, testChan, "d:project"); !errors.Is(err, ErrInvalidPortableName) {
		t.Fatalf("corrupt-name error = %v, want ErrInvalidPortableName", err)
	}
}

func TestBuildFolderDownloadManifestRejectsCycleThroughRoot(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 1, Op{Type: OpMkdir, Obj: "d:project", Parent: RootParent, Name: "Project"})
	mustOp(t, db, 2, Op{Type: OpMkdir, Obj: "d:nested", Parent: "d:project", Name: "Nested"})
	if _, err := db.Exec(`UPDATE folders SET parent_id='d:nested' WHERE channel_id=? AND id='d:project'`, testChan); err != nil {
		t.Fatalf("corrupt folder parent: %v", err)
	}
	if _, err := db.Exec(`UPDATE dirents SET parent_id='d:nested' WHERE channel_id=? AND object_id='d:project'`, testChan); err != nil {
		t.Fatalf("corrupt dirent parent: %v", err)
	}

	_, err := BuildFolderDownloadManifestContext(context.Background(), db, testChan, "d:project")
	if !errors.Is(err, ErrInvalidFolderDownload) {
		t.Fatalf("cycle error = %v, want ErrInvalidFolderDownload", err)
	}
}

func TestFileDownloadRefContextUsesPortableNameAndCurrentRevision(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 10, Op{
		Type: OpFileCommit, ProtocolVersion: 1, OpID: "create-current",
		Parent: RootParent, Name: "old.txt", ContentMsgID: 9000,
		ContentHash: "old", FileSize: 3, FileUploadTime: 100,
	})
	mustOp(t, db, 11, Op{
		Type: OpFileReplace, ProtocolVersion: 1, OpID: "replace-current",
		Obj: "f:10", ExpectedRevision: 1, ContentMsgID: 9001,
		ContentHash: "sha256:current", FileSize: 4, RetainedUntil: 500,
	})
	if _, err := db.Exec(`
		UPDATE files
		SET name='legacy/name.txt'
		WHERE channel_id=? AND msg_id=10
	`, testChan); err != nil {
		t.Fatalf("update current file: %v", err)
	}
	if _, err := db.Exec(`UPDATE dirents SET display_name='renamed.txt', name_key='renamed.txt' WHERE channel_id=? AND object_id='f:10'`, testChan); err != nil {
		t.Fatalf("update current dirent: %v", err)
	}

	file, found, err := FileDownloadRefContext(context.Background(), db, testChan, 10)
	if err != nil {
		t.Fatalf("FileDownloadRefContext: %v", err)
	}
	if !found {
		t.Fatal("file not found")
	}
	if file.Name != "renamed.txt" || file.ContentMsgID != 9001 || file.Revision != 2 {
		t.Fatalf("file = %+v, want current portable revision", file)
	}
}

func TestFileDownloadRefContextIncludesMultipartParts(t *testing.T) {
	db := newTestDB(t)
	applyMultipartUpload(t, db, "download-parts", []int64{100, 101, 102}, 200, "movie.bin", RootParent, false)

	file, found, err := FileDownloadRefContext(context.Background(), db, testChan, 200)
	if err != nil {
		t.Fatalf("FileDownloadRefContext: %v", err)
	}
	if !found {
		t.Fatal("multipart file not found")
	}
	if file.ContentMsgID != 0 || file.UploadUUID != "download-parts" || file.PartCount != 3 {
		t.Fatalf("multipart metadata = %+v", file)
	}
	if len(file.Parts) != 3 {
		t.Fatalf("parts = %+v, want 3", file.Parts)
	}
	for i, part := range file.Parts {
		if part.PartIndex != i || part.MsgID != int64(100+i) || part.Size != 100 {
			t.Fatalf("part[%d] = %+v", i, part)
		}
	}
}

func TestValidateDownloadFileRejectsCorruptMetadata(t *testing.T) {
	validMultipart := DownloadFile{
		LogicalMsgID: 10, Name: "file.bin", Revision: 1,
		UploadUUID: "parts", PartCount: 2, StoredSize: 7, OutputSize: 7,
		Parts: []FilePart{
			{PartIndex: 0, MsgID: 100, Size: 3},
			{PartIndex: 1, MsgID: 101, Size: 4},
		},
	}

	tests := []struct {
		name string
		file DownloadFile
	}{
		{
			name: "bad identity",
			file: DownloadFile{Name: "file.txt", Revision: 1, StoredSize: 1, OutputSize: 1},
		},
		{
			name: "negative size",
			file: DownloadFile{LogicalMsgID: 1, Name: "file.txt", Revision: 1, StoredSize: -1, OutputSize: 1},
		},
		{
			name: "corrupt portable name",
			file: DownloadFile{LogicalMsgID: 1, Name: "../file.txt", Revision: 1, StoredSize: 1, OutputSize: 1},
		},
		{
			name: "single file with multipart metadata",
			file: DownloadFile{
				LogicalMsgID: 1, Name: "file.txt", Revision: 1,
				PartCount: 1, StoredSize: 1, OutputSize: 1,
			},
		},
		{
			name: "multipart missing part",
			file: DownloadFile{
				LogicalMsgID: 1, Name: "file.bin", Revision: 1,
				UploadUUID: "parts", PartCount: 2, StoredSize: 3, OutputSize: 3,
				Parts: []FilePart{{PartIndex: 0, MsgID: 100, Size: 3}},
			},
		},
		{
			name: "multipart bad part index",
			file: func() DownloadFile {
				file := validMultipart
				file.Parts = append([]FilePart(nil), validMultipart.Parts...)
				file.Parts[1].PartIndex = 3
				return file
			}(),
		},
		{
			name: "multipart total mismatch",
			file: func() DownloadFile {
				file := validMultipart
				file.Parts = append([]FilePart(nil), validMultipart.Parts...)
				file.Parts[1].Size = 3
				return file
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateDownloadFile(tt.file); err == nil {
				t.Fatal("validateDownloadFile succeeded, want error")
			}
		})
	}
}
