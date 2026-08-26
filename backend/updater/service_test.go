package updater

import (
	"context"
	"crypto/ed25519"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type fakeSource struct {
	mu      sync.Mutex
	release Release
	err     error
	calls   int
}

func (f *fakeSource) LatestRelease(context.Context) (Release, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.release, f.err
}

type fakeInstaller struct {
	mu         sync.Mutex
	target     Target
	targetErr  error
	installErr error
	installed  []string
	relaunched []int
	cleaned    int
}

func (f *fakeInstaller) Target() (Target, error) { return f.target, f.targetErr }

func (f *fakeInstaller) Install(payload string, _ Target) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.installErr != nil {
		return f.installErr
	}
	f.installed = append(f.installed, payload)
	return nil
}

func (f *fakeInstaller) Relaunch(_ Target, pid int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.relaunched = append(f.relaunched, pid)
	return nil
}

func (f *fakeInstaller) Cleanup(Target) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleaned++
	return nil
}

// fixture wires a service to an in-memory release whose assets are served by
// httptest, mirroring the real GitHub layout (payload + signed checksums.txt).
type fixture struct {
	t          *testing.T
	service    *Service
	source     *fakeSource
	installer  *fakeInstaller
	server     *httptest.Server
	payload    []byte
	assetName  string
	cacheDir   string
	events     chan State
	blockBody  chan struct{}
	mu         sync.Mutex
	serveSums  string
	serveSig   []byte
	signingKey manifestTestKey
	serveBody  bool
}

func newFixture(t *testing.T, current, latest string) *fixture {
	t.Helper()
	f := &fixture{t: t, cacheDir: t.TempDir(), events: make(chan State, 256)}
	f.payload = []byte("payload for " + latest)
	f.assetName, _ = appAssetName("v"+latest, Platform{OS: "darwin", Arch: "arm64"})
	f.serveSums = sha256Hex(f.payload) + "  " + f.assetName + "\n" + sha256Hex([]byte("cli")) + "  TDrive-v" + latest + "-darwin-arm64-cli.tar.gz\n"
	f.signingKey = newManifestTestKey(t)
	f.serveSig = []byte(manifestRecord(t, f.signingKey, []byte(f.serveSums)))
	f.serveBody = true

	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		sums, signature, serveBody, block := f.serveSums, append([]byte(nil), f.serveSig...), f.serveBody, f.blockBody
		f.mu.Unlock()
		switch r.URL.Path {
		case "/" + checksumsAssetName:
			if sums == "" {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(sums))
		case "/" + checksumsSignatureAssetName:
			_, _ = w.Write(signature)
		case "/" + f.assetName:
			if !serveBody {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if block != nil {
				w.Header().Set("Content-Length", "1000000")
				_, _ = w.Write(f.payload[:1])
				if fl, ok := w.(http.Flusher); ok {
					fl.Flush()
				}
				<-block
				return
			}
			_, _ = w.Write(f.payload)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.server.Close)

	f.source = &fakeSource{release: Release{
		Tag:         "v" + latest,
		Version:     mustVersion(t, latest),
		PageURL:     "https://github.com/jeetrex17/TDrive/releases/tag/v" + latest,
		PublishedAt: time.Date(2026, 8, 25, 9, 18, 39, 0, time.UTC),
		Assets: []Asset{
			{Name: f.assetName, Size: int64(len(f.payload)), URL: f.server.URL + "/" + f.assetName},
			{Name: "TDrive-v" + latest + "-darwin-arm64-cli.tar.gz", Size: 3, URL: f.server.URL + "/cli"},
			{Name: checksumsAssetName, Size: int64(len(f.serveSums)), URL: f.server.URL + "/" + checksumsAssetName},
			{Name: checksumsSignatureAssetName, Size: int64(len(f.serveSig)), URL: f.server.URL + "/" + checksumsSignatureAssetName},
		},
	}}
	f.installer = &fakeInstaller{target: Target{Path: filepath.Join(t.TempDir(), "TDrive.app"), Kind: "bundle"}}
	f.service = New(Options{
		CurrentVersion:     current,
		Platform:           Platform{OS: "darwin", Arch: "arm64"},
		Source:             f.source,
		Client:             f.server.Client(),
		CacheDir:           f.cacheDir,
		Installer:          f.installer,
		manifestPublicKeys: []ed25519.PublicKey{f.signingKey.publicKey},
		OnChange:           func(s State) { f.events <- s },
	})
	t.Cleanup(f.service.Close)
	return f
}

func (f *fixture) setManifest(manifest string) {
	f.t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.serveSums = manifest
	f.serveSig = []byte(manifestRecord(f.t, f.signingKey, []byte(manifest)))
}

func (f *fixture) waitPhase(phase Phase) State {
	f.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s := f.service.State(); s.Phase == phase {
			return s
		}
		time.Sleep(5 * time.Millisecond)
	}
	f.t.Fatalf("phase never reached %q, last state %+v", phase, f.service.State())
	return State{}
}

