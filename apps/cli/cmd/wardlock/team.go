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

// Teams (T7.3, over T7.1's API). Teams are named on the command line, or
// given by ID, since a name is not unique, and resolved here among the
// caller's own. Inviting and removing members stays in the dashboard.

const teamUsage = "Usage: wardlock team <list|members> [TEAM] [flags]\n\n" +
	"  list            List your teams and your role in each\n" +
	"  members TEAM    List a team's members\n\n" +
	"TEAM is a team's name, in any case, or its ID. Any token can do these.\n" +
	"Members are invited and removed in the dashboard.\n"

func runTeam(ctx context.Context, a *app, args []string) error {
	global, rest, err := leadingGlobalFlags(args)
	if err != nil {
		return err
	}
	if len(rest) == 0 {
		fmt.Fprint(a.stderr, teamUsage)
		return errUsage
	}
	sub, args := rest[0], append(global, rest[1:]...)
	switch sub {
	case "list":
		return teamList(ctx, a, args)
	case "members":
		return teamMembers(ctx, a, args)
	case "help", "--help", "-h":
		fmt.Fprint(a.stdout, teamUsage)
		return nil
	}
	fmt.Fprintf(a.stderr, "wardlock: unknown team command %q\n\n%s", sub, teamUsage)
	return errUsage
}

// inTeam returns c working in the team with this ID, or in the caller's own
// account for "".
func (c *conn) inTeam(id string) *conn {
	scoped := *c
	scoped.team = nil
	if id != "" {
		scoped.team = &id
	}
	return &scoped
}

// siteTeam is the ID of the team that holds s; "" for a site in a person's
// account, or from a server older than teams.
func siteTeam(s apiclient.Site) string {
	if s.Owner.Kind == apiclient.SiteOwnerKindTeam {
		return s.Owner.Id
	}
	return ""
}

// accountName words the account that holds s for a message.
func accountName(s apiclient.Site) string {
	if siteTeam(s) != "" {
		return "the team " + s.Owner.Name
	}
	return "a personal account"
}

// teams lists the teams the caller is in, by name.
func (c *conn) teams(ctx context.Context) ([]apiclient.Team, error) {
	res, err := c.api.ListTeamsWithResponse(ctx, c.auth)
	if err != nil {
		return nil, c.unreachable(ctx, err)
	}
	if res.JSON200 == nil {
		return nil, c.failed(res.HTTPResponse, res.Body)
	}
	return res.JSON200.Items, nil
}

// findTeam returns the caller's team with this ID or name. An exact name
// wins over one differing in case. Names are not unique, so more than one
// match is an error listing the IDs to choose from.
func (c *conn) findTeam(ctx context.Context, name string) (apiclient.Team, error) {
	teams, err := c.teams(ctx)
	if err != nil {
		return apiclient.Team{}, err
	}
	name = strings.TrimSpace(name)
	var exact, folded []apiclient.Team
	for _, t := range teams {
		switch {
		case t.Id == name:
			return t, nil
		case t.Name == name:
			exact = append(exact, t)
		case groupKey(t.Name) == groupKey(name):
			folded = append(folded, t)
		}
	}
	found := exact
	if len(found) == 0 {
		found = folded
	}
	switch len(found) {
	case 0:
		return apiclient.Team{}, fmt.Errorf("no team named %q on %s: wardlock team list shows yours", name, c.server)
	case 1:
		return found[0], nil
	}
	ids := make([]string, len(found))
	for i, t := range found {
		ids[i] = t.Id
	}
	return apiclient.Team{}, fmt.Errorf("%d of your teams on %s are named %q: use the ID of the one you mean, %s", len(found), c.server, name, strings.Join(ids, " or "))
}

// teamMembersResult is team members' --json output.
type teamMembersResult struct {
	Team    apiclient.Team         `json:"team"`
	Members []apiclient.TeamMember `json:"members"`
}

type teamListResult struct {
	Teams []apiclient.Team `json:"teams"`
}

