// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"regexp"
	"strings"

	"wardlock.nz/apiclient"
)

var (
	deployIDPattern    = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	shortDeployPattern = regexp.MustCompile(`^[0-9a-f]{8,12}$`)
)

func runRollback(ctx context.Context, a *app, args []string) error {
	fs := newFlagSet(a, "rollback", "Usage: wardlock rollback DOMAIN [DEPLOY_ID] [flags]\n\n"+
		"Makes DEPLOY_ID live again at once, with no upload. DEPLOY_ID is a\n"+
		"full deploy ID or the short ID wardlock list DOMAIN shows. Without\n"+
		"it, returns to the deploy that was live before the current one.")
	g := addGlobalFlags(fs)
	pos, err := parseArgs(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(pos) < 1 || len(pos) > 2 {
		fs.Usage()
		return errUsage
	}
	t, err := parseTarget(pos[0])
	if err != nil {
		return err
	}
	var want string
	if len(pos) == 2 {
		want = strings.ToLower(pos[1])
		if !deployIDPattern.MatchString(want) && !shortDeployPattern.MatchString(want) {
			return usageErrorf("%q is not a deploy ID: use the ID or short ID that wardlock list %s shows", pos[1], t)
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
	site, err := c.mustFindSite(ctx, t)
	if err != nil {
		return err
	}
	var body apiclient.RollbackRequest
	if want != "" {
		id, err := c.deployID(ctx, site.Name, want)
		if err != nil {
			return err
		}
		body.DeployId = &id
	}
	res, err := c.api.RollbackSiteWithResponse(ctx, site.Name, body, c.auth)
	if err != nil {
		return c.unreachable(ctx, err)
	}
	if res.JSON200 == nil {
		return c.failed(res.HTTPResponse, res.Body)
	}
	act := res.JSON200
	if g.json {
		return writeJSON(a.stdout, act)
	}
	d := act.Deploy
	about := fmt.Sprintf("created %s", formatTime(d.CreatedAt))
	if d.Message != nil {
		about = fmt.Sprintf("%q, %s", *d.Message, about)
	}
	fmt.Fprintf(a.stdout, "Rolled %s back to deploy %s (%s). It is live:\n%s\n", act.Site.Name, shortID(d.Id), about, act.Site.Url)
	return nil
}

// deployID expands a short deploy ID by matching the end of the site's
// deploy IDs; a full ID is returned as it is.
func (c *conn) deployID(ctx context.Context, site, id string) (string, error) {
	if deployIDPattern.MatchString(id) {
		return id, nil
	}
	deploys, err := c.deploys(ctx, site)
	if err != nil {
		return "", err
	}
	var found []string
	for _, d := range deploys {
		if strings.HasSuffix(d.Id, id) {
			found = append(found, d.Id)
		}
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("site %s has no deploy %s: wardlock list %s shows its deploys", site, id, site)
	case 1:
		return found[0], nil
	}
	return "", fmt.Errorf("%s matches %d deploys of %s: give the full ID", id, len(found), site)
}
