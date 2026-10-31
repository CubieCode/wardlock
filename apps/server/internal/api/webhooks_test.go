// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"wardlock.nz/server/internal/api"
	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/notify"
)

const hookURL = "https://hooks.example.test/wardlock"

func (hs *harness) createWebhook(owner string, body map[string]any) api.CreatedWebhook {
	hs.t.Helper()
	res := hs.do(owner, http.MethodPost, "/webhooks", body)
	res.want(hs.t, http.StatusCreated, "")
	if cc := res.header.Get("Cache-Control"); cc != "no-store" {
		hs.t.Fatalf("create Cache-Control %q", cc)
	}
	var c api.CreatedWebhook
	res.decode(hs.t, &c)
	return c
}

func (hs *harness) webhook(owner, id string) api.Webhook {
	hs.t.Helper()
	res := hs.do(owner, http.MethodGet, "/webhooks/"+id, nil)
	res.want(hs.t, http.StatusOK, "")
	var w api.Webhook
	res.decode(hs.t, &w)
	return w
}

// queuedEvents returns the bodies queued for a webhook, oldest first.
func (hs *harness) queuedEvents(owner, id string) []string {
	hs.t.Helper()
	list, err := hs.db.Webhooks().Deliveries(hs.t.Context(), owner, id, 100)
	if err != nil {
		hs.t.Fatal(err)
	}
	var bodies []string
	for _, d := range list {
		bodies = append(bodies, d.Body)
	}
	slices.Reverse(bodies)
	return bodies
}

func TestWebhookLifecycle(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	a := hs.ownerA
	hs.createSite(a, "docs")

	var page api.WebhookList
	res := hs.do(a, http.MethodGet, "/webhooks", nil)
	res.want(t, http.StatusOK, "")
	res.decode(t, &page)
	if page.Items == nil || len(page.Items) != 0 {
		t.Fatalf("empty list = %s", res.body)
	}

	c := hs.createWebhook(a, map[string]any{
		"url": hookURL, "description": "Deploys", "events": []string{"deploy.activated"}, "sites": []string{"docs"},
	})
	if !strings.HasPrefix(c.Secret, "wlwh_") || len(c.Secret) != 48 {
		t.Fatalf("secret %q", c.Secret)
	}
	w := c.Webhook
	if w.Url != hookURL || w.Description != "Deploys" || !w.Enabled || w.Events == nil || (*w.Events)[0] != "deploy.activated" ||
		w.Sites == nil || (*w.Sites)[0] != "docs" || w.DisabledReason != nil {
		t.Fatalf("created %+v", w)
	}

	// The secret never comes back.
	for _, path := range []string{"/webhooks", "/webhooks/" + w.Id, "/webhooks/" + w.Id + "/deliveries"} {
		res := hs.do(a, http.MethodGet, path, nil)
		res.want(t, http.StatusOK, "")
		if strings.Contains(string(res.body), c.Secret) || strings.Contains(string(res.body), "secret") {
			t.Fatalf("%s shows a secret: %s", path, res.body)
		}
	}

	// A ping is queued and answered at once.
	res = hs.do(a, http.MethodPost, "/webhooks/"+w.Id+"/test", nil)
	res.want(t, http.StatusAccepted, "")
	var ping api.WebhookDelivery
	res.decode(t, &ping)
	if ping.Type != notify.EventPing || ping.Status != api.WebhookDeliveryStatusPending {
		t.Fatalf("ping %+v", ping)
	}
	res = hs.do(a, http.MethodGet, "/webhooks/"+w.Id+"/deliveries", nil)
	res.want(t, http.StatusOK, "")
	var ds api.WebhookDeliveryList
	res.decode(t, &ds)
	if len(ds.Items) != 1 || ds.Items[0].Id != ping.Id {
		t.Fatalf("deliveries %+v", ds)
	}
	var fields map[string]any
	var raw struct{ Items []map[string]any }
	if err := json.Unmarshal(res.body, &raw); err != nil {
		t.Fatal(err)
	}
	fields = raw.Items[0]
	if _, ok := fields["body"]; ok {
		t.Fatalf("a delivery shows its body: %s", res.body)
	}

	res = hs.do(a, http.MethodPost, "/webhooks/"+w.Id+"/deliveries/"+ping.Id+"/redeliver", nil)
	res.want(t, http.StatusAccepted, "")
	var again api.WebhookDelivery
	res.decode(t, &again)
	if again.EventId != ping.EventId || again.RedeliveryOf == nil || *again.RedeliveryOf != ping.Id {
		t.Fatalf("redelivery %+v of %+v", again, ping)
	}

	// Turned off, nothing can be sent; turned on, it can.
	res = hs.do(a, http.MethodPatch, "/webhooks/"+w.Id, map[string]any{"enabled": false})
	res.want(t, http.StatusOK, "")
	var off api.Webhook
	res.decode(t, &off)
	if off.Enabled || off.DisabledReason == nil || *off.DisabledReason != api.WebhookDisabledReasonOwner || off.DisabledAt == nil {
		t.Fatalf("off %+v", off)
	}
	hs.do(a, http.MethodPost, "/webhooks/"+w.Id+"/test", nil).want(t, http.StatusConflict, "webhook_disabled")
	hs.do(a, http.MethodPost, "/webhooks/"+w.Id+"/deliveries/"+ping.Id+"/redeliver", nil).want(t, http.StatusConflict, "webhook_disabled")
	hs.do(a, http.MethodPatch, "/webhooks/"+w.Id, map[string]any{"enabled": true}).want(t, http.StatusOK, "")

	res = hs.do(a, http.MethodPatch, "/webhooks/"+w.Id, map[string]any{"allEvents": true, "allSites": true, "description": "All"})
	res.want(t, http.StatusOK, "")
	var wide api.Webhook
	res.decode(t, &wide)
	if !wide.Enabled || wide.Events != nil || wide.Sites != nil || wide.Description != "All" || wide.Url != hookURL {
		t.Fatalf("widened %+v", wide)
	}

	hs.do(a, http.MethodDelete, "/webhooks/"+w.Id, nil).want(t, http.StatusNoContent, "")
	hs.do(a, http.MethodDelete, "/webhooks/"+w.Id, nil).want(t, http.StatusNotFound, "not_found")
}

