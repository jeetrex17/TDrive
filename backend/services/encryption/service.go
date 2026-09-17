// Package encryption owns the vault: the lifecycle of the user's password, the
// only in-memory copy of the unlocked master key, and the gates that decide who
// may obtain it.
//
// backend/crypto supplies the primitives. This package adds the state and the
// policy, and publishes the wrapped key as a control op on the personal
// channel, so the vault is replicated into Telegram history and survives losing
// the local database. Only forgetting the password destroys it. The master key
// is never persisted in plaintext anywhere; "password remembered" means held in
// this process's memory and nothing more.
//
// Any path that could mint a new master key first forces an authoritative
// refresh, because a missing local encryption row is a cache miss and not proof
// of absence — generating a second master key would orphan every file encrypted
// under the first. Unlocking and changing the password deliberately skip that
// refresh: they can only use an existing config, never create one, so they work
// offline. A config is published before it is stored, so a failed publish
// leaves the vault locked rather than diverged, and changing the password
// re-wraps the same master key under a fresh salt rather than re-encrypting any
// file.
//
// The three key gates are not interchangeable and choosing the wrong one is a
// real vulnerability. The upload gate refuses any channel that is not the
// personal one. The channel-scoped read gate must be consulted before any cache
// is touched, so an encrypted row in a shared drive cannot borrow the personal
// vault key. The channel-unaware gate is safe only where the channel has
// already been proven.
//
// Mounts and media take a lease instead, which holds its own copy of the key,
// so locking the vault cannot retroactively poison a mount that is mid-read.
// The mount must close its lease when it stops serving encrypted data, and the
// caller must eject the mount before clearing the session.
//
// Key derivations are serialized so that concurrent unlock attempts cannot
// multiply Argon2id's memory cost into a self-inflicted denial of service.
package encryption

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"

	tdcrypto "TDrive/backend/crypto"
	"TDrive/backend/datadir"
	"TDrive/backend/mountpolicy"
	"TDrive/backend/projection"
)

type Status struct {
	Available          bool
	PasswordSet        bool
	PasswordRemembered bool
	Hint               string
}

var ErrPasswordRequired = errors.New("encryption password required")

var ErrPasswordAlreadySet = errors.New("encryption password already exists")

var ErrWrongPassword = errors.New("wrong password")

const masterKeySize = 32

type EmitOpFunc func(channelID int64, op projection.Op) error
type deriveKEKFunc func(password, salt []byte, params tdcrypto.Params) ([]byte, error)

type Config struct {
	DB                *sql.DB
	PersonalChannelID func() int64
	EmitOp            EmitOpFunc
	// EnsurePolicy must establish authoritative personal-channel history when
	// the local encryption config is absent. It prevents stale projections from
	// creating an unrelated replacement master key.
	EnsurePolicy mountpolicy.RefreshFunc
}

type Service struct {
	db                *sql.DB
	personalChannelID func() int64
	emitOp            EmitOpFunc
	ensurePolicy      mountpolicy.RefreshFunc
	masterKeyMu       sync.RWMutex
	masterKey         []byte
	kdfMu             sync.Mutex
	deriveKEK         deriveKEKFunc
}

func NewService(cfg Config) *Service {
	return &Service{
		db:                cfg.DB,
		personalChannelID: cfg.PersonalChannelID,
		emitOp:            cfg.EmitOp,
		ensurePolicy:      cfg.EnsurePolicy,
		deriveKEK:         tdcrypto.DeriveKEK,
	}
}

func (s *Service) Status() (Status, error) {
	return s.StatusContext(context.Background())
}

func (s *Service) StatusContext(ctx context.Context) (Status, error) {
	if err := s.ready(); err != nil {
		return Status{}, err
	}
	channelID := s.personalID()
	if channelID == 0 {
		return Status{Available: false}, nil
	}
	cfg, exists, err := mountpolicy.EnsurePersonalConfig(ctx, s.db, channelID, s.ensurePolicy)
	if err != nil {
		return Status{}, err
	}
	remembered := s.hasLoadedMasterKey()
	return Status{
		Available:          true,
		PasswordSet:        exists,
		PasswordRemembered: exists && remembered,
		Hint:               cfg.Hint,
	}, nil
}

