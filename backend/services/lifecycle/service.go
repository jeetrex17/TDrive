package lifecycle

import (
	"context"
	"database/sql"
	"fmt"
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
		return err
	}
	s.Active.Set(channelID)
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
	return s.Sync.Incremental(ctx, channelID)
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
	return s.Rebuild(s.DB, channelID)
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

	go func() {
		defer func() {
			s.backfillMu.Lock()
			delete(s.backfilling, channelID)
			s.backfillMu.Unlock()
		}()
		err := s.Backfill.RunPersonal(ctx, channelID, func(ev backfill.ProgressEvent) {
			s.emit("backfill_progress", ev.ChannelID, ev.Done, ev.Total, ev.Phase)
		})
		if err != nil {
			s.warnf("backfill: %v\n", err)
			s.emit("backfill_error", channelID, err.Error())
		}
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
