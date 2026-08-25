package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"TDrive/backend/mountpolicy"
	"TDrive/backend/projection"
	encservice "TDrive/backend/services/encryption"
	readservice "TDrive/backend/services/read"

	_ "modernc.org/sqlite"
)

func TestResolveMountEncryptionPolicyLogsRefreshDetailAndReturnsSafeError(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	const channelID int64 = 909_001
	if err := projection.MigratePersonalChannel(db, channelID); err != nil {
		t.Fatalf("migrate projection: %v", err)
	}

	detail := errors.New("telegram history unavailable")
	engine := &Engine{
		reads: &readservice.Service{DB: db},
		enc:   encservice.NewService(encservice.Config{}),
		policySync: func(context.Context, int64) error {
			return detail
		},
	}
	warnings := ""
	warnf := func(format string, args ...any) {
		warnings += fmt.Sprintf(format, args...)
	}

	policy, err := engine.ResolveMountEncryptionPolicy(context.Background(), channelID, projection.KindPersonal, nil, warnf)
	if policy != (mountpolicy.Policy{}) || !errors.Is(err, mountpolicy.ErrEncryptionPolicyUnavailable) || errors.Is(err, detail) {
		t.Fatalf("ResolveMountEncryptionPolicy() = (%#v, %v)", policy, err)
	}
	if !strings.Contains(warnings, detail.Error()) {
		t.Fatalf("warning = %q, want refresh detail", warnings)
	}
}

func TestResolveMountEncryptionPolicySkipsSharedDrives(t *testing.T) {
	policy, err := (*Engine)(nil).ResolveMountEncryptionPolicy(context.Background(), 1, projection.KindShared, nil, nil)
	if err != nil || policy != (mountpolicy.Policy{}) {
		t.Fatalf("ResolveMountEncryptionPolicy(shared) = (%#v, %v)", policy, err)
	}
}
