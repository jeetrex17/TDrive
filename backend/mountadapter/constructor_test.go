package mountadapter

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

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
		{Kind: projection.KindPersonal, Online: true, Encrypted: true},
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
	commit mountwrite.CommitRequest
}

func (*constructorRemote) UploadHidden(context.Context, mountwrite.HiddenUpload, io.ReadSeeker) (mountwrite.RemoteBody, error) {
	return mountwrite.RemoteBody{}, nil
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

func (*constructorRemote) DiscardHidden(context.Context, string, *mountwrite.RemoteBody) error {
	return nil
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
