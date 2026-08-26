// Package updater keeps a TDrive desktop build current from GitHub releases:
// it discovers the newest published release, downloads and verifies the
// platform payload, swaps it into place and relaunches. It has no Wails
// dependency so the CLI can reuse it later; the app layer only translates
// state changes into runtime events.
package updater

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// DefaultRepo is the GitHub repository whose releases carry desktop builds.
const DefaultRepo = "jeetrex17/TDrive"

// Phase is where the updater is in the check → download → install lifecycle.
// Errors do not get a phase of their own: a failed step reports its message
// in State.Error while the phase stays wherever the payload actually is, so a
// verified download survives a flaky re-check.
type Phase string

const (
	PhaseIdle        Phase = "idle"        // nothing checked yet this session
	PhaseDisabled    Phase = "disabled"    // development build, updater off
	PhaseChecking    Phase = "checking"    // talking to GitHub
	PhaseUpToDate    Phase = "up_to_date"  // newest release is running
	PhaseAvailable   Phase = "available"   // newer release known, not on disk
	PhaseDownloading Phase = "downloading" // payload streaming into the cache
	PhaseReady       Phase = "ready"       // verified payload on disk
	PhaseInstalling  Phase = "installing"  // swapping files
	PhaseInstalled   Phase = "installed"   // swap done, restart pending
)

// ReleaseInfo is the user-facing summary of the newest release.
type ReleaseInfo struct {
	Version     string `json:"version"`
	Tag         string `json:"tag"`
	PageURL     string `json:"page_url"`
	PublishedAt string `json:"published_at"`
	AssetName   string `json:"asset_name"`
	AssetSize   int64  `json:"asset_size"`
}

// State is the snapshot the frontend renders. Every transition is delivered
// through Options.OnChange in order, and Service.State returns the same shape
// for hydration.
type State struct {
	Phase           Phase        `json:"phase"`
	CurrentVersion  string       `json:"current_version"`
	Latest          *ReleaseInfo `json:"latest"`
	Installable     bool         `json:"installable"`
	InstallHint     string       `json:"install_hint"`
	DownloadedBytes int64        `json:"downloaded_bytes"`
	TotalBytes      int64        `json:"total_bytes"`
	CheckedAt       int64        `json:"checked_at"` // unix milliseconds, 0 = never
	Error           string       `json:"error"`
	ErrorStage      string       `json:"error_stage"` // "check" | "download" | "install"
}

// Options configure a Service. Zero values pick production defaults.
type Options struct {
	CurrentVersion string
	Platform       Platform
	Source         Source
	Client         *http.Client
	UserAgent      string
	CacheDir       string
	Installer      Installer
	OnChange       func(State)
	Now            func() time.Time
}

var (
	ErrDisabled     = errors.New("updates are disabled for this build")
	ErrNotAvailable = errors.New("no update is waiting to be downloaded")
	ErrNotReady     = errors.New("no verified update is ready to install")
	ErrBusy         = errors.New("an update operation is already running")
)

const (
	progressEmitInterval = 150 * time.Millisecond
	eventQueueSize       = 128
)

// Service is safe for concurrent use. One instance lives for the process.
type Service struct {
	opts    Options
	dev     bool
	current Version

	mu         sync.Mutex
	state      State
	release    *Release
	asset      *Asset
	checksums  map[string]string
	payload    string // verified payload path while PhaseReady
	target     Target
	targetErr  error
	targetDone bool
	cancel     context.CancelFunc
	downloadID uint64
	lastEmit   time.Time
	events     chan State
	closed     bool
}

// New builds the service and prunes stale cache entries. A version that does
// not parse ("dev") yields a permanently disabled service.
func New(opts Options) *Service {
	if opts.Platform == (Platform{}) {
		opts.Platform = HostPlatform()
	}
	if opts.Client == nil {
		opts.Client = &http.Client{}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Installer == nil {
		opts.Installer = newPlatformInstaller()
	}
	if opts.CacheDir == "" {
		opts.CacheDir = defaultCacheDir()
	}
	if opts.Source == nil {
		opts.Source = NewGitHubSource(DefaultRepo, opts.UserAgent, opts.Client)
	}

	s := &Service{opts: opts}
	s.state = State{Phase: PhaseIdle, CurrentVersion: opts.CurrentVersion}
	if opts.OnChange != nil {
		s.events = make(chan State, eventQueueSize)
		go s.deliver()
	}

	version, err := ParseVersion(opts.CurrentVersion)
	if err != nil {
		s.dev = true
		s.state.Phase = PhaseDisabled
		s.state.InstallHint = "Development build — automatic updates are off."
		return s
	}
	s.current = version
	s.state.CurrentVersion = version.String()
	pruneCache(opts.CacheDir, version)
	return s
}

// Close stops event delivery. Only tests need it.
func (s *Service) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.events == nil {
		return
	}
	s.closed = true
	close(s.events)
}

