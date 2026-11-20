package app

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"TDrive/backend"
	"TDrive/backend/auth"
	"TDrive/backend/channelsource"
	"TDrive/backend/core"
	"TDrive/backend/projection"
	fileservice "TDrive/backend/services/file"
	"TDrive/backend/tgclient"

	"github.com/gotd/td/telegram"
)

func TestSourceCopyPickerKeepsMyDriveIndependentOfActiveDrive(t *testing.T) {
	const accountID, personalID, sharedID = 8080, 4001, 4002
	app, db := newSourceCopyTestApp(t, tgclient.NewFake(accountID), personalID)
	app.engine.SetActiveChannelID(sharedID)
	op := projection.Op{Type: projection.OpMkdir, Obj: "d:personal", Name: "Films"}
	if _, err := projection.ProjectFromOp(db, personalID, 17, op, accountID, projection.Format(op)); err != nil {
		t.Fatal(err)
	}
	page, err := app.drives.ListSourceCopyFolders(accountID, projection.RootParent)
	if err != nil {
		t.Fatal(err)
	}
	if page.DriveID != personalID || len(page.Folders) != 1 || page.Folders[0].ID != "d:personal" {
		t.Fatalf("picker returned %+v", page)
	}
	if _, err := app.drives.ListSourceCopyFolders(accountID+1, ""); err == nil {
		t.Fatal("picker accepted another account")
	}
	if _, err := app.drives.CreateSourceCopyFolder(accountID, sharedID, "", "New"); err == nil {
		t.Fatal("picker accepted the active shared drive as a destination")
	}
	if _, err := app.drives.SaveSourceCopies(t.Context(), accountID, personalID, "", nil, false); err == nil {
		t.Fatal("save accepted a request without plain-storage consent")
	}
}

func newSourceCopyTestApp(t *testing.T, fake *tgclient.Fake, personalID int64) (*App, *sql.DB) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := auth.SaveConfig(personalID); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	previous := backend.DB
	backend.DB = db
	t.Cleanup(func() { backend.DB = previous; _ = db.Close() })
	if err := projection.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	engine, err := core.New(t.Context(), core.Config{
		TG: fake, SkipDBInit: true,
		Connect: func() (*telegram.Client, error) { return nil, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(engine.Close)
	app := &App{ctx: t.Context(), engine: engine}
	app.initServices("dev")
	return app, db
}

func TestSaveSourceCopyOpensAfterSourceDisconnect(t *testing.T) {
	const accountID, personalID, groupID, sourceMsgID = 8080, 4001, 4400, 42
	fake := tgclient.NewFake(accountID)
	fake.SeedOwnedBroadcastChannels(tgclient.OwnedBroadcastChannel{ID: personalID, AccessHash: 553, Title: "My Drive"})
	fake.SeedMediaSourcePeers(tgclient.SourcePeer{Kind: tgclient.PeerGroup, ID: groupID, Title: "Book Club"})
	body := []byte("A full attachment for the reading list.")
	fake.SeedHistory(tgclient.HistoryMessage{
		PeerKind: tgclient.PeerGroup, ChannelID: groupID, MsgID: sourceMsgID, HasMedia: true,
		DocumentID: 903, DocumentAccessHash: 344, DocumentName: "notes.txt", MimeType: "text/plain", MediaSize: int64(len(body)),
	})
	fake.SeedSourceDocumentBody(tgclient.PeerGroup, groupID, sourceMsgID, body)
	app, _ := newSourceCopyTestApp(t, fake, personalID)
	source, err := app.drives.ConnectChannelSource("group", groupID, "", accountID)
	if err != nil {
		t.Fatal(err)
	}
	page, err := app.drives.ListChannelMedia(source.PeerKind, groupID, 0, 10, "", "all", accountID, source.Generation)
	if err != nil || len(page.Items) != 1 || page.Items[0].CopyIdentity == "" {
		t.Fatalf("source attachments = %+v, err = %v", page, err)
	}
	item := fileservice.SourceCopyInput{
		Name: "reading-list.txt",
		Source: channelsource.CopySource{
			AccountID: accountID, PeerKind: source.PeerKind, PeerID: groupID, Generation: source.Generation,
			MsgID: sourceMsgID, Identity: page.Items[0].CopyIdentity,
		},
	}
	copies, err := app.drives.SaveSourceCopies(t.Context(), accountID, personalID, "", []fileservice.SourceCopyInput{item}, true)
	if err != nil || len(copies) != 1 || copies[0].Status != "saved" || copies[0].FileID <= 0 {
		t.Fatalf("copy results = %+v, err = %v", copies, err)
	}
	if err := app.drives.DisconnectChannelSource(source.PeerKind, groupID, accountID, source.Generation); err != nil {
		t.Fatal(err)
	}
	opened, err := app.engine.MediaService().OpenStream(t.Context(), personalID, copies[0].FileID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.engine.MediaService().CloseSession(opened.Token) }()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, opened.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	got, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK || !bytes.Equal(got, body) {
		t.Fatalf("saved copy = %q, status = %d, err = %v", got, response.StatusCode, err)
	}
}

func TestSourceCopyWorkCancelsBatchAndDrainsBeforeLogout(t *testing.T) {
	var work sourceCopyWork
	ctx, finish, err := work.begin(t.Context(), 42)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(finish)
	if work.cancelBatchFor(43) || ctx.Err() != nil {
		t.Fatal("another account canceled the batch")
	}
	if _, _, err := work.begin(t.Context(), 42); err == nil {
		t.Fatal("a second copy batch started")
	}
	if !work.cancelBatchFor(42) || !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("the batch was not canceled")
	}
	finish()

	ctx, finishNext, err := work.begin(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(finishNext)
	drained := make(chan func(), 1)
	go func() { drained <- work.suspend() }()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("logout did not cancel copy work")
	}
	select {
	case <-drained:
		t.Fatal("logout finished while copy work was still active")
	default:
	}
	if _, _, err := work.begin(t.Context(), 42); err == nil {
		t.Fatal("logout did not stop new copy work")
	}
	finishNext()
	select {
	case resume := <-drained:
		resume()
	case <-time.After(time.Second):
		t.Fatal("logout did not finish after copy work drained")
	}
}
