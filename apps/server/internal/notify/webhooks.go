// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package notify

import (
	"cmp"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"wardlock.nz/server/internal/store"
)

// The events a webhook can ask for, plus EventPing, which only `webhook
// test` sends.
const (
	EventDeployActivated     = "deploy.activated"
	EventPreviewDeployed     = "preview.deployed"
	EventPreviewDeleted      = "preview.deleted"
	EventSiteCreated         = "site.created"
	EventSiteDeleted         = "site.deleted"
	EventDomainStatusChanged = "domain.status_changed"
	EventLinkCreated         = "link.created"
	EventLinkRevoked         = "link.revoked"
	EventLinkUsedUp          = "link.used_up"
	EventFileWithheld        = "file.withheld"
	EventPing                = "ping"
)

// WebhookEvents lists every event a webhook can ask for.
var WebhookEvents = []string{
	EventDeployActivated, EventPreviewDeployed, EventPreviewDeleted, EventSiteCreated, EventSiteDeleted,
	EventDomainStatusChanged, EventLinkCreated, EventLinkRevoked, EventLinkUsedUp, EventFileWithheld,
}

// Webhook limits and timing (SPEC "Webhooks").
const (
	// MaxWebhooks is how many webhooks an owner may have.
	MaxWebhooks = 10
	// MaxWebhookSites is how many sites a webhook may list.
	MaxWebhookSites = 100

	maxWebhookURL         = 2048
	maxWebhookDescription = 200

	// webhookRetryFor is how long a delivery is retried.
	webhookRetryFor = 24 * time.Hour
	// webhookDisableAfter is how long a webhook fails, with nothing
	// succeeding in between, before it is turned off.
	webhookDisableAfter = 3 * 24 * time.Hour
	// webhookTimeout bounds one attempt from dialling to the end of the
	// response.
	webhookTimeout = 10 * time.Second
	// maxWebhookResponse is how much of a response is read.
	maxWebhookResponse = 4 << 10
	// keepDeliveries is how many deliveries each webhook keeps.
	keepDeliveries = 50
	// webhookWorkers is how many attempts run at once, so a few slow
	// receivers do not hold up the rest.
	webhookWorkers = 8
)

var (
	// ErrNotFound means the owner has no such webhook, site or delivery.
	ErrNotFound = errors.New("not found")
	// ErrWebhookLimit means the owner has MaxWebhooks already.
	ErrWebhookLimit = errors.New("too many webhooks")
	// ErrWebhookDisabled means a webhook that is off cannot be sent to.
	ErrWebhookDisabled = errors.New("webhook disabled")
)

// WebhookError is a field of a webhook the owner must change.
type WebhookError struct{ Field, Message string }

func (e *WebhookError) Error() string { return e.Message }

// WebhookInput is what an owner gives for a new webhook. Sites are names.
// Empty Events or Sites means all of them.
type WebhookInput struct {
	URL, Description string
	Events, Sites    []string
}

// WebhookChange changes a webhook. Nil pointers and slices leave things as
// they are.
type WebhookChange struct {
	URL, Description *string
	// Events and AllEvents, and Sites and AllSites, each replace the scope.
	Events    []string
	AllEvents bool
	Sites     []string
	AllSites  bool
	// Enabled turns it off, or on again, which forgets its failures.
	Enabled *bool
}

func newSecretSalt() string { return rand.Text() }

