// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"errors"
	"net/http"

	"wardlock.nz/server/internal/notify"
	"wardlock.nz/server/internal/store"
)

// Webhook operations (I6.3). Every handler takes the owner from the verified
// request context, never from the request.

// webhookFailed maps a notify error to its response, or returns it as is for
// 500 internal.
func webhookFailed(err error) (*failure, error) {
	var we *notify.WebhookError
	switch {
	case errors.As(err, &we):
		return fail(http.StatusBadRequest, "invalid_request", we.Message, we.Field), nil
	case errors.Is(err, notify.ErrNotFound):
		return fail(http.StatusNotFound, "not_found", "Not found.", ""), nil
	case errors.Is(err, notify.ErrWebhookLimit):
		return fail(http.StatusRequestEntityTooLarge, "limit_exceeded", "You have as many webhooks as you can; delete one first.", ""), nil
	case errors.Is(err, notify.ErrWebhookDisabled):
		return fail(http.StatusConflict, "webhook_disabled", "This webhook is turned off; turn it on first.", ""), nil
	}
	return nil, err
}

func webhookBody(w store.Webhook) Webhook {
	out := Webhook{
		Id: w.ID, Url: w.URL, Description: w.Description, Enabled: w.DisabledAt.IsZero(),
		DisabledAt: optionalMillis(w.DisabledAt), FailingSince: optionalMillis(w.FailingSince),
		CreatedAt: w.CreatedAt.UnixMilli(),
	}
	if !w.AllEvents {
		events := make([]WebhookEvent, len(w.Events))
		for i, e := range w.Events {
			events[i] = WebhookEvent(e)
		}
		out.Events = &events
	}
	if !w.AllSites {
		names := []SiteName{}
		for _, s := range w.Sites {
			if s.Name != "" {
				names = append(names, s.Name)
			}
		}
		out.Sites = &names
	}
	if w.DisabledReason != "" {
		reason := WebhookDisabledReason(w.DisabledReason)
		out.DisabledReason = &reason
	}
	return out
}

func deliveryBody(d store.WebhookDelivery) WebhookDelivery {
	out := WebhookDelivery{
		Id: d.ID, EventId: d.EventID, Type: d.Event, Status: WebhookDeliveryStatus(d.Status),
		Attempts: clampInt32(int(d.Attempts)), CreatedAt: d.CreatedAt.UnixMilli(),
		LastAttemptAt: optionalMillis(d.LastAttemptAt), FinishedAt: optionalMillis(d.FinishedAt),
	}
	if d.ResponseCode != 0 {
		code := clampInt32(d.ResponseCode)
		out.ResponseCode = &code
	}
	if !d.LastAttemptAt.IsZero() {
		ms := d.Duration.Milliseconds()
		out.DurationMs = &ms
	}
	if d.Error != "" {
		class := WebhookDeliveryError(d.Error)
		out.Error = &class
	}
	if d.RedeliveryOf != "" {
		out.RedeliveryOf = &d.RedeliveryOf
	}
	return out
}

func eventNames(events *[]WebhookEvent) []string {
	if events == nil {
		return nil
	}
	out := make([]string, len(*events))
	for i, e := range *events {
		out[i] = string(e)
	}
	return out
}

func siteNames(sites *[]SiteName) []string {
	if sites == nil {
		return nil
	}
	return *sites
}

// ListWebhooks lists the owner's webhooks.
func (s *Server) ListWebhooks(ctx context.Context, _ ListWebhooksRequestObject) (ListWebhooksResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return ListWebhooksdefaultJSONResponse(*unauthenticated), nil
	}
	list, err := s.Notifications.Webhooks(ctx, owner)
	if err != nil {
		return nil, err
	}
	page := ListWebhooks200JSONResponse{Items: make([]Webhook, len(list))}
	for i, w := range list {
		page.Items[i] = webhookBody(w)
	}
	return page, nil
}

// CreateWebhook registers a webhook and returns its signing secret, the only
// time it is shown.
func (s *Server) CreateWebhook(ctx context.Context, req CreateWebhookRequestObject) (CreateWebhookResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return CreateWebhookdefaultJSONResponse(*unauthenticated), nil
	}
	if req.Body == nil {
		return CreateWebhookdefaultJSONResponse(*fail(http.StatusBadRequest, "invalid_request", "url is required.", "url")), nil
	}
	w, secret, err := s.Notifications.CreateWebhook(ctx, owner, notify.WebhookInput{
		URL: req.Body.Url, Description: deref(req.Body.Description),
		Events: eventNames(req.Body.Events), Sites: siteNames(req.Body.Sites),
	})
	if err != nil {
		f, err := webhookFailed(err)
		if err != nil {
			return nil, err
		}
		return CreateWebhookdefaultJSONResponse(*f), nil
	}
	return CreateWebhook201JSONResponse{Webhook: webhookBody(w), Secret: secret}, nil
}