func (f *fixture) drainEvents() []State {
	var out []State
	for {
		select {
		case s := <-f.events:
			out = append(out, s)
		case <-time.After(50 * time.Millisecond):
			return out
		}
	}
}

func TestDevBuildIsDisabled(t *testing.T) {
	s := New(Options{CurrentVersion: "dev", CacheDir: t.TempDir(), Installer: &fakeInstaller{}, Source: &fakeSource{}})
	if s.State().Phase != PhaseDisabled {
		t.Fatalf("phase = %s, want disabled", s.State().Phase)
	}
	if got := s.Check(context.Background()); got.Phase != PhaseDisabled || got.CheckedAt != 0 {
		t.Fatalf("Check on a dev build must be a no-op, got %+v", got)
	}
	if err := s.StartDownload(); !errors.Is(err, ErrDisabled) {
		t.Fatalf("StartDownload = %v, want ErrDisabled", err)
	}
}

func TestCheckReportsUpToDate(t *testing.T) {
	f := newFixture(t, "1.7.0", "1.7.0")
	got := f.service.Check(context.Background())
	if got.Phase != PhaseUpToDate || got.Latest != nil || got.CheckedAt == 0 {
		t.Fatalf("state = %+v", got)
	}
	f.source.release.Version = mustVersion(t, "1.6.0")
	if got := f.service.Check(context.Background()); got.Phase != PhaseUpToDate {
		t.Fatalf("older latest must still be up to date, got %+v", got)
	}
}

func TestCheckNoReleaseIsUpToDate(t *testing.T) {
	f := newFixture(t, "1.7.0", "1.8.0")
	f.source.err = ErrNoRelease
	if got := f.service.Check(context.Background()); got.Phase != PhaseUpToDate || got.Error != "" {
		t.Fatalf("state = %+v", got)
	}
}

