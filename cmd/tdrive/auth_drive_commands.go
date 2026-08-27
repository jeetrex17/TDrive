package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

	"TDrive/backend/daemon"

	"golang.org/x/term"
)

func runSetup(args []string) error {
	apiID, apiHash, err := parseSetupArgs(args)
	if err != nil {
		return err
	}
	c, err := newDaemonClient()
	if err != nil {
		return err
	}
	out, err := c.AuthSetup(apiID, apiHash)
	if err != nil {
		return err
	}
	printAuthStatus(out.Status)
	return nil
}

func runLogin(args []string) error {
	if len(args) > 1 {
		return fmt.Errorf("usage: tdrive login <phone>")
	}
	phone := ""
	var err error
	if len(args) == 1 {
		phone = strings.TrimSpace(args[0])
	} else {
		phone, err = promptLine("Phone: ")
		if err != nil {
			return err
		}
	}
	if phone == "" {
		return fmt.Errorf("phone number required")
	}

	c, err := newDaemonClient()
	if err != nil {
		return err
	}
	out, err := c.Login(phone, loginEventHandler(c))
	if err != nil {
		return err
	}
	if out.LoggedIn {
		fmt.Println("login: ok")
	}
	setup := out.PersonalDrive
	if setup.Status == "selection_required" {
		setup, err = choosePersonalDrive(c, setup, os.Stdin, os.Stderr)
		if err != nil {
			return err
		}
	}
	if setup.ActiveChannelID != "" {
		fmt.Printf("drive: %s\n", setup.ActiveChannelID)
	} else if out.ActiveChannelID != 0 {
		fmt.Printf("drive: %d\n", out.ActiveChannelID)
	}
	return nil
}

type personalDriveSetupClient interface {
	SelectPersonalDrive(channelID string) (daemon.PersonalDriveSetup, error)
	CreatePersonalDrive() (daemon.PersonalDriveSetup, error)
}

// choosePersonalDrive accepts only a displayed menu number or the explicit
// create action. It never accepts a raw Telegram channel ID.
func choosePersonalDrive(
	client personalDriveSetupClient,
	setup daemon.PersonalDriveSetup,
	reader io.Reader,
	writer io.Writer,
) (daemon.PersonalDriveSetup, error) {
	if setup.Status != "selection_required" {
		return setup, nil
	}
	if client == nil || reader == nil || writer == nil {
		return daemon.PersonalDriveSetup{}, fmt.Errorf("personal drive picker is unavailable")
	}

	scanner := bufio.NewScanner(reader)
	fmt.Fprintln(writer, "Choose the Telegram channel to use as your personal TDrive:")
	for i, candidate := range setup.Candidates {
		title := terminalSafeTitle(candidate.Title)
		if title == "" {
			title = "Untitled channel"
		}
		details := "Empty"
		if candidate.HasActivity {
			details = "Has activity"
		}
		if candidate.Recommended {
			details += ", Recommended"
		}
		fmt.Fprintf(writer, "  %d. %s - %s (Channel ID %s)\n", i+1, title, details, candidate.ID)
	}
	fmt.Fprintln(writer, "  c. Create New TDrive")

	for {
		fmt.Fprint(writer, "Selection: ")
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return daemon.PersonalDriveSetup{}, err
			}
			return daemon.PersonalDriveSetup{}, io.EOF
		}
		choice := strings.TrimSpace(scanner.Text())
		if strings.EqualFold(choice, "c") {
			fmt.Fprint(writer, "Create one new empty Telegram channel? [y/N]: ")
			if !scanner.Scan() {
				if err := scanner.Err(); err != nil {
					return daemon.PersonalDriveSetup{}, err
				}
				return daemon.PersonalDriveSetup{}, io.EOF
			}
			if strings.EqualFold(strings.TrimSpace(scanner.Text()), "y") {
				return client.CreatePersonalDrive()
			}
			fmt.Fprintln(writer, "Creation cancelled.")
			continue
		}

		index, err := strconv.Atoi(choice)
		if err == nil && index >= 1 && index <= len(setup.Candidates) {
			return client.SelectPersonalDrive(setup.Candidates[index-1].ID)
		}
		fmt.Fprintln(writer, "Enter a menu number, or c to create a new TDrive.")
	}
}

