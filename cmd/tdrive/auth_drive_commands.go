package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
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
	options, err := parseLoginArgs(args)
	if err != nil {
		return err
	}
	if cliNonInteractive() {
		return interactionRequired("Telegram login requires an interactive code or two-factor password")
	}
	phone := options.phone
	if phone == "" {
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
		if options.personalDriveID != "" || options.createPersonalDrive {
			setup, err = chooseExplicitPersonalDrive(c, setup, options.personalDriveID, options.createPersonalDrive)
		} else {
			setup, err = choosePersonalDrive(c, setup, os.Stdin, os.Stderr)
		}
		if err != nil {
			return err
		}
	}
	if setup.ActiveChannelID != "" {
		fmt.Printf("drive: %s\n", terminalSafeText(setup.ActiveChannelID))
	} else if out.ActiveChannelID != 0 {
		fmt.Printf("drive: %d\n", out.ActiveChannelID)
	}
	return nil
}

type loginOptions struct {
	phone               string
	personalDriveID     string
	createPersonalDrive bool
}

func parseLoginArgs(args []string) (loginOptions, error) {
	const usage = "usage: tdrive login [phone] [--personal-drive-id ID|--create-personal-drive]"
	var options loginOptions
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--personal-drive-id":
			index++
			if index >= len(args) || strings.TrimSpace(args[index]) == "" || strings.HasPrefix(args[index], "-") || options.personalDriveID != "" {
				return loginOptions{}, errors.New(usage)
			}
			options.personalDriveID = strings.TrimSpace(args[index])
		case "--create-personal-drive":
			if options.createPersonalDrive {
				return loginOptions{}, errors.New(usage)
			}
			options.createPersonalDrive = true
		default:
			if strings.HasPrefix(args[index], "-") || options.phone != "" {
				return loginOptions{}, errors.New(usage)
			}
			options.phone = strings.TrimSpace(args[index])
		}
	}
	if options.personalDriveID != "" && options.createPersonalDrive {
		return loginOptions{}, errors.New(usage)
	}
	return options, nil
}

