package mountcontroller

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"TDrive/backend"
	"TDrive/backend/core"
	"TDrive/backend/media"
	"TDrive/backend/mountcontent"
	"TDrive/backend/mountfs"
	"TDrive/backend/projection"
	readservice "TDrive/backend/services/read"
	"TDrive/backend/tgclient"

	"github.com/gotd/td/telegram"
	_ "modernc.org/sqlite"
)

func TestProductionControllerPinsDriveWithoutChangingEngineActiveDrive(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())

	const (
		activeDriveID  int64 = 81_001
		mountedDriveID int64 = 81_002
	)
	db := newControllerProjectionDB(t, mountedDriveID)
	previousDB := backend.DB
	backend.DB = db
	t.Cleanup(func() { backend.DB = previousDB })

	fakeTelegram := tgclient.NewFake(1)
	fakeTelegram.SeedChannel(tgclient.InputPeer{ChannelID: mountedDriveID, AccessHash: 42}, "Mounted")
	engine, err := core.New(context.Background(), core.Config{
		TG:         fakeTelegram,
		SkipDBInit: true,
		Connect: func() (*telegram.Client, error) {
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("core.New() error = %v", err)
	}
	t.Cleanup(engine.Close)
	engine.SetActiveChannelID(activeDriveID)

	connector := &fakeConnector{}
	controller, err := NewWithConnector(engine, connector)
	if err != nil {
		t.Fatalf("NewWithConnector() error = %v", err)
	}
	t.Cleanup(func() { _ = controller.Close(context.Background()) })

	started, err := controller.Start(context.Background(), Drive{
		ID:    mountedDriveID,
		Title: "Mounted",
		Kind:  DriveKindShared,
	}, StartOptions{})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	assertStatus(t, started, PhaseMounted, true, true)
	if got := engine.ActiveChannelID(); got != activeDriveID {
		t.Fatalf("Engine.ActiveChannelID() = %d, want %d", got, activeDriveID)
	}
	if started.Location != "/Volumes/Tdrive personal" || started.AttachmentKind != "darwin" {
		t.Fatalf("safe attachment status = %#v", started)
	}
	if status := controller.Status(); status.Phase != PhaseMounted {
		t.Fatalf("Status() = %#v", status)
	}
	if _, err := controller.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestNewAndDependencyValidation(t *testing.T) {
	t.Parallel()

	if _, err := New(nil); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("New(nil) error = %v", err)
	}
	if _, err := NewWithDependencies(Dependencies{}); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("empty dependencies error = %v", err)
	}
	_, err := NewWithDependencies(Dependencies{
		Filesystems:     &fakeFilesystemBuilder{content: &fakeContent{}},
		Endpoint:        &fakeEndpoint{},
		Connector:       &fakeConnector{},
		SnapshotOptions: mountfs.Options{SnapshotTTL: -1},
	})
	if !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("negative options error = %v", err)
	}
}

func TestDirectorySourcePreservesProjectionMetadata(t *testing.T) {
	t.Parallel()

	const channelID int64 = 72_001
	db := newControllerProjectionDB(t, channelID)
	applyControllerProjectionOp(t, db, channelID, 1, projection.Op{
		Type:   projection.OpMkdir,
		Obj:    "d:photos",
		Parent: projection.RootParent,
		Name:   "Photos",
	})
	applyControllerProjectionOp(t, db, channelID, 2, projection.Op{
		Type:           projection.OpFileUpload,
		Parent:         projection.RootParent,
		Name:           "plain.txt",
		FileSize:       12,
		FileUploadTime: 1_700_000_000,
	})
	applyControllerProjectionOp(t, db, channelID, 3, projection.Op{
		Type:              projection.OpFileUpload,
		Parent:            "d:photos",
		Name:              "secret.bin",
		FileSize:          100,
		FileUploadTime:    1_700_000_001,
		Encrypted:         true,
		PlaintextSize:     60,
		EncryptionVersion: 1,
	})

	source := directorySource{reads: &readservice.Service{DB: db}}
	root, err := source.ListDirectory(context.Background(), channelID, mountfs.RootID)
	if err != nil {
		t.Fatalf("ListDirectory(root) error = %v", err)
	}
	byID := controllerEntriesByID(root)
	if got := byID["d:photos"]; got.Kind != mountfs.KindDirectory || got.ParentID != mountfs.RootID {
		t.Fatalf("folder entry = %#v", got)
	}
	if got := byID["f:2"]; got.Size != 12 || got.ContentRef != "2" || !got.ModTime.Equal(time.Unix(1_700_000_000, 0)) {
		t.Fatalf("plain entry = %#v", got)
	}

	nested, err := source.ListDirectory(context.Background(), channelID, "d:photos")
	if err != nil {
		t.Fatalf("ListDirectory(nested) error = %v", err)
	}
	if len(nested) != 1 || nested[0].Size != 60 || !nested[0].Encrypted {
		t.Fatalf("nested entries = %#v", nested)
	}
	if _, err := source.ListDirectory(context.Background(), channelID, "f:2"); !errors.Is(err, mountfs.ErrContentUnavailable) {
		t.Fatalf("invalid parent error = %v", err)
	}
}

