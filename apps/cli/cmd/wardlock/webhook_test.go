// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"strings"
	"testing"

	"wardlock.nz/apiclient"
)

func TestWebhookCommands(t *testing.T) {
	srv := newFakeAPI(t)
	a, out, errOut := ciApp(t, srv)
	ctx := t.Context()
	if err := run(ctx, a, []string{"webhook", "list"}); err != nil || !strings.Contains(out.String(), "No webhooks yet") {
		t.Fatalf("%v: %s", err, out)
	}

	out.Reset()
	err := run(ctx, a, []string{"webhook", "add", "https://hooks.example.test/w", "--event", "deploy.activated", "--event", "link.created", "--site", "docs", "--description", "Deploys"})
	if err != nil {
		t.Fatalf("add: %v\n%s", err, errOut)
	}
	if !strings.Contains(out.String(), testWebhookSecret) || !strings.Contains(out.String(), "only this once") {
		t.Fatalf("add printed:\n%s", out)
	}
	hook := srv.webhooks[0]
	if hook.Description != "Deploys" || len(*hook.Events) != 2 || (*hook.Sites)[0] != "docs" {
		t.Fatalf("sent %+v", hook)
	}

	out.Reset()
	if err := run(ctx, a, []string{"webhook", "add", "https://all.example.test/", "--json"}); err != nil {
		t.Fatalf("add --json: %v\n%s", err, errOut)
	}
	assertJSONKeys(t, out.Bytes(), "webhook", "secret")
	if srv.webhooks[1].Events != nil || srv.webhooks[1].Sites != nil {
		t.Fatalf("no flags sent a scope: %+v", srv.webhooks[1])
	}

	// The secret is shown when created and never by anything else.
	out.Reset()
	if err := run(ctx, a, []string{"webhook", "list"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"https://hooks.example.test/w", "deploy.activated,link.created", "docs", "Deploys", "active"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("list lacks %q:\n%s", want, out)
		}
	}
	if !regexpLine(out.String(), `.*https://all\.example\.test/\s+active\s+all\s+all`) {
		t.Errorf("list of an all-events webhook:\n%s", out)
	}
	if strings.Contains(out.String(), testWebhookSecret) {
		t.Errorf("list shows the secret:\n%s", out)
	}
	out.Reset()
	if err := run(ctx, a, []string{"--json", "webhook", "list"}); err != nil {
		t.Fatal(err)
	}
	m := assertJSONKeys(t, out.Bytes(), "webhooks")
	var list []apiclient.Webhook
	if err := json.Unmarshal(m["webhooks"], &list); err != nil || len(list) != 2 {
		t.Fatalf("%v: %s", err, out)
	}

	out.Reset()
	if err := run(ctx, a, []string{"webhook", "deliveries", hook.Id}); err != nil || !strings.Contains(out.String(), "No deliveries yet") {
		t.Fatalf("%v: %s", err, out)
	}
	out.Reset()
	if err := run(ctx, a, []string{"webhook", "test", hook.Id}); err != nil || !strings.Contains(out.String(), "Queued ping") {
		t.Fatalf("test: %v: %s", err, out)
	}
	code, class, took := int32(204), apiclient.WebhookDeliveryErrorTimeout, int64(120)
	srv.deliveries = append(srv.deliveries,
		apiclient.WebhookDelivery{Id: "d-ok", Type: "deploy.activated", Status: apiclient.WebhookDeliveryStatusSucceeded, Attempts: 1, ResponseCode: &code, DurationMs: &took, CreatedAt: fakeDeployedAt},
		apiclient.WebhookDelivery{Id: "d-late", Type: "ping", Status: apiclient.WebhookDeliveryStatusFailed, Attempts: 9, Error: &class, DurationMs: &took, CreatedAt: fakeDeployedAt})
	out.Reset()
	if err := run(ctx, a, []string{"webhook", "deliveries", hook.Id}); err != nil {
		t.Fatal(err)
	}
	if !regexpLine(out.String(), `d-ok\s+deploy\.activated\s+succeeded\s+1\s+204\s+120ms\s+.*`) ||
		!regexpLine(out.String(), `d-late\s+ping\s+failed\s+9\s+timeout\s+120ms\s+.*`) {
		t.Fatalf("deliveries:\n%s", out)
	}
	out.Reset()
	if err := run(ctx, a, []string{"webhook", "deliveries", hook.Id, "--json"}); err != nil {
		t.Fatal(err)
	}
	assertJSONKeys(t, out.Bytes(), "deliveries")

	out.Reset()
	if err := run(ctx, a, []string{"webhook", "remove", hook.Id, "--yes"}); err != nil || !strings.Contains(out.String(), "Removed webhook") {
		t.Fatalf("remove: %v: %s", err, out)
	}
	if len(srv.webhooks) != 1 {
		t.Fatalf("left %+v", srv.webhooks)
	}
}

func TestWebhookErrors(t *testing.T) {
	srv := newFakeAPI(t)
	a, out, errOut := ciApp(t, srv)
	ctx := t.Context()
	for name, c := range map[string]struct {
		args []string
		code int
		want string
	}{
		"plain http":      {[]string{"webhook", "add", "http://hooks.example.test/"}, 1, "url must use https"},
		"unknown webhook": {[]string{"webhook", "test", "nope"}, 1, "Not found"},
		"no url":          {[]string{"webhook", "add"}, 2, "Usage: wardlock webhook add"},
		"remove unasked":  {[]string{"webhook", "remove", "x"}, 2, "pass --yes"},
		"unknown command": {[]string{"webhook", "edit"}, 2, "unknown webhook command"},
		"no command":      {[]string{"webhook"}, 2, "Usage: wardlock webhook"},
	} {
		t.Run(name, func(t *testing.T) {
			out.Reset()
			errOut.Reset()
			err := run(ctx, a, c.args)
			if code := exitCode(errOut, err); code != c.code || !strings.Contains(errOut.String(), c.want) {
				t.Fatalf("exit %d, stderr %q", code, errOut)
			}
		})
	}

	t.Run("disabled webhook", func(t *testing.T) {
		srv.webhooks = []apiclient.Webhook{{Id: "off", Url: "https://x.example.test/", Enabled: false}}
		errOut.Reset()
		err := run(ctx, a, []string{"webhook", "test", "off"})
		if code := exitCode(errOut, err); code != 1 || !strings.Contains(errOut.String(), "turned off") {
			t.Fatalf("exit %d, stderr %q", code, errOut)
		}
	})
	t.Run("token without manage", func(t *testing.T) {
		srv.scopes = []string{"deploy"}
		errOut.Reset()
		err := run(ctx, a, []string{"webhook", "list"})
		if code := exitCode(errOut, err); code != 1 || !strings.Contains(errOut.String(), "manage") {
			t.Fatalf("exit %d, stderr %q", code, errOut)
		}
	})
}