func terminalSafeTitle(title string) string {
	cleaned := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, title)
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	const maxTitleRunes = 80
	runes := []rune(cleaned)
	if len(runes) > maxTitleRunes {
		cleaned = string(runes[:maxTitleRunes]) + "..."
	}
	return cleaned
}

func runLogout(args []string) error {
	mode := "full"
	for _, arg := range args {
		switch arg {
		case "--soft":
			mode = "soft"
		case "--full":
			mode = "full"
		default:
			return fmt.Errorf("usage: tdrive logout [--soft|--full]")
		}
	}
	c, err := newDaemonClient()
	if err != nil {
		return err
	}
	out, err := c.Logout(mode)
	if err != nil {
		return err
	}
	fmt.Printf("logout: %s\n", out.Mode)
	if out.Stopping {
		fmt.Println("daemon: stopping")
	}
	return nil
}

func printWhoami() error {
	c, err := newDaemonClient()
	if err != nil {
		return err
	}
	out, err := c.Whoami()
	if err != nil {
		return err
	}
	if out.User.DisplayName != "" {
		fmt.Println(out.User.DisplayName)
	}
	if out.User.Username != "" {
		fmt.Println("@" + out.User.Username)
	}
	fmt.Printf("id: %d\n", out.User.UserID)
	return nil
}

func runDriveCreate(args []string) error {
	requireApproval, positional, err := splitApprovalFlag(args)
	if err != nil {
		return err
	}
	title := strings.TrimSpace(strings.Join(positional, " "))
	if title == "" {
		return fmt.Errorf("usage: tdrive drive create [--approval] <title>")
	}
	c, err := newDaemonClient()
	if err != nil {
		return err
	}
	out, err := c.CreateDrive(title, requireApproval)
	if err != nil {
		return err
	}
	printDriveUse(out)
	if out.Drive.InviteLink != "" {
		fmt.Println(out.Drive.InviteLink)
	}
	return nil
}

func runDriveJoin(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: tdrive drive join <invite-link>")
	}
	c, err := newDaemonClient()
	if err != nil {
		return err
	}
	out, err := c.JoinDrive(args[0])
	if err != nil {
		return err
	}
	printJoinResult(out)
	return nil
}

func runDrivePending(args []string) error {
	c, err := newDaemonClient()
	if err != nil {
		return err
	}
	if len(args) == 0 {
		out, err := c.ListPendingJoins()
		if err != nil {
			return err
		}
		printPendingJoins(out.Pending)
		return nil
	}

	switch args[0] {
	case "check":
		if len(args) == 1 {
			out, err := c.ListPendingJoins()
			if err != nil {
				return err
			}
			for _, p := range out.Pending {
				result, err := c.CheckPendingJoin(p.InviteHash)
				if err != nil {
					fmt.Fprintf(os.Stderr, "%s: %v\n", p.InviteHash, err)
					continue
				}
				printJoinResult(result)
			}
			return nil
		}
		for _, hash := range args[1:] {
			result, err := c.CheckPendingJoin(hash)
			if err != nil {
				return err
			}
			printJoinResult(result)
		}
		return nil
	case "rm", "remove":
		if len(args) != 2 {
			return fmt.Errorf("usage: tdrive drive pending rm <invite-hash>")
		}
		if err := c.RemovePendingJoin(args[1]); err != nil {
			return err
		}
		fmt.Println("removed")
		return nil
	default:
		return fmt.Errorf("usage: tdrive drive pending [check [hash...]|rm <hash>]")
	}
}

