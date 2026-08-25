package daemon

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"TDrive/backend"
	"TDrive/backend/core"
	"TDrive/backend/media"
	"TDrive/backend/mountcontent"
	"TDrive/backend/mountdav"
	"TDrive/backend/mountfs"
	"TDrive/backend/projection"
	readservice "TDrive/backend/services/read"
	"TDrive/backend/tgclient"

	"github.com/gotd/td/telegram"
	_ "modernc.org/sqlite"
)

func TestStartMountPinsSelectedDriveWithoutChangingActiveDrive(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	previousDB := backend.DB
	backend.DB = db
	defer func() {
		backend.DB = previousDB
		_ = db.Close()
	}()

	const (
		activeDriveID   int64 = 8_100_001
		selectedDriveID int64 = 8_100_002
	)
	if err := projection.MigratePersonalChannel(db, activeDriveID); err != nil {
		t.Fatalf("migrate active drive: %v", err)
	}
	if err := projection.InsertChannel(db, projection.Channel{
		ChannelID:            selectedDriveID,
		AccessHash:           42,
		Title:                "Pinned Drive",
		Kind:                 projection.KindShared,
		PersonalBackfillDone: true,
	}); err != nil {
		t.Fatalf("insert selected drive: %v", err)
	}

	fakeTelegram := tgclient.NewFake(1)
	fakeTelegram.SeedChannel(tgclient.InputPeer{ChannelID: activeDriveID, AccessHash: 41}, "Active Drive")
	fakeTelegram.SeedChannel(tgclient.InputPeer{ChannelID: selectedDriveID, AccessHash: 42}, "Pinned Drive")
	engine, err := core.New(context.Background(), core.Config{
		TG:         fakeTelegram,
		SkipDBInit: true,
		Connect: func() (*telegram.Client, error) {
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	defer engine.Close()
	engine.SetActiveChannelID(activeDriveID)

	server := &Server{engine: engine, mount: mountdav.NewServer()}
	defer func() {
		if err := server.stopMountServer(context.Background()); err != nil {
			t.Errorf("cleanup mount: %v", err)
		}
	}()

	started, err := server.startMount(context.Background(), fmt.Sprint(selectedDriveID), "q")
	if err != nil {
		t.Fatalf("start selected mount: %v", err)
	}
	if !started.Running || started.Drive.ID != selectedDriveID || started.WindowsDrive != "Q:" || started.URL == "" {
		t.Fatalf("started mount = %#v", started)
	}
	if got := engine.ActiveChannelID(); got != activeDriveID {
		t.Fatalf("active drive changed to %d, want %d", got, activeDriveID)
	}

	repeated, err := server.startMount(context.Background(), "", "")
	if err != nil {
		t.Fatalf("repeat mount start: %v", err)
	}
	if repeated.URL != started.URL || repeated.Drive.ID != selectedDriveID {
		t.Fatalf("repeated mount = %#v, want pinned status %#v", repeated, started)
	}
	if _, err := server.startMount(context.Background(), fmt.Sprint(activeDriveID), ""); err == nil {
		t.Fatal("changing the pinned drive without stopping returned no error")
	}
	if got := engine.ActiveChannelID(); got != activeDriveID {
		t.Fatalf("active drive after conflict = %d, want %d", got, activeDriveID)
	}

	stopped, err := server.stopMount(context.Background())
	if err != nil {
		t.Fatalf("stop mount: %v", err)
	}
	if stopped.Running || server.mountContent != nil {
		t.Fatalf("stopped mount retained state: response=%#v opener=%p", stopped, server.mountContent)
	}
}

func TestMountDirectorySourcePreservesProjectionIDsAndLogicalSizes(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	const channelID int64 = 7001
	if err := projection.MigratePersonalChannel(db, channelID); err != nil {
		t.Fatalf("migrate projection: %v", err)
	}
	applyMountProjectionOp(t, db, channelID, 1, projection.Op{
		Type:   projection.OpMkdir,
		Obj:    "d:photos",
		Parent: projection.RootParent,
		Name:   "Photos",
	})
	applyMountProjectionOp(t, db, channelID, 2, projection.Op{
		Type:           projection.OpFileUpload,
		Parent:         projection.RootParent,
		Name:           "plain.txt",
		FileSize:       12,
		FileUploadTime: 1_700_000_000,
	})
	applyMountProjectionOp(t, db, channelID, 3, projection.Op{
		Type:              projection.OpFileUpload,
		Parent:            "d:photos",
		Name:              "secret.bin",
		FileSize:          100,
		FileUploadTime:    1_700_000_001,
		Encrypted:         true,
		PlaintextSize:     60,
		EncryptionVersion: 1,
	})

	source := mountDirectorySource{reads: &readservice.Service{DB: db}}
	root, err := source.ListDirectory(context.Background(), channelID, mountfs.RootID)
	if err != nil {
		t.Fatalf("list root: %v", err)
	}
	rootByID := mountEntriesByID(root)
	if got := rootByID["d:photos"]; got.Kind != mountfs.KindDirectory || got.ParentID != mountfs.RootID {
		t.Fatalf("folder entry = %#v", got)
	}
	if got := rootByID["f:2"]; got.Kind != mountfs.KindFile || got.Size != 12 || got.ContentRef != "2" {
		t.Fatalf("plain file entry = %#v", got)
	}
	if got := rootByID["f:2"].ModTime; !got.Equal(time.Unix(1_700_000_000, 0)) {
		t.Fatalf("plain file ModTime = %v", got)
	}
	opener, err := mountcontent.New(mountcontent.Config{
		DB:     db,
		Peers:  mountTestPeerResolver{},
		Ranges: mountTestRangeClient{size: 12},
	})
	if err != nil {
		t.Fatalf("create content opener: %v", err)
	}
	t.Cleanup(opener.Close)
	opened, err := (mountContentAdapter{opener: opener}).OpenContent(context.Background(), channelID, rootByID["f:2"])
	if err != nil {
		t.Fatalf("open projected content: %v", err)
	}
	if err := opened.Close(); err != nil {
		t.Fatalf("close projected content: %v", err)
	}
	staleEntry := rootByID["f:2"]
	staleEntry.Size++
	if _, err := (mountContentAdapter{opener: opener}).OpenContent(context.Background(), channelID, staleEntry); !errors.Is(err, mountfs.ErrContentUnavailable) {
		t.Fatalf("stale projection size error = %v, want ErrContentUnavailable", err)
	}

	nested, err := source.ListDirectory(context.Background(), channelID, "d:photos")
	if err != nil {
		t.Fatalf("list nested directory: %v", err)
	}
	if len(nested) != 1 {
		t.Fatalf("nested entry count = %d, want 1", len(nested))
	}
	if got := nested[0]; got.ID != "f:3" || got.ParentID != "d:photos" || got.Size != 60 || !got.Encrypted {
		t.Fatalf("encrypted file entry = %#v", got)
	}
}

func TestMountDirectorySourceSanitizesProjectionFailure(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	source := mountDirectorySource{reads: &readservice.Service{DB: db}}
	_, err = source.ListDirectory(context.Background(), 7001, mountfs.RootID)
	if !errors.Is(err, mountfs.ErrContentUnavailable) {
		t.Fatalf("closed projection error = %v, want ErrContentUnavailable", err)
	}
	if strings.Contains(strings.ToLower(err.Error()), "database is closed") {
		t.Fatalf("projection error leaked internal database detail: %v", err)
	}
}

func TestMountDirectorySourcePreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := (mountDirectorySource{}).ListDirectory(ctx, 7001, mountfs.RootID)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled directory read error = %v, want context.Canceled", err)
	}
	if errors.Is(err, mountfs.ErrContentUnavailable) {
		t.Fatalf("canceled directory read was mapped to content unavailable: %v", err)
	}
}

func TestMapMountDirectoryError(t *testing.T) {
	t.Parallel()

	canceledContext, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name   string
		ctx    context.Context
		err    error
		target error
	}{
		{name: "nil", ctx: context.Background()},
		{name: "caller canceled", ctx: canceledContext, err: errors.New("database detail"), target: context.Canceled},
		{name: "operation deadline", ctx: context.Background(), err: context.DeadlineExceeded, target: context.DeadlineExceeded},
		{name: "permission", ctx: context.Background(), err: os.ErrPermission, target: mountfs.ErrAccessDenied},
		{name: "database failure", ctx: context.Background(), err: errors.New("secret database detail"), target: mountfs.ErrContentUnavailable},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := mapMountDirectoryError(test.ctx, test.err)
			if test.target == nil {
				if got != nil {
					t.Fatalf("mapMountDirectoryError(nil) = %v", got)
				}
				return
			}
			if !errors.Is(got, test.target) {
				t.Fatalf("mapMountDirectoryError(%v) = %v, want %v", test.err, got, test.target)
			}
			if strings.Contains(got.Error(), "secret database detail") {
				t.Fatalf("mapped error leaked internal detail: %v", got)
			}
		})
	}
}

