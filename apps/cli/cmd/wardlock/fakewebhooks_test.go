// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"net/http"
	"slices"

	"wardlock.nz/apiclient"
)

// The fake API's webhooks (I6.3).

const testWebhookSecret = "wlwh_EXAMPLEonlyEXAMPLEonlyEXAMPLEonlyEXAMPLEonl"

func (f *fakeAPI) handleWebhooks(handle func(string, string, func(http.ResponseWriter, *http.Request))) {
	handle("GET /api/v0/webhooks", "manage", func(w http.ResponseWriter, _ *http.Request) {
		reply(w, http.StatusOK, apiclient.WebhookList{Items: f.webhooks})
	})
	handle("POST /api/v0/webhooks", "manage", func(w http.ResponseWriter, r *http.Request) {
		var body apiclient.CreateWebhookRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			reply(w, http.StatusBadRequest, apiErr("invalid_request", "bad body"))
			return
		}
		if len(body.Url) < 8 || body.Url[:8] != "https://" {
			reply(w, http.StatusBadRequest, apiErr("invalid_request", "url must use https."))
			return
		}
		hook := apiclient.Webhook{Id: f.id(), Url: body.Url, Description: deref(body.Description), Enabled: true,
			Events: body.Events, Sites: body.Sites, CreatedAt: fakeDeployedAt}
		f.webhooks = append(f.webhooks, hook)
		reply(w, http.StatusCreated, apiclient.CreatedWebhook{Webhook: hook, Secret: testWebhookSecret})
	})
	found := func(w http.ResponseWriter, r *http.Request) int {
		i := slices.IndexFunc(f.webhooks, func(h apiclient.Webhook) bool { return h.Id == r.PathValue("webhookId") })
		if i < 0 {
			reply(w, http.StatusNotFound, apiErr("not_found", "Not found."))
		}
		return i
	}
	handle("GET /api/v0/webhooks/{webhookId}", "manage", func(w http.ResponseWriter, r *http.Request) {
		if i := found(w, r); i >= 0 {
			reply(w, http.StatusOK, f.webhooks[i])
		}
	})
	handle("DELETE /api/v0/webhooks/{webhookId}", "manage", func(w http.ResponseWriter, r *http.Request) {
		if i := found(w, r); i >= 0 {
			f.webhooks = slices.Delete(f.webhooks, i, i+1)
			w.WriteHeader(http.StatusNoContent)
		}
	})
	handle("POST /api/v0/webhooks/{webhookId}/test", "manage", func(w http.ResponseWriter, r *http.Request) {
		i := found(w, r)
		if i < 0 {
			return
		}
		if !f.webhooks[i].Enabled {
			reply(w, http.StatusConflict, apiErr("webhook_disabled", "This webhook is turned off; turn it on first."))
			return
		}
		d := apiclient.WebhookDelivery{Id: f.id(), EventId: f.id(), Type: "ping", Status: apiclient.WebhookDeliveryStatusPending, CreatedAt: fakeDeployedAt}
		f.deliveries = append([]apiclient.WebhookDelivery{d}, f.deliveries...)
		reply(w, http.StatusAccepted, d)
	})
	handle("GET /api/v0/webhooks/{webhookId}/deliveries", "manage", func(w http.ResponseWriter, r *http.Request) {
		if found(w, r) >= 0 {
			reply(w, http.StatusOK, apiclient.WebhookDeliveryList{Items: f.deliveries})
		}
	})
}
