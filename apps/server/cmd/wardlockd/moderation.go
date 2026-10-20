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
	"strings"
	"sync"
	"text/tabwriter"

	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/edge"
	"wardlock.nz/server/internal/mail"
	"wardlock.nz/server/internal/moderation"
	"wardlock.nz/server/internal/platform"
	"wardlock.nz/server/internal/store"
)

// newModeration is content screening (A2.15) over the database and blobs:
// with a Moderator when the operator turned it on, without one (operator
// decisions only) otherwise. screened, when not nil, is told each site
// whose files' screening state changed, for the edge.
func newModeration(cfg platform.Config, log *slog.Logger, db *store.DB, blobs moderation.BlobOpener, mailer mail.Mailer, q *moderation.Quarantine, screened deploys.Notifier) *moderation.Service {
	c := moderation.Config{
		Store:         db.Moderation(),
		Blobs:         blobs,
		Policy:        moderation.Policy{Quarantine: cfg.Moderation.Quarantine, Flag: cfg.Moderation.Flag},
		Quarantine:    q,
		Mailer:        mailer,
		AbuseEmail:    cfg.AbuseEmail,
		DashboardHost: cfg.DashboardHost,
		Log:           log,
	}
	if cfg.Moderation.Provider == platform.ModerationOpenAI {
		c.Moderator = moderation.NewOpenAI(moderation.OpenAIConfig{Key: cfg.Moderation.OpenAIKey})
	}
	if screened != nil {
		c.Invalidate = func(siteID string) { screened.Notify(context.Background(), siteID) }
	}
	return moderation.New(c)
}

// startModeration keeps the quarantine list current from the database
// (serve has loaded it once already) and, when screening is on, runs the
// screening worker, which queues a site's files as soon as its served
// state changes. The returned stop waits for both; call it before the
// database closes.
func startModeration(ctx context.Context, log *slog.Logger, db *store.DB, svc *moderation.Service, q *moderation.Quarantine, notifier deploys.Notifier) func() {
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Go(func() { q.Watch(ctx, db.Moderation().Quarantined, moderation.QuarantineInterval, log) })
	unsubscribe := func() {}
	if svc.Enabled() {
		unsubscribe = notifier.Subscribe(svc.Changed)
		wg.Go(func() { svc.Run(ctx) })
	}
	return func() {
		unsubscribe()
		cancel()
		wg.Wait()
	}
}

// invalidators is an edge.Invalidator over several: a site's served state
// changes (deploys) and its files' screening state (moderation).
type invalidators []edge.Invalidator

func (is invalidators) Subscribe(fn func(siteID string)) func() {
	unsubs := make([]func(), len(is))
	for i, inv := range is {
		unsubs[i] = inv.Subscribe(fn)
	}
	return func() {
		for _, u := range unsubs {
			u()
		}
	}
}

// collectModerationMetrics adds the screening backlog, the number of
// withheld hashes and the provider's failures to /metrics.
func collectModerationMetrics(m *platform.Metrics, svc *moderation.Service, q *moderation.Quarantine) {
	m.Collect(func(ctx context.Context, e *platform.Exposition) {
		if n, err := svc.Backlog(ctx); err == nil {
			e.Gauge("wardlock_moderation_backlog", "Content hashes waiting to be screened, retries included.", platform.Sample{Value: float64(n)})
		}
		e.Gauge("wardlock_moderation_quarantined", "Content hashes withheld by content screening.", platform.Sample{Value: float64(q.Len())})
		if !svc.Enabled() {
			return
		}
		paused := 0.0
		if svc.Paused() {
			paused = 1
		}
		e.Gauge("wardlock_moderation_paused", "1 while screening is paused after a provider failure or rate limit.", platform.Sample{Value: paused})
		e.Counter("wardlock_moderation_provider_errors_total",
			"Provider failures that paused screening: a refused key, a rate limit or a request it called malformed.",
			platform.Sample{Value: float64(svc.ProviderErrors())})
	})
}

const moderationUsage = `Usage:
  wardlockd admin moderation <command>

  list                     List flagged (including unscreenable) and quarantined files with
                           the sites serving them
  release SHA256           Serve the file again everywhere
  confirm SHA256           Keep a quarantined file withheld, and its stored copy held, indefinitely
  discard SHA256           Keep a quarantined file withheld; garbage collection removes its
                           stored copy once no deploy lists it
  rescan SHA256            Screen the file again; its verdict holds unless screening gives a new one
  disable --owner ADDRESS  Never send this owner's content for screening
  enable --owner ADDRESS   Screen this owner's content again (the default)

A running server picks up releases and new verdicts within a few seconds.
`