// signingSecret derives a webhook's secret from the server key, so nothing
// the database holds can sign. The key the HMAC uses is this whole string.
func (s *Service) signingSecret(webhookID, salt string) string {
	m := hmac.New(sha256.New, s.c.SigningKey)
	m.Write([]byte("wardlock webhook secret\x00" + webhookID + "\x00" + salt))
	return "wlwh_" + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (s *Service) checkURL(raw string) error {
	bad := func(msg string) error { return &WebhookError{"url", msg} }
	if raw == "" || len(raw) > maxWebhookURL {
		return bad(fmt.Sprintf("url must be 1 to %d characters.", maxWebhookURL))
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return bad("url is not a valid address.")
	}
	if u.Scheme != "https" && (u.Scheme != "http" || !s.c.AllowHTTP) {
		return bad("url must use https.")
	}
	if u.User != nil || u.Fragment != "" {
		return bad("url must not contain credentials or a fragment.")
	}
	// The dialler decides for every address; this only says so sooner when
	// the URL itself is one it would refuse.
	if a, err := netip.ParseAddr(u.Hostname()); err == nil && !s.permitted(a) {
		return bad("url points to an address Wardlock does not send webhooks to.")
	}
	return nil
}

func checkDescription(d string) error {
	if utf8.RuneCountInString(d) > maxWebhookDescription {
		return &WebhookError{"description", fmt.Sprintf("description must be at most %d characters.", maxWebhookDescription)}
	}
	return nil
}

func checkEvents(events []string) error {
	for _, e := range events {
		if !slices.Contains(WebhookEvents, e) {
			return &WebhookError{"events", "Unknown event " + e + "."}
		}
	}
	return nil
}

// resolveSites finds ownerID's sites called names. Webhooks are personal
// (T7.1), so a team's site, like another owner's, is the same ErrNotFound as
// a missing one.
func (s *Service) resolveSites(ctx context.Context, ownerID string, names []string) ([]store.WebhookSite, error) {
	if len(names) > MaxWebhookSites {
		return nil, &WebhookError{"sites", fmt.Sprintf("A webhook can list at most %d sites.", MaxWebhookSites)}
	}
	out := make([]store.WebhookSite, 0, len(names))
	for _, name := range names {
		site, err := s.c.Sites.GetByOwnerName(ctx, store.ActorFor(ownerID), name)
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("find webhook site: %w", ErrNotFound)
		}
		if err != nil {
			return nil, fmt.Errorf("find webhook site: %w", err)
		}
		if !slices.ContainsFunc(out, func(w store.WebhookSite) bool { return w.ID == site.ID }) {
			out = append(out, store.WebhookSite{ID: site.ID, Name: site.Name})
		}
	}
	return out, nil
}

func unique(list []string) []string {
	out := slices.Clone(list)
	slices.Sort(out)
	return slices.Compact(out)
}

// CreateWebhook adds a webhook for ownerID and returns it with its signing
// secret, which is never available again.
func (s *Service) CreateWebhook(ctx context.Context, ownerID string, in WebhookInput) (store.Webhook, string, error) {
	in.URL, in.Description = strings.TrimSpace(in.URL), strings.TrimSpace(in.Description)
	if err := cmp.Or(s.checkURL(in.URL), checkDescription(in.Description), checkEvents(in.Events)); err != nil {
		return store.Webhook{}, "", err
	}
	sites, err := s.resolveSites(ctx, ownerID, in.Sites)
	if err != nil {
		return store.Webhook{}, "", err
	}
	w := store.Webhook{
		ID: store.NewID(), OwnerID: ownerID, URL: in.URL, Description: in.Description,
		AllEvents: len(in.Events) == 0, Events: unique(in.Events),
		AllSites: len(in.Sites) == 0, Sites: sites,
		SecretSalt: newSecretSalt(), CreatedAt: s.now(),
	}
	err = s.c.Webhooks.Create(ctx, w, MaxWebhooks)
	if errors.Is(err, store.ErrWebhookLimit) {
		return store.Webhook{}, "", ErrWebhookLimit
	}
	if err != nil {
		return store.Webhook{}, "", fmt.Errorf("create webhook: %w", err)
	}
	return w, s.signingSecret(w.ID, w.SecretSalt), nil
}

// Webhooks lists ownerID's webhooks, oldest first.
func (s *Service) Webhooks(ctx context.Context, ownerID string) ([]store.Webhook, error) {
	list, err := s.c.Webhooks.List(ctx, ownerID)
	if err != nil {
		return nil, fmt.Errorf("list webhooks: %w", err)
	}
	return list, nil
}

// Webhook returns ownerID's webhook id.
func (s *Service) Webhook(ctx context.Context, ownerID, id string) (store.Webhook, error) {
	w, err := s.c.Webhooks.Get(ctx, ownerID, id)
	return w, webhookErr("get webhook", err)
}

func webhookErr(what string, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrNotFound):
		return fmt.Errorf("%s: %w", what, ErrNotFound)
	default:
		return fmt.Errorf("%s: %w", what, err)
	}
}

