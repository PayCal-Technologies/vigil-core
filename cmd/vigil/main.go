package main

import (
	"context"

	"os"

	"os/signal"

	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(runContext(ctx, os.Args[1:]))
}

func run(args []string) int {
	return runContext(context.Background(), args)
}
