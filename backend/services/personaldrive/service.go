// Package personaldrive coordinates explicit selection or creation of the
// Telegram broadcast channel used as the user's personal drive.
package personaldrive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"TDrive/backend/auth"
	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

const (
	StatusReady             = "ready"
	StatusSelectionRequired = "selection_required"

	defaultChannelTitle = "TDrive"
	defaultChannelAbout = "TDrive personal storage"
)

var (
	ErrInvalidSelection = errors.New("personaldrive: invalid channel selection")
	ErrCandidateMissing = errors.New("personaldrive: selected channel is no longer available")
)

type Telegram interface {
	ListOwnedBroadcastChannels(ctx context.Context) ([]tgclient.OwnedBroadcastChannel, error)
	CreateBroadcastChannel(ctx context.Context, title, about string) (tgclient.OwnedBroadcastChannel, error)
}

type AuthoritativeSync interface {
	EnsureAuthoritative(ctx context.Context, channelID int64) error
}

type Candidate struct {
	ID          int64
	Title       string
	CreatedAt   int64
	HasActivity bool
	Recommended bool
}

type State struct {
	Status     string
	ChannelID  int64
	Candidates []Candidate
}

type Config struct {
	DB         *sql.DB
	Telegram   Telegram
	Sync       AuthoritativeSync
	LoadConfig func() (int64, error)
	SaveConfig func(int64) error
	UseSaved   func(context.Context, int64) error
	SetActive  func(int64)
}

type Service struct {
	db         *sql.DB
	telegram   Telegram
	syncer     AuthoritativeSync
	loadConfig func() (int64, error)
	saveConfig func(int64) error
	useSaved   func(context.Context, int64) error
	setActive  func(int64)

	mu             sync.Mutex
	pendingCreated *tgclient.OwnedBroadcastChannel
}

func NewService(config Config) *Service {
	loadConfig := config.LoadConfig
	if loadConfig == nil {
		loadConfig = auth.LoadConfig
	}
	saveConfig := config.SaveConfig
	if saveConfig == nil {
		saveConfig = auth.SaveConfig
	}
	return &Service{
		db:         config.DB,
		telegram:   config.Telegram,
		syncer:     config.Sync,
		loadConfig: loadConfig,
		saveConfig: saveConfig,
		useSaved:   config.UseSaved,
		setActive:  config.SetActive,
	}
}

// Prepare takes the saved-config fast path when available. Otherwise it
// performs read-only discovery and returns an explicit selection state.
func (s *Service) Prepare(ctx context.Context) (State, error) {
	channelID, err := s.loadConfiguredChannel()
	if err != nil {
		return State{}, err
	}
	if channelID != 0 {
		if s.useSaved == nil {
			return State{}, fmt.Errorf("personaldrive: saved-channel activation is unavailable")
		}
		if err := s.useSaved(ctx, channelID); err != nil {
			return State{}, fmt.Errorf("personaldrive: activate saved channel: %w", err)
		}
		return State{Status: StatusReady, ChannelID: channelID}, nil
	}
	if s.telegram == nil {
		return State{}, fmt.Errorf("personaldrive: Telegram client is unavailable")
	}
	channels, err := s.telegram.ListOwnedBroadcastChannels(ctx)
	if err != nil {
		return State{}, fmt.Errorf("personaldrive: discover channels: %w", err)
	}
	slog.Info("personaldrive: channel discovery completed", "candidate_count", len(channels))
	return State{
		Status:     StatusSelectionRequired,
		Candidates: candidatesFromOwned(channels),
	}, nil
}

