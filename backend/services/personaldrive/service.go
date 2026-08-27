// Package personaldrive coordinates explicit selection or creation of the
// Telegram broadcast channel used as the user's personal drive.
//
// Setup is split into a local step and a remote step so callers can show the
// right UI for each: Prepare only reads config.json and activates a saved
// drive, Discover walks the user's Telegram dialogs for channels they own.
// Select and Create are the only paths that change local state, and Create is
// the only path that writes to Telegram.
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
	untitledChannel     = "Untitled channel"
)

var (
	ErrInvalidSelection = errors.New("personaldrive: invalid channel selection")
	ErrCandidateMissing = errors.New("personaldrive: selected channel is no longer available")

	errTelegramUnavailable = errors.New("personaldrive: Telegram client is unavailable")
)

type Telegram interface {
	ListOwnedBroadcastChannels(ctx context.Context) ([]tgclient.OwnedBroadcastChannel, error)
	CreateBroadcastChannel(ctx context.Context, title, about string) (tgclient.OwnedBroadcastChannel, error)
}

// HistorySync rebuilds a channel's projection from Telegram history.
// EnsureAuthoritative scans from message zero and is only used on an empty
// projection; Incremental continues from the channel's watermark.
type HistorySync interface {
	EnsureAuthoritative(ctx context.Context, channelID int64) error
	Incremental(ctx context.Context, channelID int64) error
}

type Candidate struct {
	ID          int64
	Title       string
	CreatedAt   int64
	HasActivity bool
	Recommended bool
}

// State is the outcome of Prepare: a saved drive is active, or the user has
// to choose one.
type State struct {
	Status    string
	ChannelID int64
}

type Config struct {
	DB         *sql.DB
	Telegram   Telegram
	Sync       HistorySync
	LoadConfig func() (int64, error)
	SaveConfig func(int64) error
	UseSaved   func(context.Context, int64) error
	SetActive  func(int64)
}

type Service struct {
	db         *sql.DB
	telegram   Telegram
	syncer     HistorySync
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

// Prepare activates the saved drive when config.json names one. It never
// touches Telegram: without a usable config it reports that the user must
// choose a drive, and the caller runs Discover to list the options.
func (s *Service) Prepare(ctx context.Context) (State, error) {
	channelID, err := s.loadConfiguredChannel()
	if err != nil {
		return State{}, err
	}
	if channelID == 0 {
		return State{Status: StatusSelectionRequired}, nil
	}
	if err := s.activateConfigured(ctx, channelID); err != nil {
		return State{}, err
	}
	return State{Status: StatusReady, ChannelID: channelID}, nil
}

// Discover lists the broadcast channels the user created, ordered so the
// most likely TDrive channel comes first. It is read-only.
func (s *Service) Discover(ctx context.Context) ([]Candidate, error) {
	channels, err := s.listOwnedChannels(ctx)
	if err != nil {
		return nil, err
	}
	slog.Info("personaldrive: channel discovery completed", "candidate_count", len(channels))
	return candidatesFromOwned(channels), nil
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
		// A drive was configured after the picker was shown; keep it rather
		// than overwrite it with a stale choice.
		return s.activateConfigured(ctx, configured)
	}
	channels, err := s.listOwnedChannels(ctx)
	if err != nil {
		return err
	}
	for _, channel := range channels {
		if channel.ID == channelID {
			return s.recover(ctx, channel)
		}
	}
	return ErrCandidateMissing
}

