// Command tdrived runs the TDrive daemon and nothing else.
//
// It exists only so packaging and service managers have a daemon-only binary:
// it installs a signal-cancelled context and hands off to backend/daemon. There
// are no flags and no configuration file, and "tdrive daemon start" in the
// foreground is the same program. Lock acquisition, the listener, engine
// construction and shutdown ordering all belong to backend/daemon.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"TDrive/backend/daemon"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := daemon.Run(ctx, daemon.ServerConfig{
		Warnf: func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, format, args...)
		},
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
