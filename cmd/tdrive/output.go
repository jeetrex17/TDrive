package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"unicode"

	"TDrive/backend/daemon"
)

func terminalSafeText(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '\u2028' || r == '\u2029' {
			return '?'
		}
		return r
	}, value)
}

type commandError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
	Hint      string `json:"hint,omitempty"`
}

func classifyCommandError(err error) (commandError, int) {
	result := commandError{Code: "operation_failed", Message: terminalSafeText(err.Error())}
	var cliErr *CLIError
	if errors.As(err, &cliErr) {
		result.Code = cliErr.Code
		result.Message = terminalSafeText(cliErr.Message)
		result.Hint = terminalSafeText(cliErr.Hint)
		result.Retryable = cliErr.Retryable
	} else {
		var remote *daemon.RemoteError
		var netErr net.Error
		switch {
		case errors.As(err, &remote):
			result.Code = remote.Code
		case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
			result.Code = "timeout"
			result.Retryable = true
		case errors.Is(err, daemon.ErrDaemonUnavailable):
			result.Code = "unavailable"
			result.Retryable = true
		case errors.Is(err, os.ErrNotExist):
			result.Code = "not_found"
		}
	}
	switch result.Code {
	case "invalid_argument", "invalid_request", "unsupported":
		return result, 2
	case "interaction_required", "encryption_password_required", "authentication_required":
		return result, 3
	case "not_found":
		return result, 4
	case "conflict", "confirmation_required":
		return result, 5
	case "timeout", "unavailable":
		return result, 6
	default:
		return result, 1
	}
}

func writeCommandError(writer io.Writer, err error, jsonOutput bool) int {
	classified, exitCode := classifyCommandError(err)
	if jsonOutput {
		response := struct {
			SchemaVersion int          `json:"schema_version"`
			OK            bool         `json:"ok"`
			Error         commandError `json:"error"`
		}{SchemaVersion: machineSchemaVersion, Error: classified}
		if encodeErr := json.NewEncoder(writer).Encode(response); encodeErr != nil {
			fmt.Fprintln(writer, "could not encode error response")
		}
		return exitCode
	}
	fmt.Fprintln(writer, classified.Message)
	if classified.Hint != "" {
		fmt.Fprintln(writer, classified.Hint)
	}
	return exitCode
}

func wantsJSON(args []string) bool {
	if opts, _, err := parseCLIOptions(args); err == nil {
		return opts.JSON
	}
	for index, arg := range args {
		if arg == "--" {
			return false
		}
		if arg == "--json" || arg == "--output=json" {
			return true
		}
		if arg == "--output" && index+1 < len(args) && args[index+1] == "json" {
			return true
		}
	}
	return false
}
