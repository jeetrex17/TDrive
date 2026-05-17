package encryption

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"

	tdcrypto "TDrive/backend/crypto"
	"TDrive/backend/projection"
)

type Status struct {
	Available          bool
	PasswordSet        bool
	PasswordRemembered bool
	Hint               string
}

var ErrPasswordRequired = errors.New("encryption password required")

type EmitOpFunc func(channelID int64, op projection.Op) error

type Config struct {
	DB                *sql.DB
	PersonalChannelID func() int64
	EmitOp            EmitOpFunc
}

type Service struct {
	db                *sql.DB
	personalChannelID func() int64
	emitOp            EmitOpFunc
	masterKey         atomic.Pointer[[]byte]
}

func NewService(cfg Config) *Service {
	return &Service{
		db:                cfg.DB,
		personalChannelID: cfg.PersonalChannelID,
		emitOp:            cfg.EmitOp,
	}
}

func (s *Service) Status() (Status, error) {
	if err := s.ready(); err != nil {
		return Status{}, err
	}
	channelID := s.personalID()
	if channelID == 0 {
		return Status{Available: false}, nil
	}
	cfg, err := projection.GetEncryptionConfig(s.db, channelID)
	exists := err == nil
	if err != nil && !errors.Is(err, projection.ErrEncryptionConfigNotFound) {
		return Status{}, err
	}
	_, remembered := s.LoadedMasterKey()
	return Status{
		Available:          true,
		PasswordSet:        exists,
		PasswordRemembered: exists && remembered,
		Hint:               cfg.Hint,
	}, nil
}

func (s *Service) CreatePassword(password string, hint string) error {
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

	_, err = projection.GetEncryptionConfig(s.db, channelID)
	if err != nil && !errors.Is(err, projection.ErrEncryptionConfigNotFound) {
		return err
	}
	if err == nil {
		return fmt.Errorf("encryption password already exists")
	}
	master, err := tdcrypto.NewMasterKey()
	if err != nil {
		return err
	}
	cfg, err := buildConfig(channelID, password, hint, master, 0)
	if err != nil {
		return err
	}
	if err := s.publishConfig(channelID, cfg); err != nil {
		return err
	}
	s.StoreMasterKey(master)
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
	return s.RememberPassword(existing, password)
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
	master, err := unwrapMasterKey(existing, currentPassword)
	if err != nil {
		return err
	}
	cfg, err := buildConfig(channelID, newPassword, hint, master, existing.CreatedAt)
	if err != nil {
		return err
	}
	if err := s.publishConfig(channelID, cfg); err != nil {
		return err
	}
	s.StoreMasterKey(master)
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

func (s *Service) WriteCiphertextTemp(plain io.Reader, plaintextSize int64, masterKey []byte) (*os.File, error) {
	tmp, err := os.CreateTemp("", "tdrive-enc-*")
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
	p := s.masterKey.Load()
	if p == nil {
		return nil, false
	}
	return append([]byte(nil), (*p)...), true
}

func (s *Service) StoreMasterKey(key []byte) {
	cp := append([]byte(nil), key...)
	s.masterKey.Store(&cp)
}

func (s *Service) Clear() {
	s.masterKey.Store(nil)
}

func (s *Service) RememberPassword(cfg projection.EncryptionConfig, password string) error {
	master, err := unwrapMasterKey(cfg, password)
	if err != nil {
		return err
	}
	s.StoreMasterKey(master)
	return nil
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
	params := tdcrypto.DefaultParams()
	salt, err := tdcrypto.NewSalt(params)
	if err != nil {
		return projection.EncryptionConfig{}, err
	}
	kek := tdcrypto.DeriveKEK([]byte(password), salt, params)
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
	params, err := tdcrypto.UnmarshalParams(cfg.KDFParamsJSON)
	if err != nil {
		return nil, err
	}
	kek := tdcrypto.DeriveKEK([]byte(password), cfg.KDFSalt, params)
	master, err := tdcrypto.UnwrapMasterKey(cfg.WrappedMasterKey, kek)
	if err != nil {
		if errors.Is(err, tdcrypto.ErrWrongPassword) {
			return nil, fmt.Errorf("wrong password")
		}
		return nil, err
	}
	if err := tdcrypto.VerifyKeyCheck(master, cfg.KeyCheck); err != nil {
		return nil, fmt.Errorf("wrong password")
	}
	return master, nil
}