// UpdateWebhook applies c to ownerID's webhook id. A new address starts with
// a clean record: the failures were the old one's.
func (s *Service) UpdateWebhook(ctx context.Context, ownerID, id string, c WebhookChange) (store.Webhook, error) {
	w, err := s.Webhook(ctx, ownerID, id)
	if err != nil {
		return store.Webhook{}, err
	}
	if c.URL != nil {
		u := strings.TrimSpace(*c.URL)
		if err := s.checkURL(u); err != nil {
			return store.Webhook{}, err
		}
		if u != w.URL {
			w.URL, w.FailingSince = u, time.Time{}
		}
	}
	if c.Description != nil {
		w.Description = strings.TrimSpace(*c.Description)
		if err := checkDescription(w.Description); err != nil {
			return store.Webhook{}, err
		}
	}
	switch {
	case c.AllEvents:
		w.AllEvents, w.Events = true, nil
	case c.Events != nil:
		if err := checkEvents(c.Events); err != nil {
			return store.Webhook{}, err
		}
		w.AllEvents, w.Events = false, unique(c.Events)
	}
	switch {
	case c.AllSites:
		w.AllSites, w.Sites = true, nil
	case c.Sites != nil:
		if w.Sites, err = s.resolveSites(ctx, ownerID, c.Sites); err != nil {
			return store.Webhook{}, err
		}
		w.AllSites = false
	}
	if c.Enabled != nil {
		switch {
		case *c.Enabled:
			w.DisabledAt, w.DisabledReason, w.FailingSince = time.Time{}, "", time.Time{}
		case w.DisabledAt.IsZero():
			w.DisabledAt, w.DisabledReason = s.now(), store.WebhookDisabledOwner
		}
	}
	if err := s.c.Webhooks.Update(ctx, w); err != nil {
		return store.Webhook{}, webhookErr("update webhook", err)
	}
	return s.Webhook(ctx, ownerID, id)
}

// DeleteWebhook removes ownerID's webhook id. Anything queued for it ends
// unsent.
func (s *Service) DeleteWebhook(ctx context.Context, ownerID, id string) error {
	return webhookErr("delete webhook", s.c.Webhooks.Delete(ctx, ownerID, id))
}

// WebhookDeliveries lists the deliveries kept for ownerID's webhook id,
// newest first.
func (s *Service) WebhookDeliveries(ctx context.Context, ownerID, id string) ([]store.WebhookDelivery, error) {
	if _, err := s.Webhook(ctx, ownerID, id); err != nil {
		return nil, err
	}
	list, err := s.c.Webhooks.Deliveries(ctx, ownerID, id, keepDeliveries)
	if err != nil {
		return nil, fmt.Errorf("list webhook deliveries: %w", err)
	}
	return list, nil
}

// enabled returns ownerID's webhook id, or ErrWebhookDisabled.
func (s *Service) enabled(ctx context.Context, ownerID, id string) (store.Webhook, error) {
	w, err := s.Webhook(ctx, ownerID, id)
	if err != nil {
		return store.Webhook{}, err
	}
	if !w.DisabledAt.IsZero() {
		return store.Webhook{}, ErrWebhookDisabled
	}
	return w, nil
}

// TestWebhook queues a ping for ownerID's webhook id and returns its
// delivery.
func (s *Service) TestWebhook(ctx context.Context, ownerID, id string) (store.WebhookDelivery, error) {
	w, err := s.enabled(ctx, ownerID, id)
	if err != nil {
		return store.WebhookDelivery{}, err
	}
	q, err := s.queued(w, webhookEvent{
		key: EventPing + ":" + store.NewID(), kind: EventPing, ownerID: ownerID,
		data: pingData{Webhook: webhookRef{ID: w.ID}},
	}, store.NewID())
	if err != nil {
		return store.WebhookDelivery{}, err
	}
	return s.requeue(ctx, q)
}

// RedeliverWebhook sends delivery deliveryID of ownerID's webhook id again:
// the same event with the same ID and body, signed afresh, as a new delivery.
func (s *Service) RedeliverWebhook(ctx context.Context, ownerID, id, deliveryID string) (store.WebhookDelivery, error) {
	w, err := s.enabled(ctx, ownerID, id)
	if err != nil {
		return store.WebhookDelivery{}, err
	}
	d, err := s.c.Webhooks.Delivery(ctx, ownerID, w.ID, deliveryID)
	if err != nil {
		return store.WebhookDelivery{}, webhookErr("find webhook delivery", err)
	}
	return s.requeue(ctx, s.redelivery(w, d))
}

func (s *Service) requeue(ctx context.Context, q store.QueuedDelivery) (store.WebhookDelivery, error) {
	if _, err := s.c.Webhooks.Enqueue(ctx, q); err != nil {
		return store.WebhookDelivery{}, fmt.Errorf("queue webhook delivery: %w", err)
	}
	s.wakeWebhooks()
	d, err := s.c.Webhooks.Delivery(ctx, q.Outbox.OwnerID, q.Delivery.WebhookID, q.Outbox.ID)
	return d, webhookErr("read webhook delivery", err)
}
