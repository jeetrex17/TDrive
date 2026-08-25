package main

import (
	"fmt"
	"strings"

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
		selector, windowsDrive, err := parseMountStartArgs(args)
		if err != nil {
			return err
		}
		client, err := newDaemonClient()
		if err != nil {
			return err
		}
		out, err := client.MountStart(selector, windowsDrive)
		if err != nil {
			return err
		}
		printMountResponse(out)
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
		printMountResponse(out)
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
		printMountResponse(out)
		return nil
	default:
		return fmt.Errorf("unknown mount command %q\n\nRun: tdrive mount [start|status|stop]", action)
	}
}

func parseMountStartArgs(args []string) (selector string, windowsDrive string, err error) {
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--drive":
			index++
			if index >= len(args) || strings.TrimSpace(args[index]) == "" {
				return "", "", fmt.Errorf("usage: tdrive mount start [--drive <name|id>] [--windows-drive T:]")
			}
			selector = strings.TrimSpace(args[index])
		case "--windows-drive":
			index++
			if index >= len(args) || strings.TrimSpace(args[index]) == "" {
				return "", "", fmt.Errorf("usage: tdrive mount start [--drive <name|id>] [--windows-drive T:]")
			}
			windowsDrive, err = normalizeWindowsDriveArg(args[index])
			if err != nil {
				return "", "", err
			}
		default:
			return "", "", fmt.Errorf("unknown mount option %q", args[index])
		}
	}
	return selector, windowsDrive, nil
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

func printMountResponse(out daemon.MountResponse) {
	if !out.Running {
		fmt.Println("mount: stopped")
		if out.Error != "" {
			fmt.Printf("error: %s\n", out.Error)
		}
		return
	}
	fmt.Printf("mount: running (%s)\n", out.Mode)
	if out.Drive.ID != 0 {
		fmt.Printf("drive: %s (%d), pinned until stopped\n", out.Drive.Title, out.Drive.ID)
	}
	fmt.Printf("url:   %s\n", out.URL)
	if out.WindowsDrive != "" {
		fmt.Printf("letter: %s (Windows mapping hint)\n", out.WindowsDrive)
	}
	if out.Commands.ActiveOSMount != "" {
		fmt.Printf("run:   %s\n", out.Commands.ActiveOSMount)
	}
	if out.Commands.WindowsMap != "" {
		fmt.Printf("win:   %s\n", out.Commands.WindowsMap)
	}
	if out.Commands.MacFinder != "" {
		fmt.Printf("mac:   %s\n", out.Commands.MacFinder)
	}
	if out.Commands.LinuxMount != "" {
		fmt.Printf("linux: %s\n", out.Commands.LinuxMount)
	}
}
