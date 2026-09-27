package main

import (
	"fmt"
	"runtime/debug"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"TDrive/backend/daemon"
)

type cliOptions struct {
	JSON           bool
	NonInteractive bool
	Yes            bool
	DriveID        int64
	Timeout        time.Duration
}

type CLIError struct {
	Code      string
	Message   string
	Hint      string
	Retryable bool
}

func (e *CLIError) Error() string { return e.Message }

func interactionRequired(message string) error {
	return &CLIError{
		Code:    "interaction_required",
		Message: message,
		Hint:    "Run the command interactively or supply its documented stdin and selection options.",
	}
}

var currentCLIOptions atomic.Pointer[cliOptions]

func cliCurrentOptions() cliOptions {
	if opts := currentCLIOptions.Load(); opts != nil {
		return *opts
	}
	return cliOptions{}
}

func cliNonInteractive() bool { return cliCurrentOptions().NonInteractive }

func requireNonInteractiveConfirmation(opts cliOptions, action string) error {
	if opts.NonInteractive && !opts.Yes {
		return &CLIError{
			Code:    "confirmation_required",
			Message: action + " requires --yes in non-interactive mode",
		}
	}
	return nil
}

func parseCLIOptions(args []string) (cliOptions, []string, error) {
	var opts cliOptions
	command := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			command = append(command, "--")
			command = append(command, args[i+1:]...)
			break
		}
		name, inline, hasInline := strings.Cut(arg, "=")
		value := func() (string, error) {
			if hasInline {
				return inline, nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s requires a value", name)
			}
			i++
			return args[i], nil
		}
		switch name {
		case "--json":
			if hasInline {
				return cliOptions{}, nil, fmt.Errorf("--json does not take a value")
			}
			opts.JSON = true
		case "--output":
			format, err := value()
			if err != nil {
				return cliOptions{}, nil, err
			}
			switch format {
			case "json":
				opts.JSON = true
			case "text":
				opts.JSON = false
			default:
				return cliOptions{}, nil, fmt.Errorf("unsupported output format %q", format)
			}
		case "--non-interactive":
			if hasInline {
				return cliOptions{}, nil, fmt.Errorf("--non-interactive does not take a value")
			}
			opts.NonInteractive = true
		case "--yes":
			if hasInline {
				return cliOptions{}, nil, fmt.Errorf("--yes does not take a value")
			}
			opts.Yes = true
		case "--drive-id":
			raw, err := value()
			if err != nil {
				return cliOptions{}, nil, err
			}
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || id <= 0 {
				return cliOptions{}, nil, fmt.Errorf("invalid drive ID %q", raw)
			}
			opts.DriveID = id
		case "--timeout":
			raw, err := value()
			if err != nil {
				return cliOptions{}, nil, err
			}
			duration, err := time.ParseDuration(raw)
			if err != nil || duration <= 0 {
				return cliOptions{}, nil, fmt.Errorf("invalid timeout %q", raw)
			}
			opts.Timeout = duration
		default:
			command = append(command, arg)
		}
	}
	return opts, command, nil
}

// Values can be injected by release packaging; VCS metadata covers local builds.
var (
	buildVersion = "dev"
	buildCommit  = "unknown"
)

type cliVersionInfo struct {
	Version  string `json:"version"`
	Commit   string `json:"commit"`
	Protocol int    `json:"protocol"`
}

func cliBuildInfo() cliVersionInfo {
	info := cliVersionInfo{Version: buildVersion, Commit: buildCommit, Protocol: daemon.ProtocolVersion}
	if build, ok := debug.ReadBuildInfo(); ok {
		if info.Version == "dev" && build.Main.Version != "" && build.Main.Version != "(devel)" {
			info.Version = build.Main.Version
		}
		if info.Commit == "unknown" {
			for _, setting := range build.Settings {
				if setting.Key == "vcs.revision" {
					info.Commit = setting.Value
					break
				}
			}
		}
	}
	return info
}