func runDriveLink(args []string) error {
	requireApproval, positional, err := splitApprovalFlag(args)
	if err != nil {
		return err
	}
	if len(positional) > 1 {
		return fmt.Errorf("usage: tdrive drive link [--approval] [name|id]")
	}
	selector := optionalString(positional, 0)
	c, err := newDaemonClient()
	if err != nil {
		return err
	}
	out, err := c.InviteLink(selector, requireApproval)
	if err != nil {
		return err
	}
	fmt.Println(out.Link)
	return nil
}

func runDriveRequests(args []string) error {
	if len(args) > 1 {
		return fmt.Errorf("usage: tdrive drive requests [name|id]")
	}
	selector := optionalString(args, 0)
	c, err := newDaemonClient()
	if err != nil {
		return err
	}
	out, err := c.JoinRequests(selector)
	if err != nil {
		return err
	}
	printJoinRequests(out.Requests)
	return nil
}

func runDriveJoinAction(args []string, approve bool) error {
	if len(args) < 1 || len(args) > 2 {
		if approve {
			return fmt.Errorf("usage: tdrive drive approve <user-id> [name|id]")
		}
		return fmt.Errorf("usage: tdrive drive deny <user-id> [name|id]")
	}
	userID, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil || userID <= 0 {
		return fmt.Errorf("invalid user id %q", args[0])
	}
	selector := optionalString(args, 1)
	c, err := newDaemonClient()
	if err != nil {
		return err
	}
	out, err := c.ResolveJoinRequest(selector, userID, approve)
	if err != nil {
		return err
	}
	if approve {
		fmt.Println("approved")
	} else {
		fmt.Println("denied")
	}
	printJoinRequests(out.Requests)
	return nil
}

func runDriveLeave(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: tdrive drive leave <name|id>")
	}
	c, err := newDaemonClient()
	if err != nil {
		return err
	}
	out, err := c.LeaveDrive(args[0])
	if err != nil {
		return err
	}
	fmt.Println("left")
	printDriveUse(out)
	return nil
}

func runSync(args []string) error {
	if len(args) > 1 {
		return fmt.Errorf("usage: tdrive sync [name|id]")
	}
	c, err := newDaemonClient()
	if err != nil {
		return err
	}
	out, err := c.Sync(optionalString(args, 0))
	if err != nil {
		return err
	}
	fmt.Printf("synced: %s (%d)\n", out.Drive.Title, out.Drive.ID)
	return nil
}

func runRebuild(args []string) error {
	if len(args) > 1 {
		return fmt.Errorf("usage: tdrive rebuild [name|id]")
	}
	c, err := newDaemonClient()
	if err != nil {
		return err
	}
	out, err := c.Rebuild(optionalString(args, 0))
	if err != nil {
		return err
	}
	fmt.Printf("rebuilt: %s (%d)\n", out.Drive.Title, out.Drive.ID)
	return nil
}

func loginEventHandler(c *daemon.Client) daemon.EventHandler {
	return func(event daemon.Event) {
		switch event.Name {
		case "login-code-required":
			code, err := promptLine("Code: ")
			if err != nil {
				fmt.Fprintf(os.Stderr, "read code: %v\n", err)
				return
			}
			if err := c.SubmitLoginCode(code); err != nil {
				fmt.Fprintf(os.Stderr, "submit code: %v\n", err)
			}
		case "login-code-invalid":
			fmt.Fprintln(os.Stderr, "wrong code")
		case "login-password-required":
			password, err := promptSecret("Password: ")
			if err != nil {
				fmt.Fprintf(os.Stderr, "read password: %v\n", err)
				return
			}
			if err := c.SubmitLoginPassword(password); err != nil {
				fmt.Fprintf(os.Stderr, "submit password: %v\n", err)
			}
		case "gothint":
			hint := eventArgString(event, 0)
			if hint != "" && !strings.Contains(strings.ToLower(hint), "no hint") {
				fmt.Fprintln(os.Stderr, hint)
			}
		}
	}
}

