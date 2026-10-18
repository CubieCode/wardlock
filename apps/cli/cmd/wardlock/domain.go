// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"
	"text/tabwriter"
	"time"

	"wardlock.nz/apiclient"
)

// wardlock domain (C1.9): attach, list, check and detach custom hostnames.

const domainUsage = "Usage: wardlock domain <add|list|verify|remove> DOMAIN [HOST] [flags]\n\n" +
	"  add DOMAIN HOST      Attach HOST to the site and print the DNS records to create\n" +
	"  list DOMAIN          List the site's custom hostnames with status and last check\n" +
	"  verify DOMAIN HOST   Check HOST's DNS now and print the result\n" +
	"  remove DOMAIN HOST   Detach HOST from the site\n\n" +
	"HOST is a hostname you control, such as docs.example.com. Adding,\n" +
	"checking and removing need a token with the manage scope.\n"

const (
	// waitTimeout is how long --wait keeps checking.
	waitTimeout = 10 * time.Minute
	// waitInterval separates --wait's checks; the server's Retry-After
	// wins when longer.
	waitInterval = 15 * time.Second
)

func runDomain(ctx context.Context, a *app, args []string) error {
	global, rest, err := leadingGlobalFlags(args)
	if err != nil {
		return err
	}
	if len(rest) == 0 {
		fmt.Fprint(a.stderr, domainUsage)
		return errUsage
	}
	sub, args := rest[0], append(global, rest[1:]...)
	switch sub {
	case "add":
		return domainAdd(ctx, a, args)
	case "list":
		return domainList(ctx, a, args)
	case "verify":
		return domainVerify(ctx, a, args)
	case "remove":
		return domainRemove(ctx, a, args)
	case "help", "--help", "-h":
		fmt.Fprint(a.stdout, domainUsage)
		return nil
	}
	fmt.Fprintf(a.stderr, "wardlock: unknown domain command %q\n\n%s", sub, domainUsage)
	return errUsage
}

// domainConn parses DOMAIN (and HOST, when wantHost), runs check on the
// flags so every usage error exits 2 before any network round trip, then
// connects and finds the site.
func domainConn(ctx context.Context, a *app, fs *flag.FlagSet, g *globalFlags, args []string, wantHost bool, check func() error) (c *conn, site apiclient.Site, host string, err error) {
	pos, err := parseArgs(fs, args)
	if err != nil {
		return nil, site, "", err
	}
	if n := map[bool]int{false: 1, true: 2}[wantHost]; len(pos) != n {
		fs.Usage()
		return nil, site, "", errUsage
	}
	t, err := parseTarget(pos[0])
	if err != nil {
		return nil, site, "", err
	}
	if wantHost {
		// The server normalises HOST (case, trailing dot, IDN) and says
		// what is wrong with it; only the obvious mistakes stop here.
		host = strings.TrimSpace(pos[1])
		if host == "" || strings.ContainsAny(host, "/@ ") {
			return nil, site, "", usageErrorf("%q is not a hostname, such as docs.example.com", pos[1])
		}
	}
	if check != nil {
		if err := check(); err != nil {
			return nil, site, "", err
		}
	}
	cfgServer, err := a.configServer()
	if err != nil {
		return nil, site, "", err
	}
	if c, err = a.connect(g.server, cfgServer); err != nil {
		return nil, site, "", err
	}
	site, err = c.mustFindSite(ctx, t)
	if err != nil {
		return nil, site, "", err
	}
	return c, site, host, nil
}

// domainResult is the --json output of add and verify.
type domainResult struct {
	Site   apiclient.Site   `json:"site"`
	Domain apiclient.Domain `json:"domain"`
}

func domainAdd(ctx context.Context, a *app, args []string) error {
	fs := newFlagSet(a, "domain add", "Usage: wardlock domain add DOMAIN HOST [--wait] [flags]\n\n"+
		"Attaches the hostname HOST to the site DOMAIN and prints the DNS records\n"+
		"to create. HOST is served once a check finds its record: Wardlock checks\n"+
		"straight away, then with growing gaps for 48 hours. With --wait, keeps\n"+
		"checking until HOST is active or 10 minutes pass. Needs a token with the\n"+
		"manage scope.")
	g := addGlobalFlags(fs)
	wait := fs.Bool("wait", false, "check until the hostname is active, for up to 10 minutes")
	c, site, host, err := domainConn(ctx, a, fs, g, args, true, nil)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	res, err := c.api.AddDomainWithResponse(ctx, site.Name, apiclient.AddDomainJSONRequestBody{Hostname: host}, c.auth)
	if err != nil {
		return c.unreachable(ctx, err)
	}
	if res.JSON201 == nil {
		return c.failed(res.HTTPResponse, res.Body)
	}
	d := *res.JSON201
	if !g.json {
		printRecords(a.stdout, site, d)
	}
	if *wait && d.Status != apiclient.DomainStatusActive {
		if d, err = waitActive(ctx, a, c, site, d); err != nil {
			return err
		}
	}
	if g.json {
		return writeJSON(a.stdout, domainResult{Site: site, Domain: d})
	}
	printStatus(a.stdout, site, d)
	return nil
}

