package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"

	"TDrive/backend/daemon"
)

func runMount(args []string) error {
	action := "start"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		action = args[0]
		args = args[1:]
	}

	switch action {
	case "start":
		selector, windowsDrive, mode, err := parseMountStartArgs(args)
		if err != nil {
			return err
		}
		client, err := newDaemonClient()
		if err != nil {
			return err
		}
		out, err := client.MountStart(selector, windowsDrive, mode)
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

func parseMountStartArgs(args []string) (selector string, windowsDrive string, mode string, err error) {
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--drive":
			index++
			if index >= len(args) || strings.TrimSpace(args[index]) == "" {
				return "", "", "", fmt.Errorf("usage: tdrive mount start [--drive <name|id>] [--windows-drive T:] [--read-only]")
			}
			selector = strings.TrimSpace(args[index])
		case "--windows-drive":
			index++
			if index >= len(args) || strings.TrimSpace(args[index]) == "" {
				return "", "", "", fmt.Errorf("usage: tdrive mount start [--drive <name|id>] [--windows-drive T:] [--read-only]")
			}
			windowsDrive, err = normalizeWindowsDriveArg(args[index])
			if err != nil {
				return "", "", "", err
			}
		case "--read-only":
			mode = "read-only"
		default:
			return "", "", "", fmt.Errorf("unknown mount option %q", args[index])
		}
	}
	return selector, windowsDrive, mode, nil
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
	label := strings.TrimSpace(out.Label)
	if label == "" {
		label = "TDrive"
	}
	mode := strings.TrimSpace(out.Mode)
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
		fmt.Fprintf(writer, "location: %s\n", out.Location)
	}
	if out.Drive.ID != 0 {
		fmt.Fprintf(writer, "drive: %s (%d), pinned until disconnected\n", out.Drive.Title, out.Drive.ID)
	}
	if out.Mode == "read-write" && !out.AcceptingWrites {
		fmt.Fprintln(writer, "writes: paused")
	}
	printMountError(writer, out.Error)
}

func printMountError(writer io.Writer, raw string) {
	if message := safeMountMessage(raw); message != "" {
		fmt.Fprintf(writer, "error: %s\n", message)
	}
}

func safeMountMessage(message string) string {
	message = strings.Map(func(character rune) rune {
		if unicode.IsControl(character) {
			return ' '
		}
		return character
	}, message)
	message = strings.Join(strings.Fields(message), " ")
	if containsSensitiveMountDetail(message) {
		return "Mount operation failed; retry or check the app logs"
	}
	runes := []rune(message)
	if len(runes) > 240 {
		message = string(runes[:240])
	}
	return message
}

func containsSensitiveMountDetail(value string) bool {
	lower := strings.ToLower(value)
	return strings.Contains(lower, "tdrive-") ||
		strings.Contains(lower, "http://") ||
		strings.Contains(lower, "https://") ||
		strings.Contains(lower, "dav://") ||
		strings.Contains(lower, "localhost") ||
		strings.Contains(lower, "127.0.0.1")
}
