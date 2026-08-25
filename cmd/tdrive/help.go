package main

import (
	"fmt"
	"strings"
)

func requestedHelp(args []string) ([]string, bool) {
	if len(args) == 0 {
		return nil, false
	}
	if isHelpFlag(args[0]) {
		return nil, true
	}
	if args[0] == "help" {
		return cleanHelpTopic(args[1:]), true
	}
	if len(args) >= 2 && args[1] == "help" && isHelpGroup(args[0]) {
		topic := append([]string{args[0]}, cleanHelpTopic(args[2:])...)
		return topic, true
	}
	for _, arg := range args[1:] {
		if isHelpFlag(arg) {
			return cleanHelpTopic(args), true
		}
	}
	return nil, false
}

func cleanHelpTopic(args []string) []string {
	topic := make([]string, 0, 2)
	for _, arg := range args {
		if isHelpFlag(arg) {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			break
		}
		topic = append(topic, arg)
		if len(topic) == 2 {
			break
		}
	}
	return topic
}

func isHelpFlag(arg string) bool {
	return arg == "-h" || arg == "--help"
}

func isHelpGroup(arg string) bool {
	switch arg {
	case "daemon", "drive", "mount", "vault":
		return true
	default:
		return false
	}
}

func printHelp(topic []string) error {
	key := canonicalHelpKey(topic)
	if key == "" {
		printUsage()
		return nil
	}
	text, ok := helpTopics[key]
	if !ok {
		return fmt.Errorf("unknown help topic %q\n\nRun: tdrive help", strings.Join(topic, " "))
	}
	fmt.Print(strings.TrimLeft(text, "\n"))
	if !strings.HasSuffix(text, "\n") {
		fmt.Println()
	}
	return nil
}

func canonicalHelpKey(topic []string) string {
	if len(topic) == 0 {
		return ""
	}
	key := strings.Join(topic, " ")
	switch key {
	case "status":
		return "daemon status"
	case "drives", "drive list", "drive ls":
		return "drive list"
	case "unlock", "vault unlock":
		return "vault unlock"
	case "drive reject":
		return "drive deny"
	case "mount":
		return "mount start"
	case "install":
		return "install-cli"
	case "uninstall":
		return "uninstall-cli"
	default:
		return key
	}
}

