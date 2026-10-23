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

// wardlock tag (V5.15): list, create, rename and delete the owner's tags.
// wardlock site organise and wardlock deploy --tag put them on sites.

const tagUsage = "Usage: wardlock tag <list|create|rename|delete> [NAME] [NEW_NAME] [flags]\n\n" +
	"  list                       List your tags with their colours\n" +
	"  create NAME [--colour N]   Create a tag\n" +
	"  rename NAME NEW_NAME       Rename a tag; --colour changes its colour\n" +
	"  delete NAME                Delete a tag and remove it from its sites\n\n" +
	"Tag names are 1-30 lowercase letters, digits and hyphens. Colours are\n" +
	"0 (neutral) to 8. Listing works with any token; the others need a\n" +
	"token with the manage scope.\n"

func runTag(ctx context.Context, a *app, args []string) error {
	global, rest, err := leadingGlobalFlags(args)
	if err != nil {
		return err
	}
	if len(rest) == 0 {
		fmt.Fprint(a.stderr, tagUsage)
		return errUsage
	}
	sub, args := rest[0], append(global, rest[1:]...)
	switch sub {
	case "list":
		return tagList(ctx, a, args)
	case "create":
		return tagCreate(ctx, a, args)
	case "rename":
		return tagRename(ctx, a, args)
	case "delete":
		return tagDelete(ctx, a, args)
	case "help", "--help", "-h":
		fmt.Fprint(a.stdout, tagUsage)
		return nil
	}
	fmt.Fprintf(a.stderr, "wardlock: unknown tag command %q\n\n%s", sub, tagUsage)
	return errUsage
}

type tagListResult struct {
	Tags []apiclient.Tag `json:"tags"`
}

type tagDeleteResult struct {
	Deleted bool          `json:"deleted"`
	Tag     apiclient.Tag `json:"tag"`
}

// colourFlag adds --colour and returns a function giving its value: nil
// when not given, a usage error when not 0-8.
func colourFlag(fs *flag.FlagSet, usage string) func() (*int32, error) {
	colour := fs.Int("colour", 0, usage)
	return func() (*int32, error) {
		given := false
		fs.Visit(func(f *flag.Flag) { given = given || f.Name == "colour" })
		if !given {
			return nil, nil
		}
		if *colour < 0 || *colour > 8 {
			return nil, usageErrorf("--colour %d: use 0 (neutral) to 8", *colour)
		}
		return ptr(int32(*colour)), nil
	}
}

func tagList(ctx context.Context, a *app, args []string) error {
	fs := newFlagSet(a, "tag list", "Usage: wardlock tag list [flags]\n\nLists your tags by name, with their colour slots.")
	g := addGlobalFlags(fs)
	c, _, err := orgConn(a, fs, g, args, 0, 0, nil)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	tags, err := c.tags(ctx)
	if err != nil {
		return err
	}
	if g.json {
		return writeJSON(a.stdout, tagListResult{Tags: tags})
	}
	if len(tags) == 0 {
		fmt.Fprintln(a.stdout, "No tags yet. Create one with wardlock tag create NAME.")
		return nil
	}
	tw := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TAG\tCOLOUR")
	for _, t := range tags {
		fmt.Fprintf(tw, "%s\t%d\n", t.Name, t.Colour)
	}
	return tw.Flush()
}

func tagCreate(ctx context.Context, a *app, args []string) error {
	fs := newFlagSet(a, "tag create", "Usage: wardlock tag create NAME [--colour N] [flags]\n\n"+
		"Creates a tag of 1-30 lowercase letters, digits and hyphens. Without\n"+
		"--colour it takes the first colour from 1 to 8 none of your tags has,\n"+
		"or 0 (neutral) when all are taken. Needs a token with the manage\n"+
		"scope.")
	g := addGlobalFlags(fs)
	colour := colourFlag(fs, "the tag's colour, 0 (neutral) to 8")
	body := apiclient.CreateTagJSONRequestBody{}
	c, _, err := orgConn(a, fs, g, args, 1, 1, func(pos []string) (err error) {
		body.Name = pos[0]
		if err := checkTagName(body.Name); err != nil {
			return err
		}
		body.Colour, err = colour()
		return err
	})
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	res, err := c.api.CreateTagWithResponse(ctx, body, c.auth)
	if err != nil {
		return c.unreachable(ctx, err)
	}
	if res.JSON201 == nil {
		return c.failed(res.HTTPResponse, res.Body)
	}
	if g.json {
		return writeJSON(a.stdout, res.JSON201)
	}
	fmt.Fprintf(a.stdout, "Created tag %s (colour %d).\n", res.JSON201.Name, res.JSON201.Colour)
	return nil
}

