package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"TDrive/backend/daemon"
)

const (
	machineSchemaVersion  = 1
	maxMachineFindResults = 1000
)

func machineInvalid(format string, args ...any) error {
	return &CLIError{Code: "invalid_argument", Message: fmt.Sprintf(format, args...)}
}

func machineConfirmation(message string) error {
	return &CLIError{Code: "confirmation_required", Message: message}
}

func machineUnsupported(format string, args ...any) error {
	return &CLIError{Code: "unsupported", Message: fmt.Sprintf(format, args...)}
}

func machineConflict(message string) error {
	return &CLIError{Code: "conflict", Message: message}
}

type machineEnvelope struct {
	SchemaVersion int    `json:"schema_version"`
	OK            bool   `json:"ok"`
	Command       string `json:"command"`
	Data          any    `json:"data"`
}

func writeMachineSuccess(writer io.Writer, command string, data any) error {
	return json.NewEncoder(writer).Encode(machineEnvelope{
		SchemaVersion: machineSchemaVersion,
		OK:            true,
		Command:       command,
		Data:          data,
	})
}

func jsonSuccess(command string, data any) error {
	return writeMachineSuccess(os.Stdout, command, data)
}

type machineArgSet struct {
	flags      map[string]bool
	values     map[string]string
	positional []string
}

// A trailing '=' in an allowed option declares a value-bearing flag.
func parseMachineArgs(args []string, allowed ...string) (machineArgSet, error) {
	result := machineArgSet{flags: make(map[string]bool), values: make(map[string]string)}
	options := make(map[string]bool, len(allowed))
	for _, option := range allowed {
		name, hasValue := strings.CutSuffix(option, "=")
		options[name] = hasValue
	}
	positionalOnly := false
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if !positionalOnly && arg == "--" {
			positionalOnly = true
			continue
		}
		if positionalOnly || !strings.HasPrefix(arg, "-") || arg == "-" {
			result.positional = append(result.positional, arg)
			continue
		}
		name, inlineValue, inline := strings.Cut(arg, "=")
		valueOption, ok := options[name]
		if !ok {
			return machineArgSet{}, machineInvalid("unknown option %q", name)
		}
		if !valueOption {
			if inline {
				return machineArgSet{}, machineInvalid("option %q does not take a value", name)
			}
			result.flags[name] = true
			continue
		}
		if !inline {
			index++
			if index >= len(args) {
				return machineArgSet{}, machineInvalid("option %q requires a value", name)
			}
			inlineValue = args[index]
		}
		if inlineValue == "" {
			return machineArgSet{}, machineInvalid("option %q requires a value", name)
		}
		result.values[name] = inlineValue
	}
	return result, nil
}

func requireAbsoluteRemote(remotePath string) error {
	if remotePath == "" || !strings.HasPrefix(remotePath, "/") || !path.IsAbs(remotePath) {
		return machineInvalid("remote path must be absolute (start with /): %q", remotePath)
	}
	if !utf8.ValidString(remotePath) || strings.ContainsRune(remotePath, 0) || path.Clean(remotePath) != remotePath {
		return machineInvalid("remote path must be a valid canonical path: %q", remotePath)
	}
	return nil
}

func requireDriveID(opts cliOptions) error {
	if opts.DriveID <= 0 {
		return machineInvalid("--drive-id is required for this JSON command")
	}
	return nil
}

func requirePositional(args machineArgSet, minCount, maxCount int, usage string) error {
	if len(args.positional) < minCount || len(args.positional) > maxCount {
		return machineInvalid("usage: %s", usage)
	}
	return nil
}

type machineCommandSpec struct {
	Name       string   `json:"name"`
	Usage      string   `json:"usage"`
	Flags      []string `json:"flags"`
	Mutating   bool     `json:"mutating"`
	NeedsDrive bool     `json:"needs_drive"`
}