func TestDirectorySourceErrorsAreTypedAndSanitized(t *testing.T) {
	t.Parallel()

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (directorySource{}).ListDirectory(canceled, 1, mountfs.RootID); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled ListDirectory() error = %v", err)
	}
	if _, err := (directorySource{}).ListDirectory(nil, 1, mountfs.RootID); !errors.Is(err, mountfs.ErrContentUnavailable) {
		t.Fatalf("nil-context ListDirectory() error = %v", err)
	}
	if _, err := (directorySource{}).ListDirectory(context.Background(), 0, mountfs.RootID); !errors.Is(err, mountfs.ErrContentUnavailable) {
		t.Fatalf("invalid-channel ListDirectory() error = %v", err)
	}

	tests := []struct {
		name   string
		ctx    context.Context
		err    error
		target error
	}{
		{name: "nil", ctx: context.Background()},
		{name: "canceled", ctx: canceled, err: errors.New("database secret"), target: context.Canceled},
		{name: "deadline", ctx: context.Background(), err: context.DeadlineExceeded, target: context.DeadlineExceeded},
		{name: "permission", ctx: context.Background(), err: os.ErrPermission, target: mountfs.ErrAccessDenied},
		{name: "internal", ctx: context.Background(), err: errors.New("database secret"), target: mountfs.ErrContentUnavailable},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			got := mapDirectoryError(test.ctx, test.err)
			if test.target == nil {
				if got != nil {
					t.Fatalf("mapDirectoryError() = %v", got)
				}
				return
			}
			if !errors.Is(got, test.target) {
				t.Fatalf("mapDirectoryError() = %v, want %v", got, test.target)
			}
			if strings.Contains(got.Error(), "database secret") {
				t.Fatalf("internal detail leaked: %v", got)
			}
		})
	}
}

func TestContentAdapterOpensProjectedFileAndRejectsUnsafeEntries(t *testing.T) {
	t.Parallel()

	const channelID int64 = 73_001
	db := newControllerProjectionDB(t, channelID)
	applyControllerProjectionOp(t, db, channelID, 9, projection.Op{
		Type:           projection.OpFileUpload,
		Parent:         projection.RootParent,
		Name:           "plain.txt",
		FileSize:       12,
		FileUploadTime: 1_700_000_000,
	})
	opener, err := mountcontent.New(mountcontent.Config{
		DB:     db,
		Peers:  controllerPeerResolver{},
		Ranges: controllerRangeClient{size: 12},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(opener.Close)
	adapter := contentAdapter{opener: opener}
	entry := mountfs.SourceEntry{Size: 12, ContentRef: "9"}
	opened, err := adapter.OpenContent(context.Background(), channelID, entry)
	if err != nil {
		t.Fatalf("OpenContent() error = %v", err)
	}
	if err := opened.Close(); err != nil {
		t.Fatalf("opened.Close() error = %v", err)
	}

	entry.Size++
	if _, err := adapter.OpenContent(context.Background(), channelID, entry); !errors.Is(err, mountfs.ErrContentUnavailable) {
		t.Fatalf("stale-size error = %v", err)
	}
	if _, err := adapter.OpenContent(context.Background(), channelID, mountfs.SourceEntry{Encrypted: true}); !errors.Is(err, mountfs.ErrAccessDenied) {
		t.Fatalf("encrypted error = %v", err)
	}
	if _, err := adapter.OpenContent(context.Background(), channelID, mountfs.SourceEntry{ContentRef: "bad"}); !errors.Is(err, mountfs.ErrContentUnavailable) {
		t.Fatalf("bad reference error = %v", err)
	}
	if _, err := (contentAdapter{}).OpenContent(context.Background(), channelID, entry); !errors.Is(err, mountfs.ErrContentUnavailable) {
		t.Fatalf("nil opener error = %v", err)
	}
	if _, err := adapter.OpenContent(nil, channelID, entry); !errors.Is(err, mountfs.ErrContentUnavailable) {
		t.Fatalf("nil context error = %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := adapter.OpenContent(canceled, channelID, entry); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context error = %v", err)
	}
}

func TestContentErrorsAreTypedAndSanitized(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		err    error
		target error
	}{
		{name: "nil"},
		{name: "canceled", err: context.Canceled, target: context.Canceled},
		{name: "deadline", err: context.DeadlineExceeded, target: context.DeadlineExceeded},
		{name: "encrypted", err: mountcontent.ErrEncryptedUnsupported, target: mountfs.ErrAccessDenied},
		{name: "media encrypted", err: media.ErrEncryptedUnsupported, target: mountfs.ErrAccessDenied},
		{name: "projection missing", err: media.ErrFileNotFound, target: mountfs.ErrNotFound},
		{name: "message missing", err: tgclient.ErrMessageNotFound, target: mountfs.ErrNotFound},
		{name: "not file", err: tgclient.ErrNotFile, target: mountfs.ErrNotFound},
		{name: "empty document", err: tgclient.ErrEmptyDocument, target: mountfs.ErrNotFound},
		{name: "internal", err: errors.New("Telegram secret"), target: mountfs.ErrContentUnavailable},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			got := mapContentOpenError(test.err)
			if test.target == nil {
				if got != nil {
					t.Fatalf("mapContentOpenError(nil) = %v", got)
				}
				return
			}
			if !errors.Is(got, test.target) {
				t.Fatalf("mapContentOpenError() = %v, want %v", got, test.target)
			}
			if strings.Contains(got.Error(), "Telegram secret") {
				t.Fatalf("internal detail leaked: %v", got)
			}
		})
	}
}

