// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"text/tabwriter"

	"wardlock.nz/apiclient"
)

// wardlock preview (C1.19): list, delete and promote a site's previews.
// Previews are made with wardlock deploy --preview NAME.

const previewUsage = "Usage: wardlock preview <list|delete|promote> DOMAIN [NAME] [flags]\n\n" +
	"  list DOMAIN             List the site's previews with their address, deploy and expiry\n" +
	"  delete DOMAIN NAME      Stop serving the preview NAME and delete it\n" +
	"  promote DOMAIN NAME     Put the preview's deploy live on the site, keeping the preview\n\n" +
	"Make or replace a preview with wardlock deploy DIR DOMAIN --preview NAME.\n" +
	"Deleting and promoting need a token with the deploy scope.\n"

// maxPreviewDays is the longest --expires the server accepts.
const maxPreviewDays = 90

// checkPreviewName refuses a preview name the server would: 1 to 20
// characters of a-z, 0-9 and '-', not starting or ending with '-' and
// without "--".
func checkPreviewName(name string) error {
	ok := name != "" && len(name) <= 20 && name[0] != '-' && name[len(name)-1] != '-' && !strings.Contains(name, "--")
	for i := 0; ok && i < len(name); i++ {
		c := name[i]
		ok = c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-'
	}
	if !ok {
		return usageErrorf("preview name %q must be 1 to 20 characters of a-z, 0-9 and '-', not starting or ending with '-' and without '--', such as pr-42", name)
	}
	return nil
}

// checkExpires refuses an --expires the server would: <days>d, 1 to 90,
// or never.
func checkExpires(v string) error {
	if v == "never" {
		return nil
	}
	n, ok := strings.CutSuffix(v, "d")
	days, err := strconv.Atoi(n)
	if !ok || err != nil || strconv.Itoa(days) != n || days < 1 || days > maxPreviewDays {
		return usageErrorf("--expires %q must be a number of days from 1d to %dd, such as 14d, or never", v, maxPreviewDays)
	}
	return nil
}

// previewURL is p's address, under prefix for a path preview.
func previewURL(p apiclient.Preview, prefix string) string {
	if prefix == "" {
		return p.Url
	}
	return strings.TrimSuffix(p.Url, "/") + prefix
}

// previewExpiry says when p expires.
func previewExpiry(p apiclient.Preview) string {
	if p.ExpiresAt == nil {
		return "never expires"
	}
	return "expires " + formatTime(*p.ExpiresAt)
}

func runPreview(ctx context.Context, a *app, args []string) error {
	global, rest, err := leadingGlobalFlags(args)
	if err != nil {
		return err
	}
	if len(rest) == 0 {
		fmt.Fprint(a.stderr, previewUsage)
		return errUsage
	}
	sub, args := rest[0], append(global, rest[1:]...)
	switch sub {
	case "list":
		return previewList(ctx, a, args)
	case "delete":
		return previewDelete(ctx, a, args)
	case "promote":
		return previewPromote(ctx, a, args)
	case "help", "--help", "-h":
		fmt.Fprint(a.stdout, previewUsage)
		return nil
	}
	fmt.Fprintf(a.stderr, "wardlock: unknown preview command %q\n\n%s", sub, previewUsage)
	return errUsage
}

