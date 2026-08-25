package mountcontroller

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"

	"TDrive/backend"
	"TDrive/backend/core"
	"TDrive/backend/mountadapter"
	"TDrive/backend/mountdav"
	"TDrive/backend/mountfs"
	"TDrive/backend/mountwrite"
)

const (
	defaultWriterMaxObjectBytes     int64 = 4 << 30
	defaultWriterMaxAggregateBytes  int64 = 8 << 30
	defaultWriterStagingConcurrency       = 3
	defaultWriterActiveOperations         = 2
	defaultWriterQueuedOperations         = 8
)

type engineWriterBuilder struct {
	engine *core.Engine
}

func newEngineWriterBuilder(engine *core.Engine) WriterBuilder {
	if engine == nil {
		return nil
	}
	return engineWriterBuilder{engine: engine}
}

func (builder engineWriterBuilder) Build(ctx context.Context, drive Drive, filesystem *mountfs.FS, lease MountKeyLease) (WriteSession, error) {
	if ctx == nil {
		return nil, ErrInvalidContext
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if builder.engine == nil || filesystem == nil || backend.DB == nil {
		return nil, fmt.Errorf("%w: writable dependencies are not ready", ErrInvalidConfiguration)
	}
	if drive.ID <= 0 || drive.Kind != DriveKindPersonal {
		return nil, ErrWritableUnavailable
	}
	if drive.Encrypted && !drive.EncryptionUnlocked {
		return nil, ErrEncryptionPasswordRequired
	}
	if drive.Encrypted && lease == nil {
		return nil, ErrEncryptionPasswordRequired
	}
	if !drive.Encrypted && lease != nil {
		return nil, ErrInvalidConfiguration
	}
	slog.Debug("mountcontroller: building writer", "drive_id", drive.ID, "encrypted", drive.Encrypted)
	var masterKeys mountadapter.MasterKeyProvider
	if lease != nil {
		masterKeys = lease
	}

	resolver, err := mountadapter.NewProjectionResolver(backend.DB, drive.ID)
	if err != nil {
		return nil, fmt.Errorf("%w: writable namespace resolver is unavailable", ErrInvalidConfiguration)
	}
	remote, err := mountadapter.NewTelegramRemote(mountadapter.TelegramRemoteConfig{
		DB:       backend.DB,
		DriveID:  drive.ID,
		Files:    builder.engine.FileService(),
		Telegram: builder.engine.Telegram(),
		Peers:    builder.engine,
		ActorID:  builder.engine.ActorID,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: writable Telegram adapter is unavailable", ErrInvalidConfiguration)
	}
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("%w: writable cache directory is unavailable", ErrInvalidConfiguration)
	}

	canonical, err := mountadapter.New(ctx, mountadapter.Config{
		DriveID: drive.ID,
		Policy: mountadapter.DrivePolicy{
			Kind:               drive.Kind,
			Encrypted:          drive.Encrypted,
			EncryptionUnlocked: drive.EncryptionUnlocked,
			Online:             true,
		},
		MasterKeys:           masterKeys,
		DB:                   backend.DB,
		StagingRoot:          writerStagingRoot(cacheRoot, drive.ID),
		Resolver:             resolver,
		Remote:               remote,
		Cache:                filesystem,
		MaxObjectBytes:       defaultWriterMaxObjectBytes,
		MaxAggregateBytes:    defaultWriterMaxAggregateBytes,
		MaxConcurrentStaging: defaultWriterStagingConcurrency,
		MaxActiveOperations:  defaultWriterActiveOperations,
		MaxQueuedOperations:  defaultWriterQueuedOperations,
	})
	if err != nil {
		slog.Warn("mountcontroller: writer construction failed", "drive_id", drive.ID, "error", err)
		return nil, err
	}
	session, err := newAdapterSession(canonical)
	if err != nil {
		_ = canonical.Close(context.Background())
		return nil, err
	}
	slog.Info("mountcontroller: writer built", "drive_id", drive.ID, "encrypted", drive.Encrypted)
	return session, nil
}

func writerStagingRoot(cacheRoot string, driveID int64) string {
	return filepath.Join(cacheRoot, "TDrive", "mount-staging", strconv.FormatInt(driveID, 10))
}

// canonicalWriteSession is implemented by mountadapter.Session. Keeping this
// narrow interface here lets the controller depend only on protocol and
// lifecycle behavior, while mountadapter owns all path, CAS, journal, staging,
// projection, and Telegram mutation logic.
type canonicalWriteSession interface {
	mountdav.WriteCoordinator
	Status() mountwrite.Status
	Drain(context.Context) error
	Close(context.Context) error
}

type adapterSession struct {
	canonicalWriteSession
}

func newAdapterSession(canonical canonicalWriteSession) (*adapterSession, error) {
	if canonical == nil {
		return nil, ErrInvalidConfiguration
	}
	return &adapterSession{canonicalWriteSession: canonical}, nil
}

func (session *adapterSession) WriteStatus() WriteStatus {
	if session == nil || session.canonicalWriteSession == nil {
		return WriteStatus{}
	}
	status := session.Status()
	return WriteStatus{Accepting: status.Accepting, Active: status.Active}
}

var (
	_ WriterBuilder             = engineWriterBuilder{}
	_ WriteSession              = (*adapterSession)(nil)
	_ mountdav.WriteCoordinator = (*adapterSession)(nil)
	_ canonicalWriteSession     = (*mountadapter.Session)(nil)
)
