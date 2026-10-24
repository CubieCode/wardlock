// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Command wardlock-demo fills a throwaway Wardlock with synthetic sites and
// 90 days of traffic, for looking at and screenshotting the dashboard. Run
// it through `pnpm demo` (scripts/demo.sh), which starts the server; never
// point it at a server or data directory you care about.
//
// It signs in as the demo owner with an emailed code read from the dev://
// mail sink, creates an API token, and seeds sites, groups, tags, pins,
// policies and share links through the wardlock CLI. History cannot go
// through the API, so it then opens the database: it backdates creation
// times (backdate.go) and appends the generated access log through
// internal/store, which also fills the analytics rollup.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"wardlock.nz/apiclient"
	"wardlock.nz/server/internal/store"
)

type config struct {
	server, email, mailDir, dataDir, cli, fixtures, baseDomain string
	seed                                                       uint64
}

func main() {
	var cfg config
	flag.StringVar(&cfg.server, "server", "", "the demo server's dashboard origin, such as http://wardlock-demo.localhost:8081")
	flag.StringVar(&cfg.email, "email", "owner@example.test", "the demo owner's address")
	flag.StringVar(&cfg.mailDir, "mail-dir", "", "the server's dev:// mail directory")
	flag.StringVar(&cfg.dataDir, "data-dir", "", "the server's WARDLOCK_DATA_DIR")
	flag.StringVar(&cfg.cli, "cli", ".local/bin/wardlock", "the wardlock CLI binary")
	flag.StringVar(&cfg.fixtures, "fixtures", "fixtures/sites", "the fixture sites directory")
	flag.StringVar(&cfg.baseDomain, "base-domain", "", "the server's WARDLOCK_BASE_DOMAIN")
	flag.Uint64Var(&cfg.seed, "seed", 1, "the traffic generator's seed")
	flag.Parse()
	if cfg.server == "" || cfg.mailDir == "" || cfg.dataDir == "" || cfg.baseDomain == "" {
		fmt.Fprintln(os.Stderr, "wardlock-demo: -server, -mail-dir, -data-dir and -base-domain are required; run pnpm demo")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	err := run(ctx, cfg)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "wardlock-demo:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cfg config) error {
	token, err := signIn(ctx, cfg.server, cfg.email, cfg.mailDir)
	if err != nil {
		return err
	}
	fixtures, err := filepath.Abs(cfg.fixtures)
	if err != nil {
		return err
	}
	c := cli{bin: cfg.cli, server: cfg.server, token: token, dir: cfg.dataDir}
	links, err := seedThroughCLI(ctx, c, fixtures)
	if err != nil {
		return err
	}
	fmt.Printf("Created %d sites, %d groups, %d tags and %d share links through the CLI.\n", len(sites), len(groups), len(tags), countLinks(links))
	return seedHistory(ctx, cfg, links, time.Now())
}

// signIn signs the owner in with an emailed code, as the dashboard does,
// and returns a new API token with both scopes. Neither the code nor the
// token is printed.
func signIn(ctx context.Context, server, email, mailDir string) (string, error) {
	httpc := &http.Client{Timeout: 30 * time.Second}
	api, err := apiclient.NewClientWithResponses(server, apiclient.WithHTTPClient(httpc))
	if err != nil {
		return "", err
	}
	origin := func(_ context.Context, req *http.Request) error {
		req.Header.Set("Origin", server)
		return nil
	}
	sent := time.Now().Add(-time.Second)
	res, err := api.SendEmailCodeWithResponse(ctx, apiclient.EmailCodeRequest{Email: openapi_types.Email(email)}, origin)
	if err != nil {
		return "", fmt.Errorf("send sign-in code: %w", err)
	}
	if res.StatusCode() != http.StatusAccepted {
		return "", fmt.Errorf("send sign-in code: HTTP %d", res.StatusCode())
	}
	code, err := waitForCode(ctx, mailDir, email, sent)
	if err != nil {
		return "", err
	}
	verified, err := api.VerifyEmailCodeWithResponse(ctx, apiclient.EmailCodeVerifyRequest{Email: openapi_types.Email(email), Code: code}, origin)
	if err != nil {
		return "", fmt.Errorf("verify sign-in code: %w", err)
	}
	if verified.StatusCode() != http.StatusOK {
		return "", fmt.Errorf("verify sign-in code: HTTP %d", verified.StatusCode())
	}
	cookies := verified.HTTPResponse.Cookies()
	session := func(_ context.Context, req *http.Request) error {
		for _, c := range cookies {
			req.AddCookie(c)
		}
		return nil
	}
	created, err := api.CreateApiTokenWithResponse(ctx, apiclient.CreateApiTokenRequest{
		Name:   "Demo seed",
		Scopes: apiclient.Scopes{apiclient.ScopeDeploy, apiclient.ScopeManage},
	}, origin, session)
	if err != nil {
		return "", fmt.Errorf("create API token: %w", err)
	}
	if created.JSON201 == nil {
		return "", fmt.Errorf("create API token: HTTP %d", created.StatusCode())
	}
	return created.JSON201.Token, nil
}

var codeLine = regexp.MustCompile(`(?m)^\s+(\d{6})\s*$`)

// waitForCode reads the sign-in code from the newest message to email the
// dev:// sink wrote after since.
func waitForCode(ctx context.Context, dir, email string, since time.Time) (string, error) {
	to := regexp.MustCompile(`(?mi)^To: (?:` + regexp.QuoteMeta(email) + `|.*<` + regexp.QuoteMeta(email) + `>)\r?$`)
	for range 100 {
		names, _ := filepath.Glob(filepath.Join(dir, "*.eml"))
		slices.Sort(names)
		for _, name := range slices.Backward(names) {
			info, err := os.Stat(name)
			if err != nil || info.ModTime().Before(since) {
				continue
			}
			raw, err := os.ReadFile(name) //nolint:gosec // G304: a file the demo server just wrote to its own mail sink
			if err != nil {
				continue
			}
			if m := codeLine.FindSubmatch(raw); to.Match(raw) && m != nil {
				return string(m[1]), nil
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return "", fmt.Errorf("no sign-in code for %s in %s", email, dir)
}

// cli runs the wardlock CLI with the demo token.
type cli struct{ bin, server, token, dir string }

func (c cli) run(ctx context.Context, stdin string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.bin, args...) //nolint:gosec // G204: the CLI that pnpm build just built, with arguments from plan.go
	cmd.Env = append(os.Environ(), "WARDLOCK_TOKEN="+c.token, "WARDLOCK_SERVER="+c.server)
	cmd.Dir = c.dir
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		// stdout can hold a share link's URL; only stderr is shown.
		return nil, fmt.Errorf("wardlock %s %s: %w: %s", args[0], args[1], err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// createdLink is a share link the CLI created, with its plan.
type createdLink struct {
	plan linkPlan
	link apiclient.ShareLink
}

func countLinks(links map[string][]createdLink) int {
	n := 0
	for _, l := range links {
		n += len(l)
	}
	return n
}

// seedThroughCLI creates the plan's tags, groups, sites, pins, policies and
// share links, returning each site's links.
func seedThroughCLI(ctx context.Context, c cli, fixtures string) (map[string][]createdLink, error) {
	for _, t := range tags {
		if _, err := c.run(ctx, "", "tag", "create", t.name, "--colour", strconv.Itoa(t.colour), "--json"); err != nil {
			return nil, err
		}
	}
	for _, g := range groups {
		args := []string{"group", "create", g.name, "--json"}
		if g.note != "" {
			args = append(args, "--note", g.note)
		}
		if _, err := c.run(ctx, "", args...); err != nil {
			return nil, err
		}
	}
	var pinned []string
	links := map[string][]createdLink{}
	for _, s := range sites {
		if err := deploy(ctx, c, fixtures, s, "First version"); err != nil {
			return nil, err
		}
		if s.redeployAgeDays > 0 {
			if err := deploy(ctx, c, fixtures, s, "Copy and layout updates"); err != nil {
				return nil, err
			}
		}
		if s.pinned {
			pinned = append(pinned, s.name)
		}
		for _, p := range s.policies {
			if _, err := c.run(ctx, p.password, policyArgs(s.name, p)...); err != nil {
				return nil, err
			}
		}
		for _, l := range s.links {
			args := []string{"share", "create", s.name, "--label", l.label, "--expires", l.expires, "--json"}
			if l.path != "" {
				args = append(args, "--path", l.path)
			}
			out, err := c.run(ctx, "", args...)
			if err != nil {
				return nil, err
			}
			var created apiclient.CreatedShareLink
			if err := json.Unmarshal(out, &created); err != nil {
				return nil, fmt.Errorf("share create %s: %w", s.name, err)
			}
			links[s.name] = append(links[s.name], createdLink{plan: l, link: created.ShareLink})
		}
	}
	if _, err := c.run(ctx, "", append([]string{"pin", "--json"}, pinned...)...); err != nil {
		return nil, err
	}
	return links, nil
}

func deploy(ctx context.Context, c cli, fixtures string, s sitePlan, message string) error {
	args := []string{"deploy", filepath.Join(fixtures, s.fixture), s.name, "--message", message, "--json"}
	if s.fixture == "spa" {
		args = append(args, "--spa")
	}
	if s.group != "" {
		args = append(args, "--group", s.group)
	}
	for _, t := range s.tags {
		args = append(args, "--tag", t)
	}
	_, err := c.run(ctx, "", args...)
	return err
}

func policyArgs(site string, p policyPlan) []string {
	args := []string{"policy", "set", site, "--json"}
	if p.path != "" {
		args = append(args, "--path", p.path)
	}
	if p.public {
		return append(args, "--public")
	}
	args = append(args, "--protected")
	if p.links {
		args = append(args, "--links")
	}
	if p.password != "" {
		args = append(args, "--password")
	}
	if p.allow != "" {
		args = append(args, "--email", "--allow", p.allow)
	}
	return args
}

// ways returns the path a site's protected policy covers ("" for a public
// site) and how visitors get past it, share links first.
func ways(policies []policyPlan) (string, []store.AccessVia) {
	for _, p := range policies {
		if p.public {
			continue
		}
		var w []store.AccessVia
		if p.links {
			w = append(w, store.ViaLink)
		}
		if p.password != "" {
			w = append(w, store.ViaPassword)
		}
		if p.allow != "" {
			w = append(w, store.ViaEmail)
		}
		prefix := p.path
		if prefix == "" {
			prefix = "/"
		}
		return prefix, w
	}
	return "", nil
}

// createdAt is when a site created ageDays ago looks created: that day, in
// New Zealand working hours.
func createdAt(now time.Time, ageDays float64, i int) time.Time {
	d := now.Add(-days(ageDays)).In(nz)
	at := time.Date(d.Year(), d.Month(), d.Day(), 9+i%8, (i*17)%60, 0, 0, nz)
	if !at.Before(now) {
		at = at.AddDate(0, 0, -1)
	}
	return at
}

// seedHistory backdates what the CLI created and appends the traffic.
func seedHistory(ctx context.Context, cfg config, links map[string][]createdLink, now time.Time) (err error) {
	path := filepath.Join(cfg.dataDir, "wardlock.db")
	db, err := store.Open(ctx, path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	bd, err := openBackdater(ctx, path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, bd.Close()) }()

	var traffic []siteTraffic
	linkIDs := map[string]string{} // public ID to ID
	for i, p := range sites {
		s, err := db.Sites().GetByName(ctx, p.name)
		if err != nil {
			return fmt.Errorf("site %s: %w", p.name, err)
		}
		if i == 0 {
			if err := bd.owner(ctx, s.ID, now.Add(-days(90))); err != nil {
				return err
			}
		}
		created := createdAt(now, p.ageDays, i)
		if err := bd.site(ctx, s.ID, created); err != nil {
			return err
		}
		deploys, err := db.Deploys().List(ctx, s.OwnerID, s.ID, store.DeployCursor{}, 10)
		if err != nil {
			return fmt.Errorf("deploys of %s: %w", p.name, err)
		}
		// Deploys list newest first; the oldest is the site's first.
		for j, d := range deploys {
			at := created.Add(3 * time.Minute)
			if j < len(deploys)-1 {
				at = createdAt(now, p.redeployAgeDays, i+j)
			}
			if err := bd.deploy(ctx, d.ID, at); err != nil {
				return err
			}
		}
		t := siteTraffic{
			id: s.ID, name: p.name, host: p.name + "." + cfg.baseDomain,
			created: created, plan: p.traffic, paths: fixturePaths[p.fixture],
		}
		t.protectedPrefix, t.ways = ways(p.policies)
		for _, l := range links[p.name] {
			at := fromMs(l.link.CreatedAt)
			var expires time.Time
			if l.link.ExpiresAt != nil {
				expires = fromMs(*l.link.ExpiresAt)
			}
			if back := backdatedLink(at, expires, now, l.plan.ageDays); back.Before(at) {
				at = back
				if floor := created.Add(time.Hour); at.Before(floor) {
					at = floor
				}
				if err := bd.link(ctx, l.link.Id, at); err != nil {
					return err
				}
			}
			linkIDs[l.link.PublicId] = l.link.Id
			t.links = append(t.links, linkVisit{publicID: l.link.PublicId, created: at, expires: expires})
		}
		traffic = append(traffic, t)
	}

	entries := generate(traffic, now.Add(-days(90)+time.Hour), now, cfg.seed)
	const batch = 2000
	for start := 0; start < len(entries); start += batch {
		if err := db.AccessLog().Append(ctx, entries[start:min(start+batch, len(entries))]); err != nil {
			return err
		}
	}
	// Each link's last access is its newest visit.
	last := map[string]time.Time{}
	for _, e := range entries {
		if e.LinkPublicID != "" {
			last[e.LinkPublicID] = e.OccurredAt
		}
	}
	for publicID, at := range last {
		if err := db.Links().Touch(ctx, linkIDs[publicID], at); err != nil {
			return fmt.Errorf("touch link %s: %w", publicID, err)
		}
	}
	fmt.Printf("Appended %d access-log entries over 90 days.\n", len(entries))
	printSummary(os.Stdout, entries)
	return nil
}

func printSummary(w io.Writer, entries []store.AccessEntry) {
	fmt.Fprintf(w, "  status: %s\n", summary(entries, func(e store.AccessEntry) string { return strconv.Itoa(e.Status) }))
	fmt.Fprintf(w, "  via: %s\n", summary(entries, func(e store.AccessEntry) string { return string(e.Via) }))
	fmt.Fprintf(w, "  browser: %s\n", summary(entries, func(e store.AccessEntry) string { return string(e.Browser) }))
	fmt.Fprintf(w, "  country: %s\n", summary(entries, func(e store.AccessEntry) string { return e.Country }))
}

func fromMs(ms int64) time.Time { return time.UnixMilli(ms) }
