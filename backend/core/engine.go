package core

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"

	"TDrive/backend"
	"TDrive/backend/auth"
	"TDrive/backend/backfill"
	"TDrive/backend/livesync"
	"TDrive/backend/media"
	"TDrive/backend/projection"
	authsvc "TDrive/backend/services/auth"
	channelservice "TDrive/backend/services/channel"
	encservice "TDrive/backend/services/encryption"
	fileservice "TDrive/backend/services/file"
	folderservice "TDrive/backend/services/folder"
	lifecycleservice "TDrive/backend/services/lifecycle"
	readservice "TDrive/backend/services/read"
	userservice "TDrive/backend/services/user"
	tdsync "TDrive/backend/sync"
	"TDrive/backend/tgclient"
	"TDrive/backend/thumbnail"

	"github.com/gotd/td/telegram"
)

// EventSink is the narrow event boundary between the headless backend and a
// frontend. Wails turns these into runtime events; the daemon will stream them
// over its local socket; tests can pass nil.
type EventSink interface {
	Emit(name string, args ...any)
}

// WarnFunc is intentionally tiny so backend/core does not depend on a logging
// framework. Frontends can route warnings to stdout, stderr, or their own logs.
type WarnFunc func(format string, args ...any)

// TelegramConnectFunc builds the gotd client from persisted credentials and
// session state. It is injected mostly for tests; production uses auth.Connect.
type TelegramConnectFunc func() (*telegram.Client, error)

// Config contains the few frontend-owned details needed to wire the shared
// backend. Everything else lives in services under backend/.
type Config struct {
	Events EventSink
	Warnf  WarnFunc

	// Connect defaults to auth.Connect. Supplying it lets tests or future
	// binaries choose a different session store without changing services.
	Connect TelegramConnectFunc

	// TG defaults to tgclient.NewGotd(Connect). Supplying it is useful for
	// tests and keeps the engine independent of the concrete Telegram adapter.
	TG tgclient.Client

	// Thumbs is optional. Nil disables thumbnail caching for this engine.
	Thumbs *thumbnail.Cache

	// MaxConcurrentUploads caps active Telegram uploads across GUI/import,
	// daemon, and mount writers owned by this engine. <= 0 uses the file
	// service default.
	MaxConcurrentUploads int

	// SkipDBInit is for tests that already installed backend.DB. Normal GUI and
	// daemon startup should leave this false.
	SkipDBInit bool

	// EncryptionPolicyRefresh is a narrow test seam for authoritative
	// policy failures. Production leaves it nil and uses the sync engine.
	EncryptionPolicyRefresh func(context.Context, int64) error
}

// Engine owns the reusable, headless TDrive backend. The Wails app and daemon
// should both build one of these instead of duplicating service wiring.
type Engine struct {
	ctx context.Context

	connect TelegramConnectFunc
	events  EventSink
	warnf   WarnFunc

	client     *telegram.Client
	tg         tgclient.Client
	auth       *authsvc.Service
	enc        *encservice.Service
	files      *fileservice.Service
	folders    *folderservice.Service
	media      *media.Service
	reads      *readservice.Service
	lifecycle  *lifecycleservice.Service
	users      *userservice.Service
	syncEngine *tdsync.Engine
	liveSync   *livesync.Coordinator
	active     *lifecycleservice.ActiveDrive
	selfUserID atomic.Int64
	thumbs     *thumbnail.Cache
	maxUploads int
	policySync func(context.Context, int64) error
}

