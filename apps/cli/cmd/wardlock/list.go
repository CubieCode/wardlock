// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"slices"
	"strings"
	"text/tabwriter"

	"wardlock.nz/apiclient"
)

// pageSize is the largest page the API serves.
const pageSize apiclient.Limit = 100

// listedSite is one entry of list's --json output: the API's Site plus its
// default policy and the names of its group and tags.
type listedSite struct {
	apiclient.Site
	// Policy is the site default's visibility: public or protected.
	Policy apiclient.PolicyVisibility `json:"policy"`
	// Group is the name of the site's group; absent when ungrouped.
	Group string `json:"group,omitempty"`
	// Tags are the names of the site's tags, in tagIds' order.
	Tags []string `json:"tags"`
}

type siteList struct {
	Sites []listedSite `json:"sites"`
}

type deployList struct {
	Site    apiclient.Site     `json:"site"`
	Deploys []apiclient.Deploy `json:"deploys"`
}

func runList(ctx context.Context, a *app, args []string) error {
	fs := newFlagSet(a, "list", "Usage: wardlock list [DOMAIN] [--group NAME] [--tag NAME]... [flags]\n\n"+
		"Without DOMAIN, lists your sites with their default policy, group,\n"+
		"tags and when they were last deployed; --group and --tag list only\n"+
		"the sites in that group and with every tag given. With DOMAIN, lists\n"+
		"that site's deploys, newest first.")
	g := addGlobalFlags(fs)
	group := fs.String("group", "", "list only the sites in this group")
	tags := stringsFlag(fs, "tag", "list only the sites with this tag (repeatable)")
	pos, err := parseArgs(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(pos) > 1 {
		fs.Usage()
		return errUsage
	}
	var f siteFilter
	fs.Visit(func(fl *flag.Flag) { f.byGroup = f.byGroup || fl.Name == "group" })
	if f.byGroup {
		if f.group, err = checkGroupName(*group); err != nil {
			return err
		}
	}
	for _, n := range *tags {
		if err := checkTagName(n); err != nil {
			return err
		}
	}
	f.tags = uniq(*tags)
	var t target
	if len(pos) == 1 {
		if f.byGroup || len(f.tags) > 0 {
			return usageErrorf("--group and --tag filter the list of sites: leave out DOMAIN to use them")
		}
		if t, err = parseTarget(pos[0]); err != nil {
			return err
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
	if len(pos) == 1 {
		return listDeploys(ctx, a, c, t, g.json)
	}
	return listSites(ctx, a, c, f, g.json)
}

// siteFilter is list's --group and --tag.
type siteFilter struct {
	byGroup bool
	group   string
	tags    []string
}

// apply returns the sites f selects, resolving its names first: an
// unknown group or tag is an error that names it.
func (f siteFilter) apply(ctx context.Context, c *conn, sites []apiclient.Site) ([]apiclient.Site, error) {
	if f.byGroup {
		grp, err := c.findGroup(ctx, f.group)
		if err != nil {
			return nil, err
		}
		sites = slices.DeleteFunc(sites, func(s apiclient.Site) bool { return s.GroupId == nil || *s.GroupId != grp.Id })
	}
	if len(f.tags) > 0 {
		found, err := c.findTags(ctx, f.tags)
		if err != nil {
			return nil, err
		}
		sites = slices.DeleteFunc(sites, func(s apiclient.Site) bool {
			return slices.ContainsFunc(found, func(t apiclient.Tag) bool { return !slices.Contains(s.TagIds, t.Id) })
		})
	}
	return sites, nil
}

func listSites(ctx context.Context, a *app, c *conn, f siteFilter, asJSON bool) error {
	sites, err := c.sites(ctx)
	if err != nil {
		return err
	}
	filtered := f.byGroup || len(f.tags) > 0
	if sites, err = f.apply(ctx, c, sites); err != nil {
		return err
	}
	o, err := c.organisationNames(ctx, sites)
	if err != nil {
		return err
	}
	out := siteList{Sites: make([]listedSite, len(sites))}
	err = forEach(ctx, len(sites), 8, func(ctx context.Context, i int) error {
		p, err := c.defaultPolicy(ctx, sites[i].Name)
		out.Sites[i] = listedSite{Site: sites[i], Policy: p, Group: o.group(sites[i]), Tags: o.tagNames(sites[i])}
		return err
	})
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(a.stdout, out)
	}
	if len(sites) == 0 {
		if filtered {
			fmt.Fprintln(a.stdout, "No sites match.")
		} else {
			fmt.Fprintln(a.stdout, "No sites yet. Publish one with wardlock deploy DIR NAME.")
		}
		return nil
	}
	tw := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SITE\tPOLICY\tGROUP\tTAGS\tLAST DEPLOYED\tURL")
	for _, s := range out.Sites {
		last := "not deployed yet"
		if s.ActivatedAt != nil {
			last = formatTime(*s.ActivatedAt)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", s.Name, s.Policy, orDash(s.Group), orDash(strings.Join(s.Tags, ", ")), last, s.Url)
	}
	return tw.Flush()
}

// sites lists all of the owner's sites, by name.
func (c *conn) sites(ctx context.Context) ([]apiclient.Site, error) {
	var all []apiclient.Site
	var cursor *string
	for {
		res, err := c.api.ListSitesWithResponse(ctx, &apiclient.ListSitesParams{Limit: ptr(pageSize), Cursor: cursor}, c.auth)
		if err != nil {
			return nil, c.unreachable(ctx, err)
		}
		if res.JSON200 == nil {
			return nil, c.failed(res.HTTPResponse, res.Body)
		}
		all = append(all, res.JSON200.Items...)
		if cursor = res.JSON200.NextCursor; cursor == nil {
			return all, nil
		}
	}
}

// defaultPolicy is the visibility of the site's "/" policy; a site without
// one is public.
func (c *conn) defaultPolicy(ctx context.Context, site string) (apiclient.PolicyVisibility, error) {
	var cursor *string
	for {
		res, err := c.api.ListPoliciesWithResponse(ctx, site, &apiclient.ListPoliciesParams{Limit: ptr(pageSize), Cursor: cursor}, c.auth)
		if err != nil {
			return "", c.unreachable(ctx, err)
		}
		if res.JSON200 == nil {
			return "", c.failed(res.HTTPResponse, res.Body)
		}
		for _, p := range res.JSON200.Items {
			if p.Path == "/" {
				return p.Visibility, nil
			}
		}
		if cursor = res.JSON200.NextCursor; cursor == nil {
			return apiclient.PolicyVisibilityPublic, nil
		}
	}
}

func listDeploys(ctx context.Context, a *app, c *conn, t target, asJSON bool) error {
	site, err := c.mustFindSite(ctx, t)
	if err != nil {
		return err
	}
	deploys, err := c.deploys(ctx, site.Name)
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(a.stdout, deployList{Site: site, Deploys: deploys})
	}
	fmt.Fprintf(a.stdout, "%s  %s\n\n", site.Name, site.Url)
	if len(deploys) == 0 {
		fmt.Fprintln(a.stdout, "No deploys yet.")
		return nil
	}
	tw := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "DEPLOY\tSTATUS\tCREATED\tPATH\tFILES\tSIZE\tMESSAGE")
	for _, d := range deploys {
		msg := ""
		if d.Message != nil {
			msg = *d.Message
		}
		// A path deploy shows the prefix it replaced; a full deploy, /.
		path := "/"
		if d.Path != nil {
			path = *d.Path
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n", shortID(d.Id), deployStatus(d.Status), formatTime(d.CreatedAt), path, d.FileCount, formatBytes(d.TotalSize), msg)
	}
	return tw.Flush()
}

// deploys lists all of a site's deploys, newest first.
func (c *conn) deploys(ctx context.Context, site string) ([]apiclient.Deploy, error) {
	var all []apiclient.Deploy
	var cursor *string
	for {
		res, err := c.api.ListDeploysWithResponse(ctx, site, &apiclient.ListDeploysParams{Limit: ptr(pageSize), Cursor: cursor}, c.auth)
		if err != nil {
			return nil, c.unreachable(ctx, err)
		}
		if res.JSON200 == nil {
			return nil, c.failed(res.HTTPResponse, res.Body)
		}
		all = append(all, res.JSON200.Items...)
		if cursor = res.JSON200.NextCursor; cursor == nil {
			return all, nil
		}
	}
}

// deployStatus words a status as the dashboard does.
func deployStatus(s apiclient.DeployStatus) string {
	switch s {
	case apiclient.DeployStatusActive:
		return "live"
	case apiclient.DeployStatusInactive:
		return "not live"
	case apiclient.DeployStatusPending:
		return "not activated"
	}
	return string(s)
}

func ptr[T any](v T) *T { return &v }
