package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"TDrive/backend/daemon"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}

	switch args[0] {
	case "daemon":
		return runDaemon(args[1:])
	case "status":
		return printDaemonStatus()
	case "drives":
		return printDrives()
	case "drive":
		return runDrive(args[1:])
	case "pwd":
		return printPWD()
	case "cd":
		return runCD(args[1:])
	case "ls":
		return runLS(args[1:])
	case "find":
		return runFind(args[1:])
	case "mkdir":
		return runMkdir(args[1:])
	case "rm":
		return runRM(args[1:])
	case "mv":
		return runMV(args[1:])
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command %q\n\nRun: tdrive help", args[0])
	}
}

func runDrive(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing drive command\n\nRun: tdrive drive list|use <name|id>")
	}
	switch args[0] {
	case "list", "ls":
		return printDrives()
	case "use":
		if len(args) != 2 {
			return fmt.Errorf("usage: tdrive drive use <name|id>")
		}
		c, err := daemon.NewClient()
		if err != nil {
			return err
		}
		out, err := c.UseDrive(args[1])
		if err != nil {
			return err
		}
		fmt.Printf("drive: %s (%d)\n", out.Drive.Title, out.Drive.ID)
		fmt.Printf("cwd:   %s\n", out.CurrentPath)
		return nil
	default:
		return fmt.Errorf("unknown drive command %q", args[0])
	}
}

func runDaemon(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing daemon command\n\nRun: tdrive daemon start|status|stop")
	}

	switch args[0] {
	case "start":
		return runDaemonForeground()
	case "status":
		return printDaemonStatus()
	case "stop":
		c, err := daemon.NewClient()
		if err != nil {
			return err
		}
		if err := c.Shutdown(); err != nil {
			return err
		}
		fmt.Println("TDrive daemon stopping")
		return nil
	case "restart":
		if err := runDaemon([]string{"stop"}); err != nil && !strings.Contains(err.Error(), "daemon is not running") {
			return err
		}
		return runDaemonForeground()
	default:
		return fmt.Errorf("unknown daemon command %q", args[0])
	}
}

func runDaemonForeground() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return daemon.Run(ctx, daemon.ServerConfig{
		Warnf: func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, format, args...)
		},
	})
}

func printDaemonStatus() error {
	c, err := daemon.NewClient()
	if err != nil {
		return err
	}
	status, err := c.Status()
	if err != nil {
		return err
	}

	fmt.Printf("daemon: running (pid %d)\n", status.PID)
	if status.ActiveChannelID != 0 {
		fmt.Printf("drive:  %d\n", status.ActiveChannelID)
	} else {
		fmt.Println("drive:  none")
	}
	if status.CurrentPath != "" {
		fmt.Printf("cwd:    %s\n", status.CurrentPath)
	}
	switch {
	case !status.VaultAvailable:
		fmt.Println("vault:  unavailable")
	case !status.VaultConfigured:
		fmt.Println("vault:  not configured")
	case status.VaultUnlocked:
		fmt.Println("vault:  unlocked")
	default:
		fmt.Println("vault:  locked")
	}
	return nil
}

func printDrives() error {
	c, err := daemon.NewClient()
	if err != nil {
		return err
	}
	out, err := c.ListDrives()
	if err != nil {
		return err
	}
	if len(out.Drives) == 0 {
		fmt.Println("No drives found")
		return nil
	}
	for _, drive := range out.Drives {
		marker := " "
		if drive.Active {
			marker = "*"
		}
		fmt.Printf("%s %-8s %-14d %s\n", marker, drive.Kind, drive.ID, drive.Title)
	}
	return nil
}

func printPWD() error {
	c, err := daemon.NewClient()
	if err != nil {
		return err
	}
	out, err := c.PWD()
	if err != nil {
		return err
	}
	fmt.Println(out.CurrentPath)
	return nil
}

func runCD(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: tdrive cd <remote-path>")
	}
	c, err := daemon.NewClient()
	if err != nil {
		return err
	}
	out, err := c.CD(args[0])
	if err != nil {
		return err
	}
	fmt.Println(out.CurrentPath)
	return nil
}

func runLS(args []string) error {
	long := false
	var target string
	for _, arg := range args {
		switch arg {
		case "-l", "--long":
			long = true
		default:
			if target != "" {
				return fmt.Errorf("usage: tdrive ls [-l] [remote-path]")
			}
			target = arg
		}
	}

	c, err := daemon.NewClient()
	if err != nil {
		return err
	}
	out, err := c.List(target)
	if err != nil {
		return err
	}
	if long {
		for _, entry := range out.Entries {
			fmt.Printf("%-6s %10s  %-16s %s\n", entryKind(entry), entrySize(entry), entryTime(entry), entryDisplayName(entry))
		}
		return nil
	}
	for _, entry := range out.Entries {
		fmt.Println(entryDisplayName(entry))
	}
	return nil
}