func parseSetupArgs(args []string) (int, string, error) {
	var apiID int
	var apiHash string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--api-id":
			if i+1 >= len(args) {
				return 0, "", fmt.Errorf("usage: tdrive setup [--api-id ID --api-hash HASH]")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n <= 0 {
				return 0, "", fmt.Errorf("invalid api id %q", args[i+1])
			}
			apiID = n
			i++
		case "--api-hash":
			if i+1 >= len(args) {
				return 0, "", fmt.Errorf("usage: tdrive setup [--api-id ID --api-hash HASH]")
			}
			apiHash = strings.TrimSpace(args[i+1])
			i++
		default:
			return 0, "", fmt.Errorf("usage: tdrive setup [--api-id ID --api-hash HASH]")
		}
	}
	var err error
	if apiID == 0 {
		raw, readErr := promptLine("API ID: ")
		if readErr != nil {
			return 0, "", readErr
		}
		apiID, err = strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || apiID <= 0 {
			return 0, "", fmt.Errorf("invalid api id %q", raw)
		}
	}
	if apiHash == "" {
		apiHash, err = promptLine("API Hash: ")
		if err != nil {
			return 0, "", err
		}
		apiHash = strings.TrimSpace(apiHash)
	}
	if apiHash == "" {
		return 0, "", fmt.Errorf("api hash required")
	}
	return apiID, apiHash, nil
}

func splitApprovalFlag(args []string) (bool, []string, error) {
	requireApproval := false
	var positional []string
	for _, arg := range args {
		switch arg {
		case "--approval", "--require-approval":
			requireApproval = true
		default:
			positional = append(positional, arg)
		}
	}
	return requireApproval, positional, nil
}

func printAuthStatus(status daemon.AuthStatus) {
	fmt.Printf("setup: %s\n", status.SystemStatus)
	if status.LoggedIn {
		fmt.Println("login: yes")
	} else {
		fmt.Println("login: no")
	}
}

func printDriveUse(out daemon.DriveUseResponse) {
	fmt.Printf("drive: %s (%d)\n", out.Drive.Title, out.Drive.ID)
	fmt.Printf("cwd:   %s\n", out.CurrentPath)
}

func printJoinResult(out daemon.DriveJoinResponse) {
	switch out.Status {
	case "joined":
		if out.Drive != nil {
			fmt.Printf("joined: %s (%d)\n", out.Drive.Title, out.Drive.ID)
		} else {
			fmt.Println("joined")
		}
	case "pending":
		if out.Pending != nil {
			fmt.Printf("pending: %s (%s)\n", out.Pending.Title, out.Pending.InviteHash)
		} else {
			fmt.Println("pending")
		}
	default:
		fmt.Println(out.Status)
	}
}

func printPendingJoins(rows []daemon.PendingJoin) {
	if len(rows) == 0 {
		fmt.Println("No pending joins")
		return
	}
	for _, row := range rows {
		fmt.Printf("%-10s %-24s %s\n", row.Status, row.InviteHash, row.Title)
		if row.LastError != "" {
			fmt.Printf("  error: %s\n", row.LastError)
		}
	}
}

func printJoinRequests(rows []daemon.JoinRequest) {
	if len(rows) == 0 {
		fmt.Println("No join requests")
		return
	}
	for _, row := range rows {
		name := row.DisplayName
		if name == "" {
			name = strconv.FormatInt(row.UserID, 10)
		}
		when := "-"
		if row.RequestedAt != 0 {
			when = time.Unix(row.RequestedAt, 0).Format("2006-01-02 15:04")
		}
		user := name
		if row.Username != "" {
			user += " @" + row.Username
		}
		fmt.Printf("%-14d %-20s %s\n", row.UserID, when, user)
		if row.About != "" {
			fmt.Printf("  %s\n", row.About)
		}
	}
}

func promptLine(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func promptSecret(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return promptLine("")
	}
	return string(b), nil
}

func optionalString(args []string, index int) string {
	if index >= len(args) {
		return ""
	}
	return args[index]
}