var machineCommands = []machineCommandSpec{
	{"version", "version", nil, false, false},
	{"commands", "commands", nil, false, false},
	{"status", "status", nil, false, false},
	{"whoami", "whoami", nil, false, false},
	{"drives", "drives", nil, false, false},
	{"drive list", "drive list", nil, false, false},
	{"drive pending", "drive pending", nil, false, false},
	{"pwd", "pwd", nil, false, false},
	{"ls", "ls <absolute-remote-path>", nil, false, true},
	{"find", "find [--limit 1..1000] <query>", []string{"--limit"}, false, true},
	{"mkdir", "mkdir [--parents] <absolute-remote-path>", []string{"--parents"}, true, true},
	{"rm", "rm [--recursive] <absolute-remote-path>", []string{"--recursive", "--yes"}, true, true},
	{"mv", "mv <absolute-source> <absolute-destination>", nil, true, true},
	{"vault status", "vault status", nil, false, false},
	{"vault lock", "vault lock", nil, true, false},
	{"vault unlock", "vault unlock --password-stdin", []string{"--password-stdin"}, true, false},
	{"put", "put [--encrypt] <local-file> <absolute-remote-path>", []string{"--encrypt"}, true, true},
	{"get", "get <absolute-remote-file> <local-file>", []string{"--yes"}, true, true},
	{"sync", "sync [drive-id]", nil, true, true},
	{"rebuild", "rebuild [drive-id]", []string{"--yes"}, true, true},
	{"mount status", "mount status", nil, false, false},
}

func machineManifest() any {
	commands := slices.Clone(machineCommands)
	for index := range commands {
		if commands[index].Flags == nil {
			commands[index].Flags = []string{}
		}
	}
	return struct {
		GlobalFlags []string             `json:"global_flags"`
		Commands    []machineCommandSpec `json:"commands"`
		Notes       []string             `json:"notes"`
	}{
		GlobalFlags: []string{"--json", "--output json", "--non-interactive", "--drive-id <positive-id>", "--yes", "--timeout <duration>"},
		Commands:    commands,
		Notes: []string{
			"Drive-scoped commands require --drive-id and absolute remote paths; they never change the shared active drive or cwd.",
			"Download no-clobber uses a best-effort local precheck; concurrent local writers can race it. Use --yes to allow overwrite.",
			"--timeout bounds a daemon RPC after startup, not daemon startup itself.",
			"JSON mode never prompts or writes transfer progress. Unsupported commands return an error.",
		},
	}
}

func runMachine(args []string, opts cliOptions) error {
	if len(args) == 0 {
		return machineInvalid("missing command; run tdrive commands --json")
	}
	command := args[0]
	if command == "version" || command == "commands" {
		if len(args) != 1 {
			return machineInvalid("usage: tdrive %s --json", command)
		}
		if command == "version" {
			return jsonSuccess(command, cliBuildInfo())
		}
		return jsonSuccess(command, machineManifest())
	}
	if err := validateMachineCommand(args); err != nil {
		return err
	}
	var c *daemon.Client
	var err error
	if command == "status" {
		c, err = daemon.NewClient()
	} else {
		c, err = newDaemonClient()
	}
	if err != nil {
		return err
	}
	if opts.Timeout > 0 {
		c = c.WithTimeout(opts.Timeout)
	}
	data, err := machineResult(c, args, opts)
	if err != nil {
		return err
	}
	data, err = redactMachineResult(data)
	if err != nil {
		return err
	}
	if command == "vault" || command == "mount" || command == "drive" {
		command += " " + args[1]
	}
	return jsonSuccess(command, data)
}

// Machine output never includes invite capabilities unless a command explicitly
// requests one. Keep this whitelist exhaustive as new JSON commands are added.
func redactMachineResult(data any) (any, error) {
	switch result := data.(type) {
	case daemon.Status, daemon.SelfUserResponse, daemon.VaultResponse:
		return result, nil
	case daemon.DriveListResponse:
		result.Drives = slices.Clone(result.Drives)
		for index := range result.Drives {
			result.Drives[index].InviteLink = ""
		}
		return result, nil
	case daemon.PendingJoinsResponse:
		result.Pending = slices.Clone(result.Pending)
		for index := range result.Pending {
			result.Pending[index].InviteLink = ""
			result.Pending[index].InviteHash = ""
		}
		return result, nil
	case daemon.PathResponse:
		result.Drive.InviteLink = ""
		return result, nil
	case daemon.ListResponse:
		result.Drive.InviteLink = ""
		return result, nil
	case daemon.FindResponse:
		result.Drive.InviteLink = ""
		return result, nil
	case daemon.EntryResponse:
		result.Drive.InviteLink = ""
		return result, nil
	case daemon.UploadResponse:
		result.Drive.InviteLink = ""
		return result, nil
	case daemon.DownloadResponse:
		result.Drive.InviteLink = ""
		return result, nil
	case daemon.MaintenanceResponse:
		result.Drive.InviteLink = ""
		return result, nil
	case daemon.MountResponse:
		result.Drive.InviteLink = ""
		if containsSensitiveMountDetail(result.Location) || strings.Contains(result.Location, "://") {
			result.Location = ""
		}
		result.Error = safeMountMessage(result.Error)
		return result, nil
	default:
		return nil, fmt.Errorf("unrecognized machine result type %T", data)
	}
}