func TestFullLifecycle(t *testing.T) {
	f := newFixture(t, "1.6.0", "1.7.0")

	got := f.service.Check(context.Background())
	if got.Phase != PhaseAvailable || !got.Installable || got.Latest == nil {
		t.Fatalf("after check: %+v", got)
	}
	if got.Latest.Version != "1.7.0" || got.Latest.AssetName != f.assetName || got.TotalBytes != int64(len(f.payload)) {
		t.Fatalf("latest = %+v", got.Latest)
	}
	if got.Latest.PublishedAt != "2026-08-25T09:18:39Z" {
		t.Fatalf("published_at = %q", got.Latest.PublishedAt)
	}
	if f.service.ReleasePageURL() != "https://github.com/jeetrex17/TDrive/releases/tag/v1.7.0" {
		t.Fatalf("ReleasePageURL = %q", f.service.ReleasePageURL())
	}

	if err := f.service.StartDownload(); err != nil {
		t.Fatalf("StartDownload: %v", err)
	}
	if err := f.service.StartDownload(); !errors.Is(err, ErrNotAvailable) {
		t.Fatalf("second StartDownload = %v, want ErrNotAvailable", err)
	}
	ready := f.waitPhase(PhaseReady)
	if ready.DownloadedBytes != int64(len(f.payload)) || ready.Error != "" {
		t.Fatalf("ready state = %+v", ready)
	}
	if _, err := os.Stat(filepath.Join(f.cacheDir, f.assetName)); err != nil {
		t.Fatalf("payload missing from cache: %v", err)
	}

	if err := f.service.Relaunch(); !errors.Is(err, ErrNotReady) {
		t.Fatalf("Relaunch before Install = %v, want ErrNotReady", err)
	}
	if err := f.service.Install(); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if got := f.service.State(); got.Phase != PhaseInstalled {
		t.Fatalf("after install: %+v", got)
	}
	if len(f.installer.installed) != 1 || f.installer.installed[0] != filepath.Join(f.cacheDir, f.assetName) {
		t.Fatalf("installer received %v", f.installer.installed)
	}
	if _, err := os.Stat(filepath.Join(f.cacheDir, f.assetName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("payload must be removed after install: %v", err)
	}
	if err := f.service.Relaunch(); err != nil {
		t.Fatalf("Relaunch: %v", err)
	}
	if len(f.installer.relaunched) != 1 || f.installer.relaunched[0] != os.Getpid() {
		t.Fatalf("relaunch pids = %v", f.installer.relaunched)
	}
	if got := f.service.Check(context.Background()); got.Phase != PhaseInstalled {
		t.Fatalf("Check after install must be a no-op, got %+v", got)
	}

	// Events arrive in order and progress never regresses.
	events := f.drainEvents()
	var lastBytes int64
	var seenChecking, seenDownloading bool
	for _, e := range events {
		switch e.Phase {
		case PhaseChecking:
			seenChecking = true
		case PhaseDownloading:
			seenDownloading = true
			if e.DownloadedBytes < lastBytes {
				t.Fatalf("progress regressed: %d after %d", e.DownloadedBytes, lastBytes)
			}
			lastBytes = e.DownloadedBytes
		}
	}
	if !seenChecking || !seenDownloading || events[len(events)-1].Phase != PhaseInstalled {
		t.Fatalf("unexpected event sequence: %+v", events)
	}
}

func TestCheckReusesVerifiedCachedPayload(t *testing.T) {
	f := newFixture(t, "1.6.0", "1.7.0")
	if err := os.WriteFile(filepath.Join(f.cacheDir, f.assetName), f.payload, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.cacheDir, "TDrive-v1.6.5-macos-arm64.zip"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := f.service.Check(context.Background())
	if got.Phase != PhaseReady || got.DownloadedBytes != int64(len(f.payload)) {
		t.Fatalf("state = %+v", got)
	}
	if exists(filepath.Join(f.cacheDir, "TDrive-v1.6.5-macos-arm64.zip")) {
		t.Fatalf("superseded cached payload must be pruned")
	}
}

func TestCheckDiscardsCorruptCachedPayload(t *testing.T) {
	f := newFixture(t, "1.6.0", "1.7.0")
	path := filepath.Join(f.cacheDir, f.assetName)
	if err := os.WriteFile(path, []byte("corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := f.service.Check(context.Background()); got.Phase != PhaseAvailable {
		t.Fatalf("state = %+v", got)
	}
	if exists(path) {
		t.Fatalf("corrupt payload must be deleted")
	}
}

func TestNewPrunesCacheBelowCurrentVersion(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "TDrive-v1.6.0-macos-arm64.zip")
	touch(t, dir, "TDrive-v1.7.0-macos-arm64.zip.part")
	New(Options{CurrentVersion: "1.7.0", CacheDir: dir, Installer: &fakeInstaller{}, Source: &fakeSource{}})
	if exists(filepath.Join(dir, "TDrive-v1.6.0-macos-arm64.zip")) || exists(filepath.Join(dir, "TDrive-v1.7.0-macos-arm64.zip.part")) {
		t.Fatalf("startup prune did not run")
	}
}

func TestMissingPlatformAssetIsNotInstallable(t *testing.T) {
	f := newFixture(t, "1.6.0", "1.7.0")
	f.source.release.Assets = f.source.release.Assets[1:]
	got := f.service.Check(context.Background())
	if got.Phase != PhaseAvailable || got.Installable || got.InstallHint == "" || got.Latest == nil {
		t.Fatalf("state = %+v", got)
	}
	var notInstallable *NotInstallableError
	if err := f.service.StartDownload(); !errors.As(err, &notInstallable) {
		t.Fatalf("StartDownload = %v, want NotInstallableError", err)
	}
}

func TestUnsupportedPlatformIsNotInstallable(t *testing.T) {
	f := newFixture(t, "1.6.0", "1.7.0")
	f.service.opts.Platform = Platform{OS: "linux", Arch: "arm64"}
	got := f.service.Check(context.Background())
	if got.Installable || got.InstallHint != "No automatic update is published for Linux arm64." {
		t.Fatalf("state = %+v", got)
	}
}

func TestMissingChecksumsIsNotInstallable(t *testing.T) {
	f := newFixture(t, "1.6.0", "1.7.0")
	f.source.release.Assets = f.source.release.Assets[:2]
	got := f.service.Check(context.Background())
	if got.Phase != PhaseAvailable || got.Installable || got.InstallHint == "" {
		t.Fatalf("state = %+v", got)
	}
}

func TestChecksumFetchFailureIsAFailedCheck(t *testing.T) {
	f := newFixture(t, "1.6.0", "1.7.0")
	f.mu.Lock()
	f.serveSums = ""
	f.mu.Unlock()
	got := f.service.Check(context.Background())
	if got.Phase != PhaseIdle || got.ErrorStage != "check" || got.Error == "" || got.Latest != nil {
		t.Fatalf("state = %+v", got)
	}
	f.setManifest(sha256Hex(f.payload) + "  " + f.assetName + "\n")
	if got := f.service.Check(context.Background()); got.Phase != PhaseAvailable || got.Error != "" {
		t.Fatalf("retry must succeed, got %+v", got)
	}
}

func TestNotInstallableTargetKeepsReleaseVisible(t *testing.T) {
	f := newFixture(t, "1.6.0", "1.7.0")
	f.installer.targetErr = &NotInstallableError{Reason: "TDrive is running from a temporary location."}
	got := f.service.Check(context.Background())
	if got.Phase != PhaseAvailable || got.Installable || got.InstallHint != "TDrive is running from a temporary location." {
		t.Fatalf("state = %+v", got)
	}
}

func TestMissingManifestSignatureIsNotInstallable(t *testing.T) {
	f := newFixture(t, "1.6.0", "1.7.0")
	f.source.release.Assets = f.source.release.Assets[:len(f.source.release.Assets)-1]

	got := f.service.Check(context.Background())
	if got.Phase != PhaseAvailable || got.Installable || got.InstallHint == "" {
		t.Fatalf("state = %+v", got)
	}
	if err := f.service.StartDownload(); err == nil {
		t.Fatalf("unsigned release must not be downloadable")
	}
}

func TestTamperedManifestIsNotInstallable(t *testing.T) {
	f := newFixture(t, "1.6.0", "1.7.0")
	f.mu.Lock()
	f.serveSums += "# changed after signing\n"
	f.mu.Unlock()

	got := f.service.Check(context.Background())
	if got.Phase != PhaseAvailable || got.Installable || got.InstallHint == "" {
		t.Fatalf("state = %+v", got)
	}
}

func TestTamperedManifestSignatureIsNotInstallable(t *testing.T) {
	f := newFixture(t, "1.6.0", "1.7.0")
	f.mu.Lock()
	f.serveSig = []byte(manifestRecord(t, f.signingKey, []byte("different manifest\n")))
	f.mu.Unlock()

	got := f.service.Check(context.Background())
	if got.Phase != PhaseAvailable || got.Installable || got.InstallHint == "" {
		t.Fatalf("state = %+v", got)
	}
}

func TestOversizedManifestSignatureIsNotInstallable(t *testing.T) {
	f := newFixture(t, "1.6.0", "1.7.0")
	f.mu.Lock()
	f.serveSig = make([]byte, manifestSignatureMaxBytes+1)
	f.mu.Unlock()

	got := f.service.Check(context.Background())
	if got.Phase != PhaseAvailable || got.Installable || got.InstallHint == "" {
		t.Fatalf("state = %+v", got)
	}
}

func TestSourceFailureKeepsPreviousPhase(t *testing.T) {
	f := newFixture(t, "1.6.0", "1.7.0")
	f.service.Check(context.Background())
	if err := f.service.StartDownload(); err != nil {
		t.Fatal(err)
	}
	f.waitPhase(PhaseReady)

	f.source.err = errors.New("dial tcp: connection refused")
	got := f.service.Check(context.Background())
	if got.Phase != PhaseReady || got.ErrorStage != "check" || got.Error == "" {
		t.Fatalf("a failed re-check must keep the verified payload, got %+v", got)
	}
	if err := f.service.Install(); err != nil {
		t.Fatalf("Install after failed re-check: %v", err)
	}
}

func TestDownloadCancelReturnsToAvailableWithoutError(t *testing.T) {
	f := newFixture(t, "1.6.0", "1.7.0")
	f.mu.Lock()
	f.blockBody = make(chan struct{})
	f.mu.Unlock()
	defer close(f.blockBody)

	f.service.Check(context.Background())
	if err := f.service.StartDownload(); err != nil {
		t.Fatal(err)
	}
	f.waitPhase(PhaseDownloading)
	if got := f.service.Check(context.Background()); got.Phase != PhaseDownloading {
		t.Fatalf("Check during download must be a no-op, got %+v", got)
	}
	f.service.CancelDownload()
	got := f.waitPhase(PhaseAvailable)
	if got.Error != "" || got.DownloadedBytes != 0 {
		t.Fatalf("state after cancel = %+v", got)
	}
	if exists(filepath.Join(f.cacheDir, f.assetName+partSuffix)) {
		t.Fatalf("partial file must be removed after cancel")
	}
	if err := f.service.StartDownload(); err != nil {
		t.Fatalf("download must be restartable after cancel: %v", err)
	}
}

func TestDownloadFailureReportsError(t *testing.T) {
	f := newFixture(t, "1.6.0", "1.7.0")
	f.mu.Lock()
	f.serveBody = false
	f.mu.Unlock()
	f.service.Check(context.Background())
	if err := f.service.StartDownload(); err != nil {
		t.Fatal(err)
	}
	got := f.waitPhase(PhaseAvailable)
	if got.ErrorStage != "download" || got.Error == "" {
		t.Fatalf("state = %+v", got)
	}
}

func TestChecksumMismatchDiscardsDownload(t *testing.T) {
	f := newFixture(t, "1.6.0", "1.7.0")
	f.setManifest(sha256Hex([]byte("something else")) + "  " + f.assetName + "\n")
	f.service.Check(context.Background())
	if err := f.service.StartDownload(); err != nil {
		t.Fatal(err)
	}
	got := f.waitPhase(PhaseAvailable)
	if !errors.Is(ErrChecksumMismatch, ErrChecksumMismatch) || got.Error != userMessage(ErrChecksumMismatch) {
		t.Fatalf("state = %+v", got)
	}
	if exists(filepath.Join(f.cacheDir, f.assetName)) {
		t.Fatalf("mismatched payload must not be kept")
	}
}

func TestInstallFailureReturnsToReady(t *testing.T) {
	f := newFixture(t, "1.6.0", "1.7.0")
	f.service.Check(context.Background())
	if err := f.service.StartDownload(); err != nil {
		t.Fatal(err)
	}
	f.waitPhase(PhaseReady)
	f.installer.installErr = errors.New("move current version aside: permission denied")
	if err := f.service.Install(); err == nil {
		t.Fatalf("expected install error")
	}
	got := f.service.State()
	if got.Phase != PhaseReady || got.ErrorStage != "install" || got.Error == "" {
		t.Fatalf("state = %+v", got)
	}
	if !exists(filepath.Join(f.cacheDir, f.assetName)) {
		t.Fatalf("payload must survive a failed install for a retry")
	}
	f.installer.installErr = nil
	if err := f.service.Install(); err != nil {
		t.Fatalf("retry: %v", err)
	}
}

func TestInstallRejectsMutatedReadyPayload(t *testing.T) {
	f := newFixture(t, "1.6.0", "1.7.0")
	f.service.Check(context.Background())
	if err := f.service.StartDownload(); err != nil {
		t.Fatal(err)
	}
	f.waitPhase(PhaseReady)
	payloadPath := filepath.Join(f.cacheDir, f.assetName)
	mutatedPayload := append([]byte(nil), f.payload...)
	mutatedPayload[0] ^= 0xff
	if err := os.WriteFile(payloadPath, mutatedPayload, 0o644); err != nil {
		t.Fatal(err)
	}

	err := f.service.Install()
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("Install = %v, want ErrChecksumMismatch", err)
	}
	got := f.service.State()
	if got.Phase != PhaseAvailable || got.DownloadedBytes != 0 || got.ErrorStage != "install" || got.Error == "" {
		t.Fatalf("state = %+v", got)
	}
	if len(f.installer.installed) != 0 {
		t.Fatalf("installer received mutated payload: %v", f.installer.installed)
	}
	if exists(payloadPath) {
		t.Fatalf("mutated payload must be removed")
	}
}

func TestInstallRequiresReady(t *testing.T) {
	f := newFixture(t, "1.6.0", "1.7.0")
	if err := f.service.Install(); !errors.Is(err, ErrNotReady) {
		t.Fatalf("Install = %v, want ErrNotReady", err)
	}
}

func TestNewerReleaseSupersedesReadyPayload(t *testing.T) {
	f := newFixture(t, "1.6.0", "1.7.0")
	f.service.Check(context.Background())
	if err := f.service.StartDownload(); err != nil {
		t.Fatal(err)
	}
	f.waitPhase(PhaseReady)

	// Promote the fixture release to 1.8.0 with a fresh payload.
	f.source.mu.Lock()
	f.source.release.Tag = "v1.8.0"
	f.source.release.Version = mustVersion(t, "1.8.0")
	newName, _ := appAssetName("v1.8.0", Platform{OS: "darwin", Arch: "arm64"})
	f.source.release.Assets[0].Name = newName
	f.source.release.Assets[0].URL = f.server.URL + "/" + f.assetName
	f.source.mu.Unlock()
	f.setManifest(sha256Hex(f.payload) + "  " + newName + "\n")

	got := f.service.Check(context.Background())
	if got.Phase != PhaseAvailable || got.Latest.Version != "1.8.0" {
		t.Fatalf("state = %+v", got)
	}
	if exists(filepath.Join(f.cacheDir, f.assetName)) {
		t.Fatalf("the superseded 1.7.0 payload must be pruned")
	}
}

func TestCleanupAfterRestartUsesInstallerTarget(t *testing.T) {
	f := newFixture(t, "1.7.0", "1.7.0")
	if err := f.service.CleanupAfterRestart(); err != nil {
		t.Fatal(err)
	}
	if f.installer.cleaned != 1 {
		t.Fatalf("cleanup calls = %d", f.installer.cleaned)
	}
	f.installer.targetErr = &NotInstallableError{Reason: "nope"}
	if err := f.service.CleanupAfterRestart(); err != nil || f.installer.cleaned != 1 {
		t.Fatalf("cleanup must be skipped when the target is unknown")
	}
}

func TestUserMessage(t *testing.T) {
	cases := map[error]string{
		&NotInstallableError{Reason: "custom"}: "custom",
		ErrRateLimited:                         userMessage(ErrRateLimited),
		context.DeadlineExceeded:               "GitHub took too long to respond. Try again later.",
		errDownloadStalled:                     "The download stalled. Check your connection and try again.",
		errors.New("plain"):                    "plain",
	}
	for err, want := range cases {
		if got := userMessage(err); got != want {
			t.Errorf("userMessage(%v) = %q, want %q", err, got, want)
		}
	}
}