func TestWebhookRequestErrors(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	a := hs.ownerA
	w := hs.createWebhook(a, map[string]any{"url": hookURL}).Webhook
	for name, c := range map[string]struct {
		method, path string
		body         any
		status       int
		code         string
	}{
		"no url":            {http.MethodPost, "/webhooks", map[string]any{}, 400, "invalid_request"},
		"plain http":        {http.MethodPost, "/webhooks", map[string]any{"url": "http://hooks.example.test/"}, 400, "invalid_request"},
		"loopback":          {http.MethodPost, "/webhooks", map[string]any{"url": "https://127.0.0.1/"}, 400, "invalid_request"},
		"unknown event":     {http.MethodPost, "/webhooks", map[string]any{"url": hookURL, "events": []string{"nope"}}, 400, "invalid_request"},
		"both events":       {http.MethodPatch, "/webhooks/" + w.Id, map[string]any{"allEvents": true, "events": []string{"site.created"}}, 400, "invalid_request"},
		"both sites":        {http.MethodPatch, "/webhooks/" + w.Id, map[string]any{"allSites": true, "sites": []string{"docs"}}, 400, "invalid_request"},
		"bad update":        {http.MethodPatch, "/webhooks/" + w.Id, map[string]any{"url": "nope"}, 400, "invalid_request"},
		"missing site":      {http.MethodPost, "/webhooks", map[string]any{"url": hookURL, "sites": []string{"missing"}}, 404, "not_found"},
		"missing webhook":   {http.MethodGet, "/webhooks/01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e30", nil, 404, "not_found"},
		"not an ID":         {http.MethodGet, "/webhooks/nope", nil, 404, "not_found"},
		"missing delivery":  {http.MethodPost, "/webhooks/" + w.Id + "/deliveries/nope/redeliver", nil, 404, "not_found"},
		"no owner":          {http.MethodGet, "/webhooks", nil, 401, "unauthenticated"},
		"no owner, create":  {http.MethodPost, "/webhooks", map[string]any{"url": hookURL}, 401, "unauthenticated"},
		"no owner, ping":    {http.MethodPost, "/webhooks/" + w.Id + "/test", nil, 401, "unauthenticated"},
		"no owner, listing": {http.MethodGet, "/webhooks/" + w.Id + "/deliveries", nil, 401, "unauthenticated"},
	} {
		t.Run(name, func(t *testing.T) {
			owner := a
			if strings.HasPrefix(name, "no owner") {
				owner = ""
			}
			hs.do(owner, c.method, c.path, c.body).want(t, c.status, c.code)
		})
	}

	for range notify.MaxWebhooks - 1 {
		hs.createWebhook(a, map[string]any{"url": hookURL})
	}
	hs.do(a, http.MethodPost, "/webhooks", map[string]any{"url": hookURL}).want(t, http.StatusRequestEntityTooLarge, "limit_exceeded")
}

