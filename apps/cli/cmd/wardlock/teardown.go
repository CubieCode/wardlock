// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"

	"wardlock.nz/apiclient"
)

type teardownResult struct {
	Deleted bool           `json:"deleted"`
	Site    apiclient.Site `json:"site"`
}

func runTeardown(ctx context.Context, a *app, args []string) error {
	fs := newFlagSet(a, "teardown", "Usage: wardlock teardown DOMAIN [--yes] [flags]\n\n"+
		"Deletes the site with its deploys, share links, policies and custom\n"+
		"domains, and frees its name. It stops being served at once and\n"+
		"cannot be undone. Asks you to type the site's name first; without a\n"+
		"terminal, or with --json, pass --yes instead. Needs a token with the\n"+
		"manage scope.")
	g := addGlobalFlags(fs)
	yes := fs.Bool("yes", false, "delete without asking")
	pos, err := parseArgs(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		fs.Usage()
		return errUsage
	}
	t, err := parseTarget(pos[0])
	if err != nil {
		return err
	}
	if !*yes && (g.json || !a.canPrompt) {
		return usageErrorf("teardown deletes %s for good: pass --yes to confirm when there is no terminal to ask in, or with --json", t)
	}
	cfgServer, err := a.configServer()
	if err != nil {
		return err
	}
	c, err := a.connect(g.server, cfgServer)
	if err != nil {
		return err
	}
	site, err := c.mustFindSite(ctx, t)
	if err != nil {
		return err
	}
	if !*yes {
		ok, err := a.confirm(fmt.Sprintf("This deletes %s (%s) with all its deploys, share links, policies and custom domains. It cannot be undone.\nType the site name to confirm: ", site.Name, site.Hostname), site.Name)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("not confirmed: nothing was deleted")
		}
	}
	res, err := c.api.DeleteSiteWithResponse(ctx, site.Name, c.auth)
	if err != nil {
		return c.unreachable(ctx, err)
	}
	if res.StatusCode() != http.StatusNoContent {
		return c.failed(res.HTTPResponse, res.Body)
	}
	if g.json {
		return writeJSON(a.stdout, teardownResult{Deleted: true, Site: site})
	}
	fmt.Fprintf(a.stdout, "Deleted %s. %s is no longer served and the name is free.\n", site.Name, site.Hostname)
	return nil
}
