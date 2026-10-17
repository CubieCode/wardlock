// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"strconv"

	"wardlock.nz/server/internal/owners"
	"wardlock.nz/server/internal/platform"
)

const adminUsage = `Usage:
  wardlockd admin create --email ADDRESS

Commands:
  create    Create an owner offline and print a one-time sign-in URL
`

// admin runs `wardlockd admin <command>` against the configured data
// directory. The server need not be running.
func admin(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] != "create" {
		if len(args) > 0 && (args[0] == "help" || args[0] == "--help" || args[0] == "-h") {
			fmt.Fprint(stdout, adminUsage)
			return nil
		}
		fmt.Fprint(stderr, adminUsage)
		return errUsage
	}
	fs := flag.NewFlagSet("wardlockd admin create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	email := fs.String("email", "", "the new owner's email address")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errUsage
	}
	if *email == "" || fs.NArg() > 0 {
		fmt.Fprint(stderr, adminUsage)
		return errUsage
	}

	cfg, err := platform.LoadConfig(getenv)
	if err != nil {
		return err
	}
	if err := cfg.RequireSecret(); err != nil {
		return err
	}
	log := platform.NewLogger(stderr, cfg)
	db, err := openStore(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	_, portStr, err := net.SplitHostPort(cfg.HTTPAddr)
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return fmt.Errorf("%w: WARDLOCK_HTTP_ADDR port %q", platform.ErrInvalidConfig, portStr)
	}
	svc, err := newOwners(cfg, log, db, publicOrigin(cfg.DashboardHost, port))
	if err != nil {
		return err
	}
	defer svc.Close()

	o, link, err := svc.CreateOwner(ctx, *email)
	switch {
	case errors.Is(err, owners.ErrInvalidEmail):
		return fmt.Errorf("%q is not a plain email address", *email)
	case err != nil:
		return err
	}
	fmt.Fprintf(stdout, "Created owner %s.\nSign in within %d minutes, once, at:\n\n  %s\n\nAfter that, sign in with a code sent to %s.\n",
		o.Email, int(owners.SignInURLValidity.Minutes()), link, o.Email)
	return nil
}
