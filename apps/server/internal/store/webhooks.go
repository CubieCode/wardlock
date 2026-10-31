// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"wardlock.nz/server/internal/store/sqlcgen"
)

// ErrWebhookLimit means the owner has as many webhooks as they may.
var ErrWebhookLimit = errors.New("too many webhooks")

// Why a webhook is off.
const (
	WebhookDisabledFailing = "failing"
	WebhookDisabledOwner   = "owner"
)

// WebhookSite is a site a webhook lists. Name is empty once the site has
// gone.
type WebhookSite struct {
	ID   string
	Name string
}

// Webhook is an endpoint an owner registered (I6.3).
type Webhook struct {
	ID          string
	OwnerID     string
	URL         string
	Description string
	// AllEvents means every event; otherwise Events lists them.
	AllEvents bool
	Events    []string
	// AllSites means every site; otherwise Sites lists them.
	AllSites bool
	Sites    []WebhookSite
	// SecretSalt goes into the derivation of the signing secret.
	SecretSalt string
	CreatedAt  time.Time
	// FailingSince is when its current run of failures began.
	FailingSince   time.Time
	DisabledAt     time.Time
	DisabledReason string
}

// WebhookDeliveryStatus is where a delivery stands. The migration describes
// each one.
type WebhookDeliveryStatus string

const (
	WebhookDeliveryPending   WebhookDeliveryStatus = "pending"
	WebhookDeliverySucceeded WebhookDeliveryStatus = "succeeded"
	WebhookDeliveryFailed    WebhookDeliveryStatus = "failed"
	WebhookDeliveryDropped   WebhookDeliveryStatus = "dropped"
)

// WebhookDelivery is one event going to one webhook. ID is also the ID of
// its row in the notification outbox.
type WebhookDelivery struct {
	ID        string
	WebhookID string
	// EventID is the same on every delivery of an event, redeliveries
	// included: receivers deduplicate on it.
	EventID string
	Event   string
	// Body is the JSON that is signed and sent.
	Body     string
	Status   WebhookDeliveryStatus
	Attempts int64
	// ResponseCode, Duration and Error describe the latest attempt.
	// ResponseCode is 0 when there was no answer.
	ResponseCode  int
	Duration      time.Duration
	Error         string
	RedeliveryOf  string
	CreatedAt     time.Time
	LastAttemptAt time.Time
	FinishedAt    time.Time
}

// WebhookSend is what the worker needs for one attempt.
type WebhookSend struct {
	WebhookID    string
	Body         string
	URL          string
	SecretSalt   string
	FailingSince time.Time
	Disabled     bool
}

// WebhookAttempt is how one attempt ended.
type WebhookAttempt struct {
	DeliveryID string
	WebhookID  string
	// Status is pending while more attempts remain.
	Status WebhookDeliveryStatus
	// OK says the receiver answered 2xx, ending the webhook's run of
	// failures; otherwise this attempt continues or starts one.
	OK           bool
	ResponseCode int
	Duration     time.Duration
	Error        string
	At           time.Time
}

// Webhooks stores owners' webhooks and their recent deliveries. The first
// methods are owner-scoped, taking the verified owner; the rest are
// instance-wide, driven by the notification worker.
type Webhooks interface {
	// Create adds w, or is ErrWebhookLimit if its owner has limit already.
	Create(ctx context.Context, w Webhook, limit int) error
	// Get returns ownerID's webhook id.
	Get(ctx context.Context, ownerID, id string) (Webhook, error)
	// List returns ownerID's webhooks, oldest first, with their scopes.
	List(ctx context.Context, ownerID string) ([]Webhook, error)
	// Update replaces everything about w but its secret, its owner and when
	// it was created; ErrNotFound if ownerID has no such webhook.
	Update(ctx context.Context, w Webhook) error
	// Delete removes ownerID's webhook id and its deliveries.
	Delete(ctx context.Context, ownerID, id string) error
	// Deliveries returns up to limit of the webhook's deliveries, newest
	// first; empty if ownerID has no such webhook.
	Deliveries(ctx context.Context, ownerID, webhookID string, limit int) ([]WebhookDelivery, error)
	// Delivery returns one delivery of ownerID's webhook.
	Delivery(ctx context.Context, ownerID, webhookID, id string) (WebhookDelivery, error)

	// Enqueue adds each delivery with its outbox row, in one transaction,
	// skipping any whose row's Channel and EventKey already exist, and
	// returns how many it added.
	Enqueue(ctx context.Context, deliveries ...QueuedDelivery) (int, error)
	// ToSend returns what an attempt on delivery id needs; ErrNotFound once
	// the webhook has gone.
	ToSend(ctx context.Context, id string) (WebhookSend, error)
	// RecordAttempt records a, marks the webhook's run of failures and,
	// when the delivery has finished, trims the webhook's deliveries to the
	// newest keep. One transaction.
	RecordAttempt(ctx context.Context, a WebhookAttempt, keep int) error
	// Disable turns off a webhook that keeps failing and queues notice to
	// its owner, in one transaction; if it is off already it does neither.
	Disable(ctx context.Context, webhookID string, at time.Time, notice OutboxEntry) error
	// Drop ends a pending delivery unsent. A delivery that has gone is
	// not an error.
	Drop(ctx context.Context, id string, at time.Time) error
	// ForgetSite removes a deleted site from every webhook's list.
	ForgetSite(ctx context.Context, siteID string) error
}