// TestWebhookOwnerIsolation sends owner B at each of owner A's resources:
// each answers exactly as for a missing one and changes nothing.
func TestWebhookOwnerIsolation(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	a, b := hs.ownerA, hs.ownerB
	hs.createSite(a, "docs")
	w := hs.createWebhook(a, map[string]any{"url": hookURL, "description": "Mine"}).Webhook
	res := hs.do(a, http.MethodPost, "/webhooks/"+w.Id+"/test", nil)
	res.want(t, http.StatusAccepted, "")
	var ping api.WebhookDelivery
	res.decode(t, &ping)

	missing := "/webhooks/01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e30"
	for name, c := range map[string]struct {
		method, suffix string
		body           any
	}{
		"get":        {http.MethodGet, "", nil},
		"update":     {http.MethodPatch, "", map[string]any{"description": "Stolen"}},
		"delete":     {http.MethodDelete, "", nil},
		"test":       {http.MethodPost, "/test", nil},
		"deliveries": {http.MethodGet, "/deliveries", nil},
		"redeliver":  {http.MethodPost, "/deliveries/" + ping.Id + "/redeliver", nil},
	} {
		t.Run(name, func(t *testing.T) {
			theirs := hs.do(b, c.method, "/webhooks/"+w.Id+c.suffix, c.body)
			nothing := hs.do(b, c.method, missing+c.suffix, c.body)
			theirs.want(t, http.StatusNotFound, "not_found")
			if string(theirs.body) != string(nothing.body) {
				t.Fatalf("%s differs from %s", theirs.body, nothing.body)
			}
		})
	}
	res = hs.do(b, http.MethodGet, "/webhooks", nil)
	res.want(t, http.StatusOK, "")
	var page api.WebhookList
	res.decode(t, &page)
	if len(page.Items) != 0 {
		t.Fatalf("B lists %+v", page)
	}
	// Scoping a webhook to A's site is the same as naming a missing one.
	hs.do(b, http.MethodPost, "/webhooks", map[string]any{"url": hookURL, "sites": []string{"docs"}}).want(t, http.StatusNotFound, "not_found")
	mine := hs.createWebhook(b, map[string]any{"url": hookURL}).Webhook
	hs.do(b, http.MethodPatch, "/webhooks/"+mine.Id, map[string]any{"sites": []string{"docs"}}).want(t, http.StatusNotFound, "not_found")
	// One of A's delivery IDs under B's own webhook.
	hs.do(b, http.MethodPost, "/webhooks/"+mine.Id+"/deliveries/"+ping.Id+"/redeliver", nil).want(t, http.StatusNotFound, "not_found")

	if got := hs.webhook(a, w.Id); got.Description != "Mine" {
		t.Fatalf("A's webhook after B: %+v", got)
	}
	if n := len(hs.queuedEvents(a, w.Id)); n != 1 {
		t.Fatalf("A's webhook has %d deliveries", n)
	}
}