func validateMachineCommand(args []string) error {
	if len(args) == 0 {
		return machineInvalid("missing command; run tdrive commands --json")
	}
	switch args[0] {
	case "status", "whoami", "drives", "pwd":
		if len(args) != 1 {
			return machineInvalid("%s does not take positional arguments in JSON mode", args[0])
		}
	case "mount":
		if len(args) != 2 || args[1] != "status" {
			return machineUnsupported("JSON mode supports only: tdrive mount status")
		}
	case "drive":
		if len(args) != 2 || (args[1] != "list" && args[1] != "pending") {
			return machineUnsupported("JSON mode supports only: tdrive drive list|pending")
		}
	case "vault":
		if len(args) < 2 || (args[1] != "status" && args[1] != "lock" && args[1] != "unlock") {
			return machineUnsupported("JSON mode supports only: tdrive vault status|lock|unlock")
		}
	default:
		if !machineSupported(args[0]) {
			return machineUnsupported("command %q is not supported in JSON mode; run tdrive commands --json", args[0])
		}
	}
	return nil
}

func machineSupported(command string) bool {
	for _, spec := range machineCommands {
		if command == strings.Fields(spec.Name)[0] {
			return true
		}
	}
	return false
}

type machineClient interface {
	Status() (daemon.Status, error)
	Whoami() (daemon.SelfUserResponse, error)
	ListDrives() (daemon.DriveListResponse, error)
	ListPendingJoins() (daemon.PendingJoinsResponse, error)
	PWD() (daemon.PathResponse, error)
	ListInDrive(int64, string) (daemon.ListResponse, error)
	FindInDrive(int64, string, int) (daemon.FindResponse, error)
	MkdirInDrive(int64, string, bool) (daemon.EntryResponse, error)
	RemoveInDrive(int64, string, bool) (daemon.EntryResponse, error)
	MoveInDrive(int64, string, string) (daemon.EntryResponse, error)
	VaultStatus() (daemon.VaultResponse, error)
	VaultLock() (daemon.VaultResponse, error)
	VaultUnlock(string) (daemon.VaultResponse, error)
	UploadInDrive(int64, string, string, bool, bool, daemon.EventHandler) (daemon.UploadResponse, error)
	DownloadInDrive(int64, string, string, daemon.EventHandler) (daemon.DownloadResponse, error)
	Sync(string) (daemon.MaintenanceResponse, error)
	Rebuild(string) (daemon.MaintenanceResponse, error)
	MountStatus() (daemon.MountResponse, error)
}

func machineResult(c machineClient, args []string, opts cliOptions) (any, error) {
	switch args[0] {
	case "status":
		if len(args) != 1 {
			return nil, machineInvalid("usage: tdrive status --json")
		}
		return c.Status()
	case "whoami":
		if len(args) != 1 {
			return nil, machineInvalid("usage: tdrive whoami --json")
		}
		return c.Whoami()
	case "drives":
		if len(args) != 1 {
			return nil, machineInvalid("usage: tdrive drives --json")
		}
		return c.ListDrives()
	case "drive":
		if len(args) != 2 {
			return nil, machineInvalid("JSON mode supports only: tdrive drive list|pending")
		}
		switch args[1] {
		case "list":
			return c.ListDrives()
		case "pending":
			return c.ListPendingJoins()
		default:
			return nil, machineInvalid("JSON mode supports only: tdrive drive list|pending")
		}
	case "pwd":
		if len(args) != 1 {
			return nil, machineInvalid("usage: tdrive pwd --json")
		}
		return c.PWD()
	case "ls", "find", "mkdir", "rm", "mv":
		return machineFilesystem(c, args, opts)
	case "vault":
		return machineVault(c, args[1:])
	case "put", "get":
		return machineTransfer(c, args, opts)
	case "sync", "rebuild":
		return machineMaintenance(c, args, opts)
	case "mount":
		if len(args) != 2 || args[1] != "status" {
			return nil, machineInvalid("JSON mode supports only: tdrive mount status")
		}
		out, err := c.MountStatus()
		if err != nil {
			return nil, err
		}
		if containsSensitiveMountDetail(out.Location) {
			out.Location = ""
		}
		out.Error = safeMountMessage(out.Error)
		return out, nil
	default:
		return nil, machineInvalid("unsupported JSON command %q", args[0])
	}
}