// printRecords shows the records an owner creates for d.
func printRecords(w io.Writer, site apiclient.Site, d apiclient.Domain) {
	fmt.Fprintf(w, "Added %s to %s. Create this DNS record:\n\n", d.Hostname, site.Name)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "  %s\t%s\t%s\n", d.RoutingRecord.Type, d.RoutingRecord.Name, d.RoutingRecord.Value)
	_ = tw.Flush()
	fmt.Fprint(w, "\nFor an apex domain (which cannot have a CNAME), or a record behind a\n"+
		"CDN proxy such as Cloudflare's, point A/AAAA records (or ALIAS) at the\n"+
		"server instead and add this TXT record:\n\n")
	tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "  %s\t%s\t%s\n", d.VerificationRecord.Type, d.VerificationRecord.Name, d.VerificationRecord.Value)
	_ = tw.Flush()
	fmt.Fprintln(w)
}

// printStatus says where d stands and what happens next.
func printStatus(w io.Writer, site apiclient.Site, d apiclient.Domain) {
	switch d.Status {
	case apiclient.DomainStatusActive:
		fmt.Fprintf(w, "%s is active: it serves %s.\n", d.Hostname, site.Name)
	case apiclient.DomainStatusLapsed:
		fmt.Fprintf(w, "%s has lapsed: its record has been missing for 72 hours, so it is not served. %s\n", d.Hostname, failure(d))
	default:
		fmt.Fprintf(w, "%s is pending: %s\n", d.Hostname, failure(d))
	}
	switch {
	case d.Status == apiclient.DomainStatusActive:
	case d.NextCheckAt != nil:
		fmt.Fprintf(w, "Wardlock checks again at %s, or run wardlock domain verify %s %s.\n", formatTime(*d.NextCheckAt), site.Name, d.Hostname)
	default:
		fmt.Fprintf(w, "Run wardlock domain verify %s %s once the record is in place.\n", site.Name, d.Hostname)
	}
}

// failure is d's last failure in words.
func failure(d apiclient.Domain) string {
	if d.FailureDetail != nil {
		return *d.FailureDetail
	}
	if d.FailureReason != nil {
		return strings.ReplaceAll(string(*d.FailureReason), "_", " ") + "."
	}
	return "not checked yet."
}

// waitActive checks d until it is active or waitTimeout passes. Progress
// goes to stderr.
func waitActive(ctx context.Context, a *app, c *conn, site apiclient.Site, d apiclient.Domain) (apiclient.Domain, error) {
	deadline := a.now().Add(waitTimeout)
	pause := waitInterval
	for {
		if !a.now().Add(pause).Before(deadline) {
			return d, fmt.Errorf("%s is still %s after 10 minutes: %s It stays attached; wardlock domain verify %s %s checks again",
				d.Hostname, d.Status, failure(d), site.Name, d.Hostname)
		}
		fmt.Fprintf(a.stderr, "Waiting for %s: %s Checking again in %s.\n", d.Hostname, failure(d), pause.Round(time.Second))
		if err := a.sleep(ctx, pause); err != nil {
			return d, errors.New("cancelled")
		}
		res, err := c.api.VerifyDomainWithResponse(ctx, site.Name, d.Hostname, c.auth)
		if err != nil {
			return d, c.unreachable(ctx, err)
		}
		pause = waitInterval
		switch {
		case res.JSON200 != nil:
			d = *res.JSON200
			if d.Status == apiclient.DomainStatusActive {
				return d, nil
			}
		case res.StatusCode() == http.StatusTooManyRequests:
			pause = max(waitInterval, retryAfter(res.HTTPResponse))
		default:
			return d, domainFailed(c, res.HTTPResponse, res.Body, site.Name, d.Hostname)
		}
	}
}

type domainListResult struct {
	Site    apiclient.Site     `json:"site"`
	Domains []apiclient.Domain `json:"domains"`
}

