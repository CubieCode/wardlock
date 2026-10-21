// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"text/tabwriter"

	"wardlock.nz/apiclient"
)

// pageSize is the largest page the API serves.
const pageSize apiclient.Limit = 100

// listedSite is one entry of list's --json output: the API's Site plus its
// default policy.
type listedSite struct {
	apiclient.Site
	// Policy is the site default's visibility: public or protected.
	Policy apiclient.PolicyVisibility `json:"policy"`
}

type siteList struct {
	Sites []listedSite `json:"sites"`
}

type deployList struct {
	Site    apiclient.Site     `json:"site"`
	Deploys []apiclient.Deploy `json:"deploys"`
}

func runList(ctx context.Context, a *app, args []string) error {
	fs := newFlagSet(a, "list", "Usage: wardlock list [DOMAIN] [flags]\n\n"+
		"Without DOMAIN, lists your sites with their default policy and when\n"+
		"they were last deployed. With DOMAIN, lists that site's deploys,\n"+
		"newest first.")
	g := addGlobalFlags(fs)
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
	var t target
	if len(pos) == 1 {
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
	return listSites(ctx, a, c, g.json)
}

func listSites(ctx context.Context, a *app, c *conn, asJSON bool) error {
	sites, err := c.sites(ctx)
	if err != nil {
		return err
	}
	out := siteList{Sites: make([]listedSite, len(sites))}
	err = forEach(ctx, len(sites), 8, func(ctx context.Context, i int) error {
		p, err := c.defaultPolicy(ctx, sites[i].Name)
		out.Sites[i] = listedSite{Site: sites[i], Policy: p}
		return err
	})
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(a.stdout, out)
	}
	if len(sites) == 0 {
		fmt.Fprintln(a.stdout, "No sites yet. Publish one with wardlock deploy DIR NAME.")
		return nil
	}
	tw := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SITE\tPOLICY\tLAST DEPLOYED\tURL")
	for _, s := range out.Sites {
		last := "not deployed yet"
		if s.ActivatedAt != nil {
			last = formatTime(*s.ActivatedAt)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", s.Name, s.Policy, last, s.Url)
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
