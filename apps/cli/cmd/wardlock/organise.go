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
	"slices"
	"strings"
	"text/tabwriter"

	"golang.org/x/text/unicode/norm"

	"wardlock.nz/apiclient"
)

// Groups, tags and pins (V5.15, over V5.3's API). Groups and tags are
// named on the command line and resolved to IDs here, before anything
// changes; an unknown name is an error that names it.

// maxOrganiseSites is how many sites one organiseSites request may name.
const maxOrganiseSites = 500

var tagNamePattern = regexp.MustCompile(`^[a-z0-9-]{1,30}$`)

// checkTagName is a usage error unless name is a valid tag name.
func checkTagName(name string) error {
	if !tagNamePattern.MatchString(name) {
		return usageErrorf("%q is not a tag name: use 1-30 lowercase letters, digits and hyphens", name)
	}
	return nil
}

// checkGroupName trims name and is a usage error unless it is 1-40
// characters. The server checks the rest.
func checkGroupName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if n := len([]rune(name)); n == 0 || n > 40 {
		return "", usageErrorf("%q is not a group name: use 1-40 characters", name)
	}
	return name, nil
}

// groupKey is how the server compares group names: trimmed, NFC and
// lowercased.
func groupKey(name string) string {
	return strings.ToLower(norm.NFC.String(strings.TrimSpace(name)))
}

// groups lists all of the owner's groups, by name.
func (c *conn) groups(ctx context.Context) ([]apiclient.Group, error) {
	res, err := c.api.ListGroupsWithResponse(ctx, c.auth)
	if err != nil {
		return nil, c.unreachable(ctx, err)
	}
	if res.JSON200 == nil {
		return nil, c.failed(res.HTTPResponse, res.Body)
	}
	return res.JSON200.Items, nil
}

// tags lists all of the owner's tags, by name.
func (c *conn) tags(ctx context.Context) ([]apiclient.Tag, error) {
	res, err := c.api.ListTagsWithResponse(ctx, c.auth)
	if err != nil {
		return nil, c.unreachable(ctx, err)
	}
	if res.JSON200 == nil {
		return nil, c.failed(res.HTTPResponse, res.Body)
	}
	return res.JSON200.Items, nil
}

// findGroup returns the group named name, in any case.
func (c *conn) findGroup(ctx context.Context, name string) (apiclient.Group, error) {
	groups, err := c.groups(ctx)
	if err != nil {
		return apiclient.Group{}, err
	}
	return groupNamed(c.server, groups, name)
}

func groupNamed(server string, groups []apiclient.Group, name string) (apiclient.Group, error) {
	key := groupKey(name)
	for _, g := range groups {
		if groupKey(g.Name) == key {
			return g, nil
		}
	}
	return apiclient.Group{}, fmt.Errorf("no group named %q on %s: wardlock group list shows yours, and wardlock group create adds one", strings.TrimSpace(name), server)
}

// findTags returns the tags named names, in that order.
func (c *conn) findTags(ctx context.Context, names []string) ([]apiclient.Tag, error) {
	all, err := c.tags(ctx)
	if err != nil {
		return nil, err
	}
	return tagsNamed(c.server, all, names)
}

func tagsNamed(server string, all []apiclient.Tag, names []string) ([]apiclient.Tag, error) {
	var found []apiclient.Tag
	var unknown []string
	for _, n := range names {
		i := slices.IndexFunc(all, func(t apiclient.Tag) bool { return t.Name == n })
		if i < 0 {
			unknown = append(unknown, n)
			continue
		}
		found = append(found, all[i])
	}
	switch len(unknown) {
	case 0:
		return found, nil
	case 1:
		return nil, fmt.Errorf("no tag named %q on %s: wardlock tag list shows yours, and wardlock tag create adds one", unknown[0], server)
	}
	return nil, fmt.Errorf("no tags named %s on %s: wardlock tag list shows yours, and wardlock tag create adds one", quoteAll(unknown), server)
}

func tagIDs(tags []apiclient.Tag) []string {
	ids := make([]string, len(tags))
	for i, t := range tags {
		ids[i] = t.Id
	}
	return ids
}

func quoteAll(names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = fmt.Sprintf("%q", n)
	}
	return strings.Join(q, ", ")
}

// stringsFlag is a repeatable flag; each use appends its value.
func stringsFlag(fs *flag.FlagSet, name, usage string) *[]string {
	var v []string
	fs.Func(name, usage, func(s string) error {
		v = append(v, s)
		return nil
	})
	return &v
}

// organisation names groups and tags by ID; names maps them back.
type organisation struct {
	groups map[string]string
	tags   map[string]string
}

// organisationNames fetches group and tag names, but only when some site
// uses them, so a server without groups and tags is never asked.
func (c *conn) organisationNames(ctx context.Context, sites []apiclient.Site) (organisation, error) {
	o := organisation{groups: map[string]string{}, tags: map[string]string{}}
	needGroups := slices.ContainsFunc(sites, func(s apiclient.Site) bool { return s.GroupId != nil })
	needTags := slices.ContainsFunc(sites, func(s apiclient.Site) bool { return len(s.TagIds) > 0 })
	if needGroups {
		groups, err := c.groups(ctx)
		if err != nil {
			return o, err
		}
		for _, g := range groups {
			o.groups[g.Id] = g.Name
		}
	}
	if needTags {
		tags, err := c.tags(ctx)
		if err != nil {
			return o, err
		}
		for _, t := range tags {
			o.tags[t.Id] = t.Name
		}
	}
	return o, nil
}