func (s *Service) CreatePassword(password string, hint string) error {
	return s.CreatePasswordContext(context.Background(), password, hint)
}

func (s *Service) CreatePasswordContext(ctx context.Context, password string, hint string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := validateNewPassword(password); err != nil {
		return err
	}
	channelID, err := s.requiredPersonalID()
	if err != nil {
		return err
	}

	_, exists, err := mountpolicy.EnsurePersonalConfig(ctx, s.db, channelID, s.ensurePolicy)
	if err != nil {
		return err
	}
	if exists {
		return ErrPasswordAlreadySet
	}
	master, err := tdcrypto.NewMasterKey()
	if err != nil {
		return err
	}
	defer zeroBytes(master)
	cfg, err := s.buildConfig(channelID, password, hint, master, 0)
	if err != nil {
		slog.Error("encryption: build config for new password failed", "channel_id", channelID, "error", err)
		return err
	}
	if err := s.publishConfig(channelID, cfg); err != nil {
		slog.Error("encryption: publish new password config failed", "channel_id", channelID, "error", err)
		return err
	}
	if err := s.StoreMasterKey(master); err != nil {
		return err
	}
	slog.Info("encryption: vault password created", "channel_id", channelID, "hint_set", strings.TrimSpace(hint) != "")
	return nil
}

func (s *Service) UsePassword(password string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if strings.TrimSpace(password) == "" {
		return fmt.Errorf("password required")
	}
	channelID, err := s.requiredPersonalID()
	if err != nil {
		return err
	}

	existing, err := projection.GetEncryptionConfig(s.db, channelID)
	if errors.Is(err, projection.ErrEncryptionConfigNotFound) {
		return fmt.Errorf("encryption password is not set")
	}
	if err != nil {
		return err
	}
	if err := s.RememberPassword(existing, password); err != nil {
		slog.Warn("encryption: unlock vault failed", "channel_id", channelID, "error", err)
		return err
	}
	slog.Info("encryption: vault unlocked", "channel_id", channelID)
	return nil
}

func (s *Service) ChangePassword(currentPassword string, newPassword string, hint string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if strings.TrimSpace(currentPassword) == "" {
		return fmt.Errorf("current password required")
	}
	if err := validateNewPassword(newPassword); err != nil {
		return err
	}
	channelID, err := s.requiredPersonalID()
	if err != nil {
		return err
	}

	existing, err := projection.GetEncryptionConfig(s.db, channelID)
	if errors.Is(err, projection.ErrEncryptionConfigNotFound) {
		return fmt.Errorf("encryption password is not set")
	}
	if err != nil {
		return err
	}
	master, err := s.unwrapMasterKey(existing, currentPassword)
	if err != nil {
		slog.Warn("encryption: change password rejected, current password did not unlock vault", "channel_id", channelID)
		return err
	}
	defer zeroBytes(master)
	cfg, err := s.buildConfig(channelID, newPassword, hint, master, existing.CreatedAt)
	if err != nil {
		slog.Error("encryption: build config for password change failed", "channel_id", channelID, "error", err)
		return err
	}
	if err := s.publishConfig(channelID, cfg); err != nil {
		slog.Error("encryption: publish changed password config failed", "channel_id", channelID, "error", err)
		return err
	}
	if err := s.StoreMasterKey(master); err != nil {
		return err
	}
	slog.Info("encryption: vault password changed", "channel_id", channelID, "hint_set", strings.TrimSpace(hint) != "")
	return nil
}

func (s *Service) MasterKeyForUpload(channelID int64, wantEncrypted bool) ([]byte, error) {
	if !wantEncrypted {
		return nil, nil
	}
	if channelID == 0 || channelID != s.personalID() {
		return nil, fmt.Errorf("encryption is only available on My Drive")
	}
	if k, ok := s.LoadedMasterKey(); ok {
		return k, nil
	}
	return nil, ErrPasswordRequired
}

func (s *Service) RequireMasterKeyForFile(encrypted bool) ([]byte, error) {
	if !encrypted {
		return nil, nil
	}
	if k, ok := s.LoadedMasterKey(); ok {
		return k, nil
	}
	return nil, ErrPasswordRequired
}