func chooseExplicitPersonalDrive(client personalDriveSetupClient, setup daemon.PersonalDriveSetup, channelID string, create bool) (daemon.PersonalDriveSetup, error) {
	if setup.Status != "selection_required" {
		return setup, nil
	}
	if client == nil {
		return daemon.PersonalDriveSetup{}, fmt.Errorf("personal drive picker is unavailable")
	}
	if create {
		return client.CreatePersonalDrive()
	}
	for _, candidate := range setup.Candidates {
		if candidate.ID == channelID {
			return client.SelectPersonalDrive(channelID)
		}
	}
	return daemon.PersonalDriveSetup{}, fmt.Errorf("personal drive %q is not among the offered channels", channelID)
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

	if cliNonInteractive() {
		return daemon.PersonalDriveSetup{}, interactionRequired("personal drive selection requires --personal-drive-id or --create-personal-drive")
	}
	buffered := bufio.NewReader(reader)
	readChoice := func() (string, error) {
		if reader == os.Stdin {
			return readStdinLine()
		}
		return readBufferedLine(buffered)
	}
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
		fmt.Fprintf(writer, "  %d. %s - %s (Channel ID %s)\n", i+1, title, details, terminalSafeText(candidate.ID))
	}
	fmt.Fprintln(writer, "  c. Create New TDrive")

	for {
		fmt.Fprint(writer, "Selection: ")
		line, err := readChoice()
		if err != nil {
			return daemon.PersonalDriveSetup{}, err
		}
		choice := strings.TrimSpace(line)
		if strings.EqualFold(choice, "c") {
			fmt.Fprint(writer, "Create one new empty Telegram channel? [y/N]: ")
			answer, err := readChoice()
			if err != nil {
				return daemon.PersonalDriveSetup{}, err
			}
			if strings.EqualFold(strings.TrimSpace(answer), "y") {
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
	cleaned = terminalSafeText(cleaned)
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
	if mode == "full" {
		if err := requireNonInteractiveConfirmation(cliCurrentOptions(), "full logout"); err != nil {
			return err
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
		fmt.Println(terminalSafeText(out.User.DisplayName))
	}
	if out.User.Username != "" {
		fmt.Println("@" + terminalSafeText(out.User.Username))
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
		fmt.Println(terminalSafeText(out.Drive.InviteLink))
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
	if len(args) > 0 && (args[0] == "rm" || args[0] == "remove") {
		if len(args) != 2 {
			return fmt.Errorf("usage: tdrive drive pending rm <invite-hash>")
		}
		if err := requireNonInteractiveConfirmation(cliCurrentOptions(), "removing a pending join"); err != nil {
			return err
		}
	}
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
					fmt.Fprintf(os.Stderr, "%s: %v\n", terminalSafeText(p.InviteHash), err)
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
	fmt.Println(terminalSafeText(out.Link))
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
	if err := requireNonInteractiveConfirmation(cliCurrentOptions(), "resolving a join request"); err != nil {
		return err
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
	if err := requireNonInteractiveConfirmation(cliCurrentOptions(), "leaving a drive"); err != nil {
		return err
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
	selector, err := driveSelectorForCommand(args, cliCurrentOptions().DriveID, "usage: tdrive sync [name|id]")
	if err != nil {
		return err
	}
	c, err := newDaemonClient()
	if err != nil {
		return err
	}
	out, err := c.Sync(selector)
	if err != nil {
		return err
	}
	fmt.Printf("synced: %s (%d)\n", terminalSafeText(out.Drive.Title), out.Drive.ID)
	return nil
}

func runRebuild(args []string) error {
	options := cliCurrentOptions()
	selector, err := driveSelectorForCommand(args, options.DriveID, "usage: tdrive rebuild [name|id]")
	if err != nil {
		return err
	}
	if err := validateRebuildOptions(options); err != nil {
		return err
	}
	c, err := newDaemonClient()
	if err != nil {
		return err
	}
	out, err := c.Rebuild(selector)
	if err != nil {
		return err
	}
	fmt.Printf("rebuilt: %s (%d)\n", terminalSafeText(out.Drive.Title), out.Drive.ID)
	return nil
}

func driveSelectorForCommand(args []string, driveID int64, usage string) (string, error) {
	if len(args) > 1 || driveID < 0 {
		return "", errors.New(usage)
	}
	if driveID > 0 {
		if len(args) != 0 {
			return "", errors.New("cannot combine --drive-id with a positional drive selector")
		}
		return strconv.FormatInt(driveID, 10), nil
	}
	return optionalString(args, 0), nil
}

func validateRebuildOptions(options cliOptions) error {
	if options.NonInteractive && !options.Yes {
		return interactionRequired("rebuild requires --yes in non-interactive mode")
	}
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
				fmt.Fprintln(os.Stderr, terminalSafeText(hint))
			}
		}
	}
}

func parseSetupArgs(args []string) (int, string, error) {
	const usage = "usage: tdrive setup [--api-id ID] [--api-hash HASH|--api-hash-stdin]"
	var apiID int
	var apiHash string
	var apiHashStdin bool
	var apiHashArg bool
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--api-id":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") || apiID != 0 {
				return 0, "", errors.New(usage)
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n <= 0 {
				return 0, "", fmt.Errorf("invalid api id %q", args[i+1])
			}
			apiID = n
			i++
		case "--api-hash":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") || apiHashArg {
				return 0, "", errors.New(usage)
			}
			apiHash = strings.TrimSpace(args[i+1])
			apiHashArg = true
			i++
		case "--api-hash-stdin":
			if apiHashStdin {
				return 0, "", errors.New(usage)
			}
			apiHashStdin = true
		default:
			return 0, "", errors.New(usage)
		}
	}
	if apiHashArg && apiHashStdin {
		return 0, "", fmt.Errorf("choose either --api-hash or --api-hash-stdin")
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
	if apiHashStdin {
		apiHash, err = readStdinLine()
		if err != nil {
			return 0, "", fmt.Errorf("read API hash from stdin: %w", err)
		}
		apiHash = strings.TrimSpace(apiHash)
	} else if apiHash == "" {
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
	fmt.Printf("setup: %s\n", terminalSafeText(status.SystemStatus))
	if status.LoggedIn {
		fmt.Println("login: yes")
	} else {
		fmt.Println("login: no")
	}
}

func printDriveUse(out daemon.DriveUseResponse) {
	fmt.Printf("drive: %s (%d)\n", terminalSafeText(out.Drive.Title), out.Drive.ID)
	fmt.Printf("cwd:   %s\n", terminalSafeText(out.CurrentPath))
}

func printJoinResult(out daemon.DriveJoinResponse) {
	switch out.Status {
	case "joined":
		if out.Drive != nil {
			fmt.Printf("joined: %s (%d)\n", terminalSafeText(out.Drive.Title), out.Drive.ID)
		} else {
			fmt.Println("joined")
		}
	case "pending":
		if out.Pending != nil {
			fmt.Printf("pending: %s (%s)\n", terminalSafeText(out.Pending.Title), terminalSafeText(out.Pending.InviteHash))
		} else {
			fmt.Println("pending")
		}
	default:
		fmt.Println(terminalSafeText(out.Status))
	}
}

func printPendingJoins(rows []daemon.PendingJoin) {
	if len(rows) == 0 {
		fmt.Println("No pending joins")
		return
	}
	for _, row := range rows {
		fmt.Printf("%-10s %-24s %s\n", terminalSafeText(row.Status), terminalSafeText(row.InviteHash), terminalSafeText(row.Title))
		if row.LastError != "" {
			fmt.Printf("  error: %s\n", terminalSafeText(row.LastError))
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
		fmt.Printf("%-14d %-20s %s\n", row.UserID, when, terminalSafeText(user))
		if row.About != "" {
			fmt.Printf("  %s\n", terminalSafeText(row.About))
		}
	}
}

var promptInput struct {
	mu     sync.Mutex
	file   *os.File
	reader *bufio.Reader
}

func readStdinLine() (string, error) {
	promptInput.mu.Lock()
	defer promptInput.mu.Unlock()
	if promptInput.file != os.Stdin {
		promptInput.file = os.Stdin
		promptInput.reader = bufio.NewReader(os.Stdin)
	}
	return readBufferedLine(promptInput.reader)
}

func readBufferedLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil && (!errors.Is(err, io.EOF) || line == "") {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func promptLine(prompt string) (string, error) {
	if cliNonInteractive() {
		return "", interactionRequired("this command requires input; provide explicit options")
	}
	fmt.Fprint(os.Stderr, prompt)
	return readStdinLine()
}

func promptSecret(prompt string) (string, error) {
	if cliNonInteractive() {
		return "", interactionRequired("this command requires a password; provide it through a supported stdin option")
	}
	fmt.Fprint(os.Stderr, prompt)
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return readStdinLine()
	}
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	return string(b), err
}

func optionalString(args []string, index int) string {
	if index >= len(args) {
		return ""
	}
	return args[index]
}