func machineFilesystem(c machineClient, args []string, opts cliOptions) (any, error) {
	if err := requireDriveID(opts); err != nil {
		return nil, err
	}
	switch args[0] {
	case "ls":
		return machineList(c, args[1:], opts.DriveID)
	case "find":
		return machineFind(c, args[1:], opts.DriveID)
	case "mkdir":
		return machineMkdir(c, args[1:], opts.DriveID)
	case "rm":
		return machineRemove(c, args[1:], opts)
	case "mv":
		return machineMove(c, args[1:], opts.DriveID)
	}
	return nil, machineInvalid("unsupported filesystem command %q", args[0])
}

func machineList(c machineClient, args []string, driveID int64) (any, error) {
	parsed, err := parseMachineArgs(args)
	if err != nil {
		return nil, err
	}
	if err := requirePositional(parsed, 1, 1, "tdrive ls <absolute-remote-path> --drive-id ID --json"); err != nil {
		return nil, err
	}
	if err := requireAbsoluteRemote(parsed.positional[0]); err != nil {
		return nil, err
	}
	return c.ListInDrive(driveID, parsed.positional[0])
}

func machineFind(c machineClient, args []string, driveID int64) (any, error) {
	parsed, err := parseMachineArgs(args, "--limit=")
	if err != nil {
		return nil, err
	}
	if err := requirePositional(parsed, 1, 1, "tdrive find [--limit N] <query> --drive-id ID --json"); err != nil {
		return nil, err
	}
	limit := 50
	if raw := parsed.values["--limit"]; raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit <= 0 || limit > maxMachineFindResults {
			return nil, machineInvalid("--limit must be between 1 and %d", maxMachineFindResults)
		}
	}
	return c.FindInDrive(driveID, parsed.positional[0], limit)
}

func machineMkdir(c machineClient, args []string, driveID int64) (any, error) {
	parsed, err := parseMachineArgs(args, "--parents")
	if err != nil {
		return nil, err
	}
	if err := requirePositional(parsed, 1, 1, "tdrive mkdir [--parents] <absolute-remote-path> --drive-id ID --json"); err != nil {
		return nil, err
	}
	if err := requireAbsoluteRemote(parsed.positional[0]); err != nil {
		return nil, err
	}
	return c.MkdirInDrive(driveID, parsed.positional[0], parsed.flags["--parents"])
}

func machineRemove(c machineClient, args []string, opts cliOptions) (any, error) {
	if !opts.Yes {
		return nil, machineConfirmation("rm in JSON mode requires --yes")
	}
	parsed, err := parseMachineArgs(args, "--recursive")
	if err != nil {
		return nil, err
	}
	if err := requirePositional(parsed, 1, 1, "tdrive rm [--recursive] <absolute-remote-path> --drive-id ID --yes --json"); err != nil {
		return nil, err
	}
	if err := requireAbsoluteRemote(parsed.positional[0]); err != nil {
		return nil, err
	}
	if parsed.positional[0] == "/" {
		return nil, machineInvalid("refusing to remove drive root")
	}
	return c.RemoveInDrive(opts.DriveID, parsed.positional[0], parsed.flags["--recursive"])
}

func machineMove(c machineClient, args []string, driveID int64) (any, error) {
	parsed, err := parseMachineArgs(args)
	if err != nil {
		return nil, err
	}
	if err := requirePositional(parsed, 2, 2, "tdrive mv <absolute-source> <absolute-destination> --drive-id ID --json"); err != nil {
		return nil, err
	}
	for _, remotePath := range parsed.positional {
		if err := requireAbsoluteRemote(remotePath); err != nil {
			return nil, err
		}
	}
	return c.MoveInDrive(driveID, parsed.positional[0], parsed.positional[1])
}

