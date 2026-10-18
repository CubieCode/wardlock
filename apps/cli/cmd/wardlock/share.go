// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"wardlock.nz/apiclient"
)

// wardlock share (A2.2): create, list, revoke and rotate share links. A
// link's token appears only in the output of create and rotate, once; it
// is never echoed anywhere else, errors included.

const shareUsage = "Usage: wardlock share <create|list|revoke|rotate> DOMAIN [LINK_ID] [flags]\n\n" +
	"  create DOMAIN   Create a share link and print its URL once\n" +
	"  list DOMAIN     List the site's links: label, ID, scope, expiry, uses, last access\n" +
	"  revoke DOMAIN LINK_ID   Stop a link working, now\n" +
	"  rotate DOMAIN LINK_ID   Give a link a new URL, printed once; the old one stops working\n\n" +
	"LINK_ID is the 10-character ID wardlock share list shows. Revoking and\n" +
	"rotating need a token with the manage scope.\n"

var linkIDPattern = regexp.MustCompile(`^[a-z2-7]{10}$`)

func runShare(ctx context.Context, a *app, args []string) error {
	// The subcommand is the first argument that is not a global flag.
	global, rest, err := leadingGlobalFlags(args)
	if err != nil {
		return err
	}
	if len(rest) == 0 {
		fmt.Fprint(a.stderr, shareUsage)
		return errUsage
	}
	sub, args := rest[0], append(global, rest[1:]...)
	switch sub {
	case "create":
		return shareCreate(ctx, a, args)
	case "list":
		return shareList(ctx, a, args)
	case "revoke":
		return shareRevoke(ctx, a, args)
	case "rotate":
		return shareRotate(ctx, a, args)
	case "help", "--help", "-h":
		fmt.Fprint(a.stdout, shareUsage)
		return nil
	}
	fmt.Fprintf(a.stderr, "wardlock: unknown share command %q\n\n%s", sub, shareUsage)
	return errUsage
}

// shareConn parses the DOMAIN (and LINK_ID, when wantID) arguments and
// connects, returning the site's name.
func shareConn(ctx context.Context, a *app, fs *flag.FlagSet, g *globalFlags, args []string, wantID bool) (c *conn, site, linkID string, err error) {
	pos, err := parseArgs(fs, args)
	if err != nil {
		return nil, "", "", err
	}
	if n := map[bool]int{false: 1, true: 2}[wantID]; len(pos) != n {
		fs.Usage()
		return nil, "", "", errUsage
	}
	t, err := parseTarget(pos[0])
	if err != nil {
		return nil, "", "", err
	}
	if wantID {
		linkID = strings.ToLower(pos[1])
		if !linkIDPattern.MatchString(linkID) {
			return nil, "", "", usageErrorf("%q is not a share link ID: use the 10-character ID wardlock share list %s shows", pos[1], t)
		}
	}
	cfgServer, err := a.configServer()
	if err != nil {
		return nil, "", "", err
	}
	if c, err = a.connect(g.server, cfgServer); err != nil {
		return nil, "", "", err
	}
	s, err := c.mustFindSite(ctx, t)
	if err != nil {
		return nil, "", "", err
	}
	return c, s.Name, linkID, nil
}

// parseExpiry turns --expires into a time: never, or a whole number of
// minutes, hours, days or weeks (30m, 1h, 7d, 2w).
func parseExpiry(raw string, now time.Time) (time.Time, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "never" {
		return time.Time{}, nil
	}
	units := map[byte]time.Duration{'m': time.Minute, 'h': time.Hour, 'd': 24 * time.Hour, 'w': 7 * 24 * time.Hour}
	if len(s) >= 2 {
		if unit, ok := units[s[len(s)-1]]; ok {
			n, err := strconv.Atoi(s[:len(s)-1])
			if err == nil && n > 0 && n <= 100_000 {
				return now.Add(time.Duration(n) * unit), nil
			}
		}
	}
	return time.Time{}, usageErrorf("--expires %q: use never, or a number of minutes, hours, days or weeks such as 30m, 1h, 7d or 2w", raw)
}