// New initializes storage, Telegram adapters, and all service dependencies.
// It deliberately preserves the existing GUI startup side effects: schema
// creation, saved personal-drive migration, and lazy Telegram connection.
func New(ctx context.Context, cfg Config) (*Engine, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	usingDefaultConnect := cfg.Connect == nil
	if cfg.Connect == nil {
		cfg.Connect = auth.Connect
	}
	if cfg.Warnf == nil {
		cfg.Warnf = func(format string, args ...any) {
			fmt.Printf(format, args...)
		}
	}

	e := &Engine{
		ctx:        ctx,
		connect:    cfg.Connect,
		events:     cfg.Events,
		warnf:      cfg.Warnf,
		tg:         cfg.TG,
		active:     lifecycleservice.NewActiveDrive(),
		thumbs:     cfg.Thumbs,
		maxUploads: cfg.MaxConcurrentUploads,
		policySync: cfg.EncryptionPolicyRefresh,
	}
	var liveActivity *livesync.TelegramActivity
	if e.tg == nil {
		if usingDefaultConnect {
			liveActivity = livesync.NewTelegramActivity(256)
			e.tg = tgclient.NewGotd(func() (*telegram.Client, error) {
				return auth.ConnectWithOptions(telegram.Options{UpdateHandler: liveActivity})
			})
		} else {
			e.tg = tgclient.NewGotd(e.connect)
		}
	}

	if client, err := e.connect(); err != nil {
		e.warnf("Warning: Telegram connect failed (offline?): %v\n", err)
	} else {
		e.client = client
	}

	if !cfg.SkipDBInit {
		if err := backend.InitDB(); err != nil {
			return nil, fmt.Errorf("init db: %w", err)
		}
		if err := backend.EnsureSchema(); err != nil {
			return nil, fmt.Errorf("init db schema: %w", err)
		}
	}

	e.auth = authsvc.NewService(e.events)
	e.enc = e.newEncryptionService()
	e.folders = e.newFolderService()
	e.files = e.newFileService()
	e.reads = e.newReadService()
	e.media = e.newMediaService()
	e.syncEngine = tdsync.NewEngine(backend.DB, e.tg, peerResolverFn(e.ResolvePeer))
	e.syncEngine.EmitTomb = func(channelID int64, fileMsgID int64) error {
		_, err := e.EmitAndProject(channelID, projection.Op{
			Type: projection.OpTomb,
			Obj:  fmt.Sprintf("%s%d", projection.FileIDPrefix, fileMsgID),
		})
		return err
	}
	e.lifecycle = e.newLifecycleService()
	e.users = e.newUserService()
	e.startLiveSync(liveActivity)

	if savedID, err := auth.LoadConfig(); err == nil && savedID != 0 {
		if err := e.lifecycle.UsePersonalChannel(e.ctx, savedID); err != nil {
			e.warnf("Warning: migration failed: %v\n", err)
		}
	}

	return e, nil
}

func (e *Engine) startLiveSync(activity *livesync.TelegramActivity) {
	if e == nil || activity == nil || e.lifecycle == nil {
		return
	}
	e.liveSync = livesync.NewCoordinator(livesync.Config{
		Activity: activity,
		Syncer:   e.lifecycle,
		Events:   e.events,
		Warnf: func(format string, args ...any) {
			e.warnf(format, args...)
		},
		ListChannels: func(ctx context.Context) ([]int64, error) {
			if backend.DB == nil {
				return nil, fmt.Errorf("db not ready")
			}
			channels, err := projection.ListChannels(backend.DB)
			if err != nil {
				return nil, err
			}
			ids := make([]int64, 0, len(channels))
			for _, channel := range channels {
				if channel.ChannelID > 0 {
					ids = append(ids, channel.ChannelID)
				}
			}
			return ids, nil
		},
	})
	e.liveSync.Start(e.ctx)
}

func (e *Engine) Close() {
	if e != nil && e.liveSync != nil {
		e.liveSync.Stop()
	}
	if e != nil && e.media != nil {
		_ = e.media.Close()
	}
	if e != nil && e.tg != nil {
		e.tg.Close()
	}
	if e != nil {
		e.ClearEncryptionSession()
	}
}

func (e *Engine) RawClient() *telegram.Client {
	if e == nil {
		return nil
	}
	return e.client
}

func (e *Engine) Telegram() tgclient.Client {
	if e == nil {
		return nil
	}
	return e.tg
}

func (e *Engine) ActiveChannelID() int64 {
	if e == nil || e.active == nil {
		return 0
	}
	return e.active.ID()
}