// QueuedDelivery is a delivery and its outbox row, which share an ID.
type QueuedDelivery struct {
	Delivery WebhookDelivery
	Outbox   OutboxEntry
}

// Webhooks returns the SQLite Webhooks repository.
func (db *DB) Webhooks() Webhooks { return webhooksRepo{db} }

type webhooksRepo struct{ db *DB }

func setWebhookScope(ctx context.Context, q *sqlcgen.Queries, w Webhook) error {
	if err := q.DeleteWebhookEvents(ctx, w.ID); err != nil {
		return mapErr("clear webhook events", err)
	}
	if err := q.DeleteWebhookSites(ctx, w.ID); err != nil {
		return mapErr("clear webhook sites", err)
	}
	if !w.AllEvents {
		for _, e := range w.Events {
			if err := q.AddWebhookEvent(ctx, sqlcgen.AddWebhookEventParams{WebhookID: w.ID, Event: e}); err != nil {
				return mapErr("add webhook event", err)
			}
		}
	}
	if !w.AllSites {
		for _, s := range w.Sites {
			if err := q.AddWebhookSite(ctx, sqlcgen.AddWebhookSiteParams{WebhookID: w.ID, SiteID: s.ID}); err != nil {
				return mapErr("add webhook site", err)
			}
		}
	}
	return nil
}

func (r webhooksRepo) Create(ctx context.Context, w Webhook, limit int) error {
	return r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		n, err := q.CountWebhooks(ctx, w.OwnerID)
		if err != nil {
			return mapErr("count webhooks", err)
		}
		if n >= int64(limit) {
			return ErrWebhookLimit
		}
		if err := q.CreateWebhook(ctx, sqlcgen.CreateWebhookParams{
			ID: w.ID, OwnerID: w.OwnerID, Url: w.URL, Description: w.Description,
			AllEvents: boolInt(w.AllEvents), AllSites: boolInt(w.AllSites),
			SecretSalt: w.SecretSalt, CreatedAt: ms(w.CreatedAt),
		}); err != nil {
			return mapErr("create webhook", err)
		}
		return setWebhookScope(ctx, q, w)
	})
}

func webhookFrom(row sqlcgen.Webhook) Webhook {
	return Webhook{
		ID: row.ID, OwnerID: row.OwnerID, URL: row.Url, Description: row.Description,
		AllEvents: row.AllEvents != 0, AllSites: row.AllSites != 0, SecretSalt: row.SecretSalt,
		CreatedAt: fromMs(row.CreatedAt), FailingSince: fromNullMs(row.FailingSince),
		DisabledAt: fromNullMs(row.DisabledAt), DisabledReason: row.DisabledReason.String,
	}
}