// Select revalidates the ID against a fresh creator-owned channel list before
// touching local state. IDs supplied by UI or daemon callers are untrusted.
func (s *Service) Select(ctx context.Context, channelID int64) error {
	if channelID <= 0 {
		return ErrInvalidSelection
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	configured, err := s.loadConfiguredChannel()
	if err != nil {
		return err
	}
	if configured != 0 {
		return s.activateConfigured(ctx, configured)
	}
	if s.telegram == nil {
		return fmt.Errorf("personaldrive: Telegram client is unavailable")
	}
	channels, err := s.telegram.ListOwnedBroadcastChannels(ctx)
	if err != nil {
		return fmt.Errorf("personaldrive: revalidate channels: %w", err)
	}
	for _, channel := range channels {
		if channel.ID == channelID {
			return s.recover(ctx, channel)
		}
	}
	return ErrCandidateMissing
}

// Create is the only path that creates a remote channel. A successful remote
// creation is retained in memory until local recovery commits, preventing a
// retry after a sync/config failure from creating duplicate channels.
func (s *Service) Create(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	configured, err := s.loadConfiguredChannel()
	if err != nil {
		return err
	}
	if configured != 0 {
		return s.activateConfigured(ctx, configured)
	}
	if s.telegram == nil {
		return fmt.Errorf("personaldrive: Telegram client is unavailable")
	}
	if s.pendingCreated == nil {
		created, err := s.telegram.CreateBroadcastChannel(ctx, defaultChannelTitle, defaultChannelAbout)
		if err != nil {
			return fmt.Errorf("personaldrive: create channel: %w", err)
		}
		if created.ID <= 0 {
			return fmt.Errorf("personaldrive: create channel returned an invalid id")
		}
		copy := created
		s.pendingCreated = &copy
	}
	if err := s.recover(ctx, *s.pendingCreated); err != nil {
		return err
	}
	s.pendingCreated = nil
	return nil
}

func (s *Service) activateConfigured(ctx context.Context, channelID int64) error {
	if s.useSaved == nil {
		return fmt.Errorf("personaldrive: saved-channel activation is unavailable")
	}
	if err := s.useSaved(ctx, channelID); err != nil {
		return fmt.Errorf("personaldrive: activate saved channel: %w", err)
	}
	return nil
}

func (s *Service) recover(ctx context.Context, channel tgclient.OwnedBroadcastChannel) (returnErr error) {
	if s.db == nil || s.syncer == nil || s.saveConfig == nil || s.setActive == nil {
		return fmt.Errorf("personaldrive: recovery dependencies are unavailable")
	}
	if channel.ID <= 0 {
		return ErrInvalidSelection
	}
	title := strings.TrimSpace(channel.Title)
	if title == "" {
		title = "Untitled channel"
	}
	wasRegistered, err := projection.ChannelExists(s.db, channel.ID)
	if err != nil {
		return fmt.Errorf("personaldrive: inspect channel registration: %w", err)
	}
	if !wasRegistered {
		defer func() {
			if returnErr == nil {
				return
			}
			exists, existsErr := projection.ChannelExists(s.db, channel.ID)
			if existsErr != nil {
				returnErr = errors.Join(returnErr, fmt.Errorf("personaldrive: inspect provisional channel for rollback: %w", existsErr))
				return
			}
			if !exists {
				return
			}
			if cleanupErr := projection.DeleteChannel(s.db, channel.ID); cleanupErr != nil {
				returnErr = errors.Join(returnErr, fmt.Errorf("personaldrive: roll back provisional channel: %w", cleanupErr))
			}
		}()
	}

	slog.Info("personaldrive: recovery started", "channel_id", channel.ID)
	if err := projection.MigratePersonalChannel(s.db, channel.ID); err != nil {
		return fmt.Errorf("personaldrive: register channel: %w", err)
	}
	if err := projection.InsertChannel(s.db, projection.Channel{
		ChannelID:  channel.ID,
		AccessHash: channel.AccessHash,
		Title:      title,
		Kind:       projection.KindPersonal,
		JoinedAt:   channel.CreatedAt,
	}); err != nil {
		return fmt.Errorf("personaldrive: store channel metadata: %w", err)
	}
	if err := s.syncer.EnsureAuthoritative(ctx, channel.ID); err != nil {
		return fmt.Errorf("personaldrive: rebuild channel history: %w", err)
	}
	if err := projection.MarkPersonalBackfillDone(s.db, channel.ID); err != nil {
		return err
	}
	if err := s.saveConfig(channel.ID); err != nil {
		return fmt.Errorf("personaldrive: save channel config: %w", err)
	}
	s.setActive(channel.ID)
	slog.Info("personaldrive: recovery completed", "channel_id", channel.ID)
	return nil
}

func (s *Service) loadConfiguredChannel() (int64, error) {
	if s.loadConfig == nil {
		return 0, fmt.Errorf("personaldrive: config loader is unavailable")
	}
	channelID, err := s.loadConfig()
	if err != nil {
		return 0, fmt.Errorf("personaldrive: load channel config: %w", err)
	}
	if channelID < 0 {
		return 0, fmt.Errorf("personaldrive: invalid configured channel id %d", channelID)
	}
	return channelID, nil
}

func candidatesFromOwned(channels []tgclient.OwnedBroadcastChannel) []Candidate {
	seen := make(map[int64]struct{}, len(channels))
	ordered := make([]tgclient.OwnedBroadcastChannel, 0, len(channels))
	for _, channel := range channels {
		if channel.ID <= 0 {
			continue
		}
		if _, exists := seen[channel.ID]; exists {
			continue
		}
		seen[channel.ID] = struct{}{}
		channel.Title = strings.TrimSpace(channel.Title)
		ordered = append(ordered, channel)
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		left, right := ordered[i], ordered[j]
		leftTDrive := strings.EqualFold(strings.TrimSpace(left.Title), defaultChannelTitle)
		rightTDrive := strings.EqualFold(strings.TrimSpace(right.Title), defaultChannelTitle)
		if leftTDrive != rightTDrive {
			return leftTDrive
		}
		if left.HasActivity != right.HasActivity {
			return left.HasActivity
		}
		if left.CreatedAt != right.CreatedAt {
			return left.CreatedAt < right.CreatedAt
		}
		leftTitle := strings.ToLower(strings.TrimSpace(left.Title))
		rightTitle := strings.ToLower(strings.TrimSpace(right.Title))
		if leftTitle != rightTitle {
			return leftTitle < rightTitle
		}
		return left.ID < right.ID
	})

	result := make([]Candidate, len(ordered))
	for i, channel := range ordered {
		result[i] = Candidate{
			ID:          channel.ID,
			Title:       strings.TrimSpace(channel.Title),
			CreatedAt:   channel.CreatedAt,
			HasActivity: channel.HasActivity,
			Recommended: i == 0 && strings.EqualFold(strings.TrimSpace(channel.Title), defaultChannelTitle),
		}
	}
	return result
}