func (o organisation) group(s apiclient.Site) string {
	if s.GroupId == nil {
		return ""
	}
	return o.groups[*s.GroupId]
}

func (o organisation) tagNames(s apiclient.Site) []string {
	names := make([]string, 0, len(s.TagIds))
	for _, id := range s.TagIds {
		if n, ok := o.tags[id]; ok {
			names = append(names, n)
		}
	}
	return names
}

// orDash is s, or "-" when empty, for a table cell.
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

const siteUsage = "Usage: wardlock site organise SITE... [--group NAME|--no-group] [--tag NAME]... [--untag NAME]... [flags]\n\n" +
	"  organise SITE...   Put sites in a group and add or remove their tags\n\n" +
	"Pin and unpin sites with wardlock pin and wardlock unpin.\n"

func runSite(ctx context.Context, a *app, args []string) error {
	global, rest, err := leadingGlobalFlags(args)
	if err != nil {
		return err
	}
	if len(rest) == 0 {
		fmt.Fprint(a.stderr, siteUsage)
		return errUsage
	}
	sub, args := rest[0], append(global, rest[1:]...)
	switch sub {
	case "organise":
		return siteOrganise(ctx, a, args)
	case "help", "--help", "-h":
		fmt.Fprint(a.stdout, siteUsage)
		return nil
	}
	fmt.Fprintf(a.stderr, "wardlock: unknown site command %q\n\n%s", sub, siteUsage)
	return errUsage
}

// organiseResult is site organise's --json output: the API's
// OrganiseSitesResult (each site's organisation from before) plus each
// site as it is now.
type organiseResult struct {
	apiclient.OrganiseSitesResult
	Sites []apiclient.Site `json:"sites"`
}

func siteOrganise(ctx context.Context, a *app, args []string) error {
	fs := newFlagSet(a, "site organise", "Usage: wardlock site organise SITE... [--group NAME|--no-group] [--tag NAME]... [--untag NAME]... [flags]\n\n"+
		"Changes every SITE at once, all or nothing: --group puts them in an\n"+
		"existing group (--no-group takes them out of theirs), --tag adds an\n"+
		"existing tag and --untag removes one. Tags not named are kept. Give\n"+
		"--tag and --untag once per tag. Needs a token with the manage scope.")
	g := addGlobalFlags(fs)
	group := fs.String("group", "", "put the sites in this group")
	noGroup := fs.Bool("no-group", false, "take the sites out of their group")
	add := stringsFlag(fs, "tag", "add this tag to the sites (repeatable)")
	remove := stringsFlag(fs, "untag", "remove this tag from the sites (repeatable)")
	pos, err := parseArgs(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		fs.Usage()
		return errUsage
	}
	targets, err := parseTargets(pos)
	if err != nil {
		return err
	}
	groupSet := false
	fs.Visit(func(f *flag.Flag) { groupSet = groupSet || f.Name == "group" })
	switch {
	case groupSet && *noGroup:
		return usageErrorf("pass --group or --no-group, not both")
	case !groupSet && !*noGroup && len(*add) == 0 && len(*remove) == 0:
		return usageErrorf("nothing to change: pass --group, --no-group, --tag or --untag")
	}
	if groupSet {
		if *group, err = checkGroupName(*group); err != nil {
			return err
		}
	}
	for _, n := range append(slices.Clone(*add), *remove...) {
		if err := checkTagName(n); err != nil {
			return err
		}
	}
	for _, n := range *add {
		if slices.Contains(*remove, n) {
			return usageErrorf("--tag %s and --untag %s: pass a tag to one of them", n, n)
		}
	}
	cfgServer, err := a.configServer()
	if err != nil {
		return err
	}
	c, err := a.connect(g.server, cfgServer)
	if err != nil {
		return err
	}
	names, err := c.siteNames(ctx, targets)
	if err != nil {
		return err
	}
	body := apiclient.OrganiseSitesRequest{Sites: names}
	if *noGroup {
		body.Ungroup = ptr(true)
	}
	if groupSet {
		grp, err := c.findGroup(ctx, *group)
		if err != nil {
			return err
		}
		body.GroupId = &grp.Id
	}
	if len(*add) > 0 || len(*remove) > 0 {
		all, err := c.tags(ctx)
		if err != nil {
			return err
		}
		if len(*add) > 0 {
			found, err := tagsNamed(c.server, all, uniq(*add))
			if err != nil {
				return err
			}
			body.AddTagIds = ptr(tagIDs(found))
		}
		if len(*remove) > 0 {
			found, err := tagsNamed(c.server, all, uniq(*remove))
			if err != nil {
				return err
			}
			body.RemoveTagIds = ptr(tagIDs(found))
		}
	}
	res, err := c.api.OrganiseSitesWithResponse(ctx, body, c.auth)
	if err != nil {
		return c.unreachable(ctx, err)
	}
	if res.JSON200 == nil {
		if res.StatusCode() == http.StatusNotFound {
			return fmt.Errorf("%w; nothing was changed", c.failed(res.HTTPResponse, res.Body))
		}
		return c.failed(res.HTTPResponse, res.Body)
	}
	sites, err := c.sitesNamed(ctx, names)
	if err != nil {
		return err
	}
	if g.json {
		return writeJSON(a.stdout, organiseResult{OrganiseSitesResult: *res.JSON200, Sites: sites})
	}
	o, err := c.organisationNames(ctx, sites)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "Organised %s.\n\n", plural(len(sites), "site", "sites"))
	return writeOrganisation(a, o, sites)
}