func domainList(ctx context.Context, a *app, args []string) error {
	fs := newFlagSet(a, "domain list", "Usage: wardlock domain list DOMAIN [flags]\n\n"+
		"Lists the site's custom hostnames with their status, last check and\n"+
		"why the last check failed.")
	g := addGlobalFlags(fs)
	c, site, _, err := domainConn(ctx, a, fs, g, args, false, nil)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	out := domainListResult{Site: site, Domains: []apiclient.Domain{}}
	var cursor *string
	for {
		res, err := c.api.ListDomainsWithResponse(ctx, site.Name, &apiclient.ListDomainsParams{Limit: ptr(pageSize), Cursor: cursor}, c.auth)
		if err != nil {
			return c.unreachable(ctx, err)
		}
		if res.JSON200 == nil {
			return c.failed(res.HTTPResponse, res.Body)
		}
		out.Domains = append(out.Domains, res.JSON200.Items...)
		if cursor = res.JSON200.NextCursor; cursor == nil {
			break
		}
	}
	if g.json {
		return writeJSON(a.stdout, out)
	}
	if len(out.Domains) == 0 {
		fmt.Fprintf(a.stdout, "%s has no custom domains. Add one with wardlock domain add %s HOST.\n", site.Name, site.Name)
		return nil
	}
	tw := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "HOSTNAME\tSTATUS\tLAST CHECK\tPROBLEM")
	for _, d := range out.Domains {
		last, problem := "never", "-"
		if d.LastCheckedAt != nil {
			last = formatTime(*d.LastCheckedAt)
		}
		if d.Status != apiclient.DomainStatusActive || d.FailureReason != nil {
			problem = failure(d)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", d.Hostname, d.Status, last, problem)
	}
	return tw.Flush()
}

func domainVerify(ctx context.Context, a *app, args []string) error {
	fs := newFlagSet(a, "domain verify", "Usage: wardlock domain verify DOMAIN HOST [flags]\n\n"+
		"Checks HOST's DNS now and prints the result. A pending hostname becomes\n"+
		"active as soon as a check finds its record. Exits 0 whenever the check\n"+
		"ran, whatever it found. Needs a token with the manage scope.")
	g := addGlobalFlags(fs)
	c, site, host, err := domainConn(ctx, a, fs, g, args, true, nil)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	res, err := c.api.VerifyDomainWithResponse(ctx, site.Name, host, c.auth)
	if err != nil {
		return c.unreachable(ctx, err)
	}
	if res.StatusCode() == http.StatusTooManyRequests {
		return fmt.Errorf("%s was checked moments ago: try again in %s", host, max(time.Second, retryAfter(res.HTTPResponse)))
	}
	if res.JSON200 == nil {
		return domainFailed(c, res.HTTPResponse, res.Body, site.Name, host)
	}
	if g.json {
		return writeJSON(a.stdout, domainResult{Site: site, Domain: *res.JSON200})
	}
	printStatus(a.stdout, site, *res.JSON200)
	return nil
}

type domainRemoveResult struct {
	Removed  bool           `json:"removed"`
	Site     apiclient.Site `json:"site"`
	Hostname string         `json:"hostname"`
}

func domainRemove(ctx context.Context, a *app, args []string) error {
	fs := newFlagSet(a, "domain remove", "Usage: wardlock domain remove DOMAIN HOST [--yes] [flags]\n\n"+
		"Detaches HOST from the site: it stops being served at once and its\n"+
		"certificate is deleted. Asks you to type HOST first; without a\n"+
		"terminal, or with --json, pass --yes instead. Delete the DNS record\n"+
		"too. Needs a token with the manage scope.")
	g := addGlobalFlags(fs)
	yes := fs.Bool("yes", false, "remove without asking")
	check := func() error {
		if !*yes && (g.json || !a.canPrompt) {
			return usageErrorf("pass --yes to remove a custom domain when there is no terminal to ask in, or with --json")
		}
		return nil
	}
	c, site, host, err := domainConn(ctx, a, fs, g, args, true, check)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	// Look it up first: the confirmation names it as stored, and a typo
	// fails before any question.
	got, err := c.api.GetDomainWithResponse(ctx, site.Name, host, c.auth)
	if err != nil {
		return c.unreachable(ctx, err)
	}
	if got.JSON200 == nil {
		return domainFailed(c, got.HTTPResponse, got.Body, site.Name, host)
	}
	host = got.JSON200.Hostname
	if !*yes {
		ok, err := a.confirm(fmt.Sprintf("This stops serving %s at once.\nType the hostname to confirm: ", host), host)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("not confirmed: nothing was removed")
		}
	}
	res, err := c.api.RemoveDomainWithResponse(ctx, site.Name, host, c.auth)
	if err != nil {
		return c.unreachable(ctx, err)
	}
	if res.StatusCode() != http.StatusNoContent {
		return domainFailed(c, res.HTTPResponse, res.Body, site.Name, host)
	}
	reminder := fmt.Sprintf("Delete the DNS records for %s too: a CNAME left pointing at %s can be claimed by whoever later holds the site name %s.", host, site.Hostname, site.Name)
	if g.json {
		fmt.Fprintln(a.stderr, reminder)
		return writeJSON(a.stdout, domainRemoveResult{Removed: true, Site: site, Hostname: host})
	}
	fmt.Fprintf(a.stdout, "Removed %s from %s. It is no longer served.\n%s\n", host, site.Name, reminder)
	return nil
}

// domainFailed is conn.failed with a missing domain explained.
func domainFailed(c *conn, res *http.Response, body []byte, site, host string) error {
	if res != nil && res.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%s has no custom domain %s: wardlock domain list %s shows them", site, host, site)
	}
	return c.failed(res, body)
}
