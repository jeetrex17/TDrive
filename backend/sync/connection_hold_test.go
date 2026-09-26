package sync

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"

	_ "modernc.org/sqlite"
)

// probeTimeout bounds how long an unrelated read is allowed to wait for the
// pool. Anything that blocks for longer is, in production, a stalled UI.
const probeTimeout = time.Second

// probingClient issues an unrelated read on every history fetch, mimicking the
// rest of the app querying the single pooled connection while a sync is in
// flight. A fetch made from inside an open transaction starves that read,
// which is the regression this file guards.
type probingClient struct {
	tgclient.Client
	db     *sql.DB
	probes int
	stalls int
}

func (c *probingClient) GetHistory(ctx context.Context, peer tgclient.InputPeer, minID, offsetID int64, limit int) ([]tgclient.HistoryMessage, error) {
	probeCtx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	var watermark int64
	c.probes++
	if err := c.db.QueryRowContext(probeCtx, `SELECT last_synced_msg FROM channels WHERE channel_id = ?`, testChan).Scan(&watermark); err != nil {
		c.stalls++
	}
	return c.Client.GetHistory(ctx, peer, minID, offsetID, limit)
}

// newPooledSyncEnv mirrors backend.InitDB's single-connection pool, which is
// what makes one long transaction an application-wide outage rather than a
// local slowdown.
func newPooledSyncEnv(t *testing.T) (*sql.DB, *tgclient.Fake, *probingClient, *Engine) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := projection.MigratePersonalChannel(db, testChan); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	fake := tgclient.NewFake(7)
	probing := &probingClient{Client: fake, db: db}
	return db, fake, probing, NewEngine(db, probing, fakePeers{})
}

func TestFullHistoryScanLeavesReadsUnblockedDuringFetches(t *testing.T) {
	db, fake, probing, engine := newPooledSyncEnv(t)
	// More than one page, so the plan has an interior fetch as well as the
	// first one and the regression cannot hide behind a single-page scan.
	for i := range defaultPageSize + 20 {
		sendOp(t, fake, projection.Op{Type: projection.OpMkdir, Obj: folderObj(i), Parent: projection.RootParent, Name: folderObj(i)})
	}

	if err := engine.EnsureAuthoritative(context.Background(), testChan); err != nil {
		t.Fatalf("EnsureAuthoritative() error = %v", err)
	}
	if probing.probes < 3 {
		t.Fatalf("probes = %d, want the scan to fetch at least three pages", probing.probes)
	}
	if probing.stalls != 0 {
		t.Fatalf("%d of %d unrelated reads were starved while the scan fetched history", probing.stalls, probing.probes)
	}

	// The scan must still be atomic and complete despite the restructuring.
	channel, err := projection.GetChannel(db, testChan)
	if err != nil {
		t.Fatalf("GetChannel() error = %v", err)
	}
	if !channel.InitialSyncDone {
		t.Fatal("scan completed without marking the channel authoritative")
	}
	for i := range defaultPageSize + 20 {
		if !projection.FolderExists(db, testChan, folderObj(i)) {
			t.Fatalf("folder %s missing after full scan", folderObj(i))
		}
	}
}

func TestInitialSyncOfEmptyChannelLeavesReadsUnblockedDuringFetches(t *testing.T) {
	_, fake, probing, engine := newPooledSyncEnv(t)
	for i := range defaultPageSize + 20 {
		sendOp(t, fake, projection.Op{Type: projection.OpMkdir, Obj: folderObj(i), Parent: projection.RootParent, Name: folderObj(i)})
	}

	if err := engine.InitialSyncEmptyChannel(context.Background(), testChan); err != nil {
		t.Fatalf("InitialSyncEmptyChannel() error = %v", err)
	}
	if probing.stalls != 0 {
		t.Fatalf("%d of %d unrelated reads were starved while the scan fetched history", probing.stalls, probing.probes)
	}
}

func folderObj(i int) string {
	return "d:scan-" + string(rune('a'+i/26)) + string(rune('a'+i%26))
}

func seedScanHistory(t *testing.T, fake *tgclient.Fake, n int) {
	t.Helper()
	for i := range n {
		sendOp(t, fake, projection.Op{Type: projection.OpMkdir, Obj: folderObj(i), Parent: projection.RootParent, Name: folderObj(i)})
	}
}