func TestProjectionFolderID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mountID string
		want    string
		wantErr bool
	}{
		{name: "root", mountID: mountfs.RootID, want: projection.RootParent},
		{name: "projected folder", mountID: "d:folder", want: "d:folder"},
		{name: "file id", mountID: "f:2", wantErr: true},
		{name: "empty folder id", mountID: "d:", wantErr: true},
		{name: "unprefixed", mountID: "folder", wantErr: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := projectionFolderID(test.mountID)
			if test.wantErr {
				if err == nil {
					t.Fatalf("projectionFolderID(%q) error = nil", test.mountID)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("projectionFolderID(%q) = (%q, %v), want (%q, nil)", test.mountID, got, err, test.want)
			}
		})
	}
}

func TestNormalizeRequestedWindowsDrive(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{input: "", want: ""},
		{input: " t ", want: "T:"},
		{input: "q:", want: "Q:"},
		{input: `T:\`, wantErr: true},
		{input: "TT:", wantErr: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.input, func(t *testing.T) {
			t.Parallel()
			got, err := normalizeRequestedWindowsDrive(test.input)
			if test.wantErr {
				if err == nil {
					t.Fatalf("normalizeRequestedWindowsDrive(%q) error = nil", test.input)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("normalizeRequestedWindowsDrive(%q) = (%q, %v), want (%q, nil)", test.input, got, err, test.want)
			}
		})
	}
}

func TestMountContentAdapterRejectsUnavailableContent(t *testing.T) {
	t.Parallel()

	adapter := mountContentAdapter{}
	_, err := adapter.OpenContent(context.Background(), 1, mountfs.SourceEntry{ContentRef: "1"})
	if !errors.Is(err, mountfs.ErrContentUnavailable) {
		t.Fatalf("nil opener error = %v", err)
	}

	opener := newMountTestOpener(t)
	adapter.opener = opener
	_, err = adapter.OpenContent(context.Background(), 1, mountfs.SourceEntry{Encrypted: true, ContentRef: "1"})
	if !errors.Is(err, mountfs.ErrAccessDenied) {
		t.Fatalf("encrypted content error = %v", err)
	}
	_, err = adapter.OpenContent(context.Background(), 1, mountfs.SourceEntry{ContentRef: "not-a-message"})
	if !errors.Is(err, mountfs.ErrContentUnavailable) {
		t.Fatalf("invalid content reference error = %v", err)
	}
}

func TestMapMountContentOpenError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		err    error
		target error
	}{
		{name: "nil", err: nil, target: nil},
		{name: "canceled", err: context.Canceled, target: context.Canceled},
		{name: "deadline", err: context.DeadlineExceeded, target: context.DeadlineExceeded},
		{name: "encrypted", err: mountcontent.ErrEncryptedUnsupported, target: mountfs.ErrAccessDenied},
		{name: "media encrypted", err: media.ErrEncryptedUnsupported, target: mountfs.ErrAccessDenied},
		{name: "projection missing", err: media.ErrFileNotFound, target: mountfs.ErrNotFound},
		{name: "message missing", err: tgclient.ErrMessageNotFound, target: mountfs.ErrNotFound},
		{name: "message is not a file", err: tgclient.ErrNotFile, target: mountfs.ErrNotFound},
		{name: "empty document", err: tgclient.ErrEmptyDocument, target: mountfs.ErrNotFound},
		{name: "network or integrity failure", err: errors.New("secret Telegram detail"), target: mountfs.ErrContentUnavailable},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := mapMountContentOpenError(test.err)
			if test.target == nil {
				if got != nil {
					t.Fatalf("mapMountContentOpenError(nil) = %v", got)
				}
				return
			}
			if !errors.Is(got, test.target) {
				t.Fatalf("mapMountContentOpenError(%v) = %v, want %v", test.err, got, test.target)
			}
			if strings.Contains(got.Error(), "secret Telegram detail") {
				t.Fatalf("mapped error leaked internal detail: %v", got)
			}
		})
	}
}

func TestStopMountServerClosesAndReleasesContentOpener(t *testing.T) {
	opener := newMountTestOpener(t)
	server := &Server{
		mount:        mountdav.NewServer(),
		mountContent: opener,
	}

	if err := server.stopMountServer(context.Background()); err != nil {
		t.Fatalf("stop mount: %v", err)
	}
	if server.mountContent != nil {
		t.Fatal("mount content opener was retained after stop")
	}
	if _, err := opener.Open(context.Background(), 1, 1); !errors.Is(err, mountcontent.ErrClosed) {
		t.Fatalf("opener.Open after stop error = %v, want ErrClosed", err)
	}
	if err := server.stopMountServer(context.Background()); err != nil {
		t.Fatalf("second stop mount: %v", err)
	}
}

func TestMountStatusReleasesContentAfterUnexpectedServerStop(t *testing.T) {
	opener := newMountTestOpener(t)
	server := &Server{
		mount:        mountdav.NewServer(),
		mountContent: opener,
	}

	status := server.mountStatus()
	if status.Running {
		t.Fatal("mount status unexpectedly running")
	}
	if server.mountContent != nil {
		t.Fatal("stale mount content opener was retained")
	}
	if _, err := opener.Open(context.Background(), 1, 1); !errors.Is(err, mountcontent.ErrClosed) {
		t.Fatalf("opener.Open after stale cleanup error = %v, want ErrClosed", err)
	}
}

func TestEnsureCompatibleWindowsDrive(t *testing.T) {
	t.Parallel()

	status := mountdav.Status{Running: true, WindowsDrive: "Q:"}
	if err := ensureCompatibleWindowsDrive(status, "Q:"); err != nil {
		t.Fatalf("same Windows drive: %v", err)
	}
	if err := ensureCompatibleWindowsDrive(status, ""); err != nil {
		t.Fatalf("unspecified Windows drive: %v", err)
	}
	if err := ensureCompatibleWindowsDrive(mountdav.Status{}, "T:"); err != nil {
		t.Fatalf("stopped mount: %v", err)
	}
	if err := ensureCompatibleWindowsDrive(status, "T:"); err == nil {
		t.Fatal("conflicting Windows drive error = nil")
	}
}

func applyMountProjectionOp(t *testing.T, db *sql.DB, channelID, messageID int64, op projection.Op) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := projection.ApplyOp(tx, channelID, messageID, op, 0); err != nil {
		t.Fatalf("apply %s: %v", op.Type, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func mountEntriesByID(entries []mountfs.SourceEntry) map[string]mountfs.SourceEntry {
	out := make(map[string]mountfs.SourceEntry, len(entries))
	for _, entry := range entries {
		out[entry.ID] = entry
	}
	return out
}

func newMountTestOpener(t *testing.T) *mountcontent.Opener {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	opener, err := mountcontent.New(mountcontent.Config{
		DB:     db,
		Peers:  mountTestPeerResolver{},
		Ranges: mountTestRangeClient{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(opener.Close)
	return opener
}

type mountTestPeerResolver struct{}

func (mountTestPeerResolver) ResolvePeer(context.Context, int64) (tgclient.InputPeer, error) {
	return tgclient.InputPeer{}, nil
}

type mountTestRangeClient struct {
	size int64
}

func (client mountTestRangeClient) ResolveDocument(_ context.Context, _ tgclient.InputPeer, messageID int64) (tgclient.DocumentRef, error) {
	return tgclient.DocumentRef{MsgID: messageID, Size: client.size}, nil
}

func (mountTestRangeClient) ReadDocumentRange(context.Context, tgclient.DocumentRef, int64, []byte) (int, error) {
	return 0, nil
}
