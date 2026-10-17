// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

// Command wardlock is the Wardlock command-line client. It talks to a server
// only through wardlock.nz/apiclient. Commands appear here only once they
// work end to end.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
)

// version is set at build time with -ldflags "-X main.version=…".
var version = "dev"

const usage = `wardlock publishes and protects static sites on a Wardlock server.

Usage:
  wardlock <command> [flags]

Commands:
  version   Print the CLI version, and the server's with --server
  help      Show this help

Run "wardlock <command> --help" for a command's flags.
`

var errUsage = errors.New("usage")

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	err := run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr)
	stop()
	switch {
	case errors.Is(err, errUsage):
		os.Exit(2)
	case err != nil:
		fmt.Fprintln(os.Stderr, "wardlock:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return errUsage
	}
	switch args[0] {
	case "version", "--version":
		return runVersion(ctx, args[1:], getenv, stdout, stderr)
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return nil
	default:
		fmt.Fprintf(stderr, "wardlock: unknown command %q\n\n%s", args[0], usage)
		return errUsage
	}
}
