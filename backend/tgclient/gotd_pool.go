package tgclient

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// MediaPoolSize is how many MTProto connections carry upload.getFile for one
// data center. Telegram meters file transfer per connection, so the single
// connection gotd dials for a FILE_MIGRATE hop tops out near a megabyte a
// second however many requests are in flight on it. Desktop clients download
// over several connections; four is their usual number. Each one costs a key
// exchange and an authorization import when the pool is first dialed, which
// WarmTransport pays at startup.
const MediaPoolSize = 4

// UploadThreads is how many 512 KiB parts one upload keeps in flight across
// the home data center's pool. gotd's default is a single part, which makes
// an upload wait a full round trip per part however fast the link is.
const UploadThreads = 2 * MediaPoolSize

// poolRetryAfter is how long a data center whose pool could not be dialed
// keeps reading over the primary connection before another attempt.
const poolRetryAfter = 30 * time.Second

// fileAPI returns the invoker for one range read: the pooled connections to
// the document's data center when it is known and reachable, otherwise the
// primary connection, which still works but funnels every read through one
// socket and Telegram's FILE_MIGRATE redirect. pooled reports which it was.
func (g *Gotd) fileAPI(ctx context.Context, client *telegram.Client, dcID int) (api *tg.Client, pooled bool) {
	if dcID <= 0 {
		return client.API(), false
	}
	invoker, err := g.filePool(ctx, client, dcID)
	if err != nil {
		return client.API(), false
	}
	return tg.NewClient(invoker), true
}

// uploadAPI returns the pool for the home data center, where uploaded parts
// are stored, or the primary connection while that pool cannot be dialed.
func (g *Gotd) uploadAPI(ctx context.Context, client *telegram.Client) *tg.Client {
	api, _ := g.fileAPI(ctx, client, client.Config().ThisDC)
	return api
}

// downloadVia runs one whole-file transfer over the pool for the document's
// data center, repeating it on the primary connection should Telegram answer
// with a redirect the pool cannot follow.
func (g *Gotd) downloadVia(ctx context.Context, client *telegram.Client, doc *tg.Document, transfer func(api *tg.Client) error) error {
	api, pooled := g.fileAPI(ctx, client, doc.DCID)
	err := transfer(api)
	if pooled && isFileMigrate(err) {
		return transfer(client.API())
	}
	return err
}

// filePool returns the connection pool for dcID, dialing it on first use.
// Concurrent first reads share one dial, and a data center that refused the
// dial is left alone for a while instead of being retried on every block.
func (g *Gotd) filePool(ctx context.Context, client *telegram.Client, dcID int) (telegram.CloseInvoker, error) {
	g.mediaMu.Lock()
	invoker, ok := g.pools[dcID]
	retryAt := g.poolRetryAt[dcID]
	g.mediaMu.Unlock()
	if ok {
		return invoker, nil
	}
	if time.Now().Before(retryAt) {
		return nil, fmt.Errorf("tgclient: file pool dc %d: dial backoff", dcID)
	}

	result, err, _ := g.poolFlights.Do(strconv.Itoa(dcID), func() (any, error) {
		return g.dialFilePool(ctx, client, dcID)
	})
	if err != nil {
		return nil, err
	}
	return result.(telegram.CloseInvoker), nil
}

// dialFilePool opens the pool for dcID. The primary data center's pool shares
// the primary session's auth key; any other data center gets its own pool and
// gotd imports the authorization into it, once at dial and once per further
// connection the pool opens.
func (g *Gotd) dialFilePool(ctx context.Context, client *telegram.Client, dcID int) (telegram.CloseInvoker, error) {
	started := time.Now()
	primary := client.Config().ThisDC
	if primary == 0 {
		return nil, fmt.Errorf("tgclient: file pool dc %d: primary data center unknown", dcID)
	}
	var invoker telegram.CloseInvoker
	var err error
	if dcID == primary {
		invoker, err = client.Pool(MediaPoolSize)
	} else {
		invoker, err = client.DC(ctx, dcID, MediaPoolSize)
	}

	g.mediaMu.Lock()
	defer g.mediaMu.Unlock()
	if err != nil {
		// Cancellation, whether the caller's or a run scope that already
		// ended, says nothing about the data center; only a refused dial
		// earns the backoff.
		if !errors.Is(err, context.Canceled) {
			g.poolRetryAt[dcID] = time.Now().Add(poolRetryAfter)
			slog.Warn("tgclient: file pool unavailable, reading over the primary connection", "dc", dcID, "error", err)
		}
		return nil, fmt.Errorf("tgclient: file pool dc %d: %w", dcID, err)
	}
	if existing, ok := g.pools[dcID]; ok {
		// The scope restarted while this dial was in flight and a newer pool
		// is already registered; keep that one.
		_ = invoker.Close()
		return existing, nil
	}
	g.pools[dcID] = invoker
	slog.Info("tgclient: file pool ready", "dc", dcID, "connections", MediaPoolSize, "elapsed", time.Since(started))
	return invoker, nil
}

// isFileMigrate reports Telegram's answer that a document lives on another
// data center. The primary connection follows that redirect itself; a pool
// pointed at the wrong data center cannot.
func isFileMigrate(err error) bool {
	rpcErr, ok := tgerr.As(err)
	return ok && rpcErr.Type == "FILE_MIGRATE"
}
