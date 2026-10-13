package processlock

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func legacyAndroidOwnerMatches(procRoot, path string, pid, selfPID int) bool {
	owner := filepath.Join(procRoot, strconv.Itoa(pid))
	self := filepath.Join(procRoot, strconv.Itoa(selfPID))
	ownerUID, ownerOK := androidProcessUID(owner)
	selfUID, selfOK := androidProcessUID(self)
	if ownerOK && selfOK && ownerUID != selfUID {
		return false
	}
	ownerName, ownerErr := os.ReadFile(filepath.Join(owner, "cmdline"))
	selfName, selfErr := os.ReadFile(filepath.Join(self, "cmdline"))
	if ownerErr == nil && selfErr == nil {
		ownerApp, selfApp := androidProcessPackage(ownerName), androidProcessPackage(selfName)
		if ownerApp != "" && selfApp != "" && ownerApp != selfApp {
			return false
		}
	}
	if pid != selfPID {
		return true
	}
	metadata, err := os.Stat(path)
	if err != nil {
		return true
	}
	fds, err := os.ReadDir(filepath.Join(self, "fd"))
	if err != nil {
		return true
	}
	for _, fd := range fds {
		opened, err := os.Stat(filepath.Join(self, "fd", fd.Name()))
		if errors.Is(err, os.ErrNotExist) {
			continue // The descriptor used to enumerate the directory has closed.
		}
		if err != nil || os.SameFile(metadata, opened) {
			return true
		}
	}
	return false
}

func androidProcessUID(processDir string) (uint64, bool) {
	status, err := os.ReadFile(filepath.Join(processDir, "status"))
	if err != nil {
		return 0, false
	}
	for line := range strings.SplitSeq(string(status), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "Uid:" {
			uid, err := strconv.ParseUint(fields[1], 10, 32)
			return uid, err == nil
		}
	}
	return 0, false
}

func androidProcessPackage(cmdline []byte) string {
	name, _, _ := strings.Cut(string(cmdline), "\x00")
	name, _, _ = strings.Cut(name, ":")
	return name
}