// withScopes fills in the scopes of ownerID's webhooks.
func (r webhooksRepo) withScopes(ctx context.Context, ownerID string, list []Webhook) ([]Webhook, error) {
	if len(list) == 0 {
		return list, nil // most owners, on every event any service reports
	}
	events, err := r.db.q.ListOwnerWebhookEvents(ctx, ownerID)
	if err != nil {
		return nil, mapErr("list webhook events", err)
	}
	sites, err := r.db.q.ListOwnerWebhookSites(ctx, ownerID)
	if err != nil {
		return nil, mapErr("list webhook sites", err)
	}
	for i := range list {
		w := &list[i]
		for _, e := range events {
			if e.WebhookID == w.ID {
				w.Events = append(w.Events, e.Event)
			}
		}
		for _, s := range sites {
			if s.WebhookID == w.ID {
				w.Sites = append(w.Sites, WebhookSite{ID: s.SiteID, Name: s.SiteName.String})
			}
		}
	}
	return list, nil
}

func (r webhooksRepo) Get(ctx context.Context, ownerID, id string) (Webhook, error) {
	row, err := r.db.q.GetWebhook(ctx, sqlcgen.GetWebhookParams{ID: id, OwnerID: ownerID})
	if err != nil {
		return Webhook{}, mapErr("get webhook", err)
	}
	list, err := r.withScopes(ctx, ownerID, []Webhook{webhookFrom(row)})
	if err != nil {
		return Webhook{}, err
	}
	return list[0], nil
}

func (r webhooksRepo) List(ctx context.Context, ownerID string) ([]Webhook, error) {
	rows, err := r.db.q.ListWebhooks(ctx, ownerID)
	if err != nil {
		return nil, mapErr("list webhooks", err)
	}
	list := make([]Webhook, len(rows))
	for i, row := range rows {
		list[i] = webhookFrom(row)
	}
	return r.withScopes(ctx, ownerID, list)
}

func (r webhooksRepo) Update(ctx context.Context, w Webhook) error {
	return r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		n, err := q.UpdateWebhook(ctx, sqlcgen.UpdateWebhookParams{
			ID: w.ID, OwnerID: w.OwnerID, Url: w.URL, Description: w.Description,
			AllEvents: boolInt(w.AllEvents), AllSites: boolInt(w.AllSites),
			FailingSince: nullMs(w.FailingSince), DisabledAt: nullMs(w.DisabledAt),
			DisabledReason: nullString(w.DisabledReason),
		})
		if err := rowsAffected("update webhook", n, err); err != nil {
			return err
		}
		return setWebhookScope(ctx, q, w)
	})
}

func (r webhooksRepo) Delete(ctx context.Context, ownerID, id string) error {
	n, err := r.db.q.DeleteWebhook(ctx, sqlcgen.DeleteWebhookParams{ID: id, OwnerID: ownerID})
	return rowsAffected("delete webhook", n, err)
}

func deliveryFrom(row sqlcgen.WebhookDelivery) WebhookDelivery {
	return WebhookDelivery{
		ID: row.ID, WebhookID: row.WebhookID, EventID: row.EventID, Event: row.Event, Body: row.Body,
		Status: WebhookDeliveryStatus(row.Status), Attempts: row.Attempts, ResponseCode: int(row.ResponseCode.Int64),
		Duration: time.Duration(row.DurationMs.Int64) * time.Millisecond, Error: row.Error.String,
		RedeliveryOf: row.RedeliveryOf.String, CreatedAt: fromMs(row.CreatedAt),
		LastAttemptAt: fromNullMs(row.LastAttemptAt), FinishedAt: fromNullMs(row.FinishedAt),
	}
}

func (r webhooksRepo) Deliveries(ctx context.Context, ownerID, webhookID string, limit int) ([]WebhookDelivery, error) {
	rows, err := r.db.q.ListWebhookDeliveries(ctx, sqlcgen.ListWebhookDeliveriesParams{
		WebhookID: webhookID, OwnerID: ownerID, RowLimit: int64(limit),
	})
	if err != nil {
		return nil, mapErr("list webhook deliveries", err)
	}
	out := make([]WebhookDelivery, len(rows))
	for i, row := range rows {
		out[i] = deliveryFrom(sqlcgen.WebhookDelivery(row))
	}
	return out, nil
}

func (r webhooksRepo) Delivery(ctx context.Context, ownerID, webhookID, id string) (WebhookDelivery, error) {
	row, err := r.db.q.GetWebhookDelivery(ctx, sqlcgen.GetWebhookDeliveryParams{ID: id, WebhookID: webhookID, OwnerID: ownerID})
	if err != nil {
		return WebhookDelivery{}, mapErr("get webhook delivery", err)
	}
	return deliveryFrom(sqlcgen.WebhookDelivery(row)), nil
}