// adminModeration runs `wardlockd admin moderation`.
func adminModeration(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(stderr, moderationUsage)
		return errUsage
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "help", "--help", "-h":
		fmt.Fprint(stdout, moderationUsage)
		return nil
	case "list", "release", "confirm", "discard", "rescan":
		want := 1
		if cmd == "list" {
			want = 0
		}
		if len(rest) != want {
			fmt.Fprint(stderr, moderationUsage)
			return errUsage
		}
	case "disable", "enable":
		fs := flag.NewFlagSet("wardlockd admin moderation "+cmd, flag.ContinueOnError)
		fs.SetOutput(stderr)
		email := fs.String("owner", "", "the owner's email address")
		if err := fs.Parse(rest); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return errUsage
		}
		if *email == "" || fs.NArg() > 0 {
			fmt.Fprint(stderr, moderationUsage)
			return errUsage
		}
		rest = []string{*email}
	default:
		fmt.Fprint(stderr, moderationUsage)
		return errUsage
	}
	a, err := openAdmin(ctx, getenv, stderr)
	if err != nil {
		return err
	}
	defer a.close()
	switch cmd {
	case "list":
		return listModeration(ctx, a, stdout)
	case "disable", "enable":
		o, err := a.owners.Lookup(ctx, rest[0])
		if err != nil {
			return operatorErr(err, "owner", rest[0])
		}
		if err := a.moderation.SetExempt(ctx, o.ID, cmd == "disable"); err != nil {
			return err
		}
		if cmd == "disable" {
			fmt.Fprintf(stdout, "Screening is off for %s (owner %s): their content is never sent.\n"+
				"Files already screened keep their verdicts.\nUndo with: wardlockd admin moderation enable --owner %s\n", o.Email, o.ID, o.Email)
		} else {
			fmt.Fprintf(stdout, "Screening is on for %s (owner %s): their images and pages are screened from now on.\n", o.Email, o.ID)
		}
		return nil
	}
	sha := strings.ToLower(strings.TrimSpace(rest[0]))
	if !deploys.ValidHash(sha) {
		return fmt.Errorf("%q is not a SHA-256: give the 64 hex characters moderation list shows", rest[0])
	}
	decide := map[string]func(context.Context, string) error{
		"release": a.moderation.Release, "confirm": a.moderation.Confirm, "discard": a.moderation.Discard, "rescan": a.moderation.Rescan,
	}[cmd]
	switch err := decide(ctx, sha); {
	case errors.Is(err, moderation.ErrUnknown):
		return fmt.Errorf("no screening result for %s on this server", sha)
	case errors.Is(err, moderation.ErrWrongStatus) && cmd == "release":
		return fmt.Errorf("%s is neither flagged nor quarantined", sha)
	case errors.Is(err, moderation.ErrWrongStatus):
		return fmt.Errorf("%s is not quarantined", sha)
	case err != nil:
		return err
	}
	fmt.Fprintln(stdout, map[string]string{
		"release": "Released " + sha + ". Every site serving it serves it again.",
		"confirm": "Confirmed " + sha + ". It stays withheld, and its stored copy is kept even once no deploy lists it.",
		"discard": "Discarded " + sha + ". It stays withheld; garbage collection removes its stored copy once no deploy lists it.",
		"rescan":  "Queued " + sha + " for screening again. Its current verdict holds unless screening gives a new one.",
	}[cmd])
	return nil
}

func listModeration(ctx context.Context, a *adminEnv, out io.Writer) error {
	rs, err := a.moderation.Review(ctx)
	if err != nil {
		return err
	}
	if len(rs) == 0 {
		fmt.Fprintln(out, "No flagged or quarantined files.")
		return nil
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "SHA256\tSTATUS\tCATEGORIES\tCHECKED\tSITES")
	for _, r := range rs {
		cats := strings.Join(r.Categories, ",")
		if r.Reason != "" {
			// An unscreenable image: served, for the operator to look at.
			cats = strings.Trim(cats+","+r.Reason, ",")
		}
		if cats == "" {
			cats = "-"
		}
		var sites []string
		for _, u := range r.Uses {
			sites = append(sites, u.SiteName+"/"+u.Path)
		}
		where := strings.Join(sites, " ")
		if where == "" {
			where = "none"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.SHA256, reviewStatus(r.ModerationResult, len(r.Uses) > 0), cats, day(r.CheckedAt), where)
	}
	return tw.Flush()
}

// reviewStatus is the status with the operator's decision and, for a
// quarantined file no site serves, whether its stored copy is still held.
func reviewStatus(r store.ModerationResult, served bool) string {
	s := string(r.Status)
	switch {
	case r.Status != store.ModerationQuarantined:
	case !r.DiscardedAt.IsZero():
		s += " (discarded)"
	case !r.ConfirmedAt.IsZero():
		s += " (confirmed, held)"
	case !served:
		s += " (held)"
	}
	if !r.NextAttemptAt.IsZero() {
		s += ", rescan queued"
	}
	return s
}

// moderationAttr names the screening provider for the start-up log line,
// never the key.
func moderationAttr(cfg platform.Config) string {
	if cfg.Moderation.Provider == "" {
		return "off"
	}
	return cfg.Moderation.Provider
}

// screenedSites is the moderation store for the edge's site source when
// screening is on, nil when it is off.
func screenedSites(svc *moderation.Service, db *store.DB) store.Moderation {
	if !svc.Enabled() {
		return nil
	}
	return db.Moderation()
}
