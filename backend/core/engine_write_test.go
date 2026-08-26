package core

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"TDrive/backend"
	"TDrive/backend/projection"
	folderservice "TDrive/backend/services/folder"
	"TDrive/backend/tgclient"

	_ "modernc.org/sqlite"
)

const engineWriteTestChannelID int64 = 818181

type engineWriteContextKey struct{}

type retryingControlClient struct {
	*tgclient.Fake
	randomIDs []int64
	contexts  []context.Context
}

func (c *retryingControlClient) SendControlWithRandomID(
	ctx context.Context,
	peer tgclient.InputPeer,
	text string,
	silent bool,
	randomID int64,
) (int64, error) {
	c.randomIDs = append(c.randomIDs, randomID)
	c.contexts = append(c.contexts, ctx)
	if len(c.randomIDs) == 1 {
		return 0, tgclient.NewFloodWaitError(0)
	}
	return c.Fake.SendControlWithRandomID(ctx, peer, text, silent, randomID)
}

func TestEmitAndProjectContextRetriesControlWithStableRandomID(t *testing.T) {
	db := openEngineWriteTestDB(t)
	previousDB := backend.DB
	backend.DB = db
	t.Cleanup(func() { backend.DB = previousDB })

	peer := tgclient.InputPeer{ChannelID: engineWriteTestChannelID, AccessHash: 91}
	fake := tgclient.NewFake(7)
	fake.SeedChannel(peer, "Test drive")
	client := &retryingControlClient{Fake: fake}
	engine := &Engine{
		ctx:   context.Background(),
		tg:    client,
		warnf: func(string, ...any) {},
	}
	engine.selfUserID.Store(7)

	ctx := context.WithValue(context.Background(), engineWriteContextKey{}, "request context")
	op := projection.Op{
		Type:   projection.OpMkdir,
		Obj:    projection.FolderIDPrefix + "context-write",
		Parent: projection.RootParent,
		Name:   "Context write",
	}
	msgID, err := engine.EmitAndProjectContext(ctx, engineWriteTestChannelID, op)
	if err != nil {
		t.Fatalf("EmitAndProjectContext: %v", err)
	}
	if msgID <= 0 {
		t.Fatalf("message id = %d, want positive", msgID)
	}
	if len(client.randomIDs) != 2 {
		t.Fatalf("control attempts = %d, want 2", len(client.randomIDs))
	}
	if client.randomIDs[0] <= 0 || client.randomIDs[0] != client.randomIDs[1] {
		t.Fatalf("control random ids = %v, want one stable positive id", client.randomIDs)
	}
	for attempt, got := range client.contexts {
		if got.Value(engineWriteContextKey{}) != "request context" {
			t.Fatalf("attempt %d lost the request context", attempt+1)
		}
	}
	if !projection.FolderExists(db, engineWriteTestChannelID, op.Obj) {
		t.Fatal("successful retried control was not projected")
	}
}

func TestNewFileServicePropagatesImportContextToFolderCreation(t *testing.T) {
	db := openEngineWriteTestDB(t)
	var emittedContext context.Context
	folderService := &folderservice.Service{
		DB: db,
		EmitOpContext: func(ctx context.Context, _ int64, _ projection.Op) error {
			emittedContext = ctx
			return nil
		},
	}
	engine := &Engine{
		ctx:     context.Background(),
		folders: folderService,
	}
	fileService := engine.newFileService()

	ctx := context.WithValue(context.Background(), engineWriteContextKey{}, "folder import")
	folderID, err := fileService.CreateFolder(ctx, engineWriteTestChannelID, "Imported", projection.RootParent)
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	if !projection.IsFolderID(folderID) {
		t.Fatalf("folder id = %q, want a folder id", folderID)
	}
	if emittedContext == nil || emittedContext.Value(engineWriteContextKey{}) != "folder import" {
		t.Fatal("file-service folder creation lost the import context")
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	emittedContext = nil
	_, err = fileService.CreateFolder(canceled, engineWriteTestChannelID, "Canceled", projection.RootParent)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled CreateFolder error = %v, want context canceled", err)
	}
	if emittedContext != nil {
		t.Fatal("canceled folder creation reached the emitter")
	}
}

func openEngineWriteTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := projection.MigratePersonalChannel(db, engineWriteTestChannelID); err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	return db
}