func (r webhooksRepo) Enqueue(ctx context.Context, deliveries ...QueuedDelivery) (int, error) {
	added := 0
	err := r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		for _, d := range deliveries {
			n, err := q.EnqueueNotification(ctx, enqueueParams(d.Outbox))
			if err != nil {
				return mapErr("enqueue webhook delivery", err)
			}
			if n == 0 {
				continue
			}
			added++
			v := d.Delivery
			if err := q.InsertWebhookDelivery(ctx, sqlcgen.InsertWebhookDeliveryParams{
				ID: d.Outbox.ID, WebhookID: v.WebhookID, EventID: v.EventID, Event: v.Event, Body: v.Body,
				RedeliveryOf: nullString(v.RedeliveryOf), CreatedAt: ms(d.Outbox.CreatedAt),
			}); err != nil {
				return mapErr("insert webhook delivery", err)
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return added, nil
}

func (r webhooksRepo) ToSend(ctx context.Context, id string) (WebhookSend, error) {
	row, err := r.db.q.GetWebhookDeliveryToSend(ctx, id)
	if err != nil {
		return WebhookSend{}, mapErr("get webhook delivery to send", err)
	}
	return WebhookSend{
		WebhookID: row.WebhookID, Body: row.Body, URL: row.Url, SecretSalt: row.SecretSalt,
		FailingSince: fromNullMs(row.FailingSince), Disabled: row.DisabledAt.Valid,
	}, nil
}

func (r webhooksRepo) RecordAttempt(ctx context.Context, a WebhookAttempt, keep int) error {
	return r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		p := sqlcgen.RecordWebhookAttemptParams{
			ID: a.DeliveryID, Status: string(a.Status), DurationMs: sql.NullInt64{Int64: a.Duration.Milliseconds(), Valid: true},
			Error: nullString(a.Error), At: nullMs(a.At),
		}
		if a.ResponseCode != 0 {
			p.ResponseCode = sql.NullInt64{Int64: int64(a.ResponseCode), Valid: true}
		}
		finished := a.Status != WebhookDeliveryPending
		if finished {
			p.FinishedAt = nullMs(a.At)
		}
		if err := q.RecordWebhookAttempt(ctx, p); err != nil {
			return mapErr("record webhook attempt", err)
		}
		var err error
		if a.OK {
			err = q.ClearWebhookFailing(ctx, a.WebhookID)
		} else {
			err = q.MarkWebhookFailing(ctx, sqlcgen.MarkWebhookFailingParams{ID: a.WebhookID, At: nullMs(a.At)})
		}
		if err != nil {
			return mapErr("mark webhook", err)
		}
		if finished {
			if err := q.TrimWebhookDeliveries(ctx, sqlcgen.TrimWebhookDeliveriesParams{WebhookID: a.WebhookID, Keep: int64(keep)}); err != nil {
				return mapErr("trim webhook deliveries", err)
			}
		}
		return nil
	})
}

func (r webhooksRepo) Disable(ctx context.Context, webhookID string, at time.Time, notice OutboxEntry) error {
	return r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		n, err := q.DisableWebhook(ctx, sqlcgen.DisableWebhookParams{ID: webhookID, At: nullMs(at)})
		if err != nil {
			return mapErr("disable webhook", err)
		}
		if n == 0 {
			return nil
		}
		if _, err := q.EnqueueNotification(ctx, enqueueParams(notice)); err != nil {
			return mapErr("enqueue webhook notice", err)
		}
		return nil
	})
}

func (r webhooksRepo) Drop(ctx context.Context, id string, at time.Time) error {
	if err := r.db.q.DropWebhookDelivery(ctx, sqlcgen.DropWebhookDeliveryParams{ID: id, At: nullMs(at)}); err != nil {
		return mapErr("drop webhook delivery", err)
	}
	return nil
}

func (r webhooksRepo) ForgetSite(ctx context.Context, siteID string) error {
	if err := r.db.q.DeleteSiteFromWebhooks(ctx, siteID); err != nil {
		return mapErr("forget site in webhooks", err)
	}
	return nil
}