// Create is the only path that creates a remote channel. A successful remote
// creation is retained in memory until local recovery commits, so a retry
// after a sync/config failure adopts it instead of creating a duplicate.
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
		return errTelegramUnavailable
	}
	if s.pendingCreated == nil {
		created, err := s.telegram.CreateBroadcastChannel(ctx, defaultChannelTitle, defaultChannelAbout)
		if err != nil {
			return fmt.Errorf("personaldrive: create channel: %w", err)
		}
		if created.ID <= 0 {
			return fmt.Errorf("personaldrive: create channel returned an invalid id")
		}
		s.pendingCreated = &created
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

func (s *Service) listOwnedChannels(ctx context.Context) ([]tgclient.OwnedBroadcastChannel, error) {
	if s.telegram == nil {
		return nil, errTelegramUnavailable
	}
	channels, err := s.telegram.ListOwnedBroadcastChannels(ctx)
	if err != nil {
		return nil, fmt.Errorf("personaldrive: list owned channels: %w", err)
	}
	return channels, nil
}

// recover makes channel the personal drive: it registers the channel
// locally, brings the projection up to date with Telegram history, retires
// any other personal registration, and only then persists config and
// activates. A failure before the config write leaves no provisional channel
// behind.
//
// An empty projection (fresh install) is rebuilt from message zero and, since
// nothing local can be missing from Telegram afterwards, marked backfilled. A
// projection that already holds this channel is the accumulated truth: it only
// gets an incremental sync, and its backfill state is left untouched so any
// local-only structure still gets published later.
func (s *Service) recover(ctx context.Context, channel tgclient.OwnedBroadcastChannel) (returnErr error) {
	if s.db == nil || s.syncer == nil || s.saveConfig == nil || s.setActive == nil {
		return fmt.Errorf("personaldrive: recovery dependencies are unavailable")
	}
	if channel.ID <= 0 {
		return ErrInvalidSelection
	}
	wasRegistered, err := projection.ChannelExists(s.db, channel.ID)
	if err != nil {
		return fmt.Errorf("personaldrive: inspect channel registration: %w", err)
	}
	if !wasRegistered {
		defer func() {
			if returnErr != nil {
				returnErr = errors.Join(returnErr, s.dropProvisionalChannel(channel.ID))
			}
		}()
	}

	slog.Info("personaldrive: recovery started", "channel_id", channel.ID)
	if err := projection.MigratePersonalChannel(s.db, channel.ID); err != nil {
		return fmt.Errorf("personaldrive: register channel: %w", err)
	}
	fresh, err := projection.ChannelIsEmpty(s.db, channel.ID)
	if err != nil {
		return fmt.Errorf("personaldrive: inspect channel projection: %w", err)
	}
	if err := projection.InsertChannel(s.db, projection.Channel{
		ChannelID:  channel.ID,
		AccessHash: channel.AccessHash,
		Title:      displayTitle(channel.Title),
		Kind:       projection.KindPersonal,
		JoinedAt:   channel.CreatedAt,
	}); err != nil {
		return fmt.Errorf("personaldrive: store channel metadata: %w", err)
	}
	if err := s.syncHistory(ctx, channel.ID, fresh); err != nil {
		return err
	}
	if err := s.retirePersonalChannelsExcept(channel.ID); err != nil {
		return err
	}
	if err := s.saveConfig(channel.ID); err != nil {
		return fmt.Errorf("personaldrive: save channel config: %w", err)
	}
	s.setActive(channel.ID)
	slog.Info("personaldrive: recovery completed", "channel_id", channel.ID)
	return nil
}

func (s *Service) syncHistory(ctx context.Context, channelID int64, fresh bool) error {
	if !fresh {
		slog.Info("personaldrive: projection already populated, syncing incrementally", "channel_id", channelID)
		if err := s.syncer.Incremental(ctx, channelID); err != nil {
			return fmt.Errorf("personaldrive: sync channel history: %w", err)
		}
		return nil
	}
	if err := s.syncer.EnsureAuthoritative(ctx, channelID); err != nil {
		return fmt.Errorf("personaldrive: rebuild channel history: %w", err)
	}
	return projection.MarkPersonalBackfillDone(s.db, channelID)
}

// dropProvisionalChannel removes a channel row that recovery registered but
// could not commit, so a later attempt starts from a clean slate.
func (s *Service) dropProvisionalChannel(channelID int64) error {
	exists, err := projection.ChannelExists(s.db, channelID)
	if err != nil {
		return fmt.Errorf("personaldrive: inspect provisional channel for rollback: %w", err)
	}
	if !exists {
		return nil
	}
	if err := projection.DeleteChannel(s.db, channelID); err != nil {
		return fmt.Errorf("personaldrive: roll back provisional channel: %w", err)
	}
	return nil
}

// retirePersonalChannelsExcept drops any other channel still registered as
// personal. Only one personal drive exists; a stale row, typically the empty
// channel an earlier version created before the user recovered the real one,
// would otherwise appear as a second drive in the sidebar and mount picker.
// Telegram is untouched; the local projection can be recovered again later.
func (s *Service) retirePersonalChannelsExcept(keep int64) error {
	channels, err := projection.ListChannels(s.db)
	if err != nil {
		return fmt.Errorf("personaldrive: list registered channels: %w", err)
	}
	for _, existing := range channels {
		if existing.Kind != projection.KindPersonal || existing.ChannelID == keep {
			continue
		}
		slog.Info("personaldrive: retiring stale personal channel", "channel_id", existing.ChannelID, "replacement", keep)
		if err := projection.DeleteChannel(s.db, existing.ChannelID); err != nil {
			return fmt.Errorf("personaldrive: retire stale personal channel %d: %w", existing.ChannelID, err)
		}
	}
	return nil
}

// loadConfiguredChannel returns 0 when no usable config exists. An
// unparseable config.json must not block recovery: the user picks a drive
// explicitly and the next SaveConfig replaces the bad file. Read failures
// still surface, since they may hide a valid configuration.
func (s *Service) loadConfiguredChannel() (int64, error) {
	if s.loadConfig == nil {
		return 0, fmt.Errorf("personaldrive: config loader is unavailable")
	}
	channelID, err := s.loadConfig()
	switch {
	case errors.Is(err, auth.ErrConfigInvalid):
		slog.Warn("personaldrive: ignoring unreadable drive config; explicit setup required", "error", err)
		return 0, nil
	case err != nil:
		return 0, fmt.Errorf("personaldrive: load channel config: %w", err)
	case channelID < 0:
		return 0, fmt.Errorf("personaldrive: invalid configured channel id %d", channelID)
	}
	return channelID, nil
}

func displayTitle(title string) string {
	if trimmed := strings.TrimSpace(title); trimmed != "" {
		return trimmed
	}
	return untitledChannel
}

func isDefaultTitle(title string) bool {
	return strings.EqualFold(strings.TrimSpace(title), defaultChannelTitle)
}

// candidatesFromOwned de-duplicates and orders channels so the most likely
// TDrive channel comes first: default title, then channels with content,
// then oldest. Only the first candidate can be marked recommended, and only
// when it carries the default title.
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
		if leftDefault, rightDefault := isDefaultTitle(left.Title), isDefaultTitle(right.Title); leftDefault != rightDefault {
			return leftDefault
		}
		if left.HasActivity != right.HasActivity {
			return left.HasActivity
		}
		if left.CreatedAt != right.CreatedAt {
			return left.CreatedAt < right.CreatedAt
		}
		if leftTitle, rightTitle := strings.ToLower(left.Title), strings.ToLower(right.Title); leftTitle != rightTitle {
			return leftTitle < rightTitle
		}
		return left.ID < right.ID
	})

	result := make([]Candidate, len(ordered))
	for i, channel := range ordered {
		result[i] = Candidate{
			ID:          channel.ID,
			Title:       channel.Title,
			CreatedAt:   channel.CreatedAt,
			HasActivity: channel.HasActivity,
			Recommended: i == 0 && isDefaultTitle(channel.Title),
		}
	}
	return result
}
