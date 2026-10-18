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
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/zalando/go-keyring"
)

// version is set at build time with -ldflags "-X main.version=…".
var version = "dev"

// command is one subcommand. Add a command here only once it works end to
// end; help lists exactly these.
type command struct {
	name, summary string
	run           func(ctx context.Context, a *app, args []string) error
}

var commands = []command{
	{"login", "Log in to a Wardlock server in your browser", runLogin},
	{"logout", "Revoke and forget the stored token", runLogout},
	{"version", "Print the CLI version, and the server's with --server", runVersion},
}

func usage() string {
	var b strings.Builder
	b.WriteString("wardlock publishes and protects static sites on a Wardlock server.\n\nUsage:\n  wardlock <command> [flags]\n\nCommands:\n")
	for _, c := range commands {
		fmt.Fprintf(&b, "  %-9s %s\n", c.name, c.summary)
	}
	fmt.Fprintf(&b, "  %-9s %s\n", "help", "Show this help")
	b.WriteString("\nRun \"wardlock <command> --help\" for a command's flags.\n" +
		"WARDLOCK_TOKEN, when set, is used instead of the stored token.\n")
	return b.String()
}

var errUsage = errors.New("usage")

// app is everything a command touches outside its arguments, so tests can
// replace it.
type app struct {
	getenv         func(string) string
	stdout, stderr io.Writer
	// interactive is true when stdout is a terminal: only then may the
	// CLI open a browser.
	interactive bool
	http        *http.Client
	creds       credentialStore
	openBrowser func(url string) error
	hostname    func() (string, error)
	// sleep waits for d or until ctx ends.
	sleep func(ctx context.Context, d time.Duration) error
	now   func() time.Time
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	a := &app{
		getenv:      os.Getenv,
		stdout:      os.Stdout,
		stderr:      os.Stderr,
		interactive: isTerminal(os.Stdout),
		http:        &http.Client{Timeout: 30 * time.Second},
		creds:       credentialStore{keyring: systemKeyring{}, dir: func() (string, error) { return configDir(os.Getenv) }},
		openBrowser: openBrowser,
		hostname:    os.Hostname,
		sleep:       sleep,
		now:         time.Now,
	}
	err := run(ctx, a, os.Args[1:])
	stop()
	switch {
	case errors.Is(err, errUsage):
		os.Exit(2)
	case err != nil:
		fmt.Fprintln(os.Stderr, "wardlock:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		fmt.Fprint(a.stderr, usage())
		return errUsage
	}
	switch args[0] {
	case "--version":
		return runVersion(ctx, a, args[1:])
	case "help", "--help", "-h":
		fmt.Fprint(a.stdout, usage())
		return nil
	}
	for _, c := range commands {
		if c.name == args[0] {
			return c.run(ctx, a, args[1:])
		}
	}
	fmt.Fprintf(a.stderr, "wardlock: unknown command %q\n\n%s", args[0], usage())
	return errUsage
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// systemKeyring is the OS keychain: macOS Keychain, Windows Credential
// Manager or the Secret Service on Linux.
type systemKeyring struct{}

func (systemKeyring) Get(service, user string) (string, error) { return keyring.Get(service, user) }
func (systemKeyring) Set(service, user, secret string) error {
	return keyring.Set(service, user, secret)
}
func (systemKeyring) Delete(service, user string) error {
	err := keyring.Delete(service, user)
	if errors.Is(err, keyring.ErrNotFound) {
		return errSecretNotFound
	}
	return err
}