func (e *Engine) SetActiveChannelID(channelID int64) {
	if e == nil {
		return
	}
	if e.active == nil {
		e.active = lifecycleservice.NewActiveDrive()
	}
	e.active.Set(channelID)
}

func (e *Engine) SetActiveChannel(channelID int64) error {
	if channelID <= 0 {
		return fmt.Errorf("invalid channel id")
	}
	if backend.DB == nil {
		return fmt.Errorf("db not ready")
	}
	var got int64
	err := backend.DB.QueryRow(`SELECT channel_id FROM channels WHERE channel_id = ?`, channelID).Scan(&got)
	if err != nil {
		return fmt.Errorf("channel not known locally")
	}
	e.SetActiveChannelID(channelID)
	return nil
}

func (e *Engine) ResolvePeer(ctx context.Context, channelID int64) (tgclient.InputPeer, error) {
	return e.ChannelPeer(ctx, channelID)
}

func (e *Engine) ChannelPeer(ctx context.Context, channelID int64) (tgclient.InputPeer, error) {
	if e == nil || e.tg == nil {
		return tgclient.InputPeer{}, fmt.Errorf("tg client not ready")
	}
	return e.tg.ResolveDriveChannel(ctx, channelID)
}

func (e *Engine) ActorID(ctx context.Context) (int64, error) {
	if e == nil || e.tg == nil {
		return 0, fmt.Errorf("tg client not ready")
	}
	if id := e.selfUserID.Load(); id != 0 {
		return id, nil
	}
	id, err := e.tg.SelfID(ctx)
	if err != nil {
		return 0, fmt.Errorf("self user id: %w", err)
	}
	if id == 0 {
		return 0, fmt.Errorf("self user id not found")
	}
	e.selfUserID.Store(id)
	return id, nil
}

// EmitAndProject sends one TDX control op to Telegram, then applies it to the
// local projection. If local projection fails after the send, the op remains in
// Telegram and the next sync can replay it.
func (e *Engine) EmitAndProject(channelID int64, op projection.Op) (int64, error) {
	return e.EmitAndProjectContext(e.ctx, channelID, op)
}