func machineVault(c machineClient, args []string) (any, error) {
	if len(args) == 0 {
		return nil, machineInvalid("usage: tdrive vault status|lock|unlock --json")
	}
	switch args[0] {
	case "status":
		if len(args) != 1 {
			return nil, machineInvalid("usage: tdrive vault status --json")
		}
		return c.VaultStatus()
	case "lock":
		if len(args) != 1 {
			return nil, machineInvalid("usage: tdrive vault lock --json")
		}
		return c.VaultLock()
	case "unlock":
		if len(args) != 2 || args[1] != "--password-stdin" {
			return nil, machineInvalid("JSON mode requires: tdrive vault unlock --password-stdin")
		}
		password, err := io.ReadAll(io.LimitReader(os.Stdin, 4097))
		if err != nil {
			return nil, err
		}
		if len(password) > 4096 {
			return nil, machineInvalid("password input exceeds 4096 bytes")
		}
		value := strings.TrimRight(string(password), "\r\n")
		if value == "" {
			return nil, machineInvalid("password required on stdin")
		}
		return c.VaultUnlock(value)
	default:
		return nil, machineInvalid("unsupported JSON vault command %q", args[0])
	}
}

func machineTransfer(c machineClient, args []string, opts cliOptions) (any, error) {
	if err := requireDriveID(opts); err != nil {
		return nil, err
	}
	switch args[0] {
	case "put":
		return machinePut(c, args[1:], opts.DriveID)
	case "get":
		return machineGet(c, args[1:], opts)
	}
	return nil, machineInvalid("unsupported transfer command %q", args[0])
}

func machinePut(c machineClient, args []string, driveID int64) (any, error) {
	parsed, err := parseMachineArgs(args, "--encrypt")
	if err != nil {
		return nil, err
	}
	if err := requirePositional(parsed, 2, 2, "tdrive put [--encrypt] <local-file> <absolute-remote-path> --drive-id ID --json"); err != nil {
		return nil, err
	}
	if err := requireAbsoluteRemote(parsed.positional[1]); err != nil {
		return nil, err
	}
	localPath, err := filepath.Abs(parsed.positional[0])
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(localPath)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, machineInvalid("JSON put supports regular files only")
	}
	return c.UploadInDrive(driveID, localPath, parsed.positional[1], parsed.flags["--encrypt"], false, nil)
}

func machineGet(c machineClient, args []string, opts cliOptions) (any, error) {
	parsed, err := parseMachineArgs(args)
	if err != nil {
		return nil, err
	}
	if err := requirePositional(parsed, 2, 2, "tdrive get <absolute-remote-file> <local-file> --drive-id ID --json"); err != nil {
		return nil, err
	}
	if err := requireAbsoluteRemote(parsed.positional[0]); err != nil {
		return nil, err
	}
	localPath, err := filepath.Abs(parsed.positional[1])
	if err != nil {
		return nil, err
	}
	if !opts.Yes {
		if _, err := os.Lstat(localPath); err == nil {
			return nil, machineConflict("local destination exists; --yes allows overwrite (no-clobber precheck is not atomic)")
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	return c.DownloadInDrive(opts.DriveID, parsed.positional[0], localPath, nil)
}

func machineMaintenance(c machineClient, args []string, opts cliOptions) (any, error) {
	if len(args) > 2 {
		return nil, machineInvalid("usage: tdrive %s [drive-id] --json", args[0])
	}
	driveID := opts.DriveID
	if len(args) == 2 {
		parsedID, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil || parsedID <= 0 {
			return nil, machineInvalid("drive ID must be a positive integer")
		}
		if driveID != 0 && driveID != parsedID {
			return nil, machineInvalid("--drive-id and positional drive ID disagree")
		}
		driveID = parsedID
	}
	if driveID <= 0 {
		return nil, machineInvalid("drive ID required: use --drive-id or a positive positional ID")
	}
	selector := strconv.FormatInt(driveID, 10)
	if args[0] == "sync" {
		return c.Sync(selector)
	}
	if !opts.Yes {
		return nil, machineConfirmation("rebuild in JSON mode requires --yes")
	}
	return c.Rebuild(selector)
}
