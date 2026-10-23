// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"text/tabwriter"

	"wardlock.nz/apiclient"
)

// wardlock group (V5.15): list, create, rename and delete the owner's
// groups. A site is in at most one group; wardlock site organise and
// wardlock deploy --group put sites in one.

const groupUsage = "Usage: wardlock group <list|create|rename|delete> [NAME] [NEW_NAME] [flags]\n\n" +
	"  list                    List your groups\n" +
	"  create NAME [--note N]  Create a group\n" +
	"  rename NAME NEW_NAME    Rename a group; --note changes its note\n" +
	"  delete NAME             Delete a group; its sites are kept, ungrouped\n\n" +
	"Names are matched ignoring case. Listing works with any token; the\n" +
	"others need a token with the manage scope.\n"

func runGroup(ctx context.Context, a *app, args []string) error {
	global, rest, err := leadingGlobalFlags(args)
	if err != nil {
		return err
	}
	if len(rest) == 0 {
		fmt.Fprint(a.stderr, groupUsage)
		return errUsage
	}
	sub, args := rest[0], append(global, rest[1:]...)
	switch sub {
	case "list":
		return groupList(ctx, a, args)
	case "create":
		return groupCreate(ctx, a, args)
	case "rename":
		return groupRename(ctx, a, args)
	case "delete":
		return groupDelete(ctx, a, args)
	case "help", "--help", "-h":
		fmt.Fprint(a.stdout, groupUsage)
		return nil
	}
	fmt.Fprintf(a.stderr, "wardlock: unknown group command %q\n\n%s", sub, groupUsage)
	return errUsage
}

type groupListResult struct {
	Groups []apiclient.Group `json:"groups"`
}

type groupDeleteResult struct {
	Deleted bool            `json:"deleted"`
	Group   apiclient.Group `json:"group"`
}

// orgConn parses minArgs to maxArgs positional arguments, runs check (so every
// usage error exits 2 before any network round trip), then connects.
func orgConn(a *app, fs *flag.FlagSet, g *globalFlags, args []string, minArgs, maxArgs int, check func(pos []string) error) (*conn, []string, error) {
	pos, err := parseArgs(fs, args)
	if err != nil {
		return nil, nil, err
	}
	if len(pos) < minArgs || len(pos) > maxArgs {
		fs.Usage()
		return nil, nil, errUsage
	}
	if check != nil {
		if err := check(pos); err != nil {
			return nil, nil, err
		}
	}
	cfgServer, err := a.configServer()
	if err != nil {
		return nil, nil, err
	}
	c, err := a.connect(g.server, cfgServer)
	if err != nil {
		return nil, nil, err
	}
	return c, pos, nil
}

func groupList(ctx context.Context, a *app, args []string) error {
	fs := newFlagSet(a, "group list", "Usage: wardlock group list [flags]\n\nLists your groups by name.")
	g := addGlobalFlags(fs)
	c, _, err := orgConn(a, fs, g, args, 0, 0, nil)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	groups, err := c.groups(ctx)
	if err != nil {
		return err
	}
	if g.json {
		return writeJSON(a.stdout, groupListResult{Groups: groups})
	}
	if len(groups) == 0 {
		fmt.Fprintln(a.stdout, "No groups yet. Create one with wardlock group create NAME.")
		return nil
	}
	tw := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "GROUP\tNOTE")
	for _, grp := range groups {
		note := ""
		if grp.Note != nil {
			note = *grp.Note
		}
		fmt.Fprintf(tw, "%s\t%s\n", grp.Name, note)
	}
	return tw.Flush()
}

func groupCreate(ctx context.Context, a *app, args []string) error {
	fs := newFlagSet(a, "group create", "Usage: wardlock group create NAME [--note N] [flags]\n\n"+
		"Creates a group of 1-40 characters, unique among yours ignoring case.\n"+
		"Needs a token with the manage scope.")
	g := addGlobalFlags(fs)
	note := fs.String("note", "", "a note of up to 200 characters, such as \"Previews for clients\"")
	var name string
	c, _, err := orgConn(a, fs, g, args, 1, 1, func(pos []string) (err error) {
		name, err = checkGroupName(pos[0])
		return err
	})
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	body := apiclient.CreateGroupJSONRequestBody{Name: name}
	if *note != "" {
		body.Note = note
	}
	res, err := c.api.CreateGroupWithResponse(ctx, body, c.auth)
	if err != nil {
		return c.unreachable(ctx, err)
	}
	if res.JSON201 == nil {
		return c.failed(res.HTTPResponse, res.Body)
	}
	if g.json {
		return writeJSON(a.stdout, res.JSON201)
	}
	fmt.Fprintf(a.stdout, "Created group %s.\n", res.JSON201.Name)
	return nil
}

