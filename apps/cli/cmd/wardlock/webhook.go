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

// wardlock webhook (I6.3): register endpoints that receive signed events,
// and see how deliveries went. Everything here needs the manage scope.

const webhookUsage = "Usage: wardlock webhook <add|list|remove|test|deliveries> [URL|ID] [flags]\n\n" +
	"  add URL [--event E]... [--site S]...   Register a webhook and print its signing secret once\n" +
	"  list                                   List your webhooks\n" +
	"  remove ID                              Delete a webhook\n" +
	"  test ID                                Send a webhook a ping\n" +
	"  deliveries ID                          List a webhook's recent deliveries\n\n" +
	"All of these need a token with the manage scope.\n"

func runWebhook(ctx context.Context, a *app, args []string) error {
	global, rest, err := leadingGlobalFlags(args)
	if err != nil {
		return err
	}
	if len(rest) == 0 {
		fmt.Fprint(a.stderr, webhookUsage)
		return errUsage
	}
	sub, args := rest[0], append(global, rest[1:]...)
	switch sub {
	case "add":
		return webhookAdd(ctx, a, args)
	case "list":
		return webhookList(ctx, a, args)
	case "remove":
		return webhookRemove(ctx, a, args)
	case "test":
		return webhookTest(ctx, a, args)
	case "deliveries":
		return webhookDeliveries(ctx, a, args)
	case "help", "--help", "-h":
		fmt.Fprint(a.stdout, webhookUsage)
		return nil
	}
	fmt.Fprintf(a.stderr, "wardlock: unknown webhook command %q\n\n%s", sub, webhookUsage)
	return errUsage
}

type webhookListResult struct {
	Webhooks []apiclient.Webhook `json:"webhooks"`
}

type webhookRemoveResult struct {
	Removed bool   `json:"removed"`
	ID      string `json:"id"`
}

type webhookDeliveriesResult struct {
	Deliveries []apiclient.WebhookDelivery `json:"deliveries"`
}

func webhookAdd(ctx context.Context, a *app, args []string) error {
	fs := newFlagSet(a, "webhook add", "Usage: wardlock webhook add URL [--event E]... [--site S]... [--description D] [flags]\n\n"+
		"Registers URL to receive signed JSON events and prints the signing secret,\n"+
		"which cannot be shown again. URL must use https. Without --event it gets\n"+
		"every event, and without --site events from every site. Needs a token\n"+
		"with the manage scope.")
	g := addGlobalFlags(fs)
	events := stringsFlag(fs, "event", "send only this event, such as deploy.activated (repeatable)")
	sites := stringsFlag(fs, "site", "send only events from this site (repeatable)")
	description := fs.String("description", "", "what the webhook is for")
	c, pos, err := orgConn(a, fs, g, args, 1, 1, nil)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	body := apiclient.CreateWebhookJSONRequestBody{Url: pos[0]}
	if *description != "" {
		body.Description = description
	}
	if len(*events) > 0 {
		list := make([]apiclient.WebhookEvent, len(*events))
		for i, e := range *events {
			list[i] = apiclient.WebhookEvent(e)
		}
		body.Events = &list
	}
	if len(*sites) > 0 {
		body.Sites = sites
	}
	res, err := c.api.CreateWebhookWithResponse(ctx, body, c.auth)
	if err != nil {
		return c.unreachable(ctx, err)
	}
	if res.JSON201 == nil {
		return c.failed(res.HTTPResponse, res.Body)
	}
	if g.json {
		return writeJSON(a.stdout, res.JSON201)
	}
	fmt.Fprintf(a.stdout, "Created webhook %s for %s.\nSigning secret, shown only this once:\n\n  %s\n",
		res.JSON201.Webhook.Id, res.JSON201.Webhook.Url, res.JSON201.Secret)
	return nil
}

// webhookStatus says whether w is on and, if not, why.
func webhookStatus(w apiclient.Webhook) string {
	switch {
	case w.DisabledReason != nil && *w.DisabledReason == apiclient.WebhookDisabledReasonFailing:
		return "disabled (failing)"
	case !w.Enabled:
		return "disabled"
	case w.FailingSince != nil:
		return "failing"
	}
	return "active"
}

// scopeList is a webhook's events or sites for a table.
func scopeList[T ~string](list *[]T) string {
	if list == nil {
		return "all"
	}
	names := make([]string, len(*list))
	for i, v := range *list {
		names[i] = string(v)
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ",")
}

