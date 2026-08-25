package mountadapter

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"TDrive/backend/mountfs"
	"TDrive/backend/mountwrite"
	"TDrive/backend/projection"
)

type DrivePolicy struct {
	Kind               string
	Encrypted          bool
	EncryptionUnlocked bool
	Online             bool
}

// MasterKeyProvider lends a caller-owned key copy for one encrypted write.
// The provider remains owned by the mount controller and is never persisted.
type MasterKeyProvider interface {
	Key() ([]byte, error)
}

type SnapshotCache interface {
	InvalidateDirectories(parentIDs ...string)
	InvalidateSubtree(rootID string)
}

type Config struct {
	DriveID              int64
	Policy               DrivePolicy
	DB                   *sql.DB
	StagingRoot          string
	Resolver             Resolver
	Remote               mountwrite.Remote
	Cache                SnapshotCache
	Engine               Engine
	MaxObjectBytes       int64
	MaxAggregateBytes    int64
	MaxConcurrentStaging int
	MaxActiveOperations  int
	MaxQueuedOperations  int
	MasterKeys           MasterKeyProvider
}

func New(ctx context.Context, config Config) (*Session, error) {
	if ctx == nil {
		return nil, mountwrite.ErrInvalidRequest
	}
	if err := validateDrivePolicy(config.Policy); err != nil {
		return nil, err
	}
	if config.Policy.Encrypted && config.MasterKeys == nil {
		return nil, mountwrite.ErrForbidden
	}
	if !config.Policy.Encrypted && config.MasterKeys != nil {
		return nil, mountwrite.ErrInvalidRequest
	}
	if config.DriveID <= 0 || config.Resolver == nil {
		return nil, mountwrite.ErrInvalidRequest
	}
	engine := config.Engine
	if engine == nil {
		built, err := buildCoordinator(ctx, config)
		if err != nil {
			return nil, fmt.Errorf("mount adapter: initialize coordinator: %w", err)
		}
		engine = built
	}
	maxObjectBytes := config.MaxObjectBytes
	if maxObjectBytes <= 0 {
		maxObjectBytes = defaultMaxObjectBytes
	}
	session := &Session{
		driveID:        config.DriveID,
		resolver:       config.Resolver,
		engine:         engine,
		maxObjectBytes: maxObjectBytes,
		encryptWrites:  config.Policy.Encrypted,
		masterKeys:     config.MasterKeys,
	}
	slog.Info("mountadapter: session constructed, recovering journal", "drive_id", config.DriveID, "encrypted", config.Policy.Encrypted)
	report, err := engine.Recover(ctx)
	session.recoveryReport = report
	if err != nil {
		slog.Error("mountadapter: journal recovery failed", "drive_id", config.DriveID, "error", err)
		_ = session.Close(context.Background())
		return nil, err
	}
	slog.Info("mountadapter: journal recovery completed", "drive_id", config.DriveID,
		"examined", report.Examined, "completed", report.Completed, "pending", report.Pending, "aborted", report.Aborted, "failed", report.Failed)
	return session, nil
}

func validateDrivePolicy(policy DrivePolicy) error {
	if policy.Kind != projection.KindPersonal || !policy.Online {
		return mountwrite.ErrForbidden
	}
	if policy.Encrypted != policy.EncryptionUnlocked {
		return mountwrite.ErrForbidden
	}
	return nil
}

func buildCoordinator(ctx context.Context, config Config) (*mountwrite.Coordinator, error) {
	if config.DB == nil || config.StagingRoot == "" || config.Remote == nil || config.Cache == nil {
		return nil, mountwrite.ErrInvalidRequest
	}
	if err := mountwrite.EnsureJournalSchema(ctx, config.DB); err != nil {
		return nil, fmt.Errorf("initialize journal schema: %w", err)
	}
	journal, err := mountwrite.NewSQLiteJournal(config.DB)
	if err != nil {
		return nil, fmt.Errorf("initialize journal: %w", err)
	}
	staging, err := mountwrite.NewDiskStagingStore(mountwrite.DiskStagingConfig{
		Root:              config.StagingRoot,
		MaxObjectBytes:    positiveInt64(config.MaxObjectBytes, defaultMaxObjectBytes),
		MaxAggregateBytes: positiveInt64(config.MaxAggregateBytes, defaultMaxObjectBytes),
		MaxConcurrent:     positiveInt(config.MaxConcurrentStaging, 1),
	})
	if err != nil {
		return nil, fmt.Errorf("initialize staging: %w", err)
	}
	invalidator, err := NewSnapshotInvalidator(config.DriveID, config.Cache)
	if err != nil {
		return nil, fmt.Errorf("initialize cache invalidator: %w", err)
	}
	return mountwrite.NewCoordinator(mountwrite.CoordinatorConfig{
		Journal:             journal,
		Staging:             staging,
		Remote:              config.Remote,
		Invalidator:         invalidator,
		MaxActiveOperations: positiveInt(config.MaxActiveOperations, 2),
		MaxQueuedOperations: positiveInt(config.MaxQueuedOperations, 16),
	})
}

type SnapshotInvalidator struct {
	driveID int64
	cache   SnapshotCache
}

func NewSnapshotInvalidator(driveID int64, cache SnapshotCache) (SnapshotInvalidator, error) {
	if driveID <= 0 || cache == nil {
		return SnapshotInvalidator{}, mountwrite.ErrInvalidRequest
	}
	return SnapshotInvalidator{driveID: driveID, cache: cache}, nil
}

func (invalidator SnapshotInvalidator) Invalidate(ctx context.Context, invalidation mountwrite.SnapshotInvalidation) error {
	if ctx == nil {
		return mountwrite.ErrInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if invalidation.DriveID != invalidator.driveID {
		return mountwrite.ErrForbidden
	}
	invalidator.cache.InvalidateDirectories(invalidation.ParentIDs...)
	for _, objectID := range invalidation.ObjectIDs {
		invalidator.cache.InvalidateSubtree(objectID)
	}
	return nil
}

func positiveInt(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func positiveInt64(value, fallback int64) int64 {
	if value > 0 {
		return value
	}
	return fallback
}

var _ mountwrite.SnapshotInvalidator = SnapshotInvalidator{}
var _ SnapshotCache = (*mountfs.FS)(nil)
