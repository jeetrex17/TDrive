//go:build darwin && !ios

package mountlocal

import (
	"context"
	_ "embed"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

//go:embed setup_darwin.sh
var installScript string

type setupDependencies struct {
	readHosts func() ([]byte, error)
	install   func(context.Context) error
	resolve   func(context.Context) ([]net.IPAddr, error)
	direct    func(string) bool
}

// Prepare installs the persistent loopback alias once, then verifies the hosts
// file, resolver and proxy route before an OS process receives a capability URL.
// The entry intentionally outlives the app and any surviving webdavfs process.
func Prepare(parent context.Context, endpoint string) error {
	if parent == nil {
		return errors.New("TDrive mount: context required")
	}
	if !nativeAvailable {
		return errors.New("TDrive mount: native macOS network checks require a build with CGO enabled")
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Hostname() != Hostname || u.Port() == "" || u.RawQuery != "" || u.Fragment != "" {
		return ErrResolution
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	return prepare(ctx, u.Port(), setupDependencies{
		readHosts: readHosts,
		install:   installHosts,
		resolve:   nativeResolve,
		direct:    directProxyRoute,
	})
}

func prepare(ctx context.Context, port string, deps setupDependencies) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := deps.readHosts()
	if err != nil {
		return err
	}
	found, err := hostsMapping(data)
	if err != nil {
		return err
	}
	if !deps.direct(port) {
		return ErrProxy
	}
	if !found {
		if err := deps.install(ctx); err != nil {
			return ErrSetup
		}
		data, err = deps.readHosts()
		if err != nil {
			return err
		}
		found, err = hostsMapping(data)
		if err != nil {
			return err
		}
		if !found {
			return ErrSetup
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	addresses, err := deps.resolve(ctx)
	if err != nil || len(addresses) == 0 {
		return ErrResolution
	}
	for _, address := range addresses {
		if address.Zone != "" || address.IP.String() != "127.0.0.1" {
			return ErrResolution
		}
	}
	if !deps.direct(port) {
		return ErrProxy
	}
	return ctx.Err()
}

func readHosts() ([]byte, error) {
	fd, err := unix.Open("/private/etc/hosts", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrHostsUnsafe
	}
	file := os.NewFile(uintptr(fd), "/private/etc/hosts")
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, ErrHostsUnsafe
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 || info.Size() > 1<<20 {
		return nil, ErrHostsUnsafe
	}
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil, ErrHostsUnsafe
	}
	return data, nil
}

func installHosts(ctx context.Context) error {
	// No endpoint, token, drive label, password or external path enters this script.
	script := "do shell script " + strconv.Quote(installScript) +
		" with administrator privileges with prompt " + strconv.Quote("TDrive needs to add TDrive.localhost → 127.0.0.1 to /etc/hosts so its local drive has a friendly name in Finder. This is a one-time setup.")
	command := exec.CommandContext(ctx, "/usr/bin/osascript", "-e", script)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	return command.Run()
}