// RequireMasterKeyForChannel returns a fresh key copy only for encrypted files
// in My Drive. File reads must use this channel-scoped form before touching
// any cache, which prevents an encrypted row from a shared drive borrowing the
// personal vault key.
func (s *Service) RequireMasterKeyForChannel(channelID int64, encrypted bool) ([]byte, error) {
	if !encrypted {
		return nil, nil
	}
	if channelID == 0 || channelID != s.personalID() {
		return nil, fmt.Errorf("encryption is only available on My Drive")
	}
	return s.RequireMasterKeyForFile(true)
}

func (s *Service) WriteCiphertextTemp(plain io.Reader, plaintextSize int64, masterKey []byte) (*os.File, error) {
	tmp, err := datadir.CreateCacheTemp("tdrive-enc-*")
	if err != nil {
		return nil, err
	}
	if err := tdcrypto.EncryptStream(plain, tmp, masterKey, plaintextSize); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return nil, err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return nil, err
	}
	return tmp, nil
}

func (s *Service) LoadedMasterKey() ([]byte, bool) {
	s.masterKeyMu.RLock()
	defer s.masterKeyMu.RUnlock()
	if s.masterKey == nil {
		return nil, false
	}
	if len(s.masterKey) != masterKeySize {
		return nil, false
	}
	return append([]byte(nil), s.masterKey...), true
}

func (s *Service) hasLoadedMasterKey() bool {
	s.masterKeyMu.RLock()
	defer s.masterKeyMu.RUnlock()
	return len(s.masterKey) == masterKeySize
}

// StoreMasterKey replaces the unlocked vault key with an owned copy. Passing
// anything other than one 32-byte key fails closed by locking the vault.
func (s *Service) StoreMasterKey(key []byte) error {
	if len(key) != masterKeySize {
		slog.Warn("encryption: rejecting master key with wrong length, locking vault", "length", len(key))
		s.Clear()
		return ErrInvalidMasterKey
	}
	cp := append([]byte(nil), key...)
	s.masterKeyMu.Lock()
	previous := s.masterKey
	s.masterKey = cp
	zeroBytes(previous)
	s.masterKeyMu.Unlock()
	slog.Debug("encryption: master key loaded into memory")
	return nil
}

func (s *Service) Clear() {
	s.masterKeyMu.Lock()
	previous := s.masterKey
	s.masterKey = nil
	zeroBytes(previous)
	s.masterKeyMu.Unlock()
	slog.Debug("encryption: vault key cleared from memory")
}

func (s *Service) RememberPassword(cfg projection.EncryptionConfig, password string) error {
	master, err := s.unwrapMasterKey(cfg, password)
	if err != nil {
		return err
	}
	defer zeroBytes(master)
	return s.StoreMasterKey(master)
}

func (s *Service) publishConfig(channelID int64, cfg projection.EncryptionConfig) error {
	if s.emitOp == nil {
		return fmt.Errorf("encryption emitter not ready")
	}
	return s.emitOp(channelID, configOp(cfg))
}

func (s *Service) requiredPersonalID() (int64, error) {
	channelID := s.personalID()
	if channelID == 0 {
		return 0, fmt.Errorf("personal drive not initialised yet")
	}
	return channelID, nil
}

func (s *Service) personalID() int64 {
	if s.personalChannelID == nil {
		return 0
	}
	return s.personalChannelID()
}

func (s *Service) ready() error {
	if s.db == nil {
		return fmt.Errorf("db not ready")
	}
	return nil
}

func validateNewPassword(password string) error {
	if strings.TrimSpace(password) == "" {
		return fmt.Errorf("password required")
	}
	if len(password) < 8 {
		return fmt.Errorf("use at least 8 characters")
	}
	return nil
}

func buildConfig(channelID int64, password string, hint string, master []byte, createdAt int64) (projection.EncryptionConfig, error) {
	return buildConfigWithKDF(channelID, password, hint, master, createdAt, tdcrypto.DeriveKEK)
}

func (s *Service) buildConfig(channelID int64, password string, hint string, master []byte, createdAt int64) (projection.EncryptionConfig, error) {
	s.kdfMu.Lock()
	defer s.kdfMu.Unlock()
	return buildConfigWithKDF(channelID, password, hint, master, createdAt, s.kdfFunction())
}

