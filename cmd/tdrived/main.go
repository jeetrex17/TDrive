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
