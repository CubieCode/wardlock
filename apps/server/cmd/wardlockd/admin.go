// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"text/tabwriter"
	"time"

	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/mail"
	"wardlock.nz/server/internal/owners"
	"wardlock.nz/server/internal/platform"
	"wardlock.nz/server/internal/sites"
	"wardlock.nz/server/internal/store"
)

const adminUsage = `Usage:
  wardlockd admin <command> [arguments]

Owners:
  create --email ADDRESS   Create an owner offline and print a one-time sign-in URL
  list-owners              List every owner and whether they are suspended
  suspend-owner ADDRESS    Sign the owner out everywhere, refuse their sign-ins and
                           API tokens, and make their sites unavailable
  reinstate-owner ADDRESS  Lift a suspension: API tokens and sites work again

Sites (by name or any hostname they answer on):
  list-sites               List every site, its owner and whether it is serving
  takedown-site SITE       Serve the unavailable page on every host, keeping all
                           deploys and files as evidence
  restore-site SITE        Serve a taken-down site again
  teardown-site SITE       Delete a taken-down site; garbage collection then
                           removes files nothing else uses

A running server notices suspensions and takedowns within a few seconds.
`

// admin runs `wardlockd admin <command>` against the configured data
// directory. The server need not be running; when it is, it picks up
// suspensions and takedowns by polling (see watchUnavailable).
func admin(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(stderr, adminUsage)
		return errUsage
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "help", "--help", "-h":
		fmt.Fprint(stdout, adminUsage)
		return nil
	case "create":
		return adminCreate(ctx, rest, getenv, stdout, stderr)
	}
	run, ok := adminCommands[cmd]
	if !ok || len(rest) != run.args {
		fmt.Fprint(stderr, adminUsage)
		return errUsage
	}
	a, err := openAdmin(ctx, getenv, stderr)
	if err != nil {
		return err
	}
	defer a.close()
	return run.fn(ctx, a, rest, stdout)
}

// adminCommand is an operator command taking exactly args positional
// arguments.
type adminCommand struct {
	args int
	fn   func(ctx context.Context, a *adminEnv, args []string, out io.Writer) error
}

var adminCommands = map[string]adminCommand{
	"list-owners":     {0, listOwners},
	"suspend-owner":   {1, suspendOwner},
	"reinstate-owner": {1, reinstateOwner},
	"list-sites":      {0, listSites},
	"takedown-site":   {1, takedownSite},
	"restore-site":    {1, restoreSite},
	"teardown-site":   {1, teardownSite},
}

// adminEnv is what the operator commands work with: the database and the
// owner and site services over it, logging operator actions with IDs only.
type adminEnv struct {
	db     *store.DB
	owners *owners.Service
	sites  *sites.Service
}

func (a *adminEnv) close() {
	a.owners.Close()
	_ = a.db.Close()
}

func openAdmin(ctx context.Context, getenv func(string) string, stderr io.Writer) (*adminEnv, error) {
	cfg, err := platform.LoadConfig(getenv)
	if err != nil {
		return nil, err
	}
	if err := cfg.RequireSecret(); err != nil {
		return nil, err
	}
	log := platform.NewLogger(stderr, cfg)
	db, err := openStore(ctx, cfg, log)
	if err != nil {
		return nil, err
	}
	svc, err := adminOwners(cfg, log, db)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	// No certificate manager: a running server in auto TLS mode keeps a
	// torn-down site's certificates until they expire, and renews none for
	// hostnames no longer in the database.
	sitesSvc := sites.NewService(sites.Config{
		Sites: db.Sites(), Domains: db.Domains(), Notifier: deploys.NewLocalNotifier(),
		BaseDomain: cfg.BaseDomain, DashboardHost: cfg.DashboardHost, Now: time.Now, Log: log,
	})
	return &adminEnv{db: db, owners: svc, sites: sitesSvc}, nil
}

// adminOwners is the owners service as `wardlockd admin` uses it, with
// sign-in URLs on the public dashboard origin.
func adminOwners(cfg platform.Config, log *slog.Logger, db *store.DB) (*owners.Service, error) {
	_, portStr, err := net.SplitHostPort(cfg.HTTPAddr)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("%w: WARDLOCK_HTTP_ADDR port %q", platform.ErrInvalidConfig, portStr)
	}
	mailer, err := mail.New(mail.Config{SMTP: cfg.SMTP, Logger: log})
	if err != nil {
		return nil, err
	}
	return newOwners(cfg, log, db, publicOrigin(cfg.DashboardHost, port), mailer)
}

