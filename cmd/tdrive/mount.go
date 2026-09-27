package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"TDrive/backend/daemon"
	"TDrive/backend/mountcontroller"
	"TDrive/backend/mountsafe"
)

func runMount(args []string) error {
	action := "start"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		action = args[0]
		args = args[1:]
	}

	switch action {
	case "start":
		options, err := parseMountStartArgs(args)
		if err != nil {
			return err
		}
		client, err := newDaemonClient()
		if err != nil {
			return err
		}
		readPassword := func() (string, error) {
			if options.passwordStdin {
				return readStdinLine()
			}
			return promptSecret("Encryption password: ")
		}
		out, err := mountStartWithUnlock(client, options, cliNonInteractive(), readPassword)
		if err != nil {
			return err
		}
		printMountResponse(os.Stdout, out)
		return nil
	case "status":
		if len(args) != 0 {
			return fmt.Errorf("usage: tdrive mount status")
		}
		client, err := newDaemonClient()
		if err != nil {
			return err
		}
		out, err := client.MountStatus()
		if err != nil {
			return err
		}
		printMountResponse(os.Stdout, out)
		return nil
	case "stop":
		if len(args) != 0 {
			return fmt.Errorf("usage: tdrive mount stop")
		}
		client, err := newDaemonClient()
		if err != nil {
			return err
		}
		out, err := client.MountStop()
		if err != nil {
			return err
		}
		printMountResponse(os.Stdout, out)
		return nil
	default:
		return fmt.Errorf("unknown mount command %q\n\nRun: tdrive mount [start|status|stop]", action)
	}
}

type mountStartOptions struct {
	selector      string
	windowsDrive  string
	mode          string
	passwordStdin bool
}

func parseMountStartArgs(args []string) (mountStartOptions, error) {
	const usage = "usage: tdrive mount start [--drive <name|id>] [--windows-drive T:] [--read-only] [--password-stdin]"
	var options mountStartOptions
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--drive":
			index++
			if index >= len(args) || strings.TrimSpace(args[index]) == "" || strings.HasPrefix(args[index], "-") {
				return mountStartOptions{}, errors.New(usage)
			}
			options.selector = strings.TrimSpace(args[index])
		case "--windows-drive":
			index++
			if index >= len(args) || strings.TrimSpace(args[index]) == "" {
				return mountStartOptions{}, errors.New(usage)
			}
			windowsDrive, err := normalizeWindowsDriveArg(args[index])
			if err != nil {
				return mountStartOptions{}, err
			}
			options.windowsDrive = windowsDrive
		case "--read-only":
			options.mode = "read-only"
		case "--password-stdin":
			if options.passwordStdin {
				return mountStartOptions{}, fmt.Errorf("--password-stdin may only be specified once")
			}
			options.passwordStdin = true
		default:
			return mountStartOptions{}, fmt.Errorf("unknown mount option %q", args[index])
		}
	}
	return options, nil
}

type mountStartClient interface {
	MountStart(selector string, windowsDrive string, mode string) (daemon.MountResponse, error)
	VaultUnlock(password string) (daemon.VaultResponse, error)
}

func mountStartWithUnlock(client mountStartClient, options mountStartOptions, nonInteractive bool, readPassword func() (string, error)) (daemon.MountResponse, error) {
	out, err := client.MountStart(options.selector, options.windowsDrive, options.mode)
	if err == nil || !mountNeedsPassword(err) {
		return out, err
	}
	if nonInteractive && !options.passwordStdin {
		return daemon.MountResponse{}, interactionRequired("encrypted drive is locked; retry with --password-stdin")
	}
	password, err := readPassword()
	if err != nil {
		return daemon.MountResponse{}, fmt.Errorf("read encryption password: %w", err)
	}
	if password == "" {
		return daemon.MountResponse{}, fmt.Errorf("encryption password is empty")
	}
	if _, err := client.VaultUnlock(password); err != nil {
		return daemon.MountResponse{}, err
	}
	return client.MountStart(options.selector, options.windowsDrive, options.mode)
}

func mountNeedsPassword(err error) bool {
	if errors.Is(err, mountcontroller.ErrEncryptionPasswordRequired) {
		return true
	}
	var remote *daemon.RemoteError
	return errors.As(err, &remote) && remote.Code == "encryption_password_required"
}

func normalizeWindowsDriveArg(value string) (string, error) {
	value = strings.ToUpper(strings.TrimSpace(value))
	if len(value) == 1 && value[0] >= 'A' && value[0] <= 'Z' {
		return value + ":", nil
	}
	if len(value) == 2 && value[0] >= 'A' && value[0] <= 'Z' && value[1] == ':' {
		return value, nil
	}
	return "", fmt.Errorf("invalid Windows drive %q: use a letter such as T:", value)
}

func printMountResponse(writer io.Writer, out daemon.MountResponse) {
	if writer == nil {
		return
	}
	label := strings.TrimSpace(terminalSafeText(out.Label))
	if label == "" {
		label = "TDrive"
	}
	mode := strings.TrimSpace(terminalSafeText(out.Mode))
	if mode == "" {
		mode = "read-only"
	}

	switch out.Phase {
	case "preparing", "attaching":
		fmt.Fprintf(writer, "mount: mounting %s (%s)\n", label, mode)
		printMountError(writer, out.Error)
		return
	case "draining":
		if out.ActiveWrites == 1 {
			fmt.Fprintf(writer, "mount: finishing 1 active write before ejecting %s\n", label)
		} else if out.ActiveWrites > 1 {
			fmt.Fprintf(writer, "mount: finishing %d active writes before ejecting %s\n", out.ActiveWrites, label)
		} else {
			fmt.Fprintf(writer, "mount: finishing pending changes before ejecting %s\n", label)
		}
		printMountError(writer, out.Error)
		return
	case "detaching":
		fmt.Fprintf(writer, "mount: disconnecting %s\n", label)
		printMountError(writer, out.Error)
		return
	}
	if !out.Mounted {
		fmt.Fprintln(writer, "mount: stopped")
		printMountError(writer, out.Error)
		return
	}
	fmt.Fprintf(writer, "mounted: %s (%s)\n", label, mode)
	if out.Location != "" && !containsSensitiveMountDetail(out.Location) {
		fmt.Fprintf(writer, "location: %s\n", terminalSafeText(out.Location))
	}
	if out.Drive.ID != 0 {
		fmt.Fprintf(writer, "drive: %s (%d), pinned until disconnected\n", terminalSafeText(out.Drive.Title), out.Drive.ID)
	}
	if out.Mode == "read-write" && !out.AcceptingWrites {
		fmt.Fprintln(writer, "writes: paused")
	}
	printMountError(writer, out.Error)
}

func printMountError(writer io.Writer, raw string) {
	if message := safeMountMessage(raw); message != "" {
		fmt.Fprintf(writer, "error: %s\n", terminalSafeText(message))
	}
}

func safeMountMessage(message string) string {
	return mountsafe.Message(message)
}

func containsSensitiveMountDetail(value string) bool {
	return mountsafe.ContainsSensitive(value)
}