func buildConfigWithKDF(channelID int64, password string, hint string, master []byte, createdAt int64, derive deriveKEKFunc) (projection.EncryptionConfig, error) {
	params := tdcrypto.DefaultParams()
	salt, err := tdcrypto.NewSalt(params)
	if err != nil {
		return projection.EncryptionConfig{}, err
	}
	passwordBytes := []byte(password)
	defer zeroBytes(passwordBytes)
	kek, err := derive(passwordBytes, salt, params)
	if err != nil {
		return projection.EncryptionConfig{}, err
	}
	defer zeroBytes(kek)
	wrapped, err := tdcrypto.WrapMasterKey(master, kek)
	if err != nil {
		return projection.EncryptionConfig{}, err
	}
	check, err := tdcrypto.EncodeKeyCheck(master)
	if err != nil {
		return projection.EncryptionConfig{}, err
	}
	paramsJSON, err := tdcrypto.MarshalParams(params)
	if err != nil {
		return projection.EncryptionConfig{}, err
	}
	return projection.EncryptionConfig{
		ChannelID:        channelID,
		Enabled:          true,
		KDFSalt:          salt,
		KDFParamsJSON:    paramsJSON,
		WrappedMasterKey: wrapped,
		KeyCheck:         check,
		Hint:             strings.TrimSpace(hint),
		CreatedAt:        createdAt,
		Version:          1,
	}, nil
}

func configOp(cfg projection.EncryptionConfig) projection.Op {
	version := cfg.Version
	if version == 0 {
		version = 1
	}
	return projection.Op{
		Type:             projection.OpEncConfig,
		KDFSalt:          append([]byte(nil), cfg.KDFSalt...),
		KDFParamsJSON:    cfg.KDFParamsJSON,
		WrappedMasterKey: append([]byte(nil), cfg.WrappedMasterKey...),
		KeyCheck:         append([]byte(nil), cfg.KeyCheck...),
		Hint:             strings.TrimSpace(cfg.Hint),
		ConfigVersion:    version,
	}
}

func unwrapMasterKey(cfg projection.EncryptionConfig, password string) ([]byte, error) {
	return unwrapMasterKeyWithKDF(cfg, password, tdcrypto.DeriveKEK)
}

func (s *Service) unwrapMasterKey(cfg projection.EncryptionConfig, password string) ([]byte, error) {
	s.kdfMu.Lock()
	defer s.kdfMu.Unlock()
	return unwrapMasterKeyWithKDF(cfg, password, s.kdfFunction())
}

func unwrapMasterKeyWithKDF(cfg projection.EncryptionConfig, password string, derive deriveKEKFunc) ([]byte, error) {
	if cfg.Version != 1 {
		return nil, fmt.Errorf("%w: %w", projection.ErrInvalidEncryptionConfig, projection.ErrUnsupportedEncryptionConfigVersion)
	}
	params, err := tdcrypto.UnmarshalParams(cfg.KDFParamsJSON)
	if err != nil {
		return nil, err
	}
	if err := tdcrypto.ValidateVaultMaterial(cfg.KDFSalt, params, cfg.WrappedMasterKey, cfg.KeyCheck); err != nil {
		return nil, err
	}
	passwordBytes := []byte(password)
	defer zeroBytes(passwordBytes)
	kek, err := derive(passwordBytes, cfg.KDFSalt, params)
	if err != nil {
		return nil, err
	}
	defer zeroBytes(kek)
	master, err := tdcrypto.UnwrapMasterKey(cfg.WrappedMasterKey, kek)
	if err != nil {
		zeroBytes(master)
		if errors.Is(err, tdcrypto.ErrWrongPassword) {
			return nil, ErrWrongPassword
		}
		return nil, err
	}
	if err := tdcrypto.VerifyKeyCheck(master, cfg.KeyCheck); err != nil {
		zeroBytes(master)
		return nil, ErrWrongPassword
	}
	return master, nil
}

func (s *Service) kdfFunction() deriveKEKFunc {
	if s.deriveKEK == nil {
		return tdcrypto.DeriveKEK
	}
	return s.deriveKEK
}

func zeroBytes(buffer []byte) {
	clear(buffer)
}