func adminCreate(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("wardlockd admin create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	email := fs.String("email", "", "the new owner's email address")
	if err := fs.Parse(args); err != nil {
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
	svc, err := adminOwners(cfg, log, db)
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

// operatorErr turns a lookup miss into a message naming what was asked
// for. The operator typed it, so echoing it is safe.
func operatorErr(err error, what, ref string) error {
	switch {
	case errors.Is(err, owners.ErrNotFound), errors.Is(err, sites.ErrNotFound):
		return fmt.Errorf("no %s %q on this server", what, ref)
	case errors.Is(err, owners.ErrInvalidEmail):
		return fmt.Errorf("%q is not a plain email address", ref)
	}
	return err
}

func listOwners(ctx context.Context, a *adminEnv, _ []string, out io.Writer) error {
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "EMAIL\tSTATUS\tCREATED\tID")
	err := a.owners.ListAll(ctx, func(o store.Owner) error {
		_, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", o.Email, ownerStatus(o), day(o.CreatedAt), o.ID)
		return err
	})
	if err != nil {
		return err
	}
	return tw.Flush()
}

func ownerStatus(o store.Owner) string {
	switch {
	case !o.SuspendedAt.IsZero():
		return "suspended " + day(o.SuspendedAt)
	case o.AcceptedAt.IsZero():
		return "invited"
	}
	return "active"
}

func suspendOwner(ctx context.Context, a *adminEnv, args []string, out io.Writer) error {
	o, err := a.owners.Suspend(ctx, args[0])
	if err != nil {
		return operatorErr(err, "owner", args[0])
	}
	fmt.Fprintf(out, "Suspended %s (owner %s) as of %s.\n"+
		"Their dashboard sessions are signed out, their API tokens and sign-ins are refused,\n"+
		"and their sites answer the unavailable page. Their files are kept.\n"+
		"Undo with: wardlockd admin reinstate-owner %s\n", o.Email, o.ID, stamp(o.SuspendedAt), o.Email)
	return nil
}

func reinstateOwner(ctx context.Context, a *adminEnv, args []string, out io.Writer) error {
	o, err := a.owners.Reinstate(ctx, args[0])
	if err != nil {
		return operatorErr(err, "owner", args[0])
	}
	fmt.Fprintf(out, "Reinstated %s (owner %s).\n"+
		"Their API tokens work again and their sites serve unless taken down; they sign in afresh.\n", o.Email, o.ID)
	return nil
}

func listSites(ctx context.Context, a *adminEnv, _ []string, out io.Writer) error {
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tOWNER\tSTATUS\tID")
	err := a.sites.ListAll(ctx, func(s store.OperatorSite) error {
		_, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", s.Name, s.OwnerEmail, siteStatus(s), s.ID)
		return err
	})
	if err != nil {
		return err
	}
	return tw.Flush()
}

func siteStatus(s store.OperatorSite) string {
	switch {
	case !s.TakenDownAt.IsZero():
		return "taken down " + day(s.TakenDownAt)
	case !s.OwnerSuspendedAt.IsZero():
		return "owner suspended"
	case s.ActiveDeployID == "":
		return "nothing published"
	}
	return "serving"
}

func takedownSite(ctx context.Context, a *adminEnv, args []string, out io.Writer) error {
	s, err := a.sites.TakeDown(ctx, args[0])
	if err != nil {
		return operatorErr(err, "site", args[0])
	}
	fmt.Fprintf(out, "Took down %s (site %s, owner %s) as of %s.\n"+
		"Every host it answers on serves the unavailable page. Its deploys and files are kept\n"+
		"and its owner cannot change or delete them.\n"+
		"Undo with: wardlockd admin restore-site %s\n", s.Name, s.ID, s.OwnerID, stamp(s.TakenDownAt), s.Name)
	return nil
}

func restoreSite(ctx context.Context, a *adminEnv, args []string, out io.Writer) error {
	s, err := a.sites.Restore(ctx, args[0])
	if err != nil {
		return operatorErr(err, "site", args[0])
	}
	fmt.Fprintf(out, "Restored %s (site %s). It serves its active deploy again.\n", s.Name, s.ID)
	return nil
}

func teardownSite(ctx context.Context, a *adminEnv, args []string, out io.Writer) error {
	s, err := a.sites.TearDown(ctx, args[0])
	if errors.Is(err, sites.ErrNotTakenDown) {
		return fmt.Errorf("site %q is not taken down; run wardlockd admin takedown-site %s first", args[0], args[0])
	}
	if err != nil {
		return operatorErr(err, "site", args[0])
	}
	fmt.Fprintf(out, "Tore down %s (site %s). Its name is free; garbage collection removes files nothing else uses.\n", s.Name, s.ID)
	return nil
}

func day(t time.Time) string { return t.UTC().Format("2006-01-02") }

func stamp(t time.Time) string { return t.UTC().Format("2006-01-02 15:04 UTC") }
