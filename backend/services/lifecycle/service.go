package lifecycle

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"TDrive/backend/backfill"
	"TDrive/backend/projection"
)

type ActiveDrive struct {
	id atomic.Int64
}

func NewActiveDrive() *ActiveDrive {
	return &ActiveDrive{}
}

func (a *ActiveDrive) ID() int64 {
	if a == nil {
		return 0
	}
	return a.id.Load()
}

func (a *ActiveDrive) Set(id int64) {
	if a == nil {
		return
	}
	a.id.Store(id)
}

type Syncer interface {
	Incremental(ctx context.Context, channelID int64) error

	// ReconcileDeletions tombstones any locally-live file whose backing
	// Telegram message(s) were deleted directly on Telegram, bypassing
	// TDrive's own delete path. Returns how many files it tombstoned.
	ReconcileDeletions(ctx context.Context, channelID int64) (int, error)
}

type Backfiller interface {
	RunPersonal(ctx context.Context, channelID int64, onProgress func(backfill.ProgressEvent)) error
}

type EventSink interface {
	Emit(name string, args ...any)
}

type PersonalChannelFunc func(ctx context.Context) (int64, error)
type RebuildFunc func(db *sql.DB, channelID int64) error
type WarnFunc func(format string, args ...any)

type Config struct {
	DB              *sql.DB
	Sync            Syncer
	Backfill        Backfiller
	Active          *ActiveDrive
	Events          EventSink
	PersonalChannel PersonalChannelFunc
	Rebuild         RebuildFunc
	Warnf           WarnFunc
}

type Service struct {
	DB              *sql.DB
	Sync            Syncer
	Backfill        Backfiller
	Active          *ActiveDrive
	Events          EventSink
	PersonalChannel PersonalChannelFunc
	Rebuild         RebuildFunc
	Warnf           WarnFunc

	backfillMu  sync.Mutex
	backfilling map[int64]bool
}

func NewService(c Config) *Service {
	if c.Active == nil {
		c.Active = NewActiveDrive()
	}
	if c.Rebuild == nil {
		c.Rebuild = projection.RebuildProjection
	}
	return &Service{
		DB:              c.DB,
		Sync:            c.Sync,
		Backfill:        c.Backfill,
		Active:          c.Active,
		Events:          c.Events,
		PersonalChannel: c.PersonalChannel,
		Rebuild:         c.Rebuild,
		Warnf:           c.Warnf,
		backfilling:     make(map[int64]bool),
	}
}

func (s *Service) InitDrive(ctx context.Context) string {
	if ctx == nil {
		return "Error: App context not ready"
	}
	if s.PersonalChannel == nil {
		return "Error: personal drive resolver not ready"
	}

	channelID, err := s.PersonalChannel(ctx)
	if err != nil {
		return "Error: " + err.Error()
	}
	if err := s.UsePersonalChannel(ctx, channelID); err != nil {
		return "Error: migration failed: " + err.Error()
	}
	return fmt.Sprintf("Success , channel ID: %d", channelID)
}

func (s *Service) UsePersonalChannel(ctx context.Context, channelID int64) error {
	if channelID == 0 || s.DB == nil {
		return nil
	}
	if err := projection.MigratePersonalChannel(s.DB, channelID); err != nil {
		slog.Error("lifecycle: migrate personal channel failed", "channel_id", channelID, "error", err)
		return err
	}
	s.Active.Set(channelID)
	slog.Info("lifecycle: active drive set", "channel_id", channelID)
	s.kickoffPersonalBackfill(ctx, channelID)
	return nil
}

func (s *Service) SyncChannel(ctx context.Context, channelID int64) error {
	if s.Sync == nil {
		return fmt.Errorf("sync engine not ready")
	}
	if channelID == 0 {
		channelID = s.Active.ID()
	}
	if channelID == 0 {
		return fmt.Errorf("no active channel")
	}
	slog.Debug("lifecycle: incremental sync starting", "channel_id", channelID)
	err := s.Sync.Incremental(ctx, channelID)
	if err != nil {
		slog.Warn("lifecycle: incremental sync failed", "channel_id", channelID, "error", err)
		return err
	}
	slog.Debug("lifecycle: incremental sync complete", "channel_id", channelID)

	// Best-effort: a failure here just means an external delete goes
	// undetected until the next sync pass, not that this sync failed.
	if n, err := s.Sync.ReconcileDeletions(ctx, channelID); err != nil {
		slog.Warn("lifecycle: reconcile deletions failed", "channel_id", channelID, "error", err)
	} else if n > 0 {
		slog.Info("lifecycle: reconciled externally deleted files", "channel_id", channelID, "count", n)
	}
	return nil
}

func (s *Service) RebuildProjection(channelID int64) error {
	if s.DB == nil {
		return fmt.Errorf("db not ready")
	}
	if channelID == 0 {
		channelID = s.Active.ID()
	}
	if channelID == 0 {
		return fmt.Errorf("no active channel")
	}
	slog.Info("lifecycle: full projection rebuild starting", "channel_id", channelID)
	if err := s.Rebuild(s.DB, channelID); err != nil {
		slog.Error("lifecycle: projection rebuild failed", "channel_id", channelID, "error", err)
		return err
	}
	slog.Info("lifecycle: full projection rebuild complete", "channel_id", channelID)
	return nil
}

func (s *Service) kickoffPersonalBackfill(ctx context.Context, channelID int64) {
	if s.Backfill == nil || channelID == 0 {
		return
	}

	s.backfillMu.Lock()
	if s.backfilling == nil {
		s.backfilling = make(map[int64]bool)
	}
	if s.backfilling[channelID] {
		s.backfillMu.Unlock()
		return
	}
	s.backfilling[channelID] = true
	s.backfillMu.Unlock()

	slog.Info("lifecycle: personal backfill starting", "channel_id", channelID)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("lifecycle: personal backfill panicked", "channel_id", channelID, "recovered", r)
				s.warnf("backfill panic: %v\n", r)
				s.emit("backfill_error", channelID, fmt.Sprintf("backfill panic: %v", r))
			}
			s.backfillMu.Lock()
			delete(s.backfilling, channelID)
			s.backfillMu.Unlock()
		}()
		err := s.Backfill.RunPersonal(ctx, channelID, func(ev backfill.ProgressEvent) {
			s.emit("backfill_progress", ev.ChannelID, ev.Done, ev.Total, ev.Phase)
		})
		if err != nil {
			slog.Warn("lifecycle: personal backfill failed", "channel_id", channelID, "error", err)
			s.warnf("backfill: %v\n", err)
			s.emit("backfill_error", channelID, err.Error())
			return
		}
		slog.Info("lifecycle: personal backfill complete", "channel_id", channelID)
	}()
}

func (s *Service) emit(name string, args ...any) {
	if s.Events != nil {
		s.Events.Emit(name, args...)
	}
}

func (s *Service) warnf(format string, args ...any) {
	if s.Warnf != nil {
		s.Warnf(format, args...)
		return
	}
	fmt.Printf(format, args...)
}