// EmitAndProjectContext is the request-scoped form used by imports and other
// cancellable operations. Idempotent Telegram retries reuse one random_id, so
// a lost response cannot create duplicate control messages.
func (e *Engine) EmitAndProjectContext(ctx context.Context, channelID int64, op projection.Op) (int64, error) {
	if e == nil || e.tg == nil {
		return 0, fmt.Errorf("tg client not ready")
	}
	if ctx == nil {
		return 0, fmt.Errorf("control write requires a context")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	actorID, err := e.ActorID(ctx)
	if err != nil {
		return 0, err
	}
	header := projection.Format(op)
	msgID, err := e.sendControlContext(ctx, channelID, header)
	if err != nil {
		return 0, err
	}
	if _, err := projection.ProjectFromOp(backend.DB, channelID, msgID, op, actorID, header); err != nil {
		e.warnf("warn: projection failed after send msg=%d op=%s: %v\n", msgID, op.Type, err)
		return msgID, fmt.Errorf("%w: msg=%d op=%s: %w", projection.ErrControlProjection, msgID, op.Type, err)
	}
	return msgID, nil
}

type projectedOp struct {
	msgID  int64
	op     projection.Op
	header string
}

// EmitAndProjectBatch sends a sequence of control ops, then commits their
// local projection in one transaction. The network side cannot be atomic, but
// the UI-facing cache must be: if a later send fails, the already-sent ops will
// converge through the next sync instead of locally half-applying a subtree.
func (e *Engine) EmitAndProjectBatch(channelID int64, ops []projection.Op) error {
	return e.EmitAndProjectBatchContext(e.ctx, channelID, ops)
}

func (e *Engine) EmitAndProjectBatchContext(ctx context.Context, channelID int64, ops []projection.Op) error {
	if len(ops) == 0 {
		return nil
	}
	if e == nil || e.tg == nil {
		return fmt.Errorf("tg client not ready")
	}
	if ctx == nil {
		return fmt.Errorf("control write requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	actorID, err := e.ActorID(ctx)
	if err != nil {
		return err
	}

	sent := make([]projectedOp, 0, len(ops))
	for _, op := range ops {
		header := projection.Format(op)
		msgID, err := e.sendControlContext(ctx, channelID, header)
		if err != nil {
			return err
		}
		sent = append(sent, projectedOp{msgID: msgID, op: op, header: header})
	}

	tx, err := backend.DB.Begin()
	if err != nil {
		return fmt.Errorf("%w: begin batch tx: %w", projection.ErrControlProjection, err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	for _, item := range sent {
		if _, err = projection.ProjectFromOpTx(tx, channelID, item.msgID, item.op, actorID, item.header); err != nil {
			return fmt.Errorf("%w: batch msg=%d op=%s: %w", projection.ErrControlProjection, item.msgID, item.op.Type, err)
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("%w: commit batch tx: %w", projection.ErrControlProjection, err)
	}
	return nil
}

func (e *Engine) sendControlContext(ctx context.Context, channelID int64, header string) (int64, error) {
	peer, cached, err := e.controlPeer(ctx, channelID)
	if err != nil {
		return 0, err
	}
	randomID, err := tgclient.StableRandomID(projection.NewUploadUUID(), "control")
	if err != nil {
		return 0, err
	}

	msgID, err := e.sendControlWithRetry(ctx, peer, header, randomID)
	if err == nil || !cached || !isStalePeerError(err) {
		return msgID, err
	}

	// Access hashes can rotate. Refresh once and retry with the same random_id;
	// Telegram can therefore deduplicate an accepted send whose response was
	// lost while the local cache was stale.
	fresh, resolveErr := e.ChannelPeer(ctx, channelID)
	if resolveErr != nil {
		return 0, err
	}
	if backend.DB != nil {
		if updateErr := projection.UpdateAccessHash(backend.DB, channelID, fresh.AccessHash); updateErr != nil {
			e.warnf("warn: could not refresh channel access hash %d: %v\n", channelID, updateErr)
		}
	}
	return e.sendControlWithRetry(ctx, fresh, header, randomID)
}

func (e *Engine) sendControlWithRetry(ctx context.Context, peer tgclient.InputPeer, header string, randomID int64) (int64, error) {
	if _, ok := e.tg.(tgclient.IdempotentSender); !ok {
		// A legacy client cannot safely retry an unknown send outcome.
		return e.tg.SendControl(ctx, peer, header, true)
	}

	var msgID int64
	policy := tgclient.DefaultWriteFloodWaitRetryPolicy()
	err := policy.Do(ctx, func() error {
		var sendErr error
		msgID, sendErr = tgclient.SendControlIdempotent(ctx, e.tg, peer, header, true, randomID)
		return sendErr
	})
	return msgID, err
}

func (e *Engine) controlPeer(ctx context.Context, channelID int64) (tgclient.InputPeer, bool, error) {
	if backend.DB != nil {
		if channel, err := projection.GetChannel(backend.DB, channelID); err == nil && channel.AccessHash != 0 {
			return tgclient.InputPeer{ChannelID: channelID, AccessHash: channel.AccessHash}, true, nil
		}
	}
	peer, err := e.ChannelPeer(ctx, channelID)
	return peer, false, err
}

func isStalePeerError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToUpper(err.Error())
	return strings.Contains(message, "CHANNEL_INVALID") ||
		strings.Contains(message, "CHANNEL_PRIVATE") ||
		strings.Contains(message, "PEER_ID_INVALID") ||
		strings.Contains(message, "ACCESS_HASH")
}

func (e *Engine) AuthService() *authsvc.Service {
	if e.auth == nil {
		e.auth = authsvc.NewService(e.events)
	}
	return e.auth
}

func (e *Engine) EncryptionService() *encservice.Service {
	if e.enc == nil {
		e.enc = e.newEncryptionService()
	}
	return e.enc
}

func (e *Engine) FileService() *fileservice.Service {
	if e.files == nil {
		e.files = e.newFileService()
	}
	return e.files
}

func (e *Engine) FolderService() *folderservice.Service {
	if e.folders == nil {
		e.folders = e.newFolderService()
	}
	return e.folders
}

func (e *Engine) ReadService() *readservice.Service {
	if e.reads == nil {
		e.reads = e.newReadService()
	}
	return e.reads
}

func (e *Engine) MediaService() *media.Service {
	if e.media == nil {
		e.media = e.newMediaService()
	}
	return e.media
}

func (e *Engine) LifecycleService() *lifecycleservice.Service {
	if e.lifecycle == nil {
		e.lifecycle = e.newLifecycleService()
	}
	return e.lifecycle
}

func (e *Engine) UserService() *userservice.Service {
	if e.users == nil {
		e.users = e.newUserService()
	}
	return e.users
}

func (e *Engine) ChannelService() *channelservice.Service {
	return &channelservice.Service{
		DB:        backend.DB,
		TG:        e.tg,
		Sync:      e.syncEngine,
		GetActive: e.ActiveChannelID,
		SetActive: e.SetActiveChannelID,
	}
}

// EnsureEncryptionPolicy synchronizes enough channel history to make a
// missing personal-drive encryption configuration authoritative. Mount entry
// and password-setup entry points call this only after both the derived config
// and canonical local replay lack a policy.
func (e *Engine) EnsureEncryptionPolicy(ctx context.Context, channelID int64) error {
	if e != nil && e.policySync != nil {
		return e.policySync(ctx, channelID)
	}
	if e == nil || e.syncEngine == nil {
		return fmt.Errorf("encryption policy sync is unavailable")
	}
	return e.syncEngine.EnsureAuthoritative(ctx, channelID)
}

func (e *Engine) ClearEncryptionSession() {
	if e != nil && e.enc != nil {
		e.enc.Clear()
	}
}

func (e *Engine) ClearUserCache() {
	if e != nil && e.users != nil {
		e.users.ClearCache()
	}
}

func (e *Engine) newEncryptionService() *encservice.Service {
	return encservice.NewService(encservice.Config{
		DB:                backend.DB,
		PersonalChannelID: PersonalChannelID,
		EnsurePolicy:      e.EnsureEncryptionPolicy,
		EmitOp: func(channelID int64, op projection.Op) error {
			_, err := e.EmitAndProject(channelID, op)
			return err
		},
	})
}

func (e *Engine) newFolderService() *folderservice.Service {
	return &folderservice.Service{
		DB:    backend.DB,
		TG:    e.tg,
		Peers: peerResolverFn(e.ResolvePeer),
		EmitOp: func(channelID int64, op projection.Op) error {
			_, err := e.EmitAndProject(channelID, op)
			return err
		},
		EmitOpContext: func(ctx context.Context, channelID int64, op projection.Op) error {
			_, err := e.EmitAndProjectContext(ctx, channelID, op)
			return err
		},
		EmitOps: func(channelID int64, ops []projection.Op) error {
			return e.EmitAndProjectBatch(channelID, ops)
		},
		EmitOpsContext: func(ctx context.Context, channelID int64, ops []projection.Op) error {
			return e.EmitAndProjectBatchContext(ctx, channelID, ops)
		},
		ActorID: func(ctx context.Context) (int64, error) {
			return e.ActorID(ctx)
		},
		RequireEncryptionKey: func(encrypted bool) ([]byte, error) {
			key, err := e.EncryptionService().RequireMasterKeyForFile(encrypted)
			if err != nil {
				return nil, encservice.ErrPasswordRequired
			}
			return key, nil
		},
		Warnf: func(format string, args ...any) {
			e.warnf(format, args...)
		},
	}
}

func (e *Engine) newFileService() *fileservice.Service {
	return &fileservice.Service{
		DB:    backend.DB,
		TG:    e.tg,
		Peers: peerResolverFn(e.ResolvePeer),
		EmitOp: func(channelID int64, op projection.Op) (int64, error) {
			return e.EmitAndProject(channelID, op)
		},
		EmitOpContext: func(ctx context.Context, channelID int64, op projection.Op) (int64, error) {
			return e.EmitAndProjectContext(ctx, channelID, op)
		},
		ActorID: func(ctx context.Context) (int64, error) {
			return e.ActorID(ctx)
		},
		RequireEncryptionKey: func(encrypted bool) ([]byte, error) {
			key, err := e.EncryptionService().RequireMasterKeyForFile(encrypted)
			if err != nil {
				return nil, encservice.ErrPasswordRequired
			}
			return key, nil
		},
		MasterKeyForUpload: func(channelID int64, wantEncrypted bool) ([]byte, error) {
			return e.EncryptionService().MasterKeyForUpload(channelID, wantEncrypted)
		},
		WriteCiphertextTemp: func(plain io.Reader, plaintextSize int64, masterKey []byte) (*os.File, error) {
			return e.EncryptionService().WriteCiphertextTemp(plain, plaintextSize, masterKey)
		},
		CreateFolder: func(ctx context.Context, channelID int64, name, parentID string) (string, error) {
			f, err := e.FolderService().CreateContext(ctx, channelID, name, parentID)
			return f.ID, err
		},
		Events: e.events,
		Warnf: func(format string, args ...any) {
			e.warnf(format, args...)
		},
		Thumbs:               e.thumbs,
		MaxConcurrentUploads: e.maxUploads,
	}
}

func (e *Engine) newReadService() *readservice.Service {
	return &readservice.Service{
		DB:    backend.DB,
		TG:    e.tg,
		Peers: peerResolverFn(e.ResolvePeer),
	}
}

func (e *Engine) newMediaService() *media.Service {
	var ranges tgclient.RangeClient
	if rc, ok := e.tg.(tgclient.RangeClient); ok {
		ranges = rc
	}
	return media.NewService(media.Config{
		DB:     backend.DB,
		Peers:  peerResolverFn(e.ResolvePeer),
		Ranges: ranges,
		Thumbs: e.thumbs,
	})
}

func (e *Engine) newLifecycleService() *lifecycleservice.Service {
	return lifecycleservice.NewService(lifecycleservice.Config{
		DB:       backend.DB,
		Sync:     e.syncEngine,
		Backfill: backfill.NewRunner(backend.DB, e.tg, peerResolverFn(e.ResolvePeer)),
		Active:   e.active,
		Events:   e.events,
		PersonalChannel: func(ctx context.Context) (int64, error) {
			client, err := e.connect()
			if err != nil {
				return 0, fmt.Errorf("Could not connect: %w", err)
			}
			var channelID int64
			err = client.Run(ctx, func(ctx context.Context) error {
				id, err := auth.GetTDriveChannel(ctx, client)
				if err != nil {
					return err
				}
				channelID = id
				return nil
			})
			return channelID, err
		},
		Warnf: func(format string, args ...any) {
			e.warnf(format, args...)
		},
	})
}

func (e *Engine) newUserService() *userservice.Service {
	return &userservice.Service{
		DB:    backend.DB,
		TG:    e.tg,
		Peers: peerResolverFn(e.ResolvePeer),
		ActorID: func(ctx context.Context) (int64, error) {
			return e.ActorID(ctx)
		},
		Active: e.ActiveChannelID,
	}
}

type peerResolverFn func(context.Context, int64) (tgclient.InputPeer, error)

func (f peerResolverFn) ResolvePeer(ctx context.Context, channelID int64) (tgclient.InputPeer, error) {
	return f(ctx, channelID)
}

// PersonalChannelID returns the saved personal drive id without requiring it to
// be the currently active drive. It returns 0 before first-run drive setup.
func PersonalChannelID() int64 {
	id, _ := auth.LoadConfig()
	return id
}
