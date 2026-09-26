// Package lifecycle owns which drive is active and the coarse refreshes over
// it: activation, incremental sync, full projection rebuild, and kicking off
// the personal backfill. It implements none of that work — the syncer, the
// backfiller and the rebuild function are all injected. It does not manage
// application startup, shutdown or mounts, despite the name.
//
// The ordering rule worth knowing is snapshot invalidation. Incremental sync
// commits one page at a time, so mounted snapshots are invalidated on both
// success and failure; a rebuild is all-or-nothing and invalidates only on
// success. Reconciling files whose Telegram messages were deleted outside
// TDrive runs only after a successful incremental and is best-effort, so it can
// never turn a good sync into a failed one.
//
// Backfill is fire-and-forget but not unbounded: at most one goroutine per
// channel exists at a time, and a panic inside it is recovered into an error
// event rather than taking the process down. It inherits the context of the
// call that started it, so there is no separate stop.
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

type RebuildFunc func(db *sql.DB, channelID int64) error
type WarnFunc func(format string, args ...any)

type Config struct {
	DB                  *sql.DB
	Sync                Syncer
	Backfill            Backfiller
	Active              *ActiveDrive
	Events              EventSink
	Rebuild             RebuildFunc
	Warnf               WarnFunc
	OnProjectionChanged func(channelID int64)
}

type Service struct {
	DB                  *sql.DB
	Sync                Syncer
	Backfill            Backfiller
	Active              *ActiveDrive
	Events              EventSink
	Rebuild             RebuildFunc
	Warnf               WarnFunc
	OnProjectionChanged func(channelID int64)

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
		DB:                  c.DB,
		Sync:                c.Sync,
		Backfill:            c.Backfill,
		Active:              c.Active,
		Events:              c.Events,
		Rebuild:             c.Rebuild,
		Warnf:               c.Warnf,
		OnProjectionChanged: c.OnProjectionChanged,
		backfilling:         make(map[int64]bool),
	}
}

func (s *Service) UsePersonalChannel(ctx context.Context, channelID int64) error {
	if err := s.RestorePersonalChannel(channelID); err != nil {
		return err
	}
	s.kickoffPersonalBackfill(ctx, channelID)
	return nil
}

// RestorePersonalChannel applies saved local state without opening Telegram.
// Network work resumes only after the caller has verified the login session.
func (s *Service) RestorePersonalChannel(channelID int64) error {
	if channelID == 0 || s.DB == nil {
		return nil
	}
	if err := projection.MigratePersonalChannel(s.DB, channelID); err != nil {
		slog.Error("lifecycle: migrate personal channel failed", "channel_id", channelID, "error", err)
		return err
	}
	s.Active.Set(channelID)
	slog.Info("lifecycle: active drive set", "channel_id", channelID)
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
	// Incremental sync commits one page at a time. Even if a later page fails,
	// earlier projection changes are durable, so mounted snapshots must be
	// invalidated before this call returns on either outcome.
	if s.OnProjectionChanged != nil {
		defer s.OnProjectionChanged(channelID)
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
	if s.OnProjectionChanged != nil {
		s.OnProjectionChanged(channelID)
	}
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