func groupRename(ctx context.Context, a *app, args []string) error {
	fs := newFlagSet(a, "group rename", "Usage: wardlock group rename NAME [NEW_NAME] [--note N] [flags]\n\n"+
		"Renames the group NAME to NEW_NAME, or changes its note with --note\n"+
		"(--note '' removes it). Its sites stay in it. Needs a token with the\n"+
		"manage scope.")
	g := addGlobalFlags(fs)
	note := fs.String("note", "", "the group's new note; '' removes it")
	var name string
	body := apiclient.UpdateGroupJSONRequestBody{}
	c, _, err := orgConn(a, fs, g, args, 1, 2, func(pos []string) (err error) {
		if name, err = checkGroupName(pos[0]); err != nil {
			return err
		}
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "note" {
				body.Note = note
			}
		})
		if len(pos) == 2 {
			newName, err := checkGroupName(pos[1])
			if err != nil {
				return err
			}
			body.Name = &newName
		}
		if body.Name == nil && body.Note == nil {
			return usageErrorf("nothing to change: pass NEW_NAME or --note")
		}
		return nil
	})
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	grp, err := c.findGroup(ctx, name)
	if err != nil {
		return err
	}
	res, err := c.api.UpdateGroupWithResponse(ctx, grp.Id, body, c.auth)
	if err != nil {
		return c.unreachable(ctx, err)
	}
	if res.JSON200 == nil {
		return c.failed(res.HTTPResponse, res.Body)
	}
	if g.json {
		return writeJSON(a.stdout, res.JSON200)
	}
	if res.JSON200.Name != grp.Name {
		fmt.Fprintf(a.stdout, "Renamed group %s to %s.\n", grp.Name, res.JSON200.Name)
	} else {
		fmt.Fprintf(a.stdout, "Updated group %s.\n", grp.Name)
	}
	return nil
}

func groupDelete(ctx context.Context, a *app, args []string) error {
	fs := newFlagSet(a, "group delete", "Usage: wardlock group delete NAME [--yes] [flags]\n\n"+
		"Deletes the group. Its sites are kept and become ungrouped. Asks you\n"+
		"to type the group's name first; without a terminal, or with --json,\n"+
		"pass --yes instead. Needs a token with the manage scope.")
	g := addGlobalFlags(fs)
	yes := fs.Bool("yes", false, "delete without asking")
	var name string
	c, _, err := orgConn(a, fs, g, args, 1, 1, func(pos []string) (err error) {
		if name, err = checkGroupName(pos[0]); err != nil {
			return err
		}
		if !*yes && (g.json || !a.canPrompt) {
			return usageErrorf("pass --yes to delete a group when there is no terminal to ask in, or with --json")
		}
		return nil
	})
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	grp, err := c.findGroup(ctx, name)
	if err != nil {
		return err
	}
	if !*yes {
		ok, err := a.confirm(fmt.Sprintf("This deletes the group %s; its sites are kept, ungrouped.\nType the group name to confirm: ", grp.Name), grp.Name)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("not confirmed: nothing was deleted")
		}
	}
	res, err := c.api.DeleteGroupWithResponse(ctx, grp.Id, c.auth)
	if err != nil {
		return c.unreachable(ctx, err)
	}
	if res.StatusCode() != http.StatusNoContent {
		return c.failed(res.HTTPResponse, res.Body)
	}
	if g.json {
		return writeJSON(a.stdout, groupDeleteResult{Deleted: true, Group: grp})
	}
	fmt.Fprintf(a.stdout, "Deleted group %s. Its sites are kept, ungrouped.\n", grp.Name)
	return nil
}
