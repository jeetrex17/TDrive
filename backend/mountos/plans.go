package mountos

type commandPlan struct {
	Path string
	Args []string
}

func newCommandPlan(path string, args ...string) commandPlan {
	return commandPlan{Path: path, Args: append([]string(nil), args...)}
}

func darwinAttachPlan(endpoint, label, target string, mode Mode) commandPlan {
	options := "rdonly,noexec,nosuid,nodev"
	if mode == ModeReadWrite {
		options = "noexec,nosuid,nodev"
	}
	return newCommandPlan(
		"/sbin/mount_webdav",
		"-S",
		"-v", label,
		"-o", options,
		endpoint,
		target,
	)
}

func darwinDetachPlan(target string) commandPlan {
	return newCommandPlan("/usr/sbin/diskutil", "unmount", target)
}

func darwinOpenPlan(target string) commandPlan {
	return newCommandPlan("/usr/bin/open", target)
}

func windowsAttachPlan(netExecutable, drive, endpoint string) commandPlan {
	return newCommandPlan(netExecutable, "use", drive, endpoint, "/persistent:no")
}

func windowsDetachPlan(netExecutable, drive string) commandPlan {
	return newCommandPlan(netExecutable, "use", drive, "/delete", "/yes")
}

func windowsOpenPlan(explorerExecutable, target string) commandPlan {
	return newCommandPlan(explorerExecutable, target)
}

func linuxAttachPlan(uri string) commandPlan {
	return newCommandPlan("/usr/bin/gio", "mount", "--anonymous", uri)
}

func linuxDetachPlan(uri string) commandPlan {
	return newCommandPlan("/usr/bin/gio", "mount", "-u", uri)
}

func linuxOpenPlan(uri string) commandPlan {
	return newCommandPlan("/usr/bin/gio", "open", uri)
}

func linuxListPlan() commandPlan {
	return newCommandPlan("/usr/bin/gio", "mount", "-l")
}