func TestEngineFilesystemBuilderValidation(t *testing.T) {
	t.Parallel()

	builder := &engineFilesystemBuilder{}
	if _, _, err := builder.Build(nil, 1, mountfs.Options{}); err == nil {
		t.Fatal("Build(nil) error = nil")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := builder.Build(canceled, 1, mountfs.Options{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Build(canceled) error = %v", err)
	}
	if _, _, err := builder.Build(context.Background(), 1, mountfs.Options{}); err == nil {
		t.Fatal("Build() without dependencies error = nil")
	}
}

func TestProjectionFolderID(t *testing.T) {
	t.Parallel()

	if got, err := projectionFolderID(mountfs.RootID); err != nil || got != projection.RootParent {
		t.Fatalf("root projectionFolderID() = (%q, %v)", got, err)
	}
	if got, err := projectionFolderID("d:photos"); err != nil || got != "d:photos" {
		t.Fatalf("folder projectionFolderID() = (%q, %v)", got, err)
	}
	for _, invalid := range []string{"d:", "f:2", "photos"} {
		if _, err := projectionFolderID(invalid); err == nil {
			t.Fatalf("projectionFolderID(%q) error = nil", invalid)
		}
	}
}

func newControllerProjectionDB(t *testing.T, channelID int64) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err := projection.MigratePersonalChannel(db, channelID); err != nil {
		t.Fatalf("MigratePersonalChannel() error = %v", err)
	}
	return db
}

func applyControllerProjectionOp(t *testing.T, db *sql.DB, channelID, messageID int64, op projection.Op) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := projection.ApplyOp(tx, channelID, messageID, op, 0); err != nil {
		t.Fatalf("ApplyOp(%s) error = %v", op.Type, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func controllerEntriesByID(entries []mountfs.SourceEntry) map[string]mountfs.SourceEntry {
	out := make(map[string]mountfs.SourceEntry, len(entries))
	for _, entry := range entries {
		out[entry.ID] = entry
	}
	return out
}

type controllerPeerResolver struct{}

func (controllerPeerResolver) ResolvePeer(_ context.Context, channelID int64) (tgclient.InputPeer, error) {
	return tgclient.InputPeer{ChannelID: channelID, AccessHash: 42}, nil
}

type controllerRangeClient struct {
	size int64
}

func (client controllerRangeClient) ResolveDocument(_ context.Context, peer tgclient.InputPeer, messageID int64) (tgclient.DocumentRef, error) {
	return tgclient.DocumentRef{Peer: peer, MsgID: messageID, Size: client.size}, nil
}

func (controllerRangeClient) ReadDocumentRange(context.Context, tgclient.DocumentRef, int64, []byte) (int, error) {
	return 0, nil
}