// writeOrganisation prints each site's group, tags and pin.
func writeOrganisation(a *app, o organisation, sites []apiclient.Site) error {
	tw := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SITE\tGROUP\tTAGS\tPINNED")
	for _, s := range sites {
		pinned := "no"
		if s.Pinned {
			pinned = "yes"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", s.Name, orDash(o.group(s)), orDash(strings.Join(o.tagNames(s), ", ")), pinned)
	}
	return tw.Flush()
}

// parseTargets parses SITE arguments, each kept once, and is a usage
// error for more than one request can change.
func parseTargets(raw []string) ([]target, error) {
	var ts []target
	for _, r := range raw {
		t, err := parseTarget(r)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(ts, t) {
			ts = append(ts, t)
		}
	}
	if len(ts) > maxOrganiseSites {
		return nil, usageErrorf("%d sites: name at most %d at once", len(ts), maxOrganiseSites)
	}
	return ts, nil
}

// siteNames turns targets into site names, looking up hostnames, each
// kept once and in order.
func (c *conn) siteNames(ctx context.Context, targets []target) ([]string, error) {
	var names []string
	for _, t := range targets {
		name := t.name
		if name == "" {
			site, err := c.mustFindSite(ctx, t)
			if err != nil {
				return nil, err
			}
			name = site.Name
		}
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names, nil
}

// sitesNamed returns the owner's sites called names, in that order.
func (c *conn) sitesNamed(ctx context.Context, names []string) ([]apiclient.Site, error) {
	all, err := c.sites(ctx)
	if err != nil {
		return nil, err
	}
	sites := make([]apiclient.Site, 0, len(names))
	for _, n := range names {
		i := slices.IndexFunc(all, func(s apiclient.Site) bool { return s.Name == n })
		if i < 0 {
			return nil, fmt.Errorf("no site %s on %s: wardlock list shows yours", n, c.server)
		}
		sites = append(sites, all[i])
	}
	return sites, nil
}

func uniq(s []string) []string {
	var out []string
	for _, v := range s {
		if !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// pinResult is pin's and unpin's --json output: each site as it is now.
type pinResult struct {
	Sites []apiclient.Site `json:"sites"`
}

func runPin(ctx context.Context, a *app, args []string) error {
	return pinSites(ctx, a, args, true)
}

func runUnpin(ctx context.Context, a *app, args []string) error {
	return pinSites(ctx, a, args, false)
}

func pinSites(ctx context.Context, a *app, args []string, pin bool) error {
	name, verb, does := "pin", "Pinned", "Pins each SITE, so it stays at hand in the dashboard. Pinning a\npinned site changes nothing."
	if !pin {
		name, verb, does = "unpin", "Unpinned", "Unpins each SITE. Unpinning a site that is not pinned changes\nnothing."
	}
	fs := newFlagSet(a, name, "Usage: wardlock "+name+" SITE... [flags]\n\n"+does+" Every SITE changes\nat once, all or nothing, so a missing one changes none. Needs a token\nwith the manage scope.")
	g := addGlobalFlags(fs)
	pos, err := parseArgs(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		fs.Usage()
		return errUsage
	}
	targets, err := parseTargets(pos)
	if err != nil {
		return err
	}
	cfgServer, err := a.configServer()
	if err != nil {
		return err
	}
	c, err := a.connect(g.server, cfgServer)
	if err != nil {
		return err
	}
	names, err := c.siteNames(ctx, targets)
	if err != nil {
		return err
	}
	res, err := c.api.OrganiseSitesWithResponse(ctx, apiclient.OrganiseSitesRequest{Sites: names, Pinned: &pin}, c.auth)
	if err != nil {
		return c.unreachable(ctx, err)
	}
	if res.JSON200 == nil {
		if res.StatusCode() == http.StatusNotFound {
			return fmt.Errorf("%w; nothing was changed", c.failed(res.HTTPResponse, res.Body))
		}
		return c.failed(res.HTTPResponse, res.Body)
	}
	sites, err := c.sitesNamed(ctx, names)
	if err != nil {
		return err
	}
	if g.json {
		return writeJSON(a.stdout, pinResult{Sites: sites})
	}
	fmt.Fprintf(a.stdout, "%s %s.\n", verb, strings.Join(names, ", "))
	return nil
}