func shareCreate(ctx context.Context, a *app, args []string) error {
	fs := newFlagSet(a, "share create", "Usage: wardlock share create DOMAIN [--path P] [--label L] [--expires 7d|never] [--uses N] [flags]\n\n"+
		"Creates a share link and prints its URL. The URL is shown only this\n"+
		"once: anyone holding it can read the site (or P and below) wherever a\n"+
		"protected policy enables share links. It opens the page directly, for\n"+
		"browsers, curl and AI tools alike. With --uses, visitors press \"Click\n"+
		"to view\" and each press spends a use. Needs a token with the manage\n"+
		"scope.")
	g := addGlobalFlags(fs)
	path := fs.String("path", "/", "limit the link to this directory, such as /docs/")
	label := fs.String("label", "", `a note for you, such as "Linear ENG-123" (never part of the URL)`)
	expires := fs.String("expires", "never", "when the link stops working: never, or such as 30m, 1h, 7d, 30d, 2w")
	uses := fs.Int("uses", 0, "how many times the link can be opened (1 for a one-time link; default unlimited)")
	c, site, _, err := shareConn(ctx, a, fs, g, args, false)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	body := apiclient.CreateShareLinkJSONRequestBody{Path: path}
	if *label != "" {
		body.Label = label
	}
	at, err := parseExpiry(*expires, a.now())
	if err != nil {
		return err
	}
	if !at.IsZero() {
		body.ExpiresAt = ptr(at.UnixMilli())
	}
	if *uses < 0 || *uses > 1_000_000 {
		return usageErrorf("--uses must be 1 to 1000000, or left out for unlimited uses")
	}
	if *uses > 0 {
		body.MaxUses = ptr(int32(*uses))
	}
	res, err := c.api.CreateShareLinkWithResponse(ctx, site, body, c.auth)
	if err != nil {
		return c.unreachable(ctx, err)
	}
	if res.JSON201 == nil {
		return c.failed(res.HTTPResponse, res.Body)
	}
	return printCreated(a, g.json, site, "Created", res.JSON201)
}

// printCreated prints a link's URL: the only output that ever shows its
// token.
func printCreated(a *app, asJSON bool, site, verb string, created *apiclient.CreatedShareLink) error {
	if asJSON {
		return writeJSON(a.stdout, created)
	}
	l := created.ShareLink
	fmt.Fprintf(a.stderr, "%s share link %s for %s (%s). This URL is shown only now:\n", verb, l.PublicId, site, describeLink(l))
	fmt.Fprintln(a.stdout, created.Url)
	return nil
}

// describeLink summarises a link's scope, expiry and use limit.
func describeLink(l apiclient.ShareLink) string {
	parts := []string{"path " + l.Path}
	if l.Label != nil {
		parts = append([]string{strconv.Quote(*l.Label)}, parts...)
	}
	if l.ExpiresAt != nil {
		parts = append(parts, "expires "+formatTime(*l.ExpiresAt))
	} else {
		parts = append(parts, "never expires")
	}
	if l.MaxUses != nil {
		parts = append(parts, plural(int(*l.MaxUses), "use", "uses"))
	}
	return strings.Join(parts, ", ")
}

type shareListResult struct {
	Site  string                `json:"site"`
	Links []apiclient.ShareLink `json:"links"`
}

