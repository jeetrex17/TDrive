package processlock

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestLegacyAndroidOwnerIdentity(t *testing.T) {
	for _, tc := range []struct {
		name      string
		ownerName string
		selfName  string
		want      bool
	}{
		{"same app", "com.tdrive.app\x00", "com.tdrive.app\x00", true},
		{"app subprocess", "com.tdrive.app:worker\x00", "com.tdrive.app\x00", true},
		{"reused by other app", "com.other.app\x00", "com.tdrive.app\x00", false},
		{"unknown owner", "", "com.tdrive.app\x00", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeProcCmdline(t, root, 77, tc.ownerName)
			writeProcCmdline(t, root, 88, tc.selfName)
			if got := legacyAndroidOwnerMatches(root, "unused", 77, 88); got != tc.want {
				t.Fatalf("owner match = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLegacyAndroidSamePIDRequiresOpenMetadata(t *testing.T) {
	for _, tc := range []struct {
		name  string
		open  bool
		fdDir bool
		want  bool
	}{
		{"live legacy owner", true, true, true},
		{"reused by new app process", false, true, false},
		{"unreadable descriptor inventory", false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeProcCmdline(t, root, 88, "com.tdrive.app\x00")
			metadata := filepath.Join(root, "backend.lock")
			if err := os.WriteFile(metadata, []byte("legacy metadata"), 0o600); err != nil {
				t.Fatal(err)
			}
			fdDir := filepath.Join(root, "88", "fd")
			if tc.fdDir {
				if err := os.Mkdir(fdDir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if tc.open {
				if err := os.Link(metadata, filepath.Join(fdDir, "5")); err != nil {
					t.Fatal(err)
				}
			}
			if got := legacyAndroidOwnerMatches(root, metadata, 88, 88); got != tc.want {
				t.Fatalf("owner match = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLegacyAndroidUnknownProcessRemainsProtected(t *testing.T) {
	if !legacyAndroidOwnerMatches(t.TempDir(), "unused", 77, 88) {
		t.Fatal("unreadable process identity must not permit concurrent backend startup")
	}
}

func TestLegacyAndroidOtherUIDCannotOwnPrivateBackend(t *testing.T) {
	root := t.TempDir()
	writeProcCmdline(t, root, 77, "com.tdrive.app\x00")
	writeProcCmdline(t, root, 88, "com.tdrive.app\x00")
	for pid, status := range map[string]string{
		"77": "Name:\tapp\nUid:\t10001\t10001\t10001\t10001\n",
		"88": "Name:\tapp\nUid:\t10002\t10002\t10002\t10002\n",
	} {
		if err := os.WriteFile(filepath.Join(root, pid, "status"), []byte(status), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if legacyAndroidOwnerMatches(root, "unused", 77, 88) {
		t.Fatal("a different Android UID cannot own the app's private backend lock")
	}
}

func writeProcCmdline(t *testing.T, root string, pid int, name string) {
	t.Helper()
	dir := filepath.Join(root, strconv.Itoa(pid))
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(name), 0o600); err != nil {
		t.Fatal(err)
	}
}
