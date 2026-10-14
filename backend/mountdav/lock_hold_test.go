package mountdav

import (
	"errors"
	"testing"
	"time"

	"golang.org/x/net/webdav"
)

func TestHeldLockRefreshPreservesExclusiveClaimAndRequestedDeadline(t *testing.T) {
	locks := newBoundedLockSystem(2)
	now := time.Unix(1_700_000_000, 0)
	token, err := locks.Create(now, webdav.LockDetails{Root: "/Docs", Duration: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	release, err := locks.Confirm(now, "/Docs/file", "", lockCondition(token))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	refreshTime := now.Add(2 * time.Second)
	details, err := locks.refreshPath(refreshTime, "/Docs", token, 3*time.Second)
	if err != nil || details.Root != "/Docs" || details.Duration != 3*time.Second {
		t.Fatalf("held Refresh = (%+v, %v)", details, err)
	}
	if _, err := locks.Confirm(refreshTime, "/Docs/file", "", lockCondition(token)); !errors.Is(err, webdav.ErrConfirmationFailed) {
		t.Fatalf("competing claim = %v, want confirmation failed", err)
	}
	if _, err := locks.Create(refreshTime, webdav.LockDetails{Root: "/Docs/file", Duration: time.Minute}); !errors.Is(err, webdav.ErrLocked) {
		t.Fatalf("competing Create = %v, want locked", err)
	}
	if err := locks.Unlock(refreshTime, token); !errors.Is(err, webdav.ErrLocked) {
		t.Fatalf("held Unlock = %v, want locked", err)
	}
	release()
	release()
	nextRelease, err := locks.Confirm(now.Add(4*time.Second), "/Docs/file", "", lockCondition(token))
	if err != nil {
		t.Fatalf("refreshed lock expired before requested deadline: %v", err)
	}
	nextRelease()
	if _, err := locks.Refresh(now.Add(5*time.Second), token, time.Minute); !errors.Is(err, webdav.ErrNoSuchLock) {
		t.Fatalf("expired Refresh = %v, want no such lock", err)
	}
	if _, err := locks.Create(now.Add(5*time.Second), webdav.LockDetails{Root: "/Docs/file", Duration: time.Minute}); err != nil {
		t.Fatalf("expired lock still blocks destination: %v", err)
	}
}

func TestHeldLockKeepsCapacityUntilReleaseWithoutRenewingDeadline(t *testing.T) {
	locks := newBoundedLockSystem(1)
	now := time.Unix(1_700_000_000, 0)
	token, err := locks.Create(now, webdav.LockDetails{Root: "/file", Duration: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	release, err := locks.Confirm(now, "/file", "", lockCondition(token))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	later := now.Add(2 * time.Second)
	if !locks.tokenApplies(later, token, "/file") {
		t.Fatal("active mutation lost its public token when timeout elapsed")
	}
	if _, err := locks.Create(later, webdav.LockDetails{Root: "/other", Duration: time.Minute}); !errors.Is(err, errLockCapacity) {
		t.Fatalf("held lock lost capacity reservation: %v", err)
	}
	release()
	if _, err := locks.Refresh(later, token, time.Minute); !errors.Is(err, webdav.ErrNoSuchLock) {
		t.Fatalf("release resurrected expired lock: %v", err)
	}
	if _, err := locks.Create(later, webdav.LockDetails{Root: "/file", Duration: time.Minute}); err != nil {
		t.Fatalf("released expired lock still blocks: %v", err)
	}
}

func TestTemporaryMutationLockRemainsExclusiveBeyondTimeout(t *testing.T) {
	locks := newBoundedLockSystem(2)
	application := &readApplication{lockSystem: locks}
	hold := &mutationLockHold{locks: locks}
	t.Cleanup(hold.release)
	if status := application.holdTemporaryLock("/file", false, hold); status != 0 {
		t.Fatalf("hold status = %d", status)
	}
	later := time.Now().Add(temporaryLockDuration + time.Second)
	if _, err := locks.Create(later, webdav.LockDetails{Root: "/file", Duration: time.Minute}); !errors.Is(err, webdav.ErrLocked) {
		t.Fatalf("temporary lock expired during active mutation: %v", err)
	}
	hold.release()
	if _, err := locks.Create(later, webdav.LockDetails{Root: "/file", Duration: time.Minute}); err != nil {
		t.Fatalf("temporary lock leaked after mutation: %v", err)
	}
}

func TestHeldLockRefreshDoesNotExtendDeadlineUntilRelease(t *testing.T) {
	locks := newBoundedLockSystem(2)
	now := time.Unix(1_700_000_000, 0)
	token, err := locks.Create(now, webdav.LockDetails{Root: "/file", Duration: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	release, err := locks.Confirm(now, "/file", "", lockCondition(token))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	if _, err := locks.Refresh(now.Add(time.Second), token, time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := locks.Create(now.Add(3*time.Second), webdav.LockDetails{Root: "/file", Duration: time.Minute}); !errors.Is(err, webdav.ErrLocked) {
		t.Fatalf("held lock lost exclusion at refreshed deadline: %v", err)
	}
	release()
	if _, err := locks.Refresh(now.Add(3*time.Second), token, time.Minute); !errors.Is(err, webdav.ErrNoSuchLock) {
		t.Fatalf("release renewed expired refresh: %v", err)
	}
	if _, err := locks.Create(now.Add(3*time.Second), webdav.LockDetails{Root: "/file", Duration: time.Minute}); err != nil {
		t.Fatalf("expired refreshed lock leaked: %v", err)
	}
}

func TestConfirmTracksOnlyClaimedTokensForBothResources(t *testing.T) {
	for _, sameParent := range []bool{false, true} {
		name := "separate locks"
		if sameParent {
			name = "shared ancestor lock"
		}
		t.Run(name, func(t *testing.T) {
			locks := newBoundedLockSystem(4)
			now := time.Unix(1_700_000_000, 0)
			firstRoot, secondRoot := "/one", "/two"
			if sameParent {
				firstRoot, secondRoot = "/", "/"
			}
			firstToken, err := locks.Create(now, webdav.LockDetails{Root: firstRoot, Duration: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			secondToken := firstToken
			if !sameParent {
				secondToken, err = locks.Create(now, webdav.LockDetails{Root: secondRoot, Duration: time.Second})
				if err != nil {
					t.Fatal(err)
				}
			}
			conditions := []webdav.Condition{lockCondition(firstToken), lockCondition(secondToken)}
			extraToken := ""
			if !sameParent {
				extraToken, err = locks.Create(now, webdav.LockDetails{Root: "/unrelated", Duration: time.Minute})
				if err != nil {
					t.Fatal(err)
				}
				conditions = append([]webdav.Condition{lockCondition(extraToken)}, conditions...)
			}
			release, err := locks.Confirm(now, "/one/file", "/two/file", conditions...)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(release)
			if extraToken != "" {
				if err := locks.Unlock(now, extraToken); err != nil {
					t.Fatalf("unrelated condition token was incorrectly claimed: %v", err)
				}
			}
			for _, token := range []string{firstToken, secondToken} {
				if _, err := locks.Refresh(now.Add(2*time.Second), token, 3*time.Second); err != nil {
					t.Fatalf("refresh claimed token: %v", err)
				}
			}
			release()
			nextRelease, err := locks.Confirm(now.Add(4*time.Second), "/one/file", "/two/file", lockCondition(firstToken), lockCondition(secondToken))
			if err != nil {
				t.Fatalf("reclaim both refreshed resources: %v", err)
			}
			nextRelease()
			if _, err := locks.Create(now.Add(5*time.Second), webdav.LockDetails{Root: "/", Duration: time.Minute}); err != nil {
				t.Fatalf("released resources did not expire: %v", err)
			}
		})
	}
}