// previewConn parses DOMAIN, and NAME when withName, checks them, runs
// check so every usage error exits 2 before any network round trip, then
// connects and finds the site.
func previewConn(ctx context.Context, a *app, fs *flag.FlagSet, g *globalFlags, args []string, withName bool, check func() error) (*conn, apiclient.Site, string, error) {
	var site apiclient.Site
	pos, err := parseArgs(fs, args)
	if err != nil {
		return nil, site, "", err
	}
	if want := map[bool]int{false: 1, true: 2}[withName]; len(pos) != want {
		fs.Usage()
		return nil, site, "", errUsage
	}
	t, err := parseTarget(pos[0])
	if err != nil {
		return nil, site, "", err
	}
	name := ""
	if withName {
		name = pos[1]
		if err := checkPreviewName(name); err != nil {
			return nil, site, "", err
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
	c, err := a.connect(g.server, cfgServer)
	if err != nil {
		return nil, site, "", err
	}
	if site, err = c.mustFindSite(ctx, t); err != nil {
		return nil, site, "", err
	}
	return c, site, name, nil
}

type previewListResult struct {
	Site     apiclient.Site      `json:"site"`
	Previews []apiclient.Preview `json:"previews"`
}

func previewList(ctx context.Context, a *app, args []string) error {
	fs := newFlagSet(a, "preview list", "Usage: wardlock preview list DOMAIN [flags]\n\n"+
		"Lists the site's previews by name: each one's address, the deploy it\n"+
		"serves and when that went live, and when the preview expires.")
	g := addGlobalFlags(fs)
	c, site, _, err := previewConn(ctx, a, fs, g, args, false, nil)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	out := previewListResult{Site: site, Previews: []apiclient.Preview{}}
	var cursor *string
	for {
		res, err := c.api.ListPreviewsWithResponse(ctx, site.Name, &apiclient.ListPreviewsParams{Limit: ptr(pageSize), Cursor: cursor}, c.auth)
		if err != nil {
			return c.unreachable(ctx, err)
		}
		if res.JSON200 == nil {
			return c.failed(res.HTTPResponse, res.Body)
		}
		out.Previews = append(out.Previews, res.JSON200.Items...)
		if cursor = res.JSON200.NextCursor; cursor == nil {
			break
		}
	}
	if g.json {
		return writeJSON(a.stdout, out)
	}
	if len(out.Previews) == 0 {
		fmt.Fprintf(a.stdout, "%s has no previews. Make one with wardlock deploy DIR %s --preview NAME.\n", site.Name, site.Name)
		return nil
	}
	tw := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tURL\tDEPLOY\tDEPLOYED\tEXPIRES\tMESSAGE")
	for _, p := range out.Previews {
		expires, message := "never", "-"
		if p.ExpiresAt != nil {
			expires = formatTime(*p.ExpiresAt)
		}
		if p.Deploy.Message != nil {
			message = *p.Deploy.Message
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", p.Name, p.Url, shortID(p.Deploy.Id), formatTime(p.DeployedAt), expires, message)
	}
	return tw.Flush()
}

type previewDeleteResult struct {
	Deleted bool           `json:"deleted"`
	Site    apiclient.Site `json:"site"`
	Preview string         `json:"preview"`
}

func previewDelete(ctx context.Context, a *app, args []string) error {
	fs := newFlagSet(a, "preview delete", "Usage: wardlock preview delete DOMAIN NAME [--yes] [flags]\n\n"+
		"Stops serving the preview NAME at once and deletes it. The live site is\n"+
		"not changed. Asks you to type the preview's name first; without a\n"+
		"terminal, or with --json, pass --yes instead.")
	g := addGlobalFlags(fs)
	yes := fs.Bool("yes", false, "delete without asking")
	c, site, name, err := previewConn(ctx, a, fs, g, args, true, func() error {
		if !*yes && (g.json || !a.canPrompt) {
			return usageErrorf("preview delete stops serving the preview for good: pass --yes to confirm when there is no terminal to ask in, or with --json")
		}
		return nil
	})
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if !*yes {
		ok, err := a.confirm(fmt.Sprintf("This stops serving the preview %s of %s at once. It cannot be undone.\nType the preview name to confirm: ", name, site.Name), name)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("not confirmed: nothing was deleted")
		}
	}
	res, err := c.api.DeletePreviewWithResponse(ctx, site.Name, name, c.auth)
	if err != nil {
		return c.unreachable(ctx, err)
	}
	if res.StatusCode() != http.StatusNoContent {
		return previewFailed(c, res.HTTPResponse, res.Body, site.Name, name)
	}
	if g.json {
		return writeJSON(a.stdout, previewDeleteResult{Deleted: true, Site: site, Preview: name})
	}
	fmt.Fprintf(a.stdout, "Deleted the preview %s of %s. It is no longer served.\n", name, site.Name)
	return nil
}

func previewPromote(ctx context.Context, a *app, args []string) error {
	fs := newFlagSet(a, "preview promote", "Usage: wardlock preview promote DOMAIN NAME [flags]\n\n"+
		"Puts the deploy the preview NAME serves live on the site, as an\n"+
		"ordinary deploy in its history (wardlock rollback returns to the one\n"+
		"before). The preview keeps serving it.")
	g := addGlobalFlags(fs)
	c, site, name, err := previewConn(ctx, a, fs, g, args, true, nil)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	res, err := c.api.PromotePreviewWithResponse(ctx, site.Name, name, c.auth)
	if err != nil {
		return c.unreachable(ctx, err)
	}
	if res.JSON200 == nil {
		if res.StatusCode() == http.StatusConflict && errorCode(res.Body) == "stale_base" {
			return fmt.Errorf("the preview %s replaces one path of %s, and the site has changed since it was deployed: deploy the preview again, then promote it", name, site.Name)
		}
		return previewFailed(c, res.HTTPResponse, res.Body, site.Name, name)
	}
	act := *res.JSON200
	if g.json {
		return writeJSON(a.stdout, act)
	}
	fmt.Fprintf(a.stdout, "Promoted the preview %s. Deploy %s is live:\n%s\n", name, shortID(act.Deploy.Id), act.Site.Url)
	return nil
}

// previewFailed reports a refused preview operation, naming the preview
// when it does not exist.
func previewFailed(c *conn, res *http.Response, body []byte, site, name string) error {
	if res != nil && res.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%s has no preview %s; wardlock preview list %s lists them", site, name, site)
	}
	return c.failed(res, body)
}
