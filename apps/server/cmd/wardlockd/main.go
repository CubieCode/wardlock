// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Command wardlockd is the Wardlock server: edge, owner API and dashboard in
// one process. Configuration comes from WARDLOCK_* environment variables.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

const usage = `wardlockd runs the Wardlock server.

Usage:
  wardlockd <command>

Commands:
  serve                         Start the server
  admin create --email ADDRESS  Create an owner and print a one-time sign-in URL
  gc                            Prune deploy history and remove unreferenced blobs
  backup PATH                   Write a consistent copy of the database to PATH
  version                       Print the version
  help                          Show this help

Configuration is read from WARDLOCK_* environment variables;
see .env.example in the source repository.
`

var errUsage = errors.New("usage")

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr)
	stop()
	switch {
	case errors.Is(err, errUsage):
		os.Exit(2)
	case err != nil:
		fmt.Fprintln(os.Stderr, "wardlockd:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return errUsage
	}
	switch args[0] {
	case "serve":
		return serve(ctx, getenv, stderr)
	case "admin":
		return admin(ctx, args[1:], getenv, stdout, stderr)
	case "gc":
		return gcCommand(ctx, args[1:], getenv, stdout, stderr)
	case "backup":
		return backupCommand(ctx, args[1:], getenv, stdout, stderr)
	case "version", "--version":
		fmt.Fprintln(stdout, "wardlockd", version())
		return nil
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return nil
	default:
		fmt.Fprintf(stderr, "wardlockd: unknown command %q\n\n%s", args[0], usage)
		return errUsage
	}
}