// GetWebhook returns one of the owner's webhooks.
func (s *Server) GetWebhook(ctx context.Context, req GetWebhookRequestObject) (GetWebhookResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return GetWebhookdefaultJSONResponse(*unauthenticated), nil
	}
	w, err := s.Notifications.Webhook(ctx, owner, req.WebhookId)
	if err != nil {
		f, err := webhookFailed(err)
		if err != nil {
			return nil, err
		}
		return GetWebhookdefaultJSONResponse(*f), nil
	}
	return GetWebhook200JSONResponse(webhookBody(w)), nil
}

// UpdateWebhook changes one of the owner's webhooks.
func (s *Server) UpdateWebhook(ctx context.Context, req UpdateWebhookRequestObject) (UpdateWebhookResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return UpdateWebhookdefaultJSONResponse(*unauthenticated), nil
	}
	if req.Body == nil {
		return UpdateWebhookdefaultJSONResponse(*fail(http.StatusBadRequest, "invalid_request", "A body is required.", "body")), nil
	}
	b := req.Body
	if b.AllEvents != nil && *b.AllEvents && b.Events != nil {
		return UpdateWebhookdefaultJSONResponse(*fail(http.StatusBadRequest, "invalid_request", "Send events or allEvents, not both.", "allEvents")), nil
	}
	if b.AllSites != nil && *b.AllSites && b.Sites != nil {
		return UpdateWebhookdefaultJSONResponse(*fail(http.StatusBadRequest, "invalid_request", "Send sites or allSites, not both.", "allSites")), nil
	}
	w, err := s.Notifications.UpdateWebhook(ctx, owner, req.WebhookId, notify.WebhookChange{
		URL: b.Url, Description: b.Description, Enabled: b.Enabled,
		Events: eventNames(b.Events), AllEvents: b.AllEvents != nil && *b.AllEvents,
		Sites: siteNames(b.Sites), AllSites: b.AllSites != nil && *b.AllSites,
	})
	if err != nil {
		f, err := webhookFailed(err)
		if err != nil {
			return nil, err
		}
		return UpdateWebhookdefaultJSONResponse(*f), nil
	}
	return UpdateWebhook200JSONResponse(webhookBody(w)), nil
}

// DeleteWebhook deletes one of the owner's webhooks.
func (s *Server) DeleteWebhook(ctx context.Context, req DeleteWebhookRequestObject) (DeleteWebhookResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return DeleteWebhookdefaultJSONResponse(*unauthenticated), nil
	}
	if err := s.Notifications.DeleteWebhook(ctx, owner, req.WebhookId); err != nil {
		f, err := webhookFailed(err)
		if err != nil {
			return nil, err
		}
		return DeleteWebhookdefaultJSONResponse(*f), nil
	}
	return DeleteWebhook204Response{}, nil
}

// TestWebhook queues a ping.
func (s *Server) TestWebhook(ctx context.Context, req TestWebhookRequestObject) (TestWebhookResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return TestWebhookdefaultJSONResponse(*unauthenticated), nil
	}
	d, err := s.Notifications.TestWebhook(ctx, owner, req.WebhookId)
	if err != nil {
		f, err := webhookFailed(err)
		if err != nil {
			return nil, err
		}
		return TestWebhookdefaultJSONResponse(*f), nil
	}
	return TestWebhook202JSONResponse(deliveryBody(d)), nil
}

// ListWebhookDeliveries lists a webhook's recent deliveries.
func (s *Server) ListWebhookDeliveries(ctx context.Context, req ListWebhookDeliveriesRequestObject) (ListWebhookDeliveriesResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return ListWebhookDeliveriesdefaultJSONResponse(*unauthenticated), nil
	}
	list, err := s.Notifications.WebhookDeliveries(ctx, owner, req.WebhookId)
	if err != nil {
		f, err := webhookFailed(err)
		if err != nil {
			return nil, err
		}
		return ListWebhookDeliveriesdefaultJSONResponse(*f), nil
	}
	page := ListWebhookDeliveries200JSONResponse{Items: make([]WebhookDelivery, len(list))}
	for i, d := range list {
		page.Items[i] = deliveryBody(d)
	}
	return page, nil
}

// RedeliverWebhookDelivery sends a delivery again.
func (s *Server) RedeliverWebhookDelivery(ctx context.Context, req RedeliverWebhookDeliveryRequestObject) (RedeliverWebhookDeliveryResponseObject, error) {
	owner, ok := OwnerID(ctx)
	if !ok {
		return RedeliverWebhookDeliverydefaultJSONResponse(*unauthenticated), nil
	}
	d, err := s.Notifications.RedeliverWebhook(ctx, owner, req.WebhookId, req.DeliveryId)
	if err != nil {
		f, err := webhookFailed(err)
		if err != nil {
			return nil, err
		}
		return RedeliverWebhookDeliverydefaultJSONResponse(*f), nil
	}
	return RedeliverWebhookDelivery202JSONResponse(deliveryBody(d)), nil
}