func tagRename(ctx context.Context, a *app, args []string) error {
	fs := newFlagSet(a, "tag rename", "Usage: wardlock tag rename NAME [NEW_NAME] [--colour N] [flags]\n\n"+
		"Renames the tag NAME to NEW_NAME, or changes its colour with --colour.\n"+
		"Its sites keep it. Needs a token with the manage scope.")
	g := addGlobalFlags(fs)
	colour := colourFlag(fs, "the tag's new colour, 0 (neutral) to 8")
	var name string
	body := apiclient.UpdateTagJSONRequestBody{}
	c, _, err := orgConn(a, fs, g, args, 1, 2, func(pos []string) (err error) {
		name = pos[0]
		if err := checkTagName(name); err != nil {
			return err
		}
		if len(pos) == 2 {
			if err := checkTagName(pos[1]); err != nil {
				return err
			}
			body.Name = &pos[1]
		}
		if body.Colour, err = colour(); err != nil {
			return err
		}
		if body.Name == nil && body.Colour == nil {
			return usageErrorf("nothing to change: pass NEW_NAME or --colour")
		}
		return nil
	})
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	found, err := c.findTags(ctx, []string{name})
	if err != nil {
		return err
	}
	res, err := c.api.UpdateTagWithResponse(ctx, found[0].Id, body, c.auth)
	if err != nil {
		return c.unreachable(ctx, err)
	}
	if res.JSON200 == nil {
		return c.failed(res.HTTPResponse, res.Body)
	}
	if g.json {
		return writeJSON(a.stdout, res.JSON200)
	}
	if res.JSON200.Name != name {
		fmt.Fprintf(a.stdout, "Renamed tag %s to %s (colour %d).\n", name, res.JSON200.Name, res.JSON200.Colour)
	} else {
		fmt.Fprintf(a.stdout, "Updated tag %s (colour %d).\n", name, res.JSON200.Colour)
	}
	return nil
}

func tagDelete(ctx context.Context, a *app, args []string) error {
	fs := newFlagSet(a, "tag delete", "Usage: wardlock tag delete NAME [--yes] [flags]\n\n"+
		"Deletes the tag and removes it from every site that has it. Asks you\n"+
		"to type the tag's name first; without a terminal, or with --json, pass\n"+
		"--yes instead. Needs a token with the manage scope.")
	g := addGlobalFlags(fs)
	yes := fs.Bool("yes", false, "delete without asking")
	var name string
	c, _, err := orgConn(a, fs, g, args, 1, 1, func(pos []string) error {
		name = pos[0]
		if err := checkTagName(name); err != nil {
			return err
		}
		if !*yes && (g.json || !a.canPrompt) {
			return usageErrorf("pass --yes to delete a tag when there is no terminal to ask in, or with --json")
		}
		return nil
	})
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	found, err := c.findTags(ctx, []string{name})
	if err != nil {
		return err
	}
	tag := found[0]
	if !*yes {
		ok, err := a.confirm(fmt.Sprintf("This deletes the tag %s and removes it from every site that has it.\nType the tag name to confirm: ", tag.Name), tag.Name)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("not confirmed: nothing was deleted")
		}
	}
	res, err := c.api.DeleteTagWithResponse(ctx, tag.Id, c.auth)
	if err != nil {
		return c.unreachable(ctx, err)
	}
	if res.StatusCode() != http.StatusNoContent {
		return c.failed(res.HTTPResponse, res.Body)
	}
	if g.json {
		return writeJSON(a.stdout, tagDeleteResult{Deleted: true, Tag: tag})
	}
	fmt.Fprintf(a.stdout, "Deleted tag %s and removed it from its sites.\n", tag.Name)
	return nil
}