// State returns the current snapshot.
func (s *Service) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

// ReleasePageURL returns the newest release's GitHub page, or "" when no
// release newer than the running one is known.
func (s *Service) ReleasePageURL() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Latest == nil {
		return ""
	}
	return s.state.Latest.PageURL
}

// Check asks the source for the newest release and reconciles the local
// cache against it. It is a no-op while a download or install is running.
func (s *Service) Check(ctx context.Context) State {
	s.mu.Lock()
	if s.dev || !s.checkAllowedLocked() {
		snapshot := s.snapshotLocked()
		s.mu.Unlock()
		return snapshot
	}
	previous := s.state.Phase
	s.state.Phase = PhaseChecking
	s.state.Error, s.state.ErrorStage = "", ""
	s.emitLocked()
	s.mu.Unlock()

	release, err := s.opts.Source.LatestRelease(ctx)
	var res resolution
	if err == nil && release.Version.Newer(s.current) {
		res, err = s.resolve(ctx, release)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.CheckedAt = s.opts.Now().UnixMilli()
	switch {
	case errors.Is(err, ErrNoRelease):
		s.setUpToDateLocked()
	case err != nil:
		s.state.Phase = previous
		s.state.Error = userMessage(err)
		s.state.ErrorStage = "check"
	case !release.Version.Newer(s.current):
		s.setUpToDateLocked()
	default:
		s.applyResolutionLocked(release, res)
	}
	s.emitLocked()
	return s.snapshotLocked()
}

func (s *Service) checkAllowedLocked() bool {
	switch s.state.Phase {
	case PhaseIdle, PhaseUpToDate, PhaseAvailable, PhaseReady:
		return true
	}
	return false
}

// resolution is everything Check learns about a newer release beyond the
// release itself: which asset applies, its expected digest, and whether a
// verified copy is already cached.
type resolution struct {
	asset       *Asset
	checksums   map[string]string
	installable bool
	hint        string
	payload     string
}

func (s *Service) resolve(ctx context.Context, release Release) (resolution, error) {
	var res resolution
	name, ok := appAssetName(release.Tag, s.opts.Platform)
	if !ok {
		res.hint = fmt.Sprintf("No automatic update is published for %s %s.", displayOS(s.opts.Platform.OS), s.opts.Platform.Arch)
		return res, nil
	}
	asset, ok := release.asset(name)
	if !ok {
		res.hint = fmt.Sprintf("This release has no %s %s build yet.", displayOS(s.opts.Platform.OS), s.opts.Platform.Arch)
		return res, nil
	}
	res.asset = &asset

	target, err := s.resolveTarget()
	if err != nil {
		res.hint = userMessage(err)
		return res, nil
	}
	if parent := filepath.Dir(target.Path); !dirWritable(parent) {
		res.hint = fmt.Sprintf("TDrive can't replace itself in %s. Move it to a folder you can write to, or update manually.", parent)
		return res, nil
	}

	sumsAsset, ok := release.asset(checksumsAssetName)
	if !ok {
		res.hint = "This release has no checksum file, so the download can't be verified. Install it manually."
		return res, nil
	}
	body, err := fetchSmall(ctx, s.opts.Client, s.opts.UserAgent, sumsAsset.URL)
	if err != nil {
		// Almost always transient; surface it as a failed check so the next
		// attempt (manual or scheduled) retries instead of caching "manual".
		return res, fmt.Errorf("fetch checksums: %w", err)
	}
	sums, err := parseChecksums(bytes.NewReader(body))
	if err != nil {
		res.hint = "The release checksum file is malformed, so the download can't be verified. Install it manually."
		return res, nil
	}
	want, ok := sums[name]
	if !ok {
		res.hint = fmt.Sprintf("The release checksums don't cover the %s %s build. Install it manually.", displayOS(s.opts.Platform.OS), s.opts.Platform.Arch)
		return res, nil
	}
	res.checksums = sums
	res.installable = true

	dest := s.payloadPath(name)
	if verifyFile(dest, asset.Size, want) == nil {
		res.payload = dest
	} else {
		_ = os.Remove(dest)
	}
	pruneCacheExcept(s.opts.CacheDir, name)
	return res, nil
}

func (s *Service) resolveTarget() (Target, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.targetDone {
		s.target, s.targetErr = s.opts.Installer.Target()
		s.targetDone = true
	}
	return s.target, s.targetErr
}

func (s *Service) applyResolutionLocked(release Release, res resolution) {
	rel := release
	s.release = &rel
	s.asset = res.asset
	s.checksums = res.checksums

	info := &ReleaseInfo{
		Version: release.Version.String(),
		Tag:     release.Tag,
		PageURL: release.PageURL,
	}
	if !release.PublishedAt.IsZero() {
		info.PublishedAt = release.PublishedAt.UTC().Format(time.RFC3339)
	}
	if res.asset != nil {
		info.AssetName = res.asset.Name
		info.AssetSize = res.asset.Size
	}
	s.state.Latest = info
	s.state.Installable = res.installable
	s.state.InstallHint = res.hint
	s.state.TotalBytes = info.AssetSize
	if res.payload != "" {
		s.payload = res.payload
		s.state.Phase = PhaseReady
		s.state.DownloadedBytes = info.AssetSize
		return
	}
	s.payload = ""
	s.state.Phase = PhaseAvailable
	s.state.DownloadedBytes = 0
}

func (s *Service) setUpToDateLocked() {
	if s.payload != "" {
		_ = os.Remove(s.payload)
	}
	s.release, s.asset, s.checksums, s.payload = nil, nil, nil, ""
	s.state.Phase = PhaseUpToDate
	s.state.Latest = nil
	s.state.Installable = false
	s.state.InstallHint = ""
	s.state.DownloadedBytes, s.state.TotalBytes = 0, 0
}

// StartDownload begins fetching the available payload in the background.
// Progress and completion arrive through OnChange.
func (s *Service) StartDownload() error {
	s.mu.Lock()
	if s.dev {
		s.mu.Unlock()
		return ErrDisabled
	}
	if s.state.Phase != PhaseAvailable {
		s.mu.Unlock()
		return ErrNotAvailable
	}
	if !s.state.Installable || s.asset == nil {
		s.mu.Unlock()
		return &NotInstallableError{Reason: s.state.InstallHint}
	}
	if err := os.MkdirAll(s.opts.CacheDir, 0o755); err != nil {
		s.mu.Unlock()
		return fmt.Errorf("create update cache: %w", err)
	}
	asset := *s.asset
	want := s.checksums[asset.Name]
	dest := s.payloadPath(asset.Name)

	ctx, cancel := context.WithCancel(context.Background())
	s.downloadID++
	id := s.downloadID
	s.cancel = cancel
	s.state.Phase = PhaseDownloading
	s.state.DownloadedBytes = 0
	s.state.TotalBytes = asset.Size
	s.state.Error, s.state.ErrorStage = "", ""
	s.emitLocked()
	s.mu.Unlock()

	go s.runDownload(ctx, id, asset, want, dest)
	return nil
}

func (s *Service) runDownload(ctx context.Context, id uint64, asset Asset, want, dest string) {
	err := downloadFile(ctx, s.opts.Client, s.opts.UserAgent, asset.URL, dest, asset.Size, want, func(done, total int64) {
		s.progress(id, done, total)
	})

	s.mu.Lock()
	defer s.mu.Unlock()
	if id != s.downloadID {
		return
	}
	s.cancel = nil
	if err != nil {
		s.state.Phase = PhaseAvailable
		s.state.DownloadedBytes = 0
		if !errors.Is(err, context.Canceled) {
			s.state.Error = userMessage(err)
			s.state.ErrorStage = "download"
		}
		s.emitLocked()
		return
	}
	s.payload = dest
	s.state.Phase = PhaseReady
	s.state.DownloadedBytes = asset.Size
	s.state.TotalBytes = asset.Size
	s.emitLocked()
}

func (s *Service) progress(id uint64, done, total int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id != s.downloadID || s.state.Phase != PhaseDownloading {
		return
	}
	s.state.DownloadedBytes = done
	if total > 0 {
		s.state.TotalBytes = total
	}
	if done < s.state.TotalBytes && s.opts.Now().Sub(s.lastEmit) < progressEmitInterval {
		return
	}
	s.emitLocked()
}

// CancelDownload aborts an in-flight download; the phase returns to
// available without an error message because the user asked for it.
func (s *Service) CancelDownload() {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Install swaps the verified payload into place. On success the phase becomes
// installed and the caller is expected to Relaunch and quit.
func (s *Service) Install() error {
	s.mu.Lock()
	if s.state.Phase != PhaseReady || s.payload == "" {
		s.mu.Unlock()
		return ErrNotReady
	}
	if !s.targetDone || s.targetErr != nil {
		s.mu.Unlock()
		return ErrNotReady
	}
	payload, target := s.payload, s.target
	s.state.Phase = PhaseInstalling
	s.state.Error, s.state.ErrorStage = "", ""
	s.emitLocked()
	s.mu.Unlock()

	err := s.opts.Installer.Install(payload, target)

	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.state.Phase = PhaseReady
		s.state.Error = userMessage(err)
		s.state.ErrorStage = "install"
		s.emitLocked()
		return err
	}
	s.state.Phase = PhaseInstalled
	s.payload = ""
	_ = os.Remove(payload)
	s.emitLocked()
	return nil
}

// Relaunch starts the installed version. Valid only after a successful Install.
func (s *Service) Relaunch() error {
	s.mu.Lock()
	phase, target := s.state.Phase, s.target
	s.mu.Unlock()
	if phase != PhaseInstalled {
		return ErrNotReady
	}
	return s.opts.Installer.Relaunch(target, os.Getpid())
}

// CleanupAfterRestart removes the previous version and staging leftovers.
// Call it once the new build has started successfully, so a broken update
// still leaves the old copy on disk for manual recovery.
func (s *Service) CleanupAfterRestart() error {
	target, err := s.opts.Installer.Target()
	if err != nil {
		return nil
	}
	return s.opts.Installer.Cleanup(target)
}

func (s *Service) payloadPath(name string) string {
	return filepath.Join(s.opts.CacheDir, name)
}

func (s *Service) snapshotLocked() State {
	snapshot := s.state
	if s.state.Latest != nil {
		latest := *s.state.Latest
		snapshot.Latest = &latest
	}
	return snapshot
}

// emitLocked queues a snapshot for ordered delivery. The queue coalesces by
// dropping the oldest entry when full: the newest state is the only one that
// matters to a renderer.
func (s *Service) emitLocked() {
	s.lastEmit = s.opts.Now()
	if s.events == nil || s.closed {
		return
	}
	snapshot := s.snapshotLocked()
	for {
		select {
		case s.events <- snapshot:
			return
		default:
			select {
			case <-s.events:
			default:
			}
		}
	}
}

func (s *Service) deliver() {
	for state := range s.events {
		s.opts.OnChange(state)
	}
}

// userMessage turns an internal error into one sentence a person can act on.
func userMessage(err error) string {
	var notInstallable *NotInstallableError
	switch {
	case errors.As(err, &notInstallable):
		return notInstallable.Reason
	case errors.Is(err, ErrRateLimited):
		return "GitHub is rate-limiting update checks right now. Try again in a little while."
	case errors.Is(err, ErrChecksumMismatch):
		return "The downloaded file didn't match the release checksums, so it was discarded."
	case errors.Is(err, errDownloadStalled):
		return "The download stalled. Check your connection and try again."
	case errors.Is(err, context.DeadlineExceeded):
		return "GitHub took too long to respond. Try again later."
	}
	var urlErr *url.Error
	var netErr net.Error
	if errors.As(err, &urlErr) || errors.As(err, &netErr) {
		return "Couldn't reach GitHub. Check your connection and try again."
	}
	msg := err.Error()
	if len(msg) > 200 {
		msg = msg[:200] + "…"
	}
	return msg
}