func runFind(args []string) error {
	limit := 50
	var queryParts []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-n", "--limit":
			if i+1 >= len(args) {
				return fmt.Errorf("usage: tdrive find [-n limit] <query>")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n <= 0 {
				return fmt.Errorf("invalid limit %q", args[i+1])
			}
			limit = n
			i++
		default:
			queryParts = append(queryParts, args[i])
		}
	}
	query := strings.TrimSpace(strings.Join(queryParts, " "))
	if query == "" {
		return fmt.Errorf("usage: tdrive find [-n limit] <query>")
	}

	c, err := daemon.NewClient()
	if err != nil {
		return err
	}
	out, err := c.Find(query, limit)
	if err != nil {
		return err
	}
	for _, entry := range out.Results {
		fmt.Printf("%-6s %10s  %s\n", entryKind(entry), entrySize(entry), entry.Path)
	}
	return nil
}

func runMkdir(args []string) error {
	parents := false
	var target string
	for _, arg := range args {
		switch arg {
		case "-p", "--parents":
			parents = true
		default:
			if target != "" {
				return fmt.Errorf("usage: tdrive mkdir [-p] <remote-path>")
			}
			target = arg
		}
	}
	if target == "" {
		return fmt.Errorf("usage: tdrive mkdir [-p] <remote-path>")
	}

	c, err := daemon.NewClient()
	if err != nil {
		return err
	}
	out, err := c.Mkdir(target, parents)
	if err != nil {
		return err
	}
	fmt.Println(out.Entry.Path)
	return nil
}

func runRM(args []string) error {
	recursive := false
	var target string
	for _, arg := range args {
		switch arg {
		case "-r", "-R", "--recursive":
			recursive = true
		default:
			if target != "" {
				return fmt.Errorf("usage: tdrive rm [-r] <remote-path>")
			}
			target = arg
		}
	}
	if target == "" {
		return fmt.Errorf("usage: tdrive rm [-r] <remote-path>")
	}

	c, err := daemon.NewClient()
	if err != nil {
		return err
	}
	out, err := c.Remove(target, recursive)
	if err != nil {
		return err
	}
	fmt.Println(out.Entry.Path)
	return nil
}

func runMV(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: tdrive mv <source> <destination>")
	}
	c, err := daemon.NewClient()
	if err != nil {
		return err
	}
	out, err := c.Move(args[0], args[1])
	if err != nil {
		return err
	}
	fmt.Println(out.Entry.Path)
	return nil
}

func entryDisplayName(entry daemon.Entry) string {
	if entry.Type == "folder" {
		return entry.Name + "/"
	}
	return entry.Name
}

func entryKind(entry daemon.Entry) string {
	if entry.Type == "folder" {
		return "dir"
	}
	return "file"
}

func entrySize(entry daemon.Entry) string {
	if entry.Type == "folder" {
		return "-"
	}
	return humanSize(entry.Size)
}

func entryTime(entry daemon.Entry) string {
	if entry.UploadTime == 0 {
		return "-"
	}
	return time.Unix(entry.UploadTime, 0).Format("2006-01-02 15:04")
}

func humanSize(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	value := float64(n)
	for _, unit := range units {
		value /= 1024
		if value < 1024 {
			return fmt.Sprintf("%.1f %s", value, unit)
		}
	}
	return fmt.Sprintf("%.1f PiB", value/1024)
}

func printUsage() {
	fmt.Print(`TDrive CLI

Usage:
  tdrive daemon start       Run the daemon in the foreground
  tdrive daemon status      Show daemon status
  tdrive daemon stop        Ask the daemon to stop
  tdrive status             Alias for daemon status
  tdrive drives             List known drives
  tdrive drive use <name|id> Switch the active drive
  tdrive pwd                Print the remote working directory
  tdrive cd <path>          Change the remote working directory
  tdrive ls [-l] [path]     List remote files
  tdrive find [-n N] <query> Search remote files and folders
  tdrive mkdir [-p] <path>  Create a remote folder
  tdrive rm [-r] <path>     Remove a remote file or folder
  tdrive mv <src> <dst>     Move or rename a remote file or folder

The CLI talks to the local daemon. It does not auto-start it.
`)
}
