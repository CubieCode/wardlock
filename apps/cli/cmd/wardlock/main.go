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
	"golang.org/x/term"
)

// version is set at build time with -ldflags "-X main.version=…".
var version = "dev"

// command is one subcommand. Add a command here only once it works end to
// end; help lists exactly these, in this order. Each command lives in its
// own file and needs only its line here.
type command struct {
	name, summary string
	run           func(ctx context.Context, a *app, args []string) error
}

var commands = []command{
	{"deploy", "Publish a directory to a site", runDeploy},
	{"list", "List your sites, or one site's deploys", runList},
	{"rollback", "Reactivate an earlier deploy of a site", runRollback},
	{"pull", "Download the files of a site's deploy", runPull},
	{"teardown", "Delete a site with all its deploys", runTeardown},
	{"policy", "Set who may open a site or a path", runPolicy},
	{"share", "Create, list, revoke and rotate share links", runShare},
	{"domain", "Add, list, check and remove custom domains", runDomain},
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
	b.WriteString("\nRun \"wardlock <command> --help\" for a command's flags. Every command\n" +
		"takes --json for machine-readable output, and never prompts with it or\n" +
		"without a terminal. WARDLOCK_TOKEN, when set, is used instead of the\n" +
		"stored token. Exit status: 0 success, 1 failure, 2 usage error.\n")
	return b.String()
}

// errUsage is a usage error: exit status 2. Commands that print their own
// message return it bare; usageErrorf carries a message for main to print.
var errUsage = errors.New("usage")

type usageError struct{ msg string }

func (e *usageError) Error() string        { return e.msg }
func (e *usageError) Is(target error) bool { return target == errUsage }

func usageErrorf(format string, args ...any) error {
	return &usageError{fmt.Sprintf(format, args...)}
}

// app is everything a command touches outside its arguments, so tests can
// replace it.
type app struct {
	getenv         func(string) string
	getwd          func() (string, error)
	stdin          io.Reader
	stdout, stderr io.Writer
	// interactive is true when stdout is a terminal: only then may the
	// CLI open a browser.
	interactive bool
	// canPrompt is true when stdin and stderr are terminals: only then
	// may a command ask a question (and never with --json).
	canPrompt bool
	// stdinTerminal is true when stdin is a terminal: a secret read from it
	// must then be read without echo, even with --json.
	stdinTerminal bool
	// liveProgress is true when stderr is a terminal, so progress can
	// redraw one line.
	liveProgress bool
	http         *http.Client
	creds        credentialStore
	openBrowser  func(url string) error
	hostname     func() (string, error)
	// readPassword reads a line from the terminal without echoing it;
	// call it only when stdinTerminal.
	readPassword func() (string, error)
	// sleep waits for d or until ctx ends.
	sleep func(ctx context.Context, d time.Duration) error
	now   func() time.Time
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	a := &app{
		getenv:        os.Getenv,
		getwd:         os.Getwd,
		stdin:         os.Stdin,
		stdout:        os.Stdout,
		stderr:        os.Stderr,
		interactive:   isTerminal(os.Stdout),
		canPrompt:     isTerminal(os.Stdin) && isTerminal(os.Stderr),
		stdinTerminal: isTerminal(os.Stdin),
		liveProgress:  isTerminal(os.Stderr),
		http:          &http.Client{Timeout: 30 * time.Second},
		creds:         credentialStore{keyring: systemKeyring{}, dir: func() (string, error) { return configDir(os.Getenv) }},
		openBrowser:   openBrowser,
		hostname:      os.Hostname,
		readPassword:  readTerminalPassword,
		sleep:         sleep,
		now:           time.Now,
	}
	err := run(ctx, a, os.Args[1:])
	stop()
	os.Exit(exitCode(os.Stderr, err))
}

// exitCode prints err and returns the exit status for it (docs/SPEC.md
// "CLI"): 0 success, 1 failure, 2 usage error.
func exitCode(stderr io.Writer, err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, errUsage):
		var ue *usageError
		if errors.As(err, &ue) {
			fmt.Fprintln(stderr, "wardlock:", err)
		}
		return 2
	default:
		fmt.Fprintln(stderr, "wardlock:", err)
		return 1
	}
}

func run(ctx context.Context, a *app, args []string) error {
	global, args, err := leadingGlobalFlags(args)
	if err != nil {
		return err
	}
	if len(args) == 0 {
		fmt.Fprint(a.stderr, usage())
		return errUsage
	}
	switch args[0] {
	case "--version":
		return runVersion(ctx, a, append(global, args[1:]...))
	case "help", "--help", "-h":
		fmt.Fprint(a.stdout, usage())
		return nil
	}
	for _, c := range commands {
		if c.name == args[0] {
			return c.run(ctx, a, append(global, args[1:]...))
		}
	}
	fmt.Fprintf(a.stderr, "wardlock: unknown command %q\n\n%s", args[0], usage())
	return errUsage
}

// leadingGlobalFlags splits the global flags --json and --server off the
// front of args, so "wardlock --json list" works; they are handed to the
// command like its own flags.
func leadingGlobalFlags(args []string) (global, rest []string, err error) {
	for len(args) > 0 {
		name, _, hasValue := strings.Cut(strings.TrimLeft(args[0], "-"), "=")
		if !strings.HasPrefix(args[0], "-") {
			break
		}
		switch {
		case name == "json":
			global, args = append(global, args[0]), args[1:]
		case name == "server" && hasValue:
			global, args = append(global, args[0]), args[1:]
		case name == "server" && len(args) > 1:
			global, args = append(global, args[0], args[1]), args[2:]
		case name == "server":
			return nil, nil, usageErrorf("--server needs a value, such as --server https://wardlock.example.com")
		default:
			return global, args, nil
		}
	}
	return global, args, nil
}

// isTerminal reports whether f is a terminal. /dev/null and other
// character devices are not.
func isTerminal(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}

func readTerminalPassword() (string, error) {
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	return string(b), err
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