func webhookList(ctx context.Context, a *app, args []string) error {
	fs := newFlagSet(a, "webhook list", "Usage: wardlock webhook list [flags]\n\nLists your webhooks, oldest first. Needs a token with the manage scope.")
	g := addGlobalFlags(fs)
	c, _, err := orgConn(a, fs, g, args, 0, 0, nil)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	res, err := c.api.ListWebhooksWithResponse(ctx, c.auth)
	if err != nil {
		return c.unreachable(ctx, err)
	}
	if res.JSON200 == nil {
		return c.failed(res.HTTPResponse, res.Body)
	}
	list := res.JSON200.Items
	if g.json {
		return writeJSON(a.stdout, webhookListResult{Webhooks: list})
	}
	if len(list) == 0 {
		fmt.Fprintln(a.stdout, "No webhooks yet. Add one with wardlock webhook add URL.")
		return nil
	}
	tw := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tURL\tSTATUS\tEVENTS\tSITES\tDESCRIPTION")
	for _, w := range list {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", w.Id, w.Url, webhookStatus(w), scopeList(w.Events), scopeList(w.Sites), w.Description)
	}
	return tw.Flush()
}

func webhookRemove(ctx context.Context, a *app, args []string) error {
	fs := newFlagSet(a, "webhook remove", "Usage: wardlock webhook remove ID [--yes] [flags]\n\n"+
		"Deletes the webhook and its deliveries; anything queued for it is not\n"+
		"sent. Asks you to type the ID first; without a terminal, or with --json,\n"+
		"pass --yes instead. Needs a token with the manage scope.")
	g := addGlobalFlags(fs)
	yes := fs.Bool("yes", false, "delete without asking")
	c, pos, err := orgConn(a, fs, g, args, 1, 1, func([]string) error {
		if !*yes && (g.json || !a.canPrompt) {
			return usageErrorf("pass --yes to remove a webhook when there is no terminal to ask in, or with --json")
		}
		return nil
	})
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	id := pos[0]
	if !*yes {
		got, err := c.api.GetWebhookWithResponse(ctx, id, c.auth)
		if err != nil {
			return c.unreachable(ctx, err)
		}
		if got.JSON200 == nil {
			return c.failed(got.HTTPResponse, got.Body)
		}
		ok, err := a.confirm(fmt.Sprintf("This deletes the webhook to %s.\nType its ID to confirm: ", got.JSON200.Url), id)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("not confirmed: nothing was deleted")
		}
	}
	res, err := c.api.DeleteWebhookWithResponse(ctx, id, c.auth)
	if err != nil {
		return c.unreachable(ctx, err)
	}
	if res.StatusCode() != http.StatusNoContent {
		return c.failed(res.HTTPResponse, res.Body)
	}
	if g.json {
		return writeJSON(a.stdout, webhookRemoveResult{Removed: true, ID: id})
	}
	fmt.Fprintf(a.stdout, "Removed webhook %s.\n", id)
	return nil
}

func webhookTest(ctx context.Context, a *app, args []string) error {
	fs := newFlagSet(a, "webhook test", "Usage: wardlock webhook test ID [flags]\n\n"+
		"Queues a ping to the webhook. wardlock webhook deliveries ID shows how it\n"+
		"went. Needs a token with the manage scope.")
	g := addGlobalFlags(fs)
	c, pos, err := orgConn(a, fs, g, args, 1, 1, nil)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	res, err := c.api.TestWebhookWithResponse(ctx, pos[0], c.auth)
	if err != nil {
		return c.unreachable(ctx, err)
	}
	if res.JSON202 == nil {
		return c.failed(res.HTTPResponse, res.Body)
	}
	if g.json {
		return writeJSON(a.stdout, res.JSON202)
	}
	fmt.Fprintf(a.stdout, "Queued ping %s. See how it went with wardlock webhook deliveries %s.\n", res.JSON202.Id, pos[0])
	return nil
}

func webhookDeliveries(ctx context.Context, a *app, args []string) error {
	fs := newFlagSet(a, "webhook deliveries", "Usage: wardlock webhook deliveries ID [flags]\n\n"+
		"Lists the webhook's newest 50 deliveries, newest first, each with its\n"+
		"latest attempt. Needs a token with the manage scope.")
	g := addGlobalFlags(fs)
	c, pos, err := orgConn(a, fs, g, args, 1, 1, nil)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	res, err := c.api.ListWebhookDeliveriesWithResponse(ctx, pos[0], c.auth)
	if err != nil {
		return c.unreachable(ctx, err)
	}
	if res.JSON200 == nil {
		return c.failed(res.HTTPResponse, res.Body)
	}
	list := res.JSON200.Items
	if g.json {
		return writeJSON(a.stdout, webhookDeliveriesResult{Deliveries: list})
	}
	if len(list) == 0 {
		fmt.Fprintln(a.stdout, "No deliveries yet. Send one with wardlock webhook test "+pos[0]+".")
		return nil
	}
	tw := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tEVENT\tSTATUS\tATTEMPTS\tRESPONSE\tDURATION\tCREATED")
	for _, d := range list {
		response := "-"
		switch {
		case d.ResponseCode != nil:
			response = strconv.Itoa(int(*d.ResponseCode))
		case d.Error != nil:
			response = string(*d.Error)
		}
		duration := "-"
		if d.DurationMs != nil {
			duration = strconv.FormatInt(*d.DurationMs, 10) + "ms"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\t%s\n", d.Id, d.Type, d.Status, d.Attempts, response, duration, formatTime(d.CreatedAt))
	}
	return tw.Flush()
}
