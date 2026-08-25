package sync

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

func TestEnsureAuthoritativeReconcilesNonEmptyHistoryAndPersistsMarker(t *testing.T) {
	db, tg, engine := newSyncEnv(t)
	cfg := authoritativeEncryptionConfig()
	configID := sendOp(t, tg, encryptionOp(cfg))
	folderOp := projection.Op{Type: projection.OpMkdir, Obj: "d:known", Parent: projection.RootParent, Name: "Known"}
	folderID := sendOp(t, tg, folderOp)
	if _, err := projection.ProjectFromOp(db, testChan, folderID, folderOp, 7, projection.Format(folderOp)); err != nil {
		t.Fatalf("seed non-empty replay: %v", err)
	}
	if _, err := db.Exec(`UPDATE channels SET last_synced_msg = ?, initial_sync_done = 0 WHERE channel_id = ?`, folderID, testChan); err != nil {
		t.Fatalf("seed stale watermark: %v", err)
	}

	if err := engine.EnsureAuthoritative(context.Background(), testChan); err != nil {
		t.Fatalf("EnsureAuthoritative() error = %v", err)
	}
	if _, err := projection.GetEncryptionConfig(db, testChan); err != nil {
		t.Fatalf("encryption config msg %d was not reconciled: %v", configID, err)
	}
	channel, err := projection.GetChannel(db, testChan)
	if err != nil {
		t.Fatalf("GetChannel() error = %v", err)
	}
	if !channel.InitialSyncDone || channel.LastSyncedMsg != folderID {
		t.Fatalf("authoritative channel state = %#v, want marker through %d", channel, folderID)
	}
}

func TestEnsureAuthoritativeDoesNotPersistMarkerAfterPartialFailure(t *testing.T) {
	db, tg, engine := newSyncEnv(t)
	sendOp(t, tg, encryptionOp(authoritativeEncryptionConfig()))
	tg.InjectReadFloodWaits(maxFloodWaitRetries + 2)

	err := engine.EnsureAuthoritative(context.Background(), testChan)
	if err == nil {
		t.Fatal("EnsureAuthoritative() error = nil, want history failure")
	}
	channel, getErr := projection.GetChannel(db, testChan)
	if getErr != nil {
		t.Fatalf("GetChannel() error = %v", getErr)
	}
	if channel.InitialSyncDone {
		t.Fatal("failed authoritative sync persisted initial_sync_done")
	}
	if _, getErr := projection.GetEncryptionConfig(db, testChan); !errors.Is(getErr, projection.ErrEncryptionConfigNotFound) {
		t.Fatalf("failed authoritative sync projected config: %v", getErr)
	}
}

func TestEnsureAuthoritativeUsesIncrementalAfterMarker(t *testing.T) {
	db, tg, engine := newSyncEnv(t)
	if err := engine.EnsureAuthoritative(context.Background(), testChan); err != nil {
		t.Fatalf("establish empty authority: %v", err)
	}
	sendOp(t, tg, encryptionOp(authoritativeEncryptionConfig()))

	if err := engine.EnsureAuthoritative(context.Background(), testChan); err != nil {
		t.Fatalf("refresh authoritative policy: %v", err)
	}
	if _, err := projection.GetEncryptionConfig(db, testChan); err != nil {
		t.Fatalf("incremental policy refresh did not project config: %v", err)
	}
}

func TestEnsureAuthoritativeRejectsRepeatedFullHistoryPageWithoutProgress(t *testing.T) {
	db, _, _ := newSyncEnv(t)
	page := make([]tgclient.HistoryMessage, defaultPageSize)
	for index := range page {
		page[index] = tgclient.HistoryMessage{MsgID: int64(200 - index)}
	}
	telegram := &repeatingHistoryPager{Fake: tgclient.NewFake(7), page: page}
	engine := NewEngine(db, telegram, fakePeers{})

	err := engine.EnsureAuthoritative(context.Background(), testChan)
	if !errors.Is(err, errHistoryPaginationNoProgress) || err.Error() != "sync: history pagination made no progress" {
		t.Fatalf("EnsureAuthoritative() error = %v, want pagination no-progress", err)
	}
	if telegram.calls != 2 {
		t.Fatalf("GetHistory() calls = %d, want 2", telegram.calls)
	}
	channel, getErr := projection.GetChannel(db, testChan)
	if getErr != nil {
		t.Fatalf("GetChannel() error = %v", getErr)
	}
	if channel.InitialSyncDone || channel.LastSyncedMsg != 0 {
		t.Fatalf("partial scan authorized plaintext state: %#v", channel)
	}
}

type repeatingHistoryPager struct {
	*tgclient.Fake
	page  []tgclient.HistoryMessage
	calls int
}

func (pager *repeatingHistoryPager) GetHistory(
	context.Context,
	tgclient.InputPeer,
	int64,
	int64,
	int,
) ([]tgclient.HistoryMessage, error) {
	pager.calls++
	return append([]tgclient.HistoryMessage(nil), pager.page...), nil
}

func authoritativeEncryptionConfig() projection.EncryptionConfig {
	return projection.EncryptionConfig{
		ChannelID:        testChan,
		Enabled:          true,
		KDFSalt:          bytes.Repeat([]byte{0x11}, 16),
		KDFParamsJSON:    `{"memory":65536,"time":3,"parallelism":4,"key_len":32,"salt_len":16}`,
		WrappedMasterKey: bytes.Repeat([]byte{0x22}, 72),
		KeyCheck:         bytes.Repeat([]byte{0x33}, 59),
		Version:          1,
	}
}

func encryptionOp(cfg projection.EncryptionConfig) projection.Op {
	return projection.Op{
		Type:             projection.OpEncConfig,
		KDFSalt:          append([]byte(nil), cfg.KDFSalt...),
		KDFParamsJSON:    cfg.KDFParamsJSON,
		WrappedMasterKey: append([]byte(nil), cfg.WrappedMasterKey...),
		KeyCheck:         append([]byte(nil), cfg.KeyCheck...),
		ConfigVersion:    cfg.Version,
	}
}