func teamList(ctx context.Context, a *app, args []string) error {
	fs := newFlagSet(a, "team list", "Usage: wardlock team list [flags]\n\nLists your teams by name, with your role in each. A team token sees its\nown team.")
	g := addGlobalFlags(fs)
	pos, err := parseArgs(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(pos) != 0 {
		fs.Usage()
		return errUsage
	}
	cfgServer, err := a.configServer()
	if err != nil {
		return err
	}
	c, err := a.connect(g.server, cfgServer)
	if err != nil {
		return err
	}
	teams, err := c.teams(ctx)
	if err != nil {
		return err
	}
	if g.json {
		return writeJSON(a.stdout, teamListResult{Teams: teams})
	}
	if len(teams) == 0 {
		fmt.Fprintln(a.stdout, "You are in no teams.")
		return nil
	}
	tw := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TEAM\tROLE\tID")
	for _, t := range teams {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", t.Name, t.Role, t.Id)
	}
	return tw.Flush()
}

func teamMembers(ctx context.Context, a *app, args []string) error {
	fs := newFlagSet(a, "team members", "Usage: wardlock team members TEAM [flags]\n\nLists the team's members by address, with their roles. TEAM is its name\nor ID. Members are invited and removed in the dashboard.")
	g := addGlobalFlags(fs)
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
	if strings.TrimSpace(pos[0]) == "" {
		return usageErrorf("which team? Name it, or give its ID: wardlock team list shows yours")
	}
	cfgServer, err := a.configServer()
	if err != nil {
		return err
	}
	c, err := a.connect(g.server, cfgServer)
	if err != nil {
		return err
	}
	team, err := c.findTeam(ctx, pos[0])
	if err != nil {
		return err
	}
	members := []apiclient.TeamMember{}
	var cursor *string
	for {
		res, err := c.api.ListTeamMembersWithResponse(ctx, team.Id, &apiclient.ListTeamMembersParams{Limit: ptr(pageSize), Cursor: cursor}, c.auth)
		if err != nil {
			return c.unreachable(ctx, err)
		}
		if res.JSON200 == nil {
			return c.failed(res.HTTPResponse, res.Body)
		}
		members = append(members, res.JSON200.Items...)
		if cursor = res.JSON200.NextCursor; cursor == nil {
			break
		}
	}
	if g.json {
		return writeJSON(a.stdout, teamMembersResult{Team: team, Members: members})
	}
	tw := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "MEMBER\tROLE\tSTATUS\tJOINED")
	for _, m := range members {
		status := "active"
		if m.Suspended {
			status = "suspended"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", m.Email, m.Role, status, formatTime(m.CreatedAt))
	}
	return tw.Flush()
}

func siteMove(ctx context.Context, a *app, args []string) error {
	fs := newFlagSet(a, "site move", "Usage: wardlock site move SITE (--team TEAM | --personal) [--yes] [flags]\n\n"+
		"Moves SITE to the team TEAM, by name or ID, or with --personal to your\n"+
		"own account. Deploys, share links, domains and analytics stay with it;\n"+
		"its group and tags are cleared, since they belong to the account it\n"+
		"leaves. You need to be admin of any team it leaves or joins, so a team\n"+
		"token cannot move a site. Asks you to type the site's name first;\n"+
		"without a terminal, or with --json, pass --yes instead. Needs a token\n"+
		"with the manage scope.")
	g := addGlobalFlags(fs)
	team := fs.String("team", "", "move the site to this team")
	personal := fs.Bool("personal", false, "move the site to your own account")
	yes := fs.Bool("yes", false, "move without asking")
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
	var flags []string
	fs.Visit(func(f *flag.Flag) { flags = append(flags, f.Name) })
	teamSet := slices.Contains(flags, "team")
	switch {
	case teamSet && *personal:
		return usageErrorf("pass --team or --personal, not both")
	case !teamSet && !*personal:
		return usageErrorf("where to? Pass --team TEAM or --personal")
	case teamSet && strings.TrimSpace(*team) == "":
		return usageErrorf("--team needs a team's name or ID: wardlock team list shows yours")
	case !*yes && (g.json || !a.canPrompt):
		return usageErrorf("moving %s clears its group and tags: pass --yes to confirm when there is no terminal to ask in, or with --json", t)
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
	var body apiclient.TransferSiteJSONRequestBody
	body.To.Kind = apiclient.TransferSiteRequestToKindOwner
	where := "your own account"
	if teamSet {
		found, err := c.findTeam(ctx, *team)
		if err != nil {
			return err
		}
		body.To.Kind, body.To.Id = apiclient.TransferSiteRequestToKindTeam, &found.Id
		where = "the team " + found.Name
	}
	if !*yes {
		ok, err := a.confirm(fmt.Sprintf("This moves %s from %s to %s. Deploys, share links, domains and analytics stay with it; its group and tags are cleared.\nType the site name to confirm: ", site.Name, accountName(site), where), site.Name)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("not confirmed: nothing was moved")
		}
	}
	res, err := c.api.TransferSiteWithResponse(ctx, site.Name, body, c.auth)
	if err != nil {
		return c.unreachable(ctx, err)
	}
	if res.JSON200 == nil {
		err := c.failed(res.HTTPResponse, res.Body)
		if errorCode(res.Body) == "forbidden_role" {
			return fmt.Errorf("%w Moving a site needs admin of any team it leaves or joins, and a team token cannot move one", err)
		}
		return err
	}
	if g.json {
		return writeJSON(a.stdout, res.JSON200)
	}
	fmt.Fprintf(a.stdout, "Moved %s to %s.\n", site.Name, where)
	return nil
}
