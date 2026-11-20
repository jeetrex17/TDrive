package app

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"TDrive/backend/channelsource"
	"TDrive/backend/projection"
	fileservice "TDrive/backend/services/file"
	"TDrive/backend/tgclient"
)

func TestSharedDriveFileCopiesIntoMyDriveFolder(t *testing.T) {
	const accountID, personalID, sharedID, sharedMessageID = 8080, 4001, 4400, 42
	const parent = "d:archive"
	fake := tgclient.NewFake(accountID)
	fake.SeedOwnedBroadcastChannels(tgclient.OwnedBroadcastChannel{ID: personalID, AccessHash: 553, Title: "My Drive"})
	shared := tgclient.InputPeer{Kind: tgclient.PeerSupergroup, ChannelID: sharedID, AccessHash: 556}
	fake.SeedChannel(shared, "Team Drive")
	fake.SeedMediaSourcePeers(tgclient.SourcePeer{Kind: shared.Kind, ID: sharedID, AccessHash: shared.AccessHash, Title: "Team Drive"})
	folderOp := projection.Op{Type: projection.OpMkdir, Obj: parent, Name: "Archive"}
	body := []byte("A Shared Drive attachment from another member.")
	fake.SeedHistory(
		tgclient.HistoryMessage{PeerKind: tgclient.PeerChannel, ChannelID: personalID, MsgID: 17,
			FromID: accountID, Text: projection.Format(folderOp)},
		tgclient.HistoryMessage{
			PeerKind: shared.Kind, ChannelID: sharedID, MsgID: sharedMessageID, FromID: 7123, HasMedia: true,
			DocumentID: 903, DocumentAccessHash: 344, DocumentName: "team-notes.txt", MimeType: "text/plain", MediaSize: int64(len(body)),
		})
	fake.SeedSourceDocumentBody(shared.Kind, sharedID, sharedMessageID, body)

	app, db := newSourceCopyTestApp(t, fake, personalID)
	if err := projection.InsertChannel(db, projection.Channel{
		ChannelID: sharedID, AccessHash: shared.AccessHash, Title: "Team Drive", Kind: projection.KindShared,
	}); err != nil {
		t.Fatal(err)
	}
	app.engine.SetActiveChannelID(sharedID)
	if _, err := projection.ProjectFromOp(db, personalID, 17, folderOp, accountID, projection.Format(folderOp)); err != nil {
		t.Fatal(err)
	}
	destinations, err := app.drives.ListSourceCopyFolders(accountID, "")
	if err != nil || destinations.DriveID != personalID || len(destinations.Folders) != 1 || destinations.Folders[0].ID != parent {
		t.Fatalf("destination folders = %+v, error = %v", destinations, err)
	}
	files := app.GetFileList()
	if len(files) != 1 || files[0].ID != sharedMessageID || files[0].CopyIdentity == "" {
		t.Fatalf("Shared Drive attachments = %+v, want a copyable raw file", files)
	}

	item := fileservice.SourceCopyInput{
		Name: "team-notes.txt",
		Source: channelsource.CopySource{
			AccountID: accountID, PeerKind: "shared-drive", PeerID: sharedID, MsgID: sharedMessageID,
			Generation: "0", Identity: files[0].CopyIdentity,
		},
	}
	copies, err := app.drives.SaveSourceCopies(t.Context(), accountID, personalID, parent, []fileservice.SourceCopyInput{item}, true)
	if err != nil || len(copies) != 1 || copies[0].Status != "saved" || copies[0].FileID <= 0 || copies[0].ParentID != parent {
		t.Fatalf("copy results = %+v, error = %v", copies, err)
	}
	if app.engine.ActiveChannelID() != sharedID || len(fake.DeletedBatches()) != 0 {
		t.Fatal("copy changed the source drive or deleted a message")
	}
	path := filepath.Join(t.TempDir(), "team-notes.txt")
	result := app.engine.FileService().Download(t.Context(), personalID, int(copies[0].FileID), 0, func(string) (string, error) {
		return path, nil
	})
	got, err := os.ReadFile(path)
	if result.Err != nil || err != nil || !bytes.Equal(got, body) {
		t.Fatalf("saved copy = %q, status = %q, download error = %v, read error = %v", got, result.Status, result.Err, err)
	}
}