func shareList(ctx context.Context, a *app, args []string) error {
	fs := newFlagSet(a, "share list", "Usage: wardlock share list DOMAIN [flags]\n\n"+
		"Lists the site's share links, newest first, with label, ID, scope,\n"+
		"expiry, uses and last access. Revoked links are not shown, and URLs\n"+
		"never are: rotate a link for a new one.")
	g := addGlobalFlags(fs)
	c, site, _, err := shareConn(ctx, a, fs, g, args, false)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	out := shareListResult{Site: site, Links: []apiclient.ShareLink{}}
	var cursor *string
	for {
		res, err := c.api.ListShareLinksWithResponse(ctx, site, &apiclient.ListShareLinksParams{Limit: ptr(pageSize), Cursor: cursor}, c.auth)
		if err != nil {
			return c.unreachable(ctx, err)
		}
		if res.JSON200 == nil {
			return c.failed(res.HTTPResponse, res.Body)
		}
		out.Links = append(out.Links, res.JSON200.Items...)
		if cursor = res.JSON200.NextCursor; cursor == nil {
			break
		}
	}
	if g.json {
		return writeJSON(a.stdout, out)
	}
	if len(out.Links) == 0 {
		fmt.Fprintf(a.stdout, "%s has no share links. Create one with wardlock share create %s.\n", site, site)
		return nil
	}
	tw := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tLABEL\tPATH\tEXPIRES\tUSES\tLAST ACCESS\tSTATUS")
	for _, l := range out.Links {
		label, expires, uses, last := "-", "never", strconv.Itoa(int(l.Uses)), "never"
		if l.Label != nil {
			label = *l.Label
		}
		if l.ExpiresAt != nil {
			expires = formatTime(*l.ExpiresAt)
		}
		if l.MaxUses != nil {
			uses = fmt.Sprintf("%d of %d", l.Uses, *l.MaxUses)
		}
		if l.LastAccessedAt != nil {
			last = formatTime(*l.LastAccessedAt)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", l.PublicId, label, l.Path, expires, uses, last, strings.ReplaceAll(string(l.Status), "_", " "))
	}
	return tw.Flush()
}

type shareRevokeResult struct {
	Revoked  bool   `json:"revoked"`
	Site     string `json:"site"`
	PublicID string `json:"publicId"`
}

func shareRevoke(ctx context.Context, a *app, args []string) error {
	fs := newFlagSet(a, "share revoke", "Usage: wardlock share revoke DOMAIN LINK_ID [flags]\n\n"+
		"Stops the link working at once, with every browser session opened\n"+
		"with it. Needs a token with the manage scope.")
	g := addGlobalFlags(fs)
	c, site, id, err := shareConn(ctx, a, fs, g, args, true)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	res, err := c.api.RevokeShareLinkWithResponse(ctx, site, id, c.auth)
	if err != nil {
		return c.unreachable(ctx, err)
	}
	if res.StatusCode() != http.StatusNoContent {
		return linkFailed(c, res.HTTPResponse, res.Body, site, id)
	}
	if g.json {
		return writeJSON(a.stdout, shareRevokeResult{Revoked: true, Site: site, PublicID: id})
	}
	fmt.Fprintf(a.stdout, "Revoked share link %s on %s. It and every session opened with it stop working now.\n", id, site)
	return nil
}

func shareRotate(ctx context.Context, a *app, args []string) error {
	fs := newFlagSet(a, "share rotate", "Usage: wardlock share rotate DOMAIN LINK_ID [flags]\n\n"+
		"Gives the link a new URL, printed once, keeping its ID, label, scope,\n"+
		"expiry and uses. The old URL, and every session opened with it, stop\n"+
		"working at once. Needs a token with the manage scope.")
	g := addGlobalFlags(fs)
	c, site, id, err := shareConn(ctx, a, fs, g, args, true)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	res, err := c.api.RotateShareLinkWithResponse(ctx, site, id, c.auth)
	if err != nil {
		return c.unreachable(ctx, err)
	}
	if res.JSON200 == nil {
		return linkFailed(c, res.HTTPResponse, res.Body, site, id)
	}
	return printCreated(a, g.json, site, "Rotated", res.JSON200)
}

// linkFailed is conn.failed with a missing link explained.
func linkFailed(c *conn, res *http.Response, body []byte, site, id string) error {
	if res != nil && res.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%s has no share link %s (it may already be revoked): wardlock share list %s shows its links", site, id, site)
	}
	return c.failed(res, body)
}