var helpTopics = map[string]string{
	"daemon": `
tdrive daemon <command>

Manage the local TDrive daemon.

Commands:
  start [--background|-b]  Run the daemon
  status                  Show daemon status
  stop                    Stop the daemon
  restart [--background]  Restart the daemon

Most normal CLI commands auto-start the daemon when needed.
The GUI and CLI daemon cannot run at the same time.
`,
	"daemon start": `
tdrive daemon start [--background|-b]

Run the daemon.

Options:
  -b, --background  Start in the background and write logs to daemon.log

Without --background, the daemon runs in the foreground until interrupted.
`,
	"daemon status": `
tdrive daemon status
tdrive status

Show daemon status, active drive, current path, and vault state.
`,
	"daemon stop": `
tdrive daemon stop

Ask the running daemon to stop.
`,
	"daemon restart": `
tdrive daemon restart [--background|-b]

Stop the daemon if it is running, then start it again.
`,
	"install-cli": `
tdrive install-cli [--target PATH] [--update-shell] [--force]

Install the current tdrive binary into a stable PATH location.

Options:
  --target PATH    Install to PATH instead of ~/.local/bin/tdrive
  --update-shell   Add the target directory to your shell config
  --force          Replace an existing non-regular target
`,
	"uninstall-cli": `
tdrive uninstall-cli [--target PATH]

Remove the installed CLI binary and the shell PATH block added by install-cli.

Options:
  --target PATH  Remove PATH instead of ~/.local/bin/tdrive
`,
	"setup": `
tdrive setup [--api-id ID --api-hash HASH]

Save Telegram API credentials used by TDrive.

Options:
  --api-id ID      Telegram API ID from https://my.telegram.org/apps
  --api-hash HASH  Telegram API hash from https://my.telegram.org/apps

Without flags, setup prompts for both values interactively.
`,
	"login": `
tdrive login [phone]

Log in to Telegram. If phone is omitted, the CLI prompts for it.
The CLI will ask for the login code and optional 2FA password.
`,
	"logout": `
tdrive logout [--soft|--full]

Log out and stop the daemon.

Options:
  --full  Clear local Telegram session and user data (default)
  --soft  Clear local app state without revoking the Telegram session
`,
	"whoami": `
tdrive whoami

Print the logged-in Telegram user.
`,
	"drive": `
tdrive drive <command>

Manage drives.

Commands:
  list, ls                      List known drives
  use <name|id>                 Switch active drive
  create [--approval] <title>   Create a shared drive
  join <invite-link>            Join a shared drive
  link [--approval] [name|id]   Print an invite link
  pending [check|rm]            Manage pending joins
  requests [name|id]            List join requests
  approve <user-id> [name|id]   Approve a join request
  deny <user-id> [name|id]      Deny a join request
  leave <name|id>               Leave a shared drive
`,
	"drive list": `
tdrive drives
tdrive drive list
tdrive drive ls

List known drives. The active drive is marked with *.
`,
	"drive use": `
tdrive drive use <name|id>

Switch the active drive for later CLI commands.
`,
	"drive create": `
tdrive drive create [--approval] <title>

Create a shared drive and make it active.

Options:
  --approval, --require-approval  Invite links require approval
`,
	"drive join": `
tdrive drive join <invite-link>

Join a shared drive from an invite link.
If approval is required, the request is stored as pending.
`,
	"drive link": `
tdrive drive link [--approval] [name|id]

Print an invite link for a drive. Defaults to the active drive.

Options:
  --approval, --require-approval  Create an approval-required invite link
`,
	"drive pending": `
tdrive drive pending
tdrive drive pending check [invite-hash...]
tdrive drive pending rm <invite-hash>

List, check, or remove pending shared-drive join requests.
`,
	"drive requests": `
tdrive drive requests [name|id]

List pending join requests for a shared drive.
Defaults to the active drive.
`,
	"drive approve": `
tdrive drive approve <user-id> [name|id]

Approve a user's shared-drive join request.
Defaults to the active drive.
`,
	"drive deny": `
tdrive drive deny <user-id> [name|id]

Deny a user's shared-drive join request.
Defaults to the active drive.
`,
	"drive leave": `
tdrive drive leave <name|id>

Leave a shared drive.
`,
	"pwd": `
tdrive pwd

Print the current remote directory for the active drive.
`,
	"cd": `
tdrive cd <remote-path>

Change the current remote directory for the active drive.
`,
	"ls": `
tdrive ls [-l|--long] [remote-path]

List remote files and folders.

Options:
  -l, --long  Show type, size, date, and name
`,
	"find": `
tdrive find [-n limit|--limit limit] <query>

Search remote files and folders in the active drive.

Options:
  -n, --limit  Maximum results to print (default: 50)
`,
	"mkdir": `
tdrive mkdir [-p|--parents] <remote-path>

Create a remote folder.

Options:
  -p, --parents  Create missing parent folders
`,
	"rm": `
tdrive rm [-r|-R|--recursive] <remote-path>

Remove a remote file or folder.

Options:
  -r, -R, --recursive  Required when removing folders
`,
	"mv": `
tdrive mv <source> <destination>

Move or rename a remote file or folder.
`,
	"vault": `
tdrive vault <command>

Manage the personal-drive encryption vault.

Commands:
  status  Show vault state
  unlock  Unlock encrypted files in the daemon
  lock    Forget the in-memory vault key
`,
	"vault status": `
tdrive vault status

Show whether the encryption vault is configured and unlocked.
`,
	"vault unlock": `
tdrive unlock [--password-stdin]
tdrive vault unlock [--password-stdin]

Unlock the encryption vault in the daemon.

Options:
  --password-stdin  Read the password from stdin

The vault key is kept only in daemon memory.
`,
	"vault lock": `
tdrive vault lock

Forget the in-memory vault key from the daemon.
`,
	"mount start": `
tdrive mount [start] [--drive <name|id>] [--windows-drive T:]

Attach one TDrive as a read-only desktop drive.

Options:
  --drive <name|id>       Pin this drive; defaults to the current active drive
  --windows-drive T:      Windows Explorer drive letter, default T:

Selecting --drive does not change the active drive used by other CLI commands.
TDrive attaches the drive automatically: Finder on macOS, T: in Windows
Explorer, or the current GVfs/GIO desktop session on Linux.

Only browsing and reading are supported. Creating, editing, renaming, and
deleting through the mount are rejected.

Close the TDrive GUI before starting this CLI-owned mount. Keep the daemon
running until "tdrive mount stop" has completed.
`,
	"mount status": `
tdrive mount status

Show the pinned drive and its safe OS location.
`,
	"mount stop": `
tdrive mount stop

Disconnect the OS drive, then stop the private read-only server.
`,
	"put": `
tdrive put [-e|--encrypt] [--extract] <local> [remote-path]

Upload a local file, folder, or archive.

Options:
  -e, --encrypt  Encrypt file uploads in My Drive
  --extract      Extract .zip/.tar/.tar.gz/.tgz archives before upload

Notes:
  Folder and archive imports require the destination folder to exist first.
  Single-file uploads can create or rename the final file path.
`,
	"get": `
tdrive get <remote-file> [local-path]

Download a remote file.

If local-path is a directory, the remote filename is used inside it.
If local-path is omitted, the file is saved in the current local directory.
`,
	"cat": `
tdrive cat <remote-file>

Write a remote file to stdout.

Encrypted files may be staged through a temporary file before stdout.
`,
	"sync": `
tdrive sync [name|id]

Sync a drive from Telegram history. Defaults to the active drive.
`,
	"rebuild": `
tdrive rebuild [name|id]

Rebuild the local projection for a drive from Telegram history.
Defaults to the active drive.
`,
}