// TestEventsFromTheServices drives each event through the API and the
// services behind it, and reads what was queued: the real payloads, only
// for the owner they belong to, and none with a secret in it.
func TestEventsFromTheServices(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	a, b := hs.ownerA, hs.ownerB
	mine := hs.createWebhook(a, map[string]any{"url": hookURL}).Webhook
	theirs := hs.createWebhook(b, map[string]any{"url": hookURL}).Webhook

	site := hs.createSite(a, "docs")
	first := hs.deploy(a, "docs", file{"index.html", "<p>one</p>"})
	second := hs.deploy(a, "docs", file{"index.html", "<p>two</p>"})
	hs.do(a, http.MethodPost, "/sites/docs/rollback", nil).want(t, http.StatusOK, "")
	preview := hs.previewDeploy(a, "docs", "pr-1", api.CreateDeployRequest{}, file{"index.html", "<p>three</p>"})
	hs.do(a, http.MethodPost, "/sites/docs/previews/pr-1/promote", nil).want(t, http.StatusOK, "")
	hs.do(a, http.MethodDelete, "/sites/docs/previews/pr-1", nil).want(t, http.StatusNoContent, "")
	link := hs.createLink(a, "docs", map[string]any{"label": "Client", "path": "/"})
	hs.do(a, http.MethodDelete, "/sites/docs/links/"+link.ShareLink.PublicId, nil).want(t, http.StatusNoContent, "")
	hs.do(a, http.MethodDelete, "/sites/docs", nil).want(t, http.StatusNoContent, "")

	if got := hs.queuedEvents(b, theirs.Id); len(got) != 0 {
		t.Fatalf("another owner's webhook was queued %q", got)
	}
	bodies := hs.queuedEvents(a, mine.Id)
	var seen []string
	want := func(i int, kind string, data ...string) {
		t.Helper()
		if i >= len(bodies) {
			t.Fatalf("no event %d in %q", i, bodies)
		}
		var e struct {
			ID, Type string
			Created  int64
			Data     json.RawMessage
		}
		if err := json.Unmarshal([]byte(bodies[i]), &e); err != nil {
			t.Fatal(err)
		}
		if e.Type != kind || e.ID == "" || e.Created == 0 {
			t.Fatalf("event %d = %s, want %s", i, bodies[i], kind)
		}
		for _, d := range data {
			if !strings.Contains(string(e.Data), d) {
				t.Errorf("%s lacks %s: %s", kind, d, e.Data)
			}
		}
		seen = append(seen, kind)
	}
	siteJSON := fmt.Sprintf(`"site":{"id":%q,"name":"docs"}`, site.Id)
	want(0, "site.created", siteJSON)
	want(1, "deploy.activated", siteJSON, first.Deploy.Id, `"cause":"deploy"`)
	want(2, "deploy.activated", second.Deploy.Id, `"cause":"deploy"`)
	want(3, "deploy.activated", first.Deploy.Id, `"cause":"rollback"`)
	want(4, "preview.deployed", siteJSON, `"name":"pr-1"`, preview.Deploy.Id)
	want(5, "deploy.activated", preview.Deploy.Id, `"cause":"promote"`)
	want(6, "preview.deleted", siteJSON, `"preview":{"name":"pr-1"}`)
	want(7, "link.created", siteJSON, link.ShareLink.PublicId, `"label":"Client"`)
	want(8, "link.revoked", siteJSON, link.ShareLink.PublicId)
	want(9, "site.deleted", siteJSON)
	if len(bodies) != 10 {
		t.Fatalf("%d events: %q", len(bodies), seen)
	}
	for _, body := range bodies {
		for _, leak := range []string{link.Token, link.Url, "<p>", "@"} {
			if strings.Contains(body, leak) {
				t.Errorf("%s holds %q", body, leak)
			}
		}
	}
}
