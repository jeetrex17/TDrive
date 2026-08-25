package mountadapter

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	tdcrypto "TDrive/backend/crypto"
	"TDrive/backend/mountdav"
	"TDrive/backend/mountwrite"
	"TDrive/backend/projection"
)

func TestNewBuildsAndOwnsDurableCoordinator(t *testing.T) {
	db := newProjectionDB(t)
	cache := &fakeSnapshotCache{}
	remote := &constructorRemote{}
	root := filepath.Join(t.TempDir(), "stage")
	resolver := newFakeResolver()

	session, err := New(context.Background(), Config{
		DriveID: testDriveID,
		Policy:  DrivePolicy{Kind: projection.KindPersonal, Online: true},
		DB:      db, StagingRoot: root, Resolver: resolver, Remote: remote, Cache: cache,
		MaxObjectBytes: 1024, MaxAggregateBytes: 4096,
		MaxConcurrentStaging: 1, MaxActiveOperations: 1, MaxQueuedOperations: 2,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = session.Close(context.Background()) })
	if info, err := os.Stat(root); err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("staging root info = %+v, err=%v", info, err)
	}
	var journalTable string
	if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='mount_write_journal'`).Scan(&journalTable); err != nil {
		t.Fatalf("journal schema: %v", err)
	}

	result, err := session.Mkdir(context.Background(), mountdav.MkdirRequest{OperationID: "mkdir-owned", Path: "/Owned"})
	if err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if !result.Created || result.ETag == "" || remote.commit.Mutation.Kind != mountwrite.MutationMkdir {
		t.Fatalf("mkdir result/commit = %+v / %+v", result, remote.commit)
	}
	if len(cache.directories) != 1 || cache.directories[0] != "" {
		t.Fatalf("invalidated directories = %v", cache.directories)
	}
	if len(cache.subtrees) != 1 || cache.subtrees[0] == "" {
		t.Fatalf("invalidated subtrees = %v", cache.subtrees)
	}
}

func TestNewRejectsUnsupportedDrivePoliciesBeforeSideEffects(t *testing.T) {
	policies := []DrivePolicy{
		{Kind: projection.KindShared, Online: true},
		{Kind: projection.KindPersonal, Online: true, Encrypted: true}, // locked
		{Kind: projection.KindPersonal, Online: false},
	}
	for _, policy := range policies {
		t.Run(policy.Kind, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "must-not-exist")
			_, err := New(context.Background(), Config{
				DriveID: testDriveID, Policy: policy, Engine: &fakeEngine{}, Resolver: newFakeResolver(),
				StagingRoot: root,
			})
			if !errors.Is(err, mountwrite.ErrForbidden) {
				t.Fatalf("New error = %v, want forbidden", err)
			}
			if _, statErr := os.Stat(root); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("unsupported policy created staging root: %v", statErr)
			}
		})
	}
}

func TestNewAcceptsUnlockedEncryptedPersonalPolicy(t *testing.T) {
	key := bytes.Repeat([]byte{4}, 32)
	engine := &fakeEngine{}
	session, err := New(context.Background(), Config{
		DriveID: testDriveID,
		Policy: DrivePolicy{
			Kind: projection.KindPersonal, Online: true,
			Encrypted: true, EncryptionUnlocked: true,
		},
		MasterKeys: staticMountKeyProvider(append([]byte(nil), key...)),
		Engine:     engine, Resolver: newFakeResolver(), MaxObjectBytes: 100,
	})
	if err != nil {
		t.Fatalf("New encrypted: %v", err)
	}
	t.Cleanup(func() { _ = session.Close(context.Background()) })
	if !session.encryptWrites || session.masterKeys == nil {
		t.Fatalf("encrypted session policy/provider = %t/%T", session.encryptWrites, session.masterKeys)
	}
	owned, err := session.masterKeys.Key()
	if err != nil || !bytes.Equal(owned, key) {
		t.Fatalf("encrypted session key = %x, %v", owned, err)
	}
	key[0] ^= 0xff
	again, err := session.masterKeys.Key()
	if err != nil || bytes.Equal(again, key) {
		t.Fatal("session provider retained caller-owned master key memory")
	}
}

func TestEncryptedSessionStagesCiphertextAndCommitsPlaintextMetadata(t *testing.T) {
	db := newProjectionDB(t)
	cache := &fakeSnapshotCache{}
	remote := &constructorRemote{}
	root := filepath.Join(t.TempDir(), "encrypted-stage")
	key := bytes.Repeat([]byte{3}, 32)
	session, err := New(context.Background(), Config{
		DriveID: testDriveID,
		Policy: DrivePolicy{
			Kind: projection.KindPersonal, Online: true,
			Encrypted: true, EncryptionUnlocked: true,
		},
		MasterKeys: staticMountKeyProvider(key),
		DB:         db, StagingRoot: root, Resolver: newFakeResolver(), Remote: remote, Cache: cache,
		MaxObjectBytes: 1024, MaxAggregateBytes: 4096,
		MaxConcurrentStaging: 1, MaxActiveOperations: 1, MaxQueuedOperations: 1,
	})
	if err != nil {
		t.Fatalf("New encrypted session: %v", err)
	}
	t.Cleanup(func() { _ = session.Close(context.Background()) })

	plaintext := []byte("mounted secret")
	if _, err := session.Put(context.Background(), mountdav.PutRequest{
		OperationID: "encrypted-put", Path: "/secret.txt", ContentLength: int64(len(plaintext)),
	}, bytes.NewReader(plaintext)); err != nil {
		t.Fatalf("encrypted Put: %v", err)
	}
	if remote.upload.EncryptionVersion != mountwrite.EncryptionTDE1 || remote.upload.PlaintextSize != int64(len(plaintext)) {
		t.Fatalf("hidden upload metadata = %+v", remote.upload)
	}
	if bytes.Equal(remote.stored, plaintext) || int64(len(remote.stored)) != tdcrypto.CiphertextSize(int64(len(plaintext))) {
		t.Fatalf("stored body is not exact TDE1 ciphertext: %d bytes", len(remote.stored))
	}
	var decrypted bytes.Buffer
	if _, err := tdcrypto.DecryptStream(bytes.NewReader(remote.stored), &decrypted, key); err != nil {
		t.Fatalf("decrypt staged body: %v", err)
	}
	if !bytes.Equal(decrypted.Bytes(), plaintext) {
		t.Fatalf("decrypted body = %q", decrypted.Bytes())
	}
	if remote.commit.Body == nil || !remote.commit.Body.Encrypted || remote.commit.Body.SHA256 != ([32]byte{}) {
		t.Fatalf("committed encrypted metadata = %+v", remote.commit.Body)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("staging cleanup entries=%d error=%v", len(entries), err)
	}
}

func TestNewSupportsInjectedEngineAndRunsRecovery(t *testing.T) {
	engine := &fakeEngine{}
	session, err := New(context.Background(), Config{
		DriveID: testDriveID,
		Policy:  DrivePolicy{Kind: projection.KindPersonal, Online: true},
		Engine:  engine, Resolver: newFakeResolver(), MaxObjectBytes: 100,
	})
	if err != nil {
		t.Fatalf("New injected: %v", err)
	}
	if session.RecoveryReport() != (mountwrite.RecoveryReport{}) || engine.recoverCalls != 1 {
		t.Fatalf("recovery report/calls = %+v / %d", session.RecoveryReport(), engine.recoverCalls)
	}
}

func TestNewStartsWithDeferredHiddenCleanupRecovery(t *testing.T) {
	ctx := context.Background()
	db := newProjectionDB(t)
	if err := mountwrite.EnsureJournalSchema(ctx, db); err != nil {
		t.Fatalf("EnsureJournalSchema: %v", err)
	}
	journal, err := mountwrite.NewSQLiteJournal(db)
	if err != nil {
		t.Fatalf("NewSQLiteJournal: %v", err)
	}
	at := time.Unix(1_700_000_000, 0).UTC()
	if err := journal.Create(ctx, mountwrite.JournalRecord{
		OperationID: "deferred-cleanup",
		Mutation: mountwrite.Mutation{
			Kind: mountwrite.MutationPut, DriveID: testDriveID,
			DestinationName: "interrupted.txt", ContentLength: 1,
		},
		State: mountwrite.StateCleanupPending,
		Body: &mountwrite.RemoteBody{
			UploadUUID: "cleanup-upload", PartCount: 1,
			StoredSize: 1, PlaintextSize: 1, MessageIDs: []int64{71},
		},
		CreatedAt: at, UpdatedAt: at,
	}); err != nil {
		t.Fatalf("seed cleanup journal: %v", err)
	}
	remote := &constructorRemote{discardErr: mountwrite.ErrUnavailable}
	session, err := New(ctx, Config{
		DriveID: testDriveID,
		Policy:  DrivePolicy{Kind: projection.KindPersonal, Online: true},
		DB:      db, StagingRoot: filepath.Join(t.TempDir(), "stage"),
		Resolver: newFakeResolver(), Remote: remote, Cache: &fakeSnapshotCache{},
	})
	if err != nil {
		t.Fatalf("New with deferred cleanup: %v", err)
	}
	t.Cleanup(func() { _ = session.Close(context.Background()) })
	if report := session.RecoveryReport(); report.Examined != 1 || report.Pending != 1 || report.Failed != 0 {
		t.Fatalf("recovery report = %+v", report)
	}
	if remote.discardCalls != 1 {
		t.Fatalf("discard calls = %d, want 1", remote.discardCalls)
	}
}

func TestSnapshotInvalidatorTargetsExactParentsAndSubtrees(t *testing.T) {
	cache := &fakeSnapshotCache{}
	invalidator, err := NewSnapshotInvalidator(testDriveID, cache)
	if err != nil {
		t.Fatal(err)
	}
	err = invalidator.Invalidate(context.Background(), mountwrite.SnapshotInvalidation{
		DriveID:   testDriveID,
		ParentIDs: []string{"d:a", "", "d:a"},
		ObjectIDs: []string{"f:1", "d:tree", "d:tree"},
	})
	if err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	if !equalStrings(cache.directories, []string{"d:a", "", "d:a"}) || !equalStrings(cache.subtrees, []string{"f:1", "d:tree", "d:tree"}) {
		t.Fatalf("cache calls = dirs %v subtrees %v", cache.directories, cache.subtrees)
	}
	if err := invalidator.Invalidate(context.Background(), mountwrite.SnapshotInvalidation{DriveID: 999}); !errors.Is(err, mountwrite.ErrForbidden) {
		t.Fatalf("wrong-drive invalidation = %v", err)
	}
}

type fakeSnapshotCache struct {
	directories []string
	subtrees    []string
}

func (c *fakeSnapshotCache) InvalidateDirectories(parentIDs ...string) {
	c.directories = append(c.directories, parentIDs...)
}

func (c *fakeSnapshotCache) InvalidateSubtree(rootID string) {
	c.subtrees = append(c.subtrees, rootID)
}

type constructorRemote struct {
	commit       mountwrite.CommitRequest
	upload       mountwrite.HiddenUpload
	stored       []byte
	discardErr   error
	discardCalls int
}

func (r *constructorRemote) UploadHidden(_ context.Context, upload mountwrite.HiddenUpload, source io.ReadSeeker) (mountwrite.RemoteBody, error) {
	stored, err := io.ReadAll(source)
	if err != nil {
		return mountwrite.RemoteBody{}, err
	}
	r.upload = upload
	r.stored = append([]byte(nil), stored...)
	return mountwrite.RemoteBody{
		UploadUUID: "constructor-upload", PartCount: 1,
		PlaintextSize: upload.PlaintextSize, StoredSize: upload.StoredSize,
		Encrypted: upload.Encrypted, EncryptionVersion: upload.EncryptionVersion,
		SHA256: upload.SHA256, StoredSHA256: upload.StoredSHA256,
	}, nil
}

func (*constructorRemote) RecoverHidden(context.Context, mountwrite.HiddenUpload, io.ReadSeeker) (mountwrite.RemoteBody, error) {
	return mountwrite.RemoteBody{}, errors.New("unexpected hidden recovery")
}

func (r *constructorRemote) Commit(_ context.Context, request mountwrite.CommitRequest) (mountwrite.MutationResult, error) {
	r.commit = request
	return mountwrite.MutationResult{
		OperationID: request.OperationID,
		ObjectID:    deterministicFolderID(request.OperationID),
		Revision:    1,
		Created:     true,
	}, nil
}

func (*constructorRemote) Reconcile(context.Context, string) (mountwrite.MutationResult, bool, error) {
	return mountwrite.MutationResult{}, false, nil
}

func (r *constructorRemote) DiscardHidden(context.Context, string, *mountwrite.RemoteBody) error {
	r.discardCalls++
	return r.discardErr
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

var (
	_ mountwrite.Remote = (*constructorRemote)(nil)
	_ SnapshotCache     = (*fakeSnapshotCache)(nil)
)