func spoolRows(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM initial_scan_spool WHERE channel_id = ?`, testChan).Scan(&n); err != nil {
		t.Fatalf("count spool: %v", err)
	}
	return n
}

// spoolWatchingClient records how much history had been parked on disk by the
// time each fetch ran, and can fail a chosen fetch to simulate a scan that
// dies partway through.
type spoolWatchingClient struct {
	tgclient.Client
	db *sql.DB
	// failWhenSpooled aborts the first fetch that runs with history already
	// parked, which is exactly a scan dying after at least one page.
	failWhenSpooled bool
	maxSpool        int
	spoolSeen       bool
}

func (c *spoolWatchingClient) GetHistory(ctx context.Context, peer tgclient.InputPeer, minID, offsetID int64, limit int) ([]tgclient.HistoryMessage, error) {
	// The spool table only exists once a scan has started; before then this
	// is a counting-pass fetch and there is nothing to observe. The read is
	// deadlined because a fetch made from inside an open transaction would
	// otherwise starve it forever rather than fail the test.
	probeCtx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	var n int
	if err := c.db.QueryRowContext(probeCtx, `SELECT COUNT(*) FROM initial_scan_spool WHERE channel_id = ?`, testChan).Scan(&n); err == nil && n > 0 {
		c.spoolSeen = true
		if n > c.maxSpool {
			c.maxSpool = n
		}
		if c.failWhenSpooled {
			return nil, errors.New("injected mid-scan history failure")
		}
	}
	return c.Client.GetHistory(ctx, peer, minID, offsetID, limit)
}

// TestFullHistoryScanKeepsFetchedHistoryOutOfMemory pins the structural
// property that bounds the scan's footprint: fetched pages are already on disk
// while the scan is still fetching, rather than accumulating in a slice whose
// size is the channel's whole history.
func TestFullHistoryScanKeepsFetchedHistoryOutOfMemory(t *testing.T) {
	db, fake, _, _ := newPooledSyncEnv(t)
	// Three pages, so a fetch runs with more than one page already parked.
	seedScanHistory(t, fake, 2*defaultPageSize+20)
	watcher := &spoolWatchingClient{Client: fake, db: db}
	engine := NewEngine(db, watcher, fakePeers{})

	if err := engine.EnsureAuthoritative(context.Background(), testChan); err != nil {
		t.Fatalf("EnsureAuthoritative() error = %v", err)
	}
	if !watcher.spoolSeen {
		t.Fatal("no page was spooled before the scan finished fetching; history is being buffered in memory")
	}
	if watcher.maxSpool < defaultPageSize {
		t.Fatalf("peak spooled rows = %d, want a full page parked on disk", watcher.maxSpool)
	}
	if got := spoolRows(t, db); got != 0 {
		t.Fatalf("spool left %d rows behind after a successful scan", got)
	}
}

// TestInterruptedFullHistoryScanLeavesNoProjectionState guards the atomicity
// the spool exists to preserve. A scan killed partway must leave the channel
// indistinguishable from one that never ran: projection.ChannelIsEmpty gates
// both InitialSyncEmptyChannel and caption-less adoption, and a design that
// committed pages as it went would trip it here.
func TestInterruptedFullHistoryScanLeavesNoProjectionState(t *testing.T) {
	db, fake, _, _ := newPooledSyncEnv(t)
	seedScanHistory(t, fake, defaultPageSize+20)
	watcher := &spoolWatchingClient{Client: fake, db: db, failWhenSpooled: true}
	engine := NewEngine(db, watcher, fakePeers{})

	if err := engine.EnsureAuthoritative(context.Background(), testChan); err == nil {
		t.Fatal("EnsureAuthoritative() error = nil, want the injected failure")
	}
	if !watcher.spoolSeen {
		t.Fatal("test never reached a fetch with history already spooled")
	}
	empty, err := projection.ChannelIsEmpty(db, testChan)
	if err != nil {
		t.Fatalf("ChannelIsEmpty() error = %v", err)
	}
	if !empty {
		t.Fatal("an interrupted scan left projection state behind")
	}
	channel, err := projection.GetChannel(db, testChan)
	if err != nil {
		t.Fatalf("GetChannel() error = %v", err)
	}
	if channel.InitialSyncDone {
		t.Fatal("an interrupted scan marked the channel authoritative")
	}

	// The retry must still see a pristine channel and complete it in full.
	if err := NewEngine(db, fake, fakePeers{}).EnsureAuthoritative(context.Background(), testChan); err != nil {
		t.Fatalf("retry after interruption: %v", err)
	}
	for i := range defaultPageSize + 20 {
		if !projection.FolderExists(db, testChan, folderObj(i)) {
			t.Fatalf("folder %s missing after the retried scan", folderObj(i))
		}
	}
}
