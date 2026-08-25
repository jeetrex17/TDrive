package projection

import (
	"errors"
	"testing"
)

func TestRebuildEncryptionConfigFromReplayRestoresCanonicalConfig(t *testing.T) {
	db := newTestDB(t)
	cfg := validEncryptionConfig()
	op := encryptionConfigOp(cfg)
	header := Format(op)

	if _, err := ProjectFromOp(db, testChan, 101, op, 7, header); err != nil {
		t.Fatalf("project encryption config: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM encryption WHERE channel_id = ?`, testChan); err != nil {
		t.Fatalf("delete derived encryption config: %v", err)
	}

	found, err := RebuildEncryptionConfigFromReplay(db, testChan)
	if err != nil {
		t.Fatalf("RebuildEncryptionConfigFromReplay() error = %v", err)
	}
	if !found {
		t.Fatal("RebuildEncryptionConfigFromReplay() found = false, want true")
	}
	got, err := GetEncryptionConfig(db, testChan)
	if err != nil {
		t.Fatalf("GetEncryptionConfig() error = %v", err)
	}
	if got.Version != cfg.Version || got.Hint != cfg.Hint || string(got.WrappedMasterKey) != string(cfg.WrappedMasterKey) {
		t.Fatalf("rebuilt config = %#v, want canonical replay config", got)
	}
}

func TestRebuildEncryptionConfigFromReplaySelectsLatestPasswordConfig(t *testing.T) {
	db := newTestDB(t)
	first := validEncryptionConfig()
	first.Hint = "first"
	latest := validEncryptionConfig()
	latest.Hint = "latest"
	latest.WrappedMasterKey[0] = 0x44
	for index, cfg := range []EncryptionConfig{first, latest} {
		op := encryptionConfigOp(cfg)
		if _, err := ProjectFromOp(db, testChan, int64(101+index), op, 7, Format(op)); err != nil {
			t.Fatalf("project config %d: %v", index, err)
		}
	}
	if _, err := db.Exec(`DELETE FROM encryption WHERE channel_id = ?`, testChan); err != nil {
		t.Fatalf("delete derived encryption config: %v", err)
	}

	found, err := RebuildEncryptionConfigFromReplay(db, testChan)
	if err != nil || !found {
		t.Fatalf("RebuildEncryptionConfigFromReplay() = (%t, %v)", found, err)
	}
	got, err := GetEncryptionConfig(db, testChan)
	if err != nil {
		t.Fatalf("GetEncryptionConfig() error = %v", err)
	}
	if got.Hint != latest.Hint || got.WrappedMasterKey[0] != latest.WrappedMasterKey[0] {
		t.Fatalf("rebuilt config = %#v, want latest config", got)
	}
}

func TestRebuildEncryptionConfigFromReplayLeavesPlaintextPolicyAbsent(t *testing.T) {
	db := newTestDB(t)

	found, err := RebuildEncryptionConfigFromReplay(db, testChan)
	if err != nil {
		t.Fatalf("RebuildEncryptionConfigFromReplay() error = %v", err)
	}
	if found {
		t.Fatal("RebuildEncryptionConfigFromReplay() found = true, want false")
	}
	if _, err := GetEncryptionConfig(db, testChan); !errors.Is(err, ErrEncryptionConfigNotFound) {
		t.Fatalf("GetEncryptionConfig() error = %v, want not found", err)
	}
}

func TestRebuildEncryptionConfigFromReplayPreservesNoncanonicalDerivedConfig(t *testing.T) {
	db := newTestDB(t)
	want := validEncryptionConfig()
	if err := PutEncryptionConfig(db, want); err != nil {
		t.Fatalf("put derived config: %v", err)
	}

	found, err := RebuildEncryptionConfigFromReplay(db, testChan)
	if err != nil || found {
		t.Fatalf("RebuildEncryptionConfigFromReplay() = (%t, %v), want false, nil", found, err)
	}
	got, err := GetEncryptionConfig(db, testChan)
	if err != nil {
		t.Fatalf("GetEncryptionConfig() error = %v", err)
	}
	if got.Hint != want.Hint || string(got.WrappedMasterKey) != string(want.WrappedMasterKey) {
		t.Fatalf("derived config = %#v, want preserved config", got)
	}
}

func TestRebuildEncryptionConfigFromReplayRejectsTamper(t *testing.T) {
	db := newTestDB(t)
	cfg := validEncryptionConfig()
	op := encryptionConfigOp(cfg)
	header := Format(op)

	if _, err := ProjectFromOp(db, testChan, 101, op, 7, header); err != nil {
		t.Fatalf("project encryption config: %v", err)
	}
	if _, err := ProjectFromOp(db, testChan, 101, op, 7, header+"-edited"); err != nil {
		t.Fatalf("record replay tamper: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM encryption WHERE channel_id = ?`, testChan); err != nil {
		t.Fatalf("delete derived encryption config: %v", err)
	}

	found, err := RebuildEncryptionConfigFromReplay(db, testChan)
	if found || !errors.Is(err, ErrEncryptionConfigReplayInvalid) {
		t.Fatalf("RebuildEncryptionConfigFromReplay() = (%t, %v), want false and replay-invalid", found, err)
	}
	if _, err := GetEncryptionConfig(db, testChan); !errors.Is(err, ErrEncryptionConfigNotFound) {
		t.Fatalf("tampered replay changed config: %v", err)
	}
}

func TestRebuildEncryptionConfigFromReplayRejectsInvalidCanonicalHeader(t *testing.T) {
	db := newTestDB(t)
	cfg := validEncryptionConfig()
	op := encryptionConfigOp(cfg)
	header := Format(op)
	if _, err := ProjectFromOp(db, testChan, 101, op, 7, header); err != nil {
		t.Fatalf("project encryption config: %v", err)
	}
	if _, err := db.Exec(`UPDATE replay_log SET raw_header = ? WHERE channel_id = ? AND msg_id = ?`, "TDX1|t=encfg|broken", testChan, 101); err != nil {
		t.Fatalf("corrupt canonical replay: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM encryption WHERE channel_id = ?`, testChan); err != nil {
		t.Fatalf("delete derived encryption config: %v", err)
	}

	found, err := RebuildEncryptionConfigFromReplay(db, testChan)
	if found || !errors.Is(err, ErrEncryptionConfigReplayInvalid) {
		t.Fatalf("RebuildEncryptionConfigFromReplay() = (%t, %v), want false and replay-invalid", found, err)
	}
}

func TestRebuildEncryptionConfigFromReplayRejectsHashValidMalformedHeader(t *testing.T) {
	db := newTestDB(t)
	cfg := validEncryptionConfig()
	op := encryptionConfigOp(cfg)
	if _, err := ProjectFromOp(db, testChan, 101, op, 7, Format(op)); err != nil {
		t.Fatalf("project encryption config: %v", err)
	}
	broken := "TDX1|t=encfg|broken"
	if _, err := db.Exec(`UPDATE replay_log SET raw_header = ?, first_seen_hash = ? WHERE channel_id = ? AND msg_id = ?`, broken, HashHeader(broken), testChan, 101); err != nil {
		t.Fatalf("corrupt canonical replay: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM encryption WHERE channel_id = ?`, testChan); err != nil {
		t.Fatalf("delete derived encryption config: %v", err)
	}

	found, err := RebuildEncryptionConfigFromReplay(db, testChan)
	if found || !errors.Is(err, ErrEncryptionConfigReplayInvalid) {
		t.Fatalf("RebuildEncryptionConfigFromReplay() = (%t, %v), want false and replay-invalid", found, err)
	}
}

func TestRebuildEncryptionConfigFromReplayRejectsInvalidVaultOp(t *testing.T) {
	db := newTestDB(t)
	cfg := validEncryptionConfig()
	op := encryptionConfigOp(cfg)
	op.ConfigVersion = 2
	header := Format(op)
	if _, err := ProjectFromOp(db, testChan, 101, op, 7, header); err != nil {
		t.Fatalf("record rejected encryption config: %v", err)
	}

	found, err := RebuildEncryptionConfigFromReplay(db, testChan)
	if found || !errors.Is(err, ErrEncryptionConfigReplayInvalid) {
		t.Fatalf("RebuildEncryptionConfigFromReplay() = (%t, %v), want false and replay-invalid", found, err)
	}
}

func TestRebuildEncryptionConfigFromReplayRejectsInvalidBoundariesAndStorage(t *testing.T) {
	if found, err := RebuildEncryptionConfigFromReplay(nil, testChan); found || !errors.Is(err, ErrEncryptionConfigReplayInvalid) {
		t.Fatalf("nil database = (%t, %v)", found, err)
	}
	db := newTestDB(t)
	if found, err := RebuildEncryptionConfigFromReplay(db, 0); found || !errors.Is(err, ErrEncryptionConfigReplayInvalid) {
		t.Fatalf("zero channel = (%t, %v)", found, err)
	}
	if _, err := db.Exec(`DROP TABLE replay_log`); err != nil {
		t.Fatalf("drop replay_log: %v", err)
	}
	if found, err := RebuildEncryptionConfigFromReplay(db, testChan); found || !errors.Is(err, ErrEncryptionConfigReplayInvalid) {
		t.Fatalf("missing replay storage = (%t, %v)", found, err)
	}
}

func encryptionConfigOp(cfg EncryptionConfig) Op {
	return Op{
		Type:             OpEncConfig,
		KDFSalt:          append([]byte(nil), cfg.KDFSalt...),
		KDFParamsJSON:    cfg.KDFParamsJSON,
		WrappedMasterKey: append([]byte(nil), cfg.WrappedMasterKey...),
		KeyCheck:         append([]byte(nil), cfg.KeyCheck...),
		Hint:             cfg.Hint,
		ConfigVersion:    cfg.Version,
	}
}
