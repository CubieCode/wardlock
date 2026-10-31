// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"wardlock.nz/server/internal/sites"
	"wardlock.nz/server/internal/store"
)

// What each event's data holds (SPEC "Webhooks"): names and IDs, built from
// fields that are neither tokens, link secrets, visitors' details nor
// content. Nothing here has a path to one: the structs are the allowlist.
type (
	siteRef struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	webhookRef struct {
		ID string `json:"id"`
	}
	deployRef struct {
		ID          string `json:"id"`
		ActivatedAt int64  `json:"activatedAt"`
		// Path is set for a path deploy.
		Path string `json:"path,omitempty"`
	}
	previewRef struct {
		Name     string `json:"name"`
		DeployID string `json:"deployId,omitempty"`
	}
	domainRef struct {
		ID       string `json:"id"`
		Hostname string `json:"hostname"`
		Status   string `json:"status"`
	}
	linkRef struct {
		ID       string `json:"id"`
		PublicID string `json:"publicId"`
		Label    string `json:"label,omitempty"`
		Path     string `json:"path,omitempty"`
	}

	siteData struct {
		Site siteRef `json:"site"`
	}
	deployData struct {
		Site   siteRef   `json:"site"`
		Deploy deployRef `json:"deploy"`
		// Cause is deploy, rollback or promote.
		Cause string `json:"cause"`
	}
	previewData struct {
		Site    siteRef    `json:"site"`
		Preview previewRef `json:"preview"`
	}
	domainData struct {
		Site   siteRef   `json:"site"`
		Domain domainRef `json:"domain"`
	}
	linkData struct {
		Site siteRef `json:"site"`
		Link linkRef `json:"link"`
	}
	withheldData struct {
		Site  siteRef  `json:"site"`
		Paths []string `json:"paths"`
	}
	pingData struct {
		Webhook webhookRef `json:"webhook"`
	}
)

// envelope is the body of every delivery.
type envelope struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Created int64  `json:"created"`
	Data    any    `json:"data"`
}

// webhookEvent is something that happened to an owner.
type webhookEvent struct {
	// key names the event, so noticing it twice queues it once.
	key, kind, ownerID string
	// siteID is empty for an event about no site.
	siteID string
	// at is when it happened, for an event a scan noticed: a webhook made
	// later does not hear of it. Zero for one reported as it happens.
	at   time.Time
	data any
}

func (e webhookEvent) wantedBy(w store.Webhook) bool {
	return w.DisabledAt.IsZero() && (e.at.IsZero() || !w.CreatedAt.After(e.at)) &&
		(w.AllEvents || slices.Contains(w.Events, e.kind)) &&
		(w.AllSites || slices.ContainsFunc(w.Sites, func(s store.WebhookSite) bool { return s.ID == e.siteID }))
}

// queued builds the delivery of e to w, with eventID.
func (s *Service) queued(w store.Webhook, e webhookEvent, eventID string) (store.QueuedDelivery, error) {
	body, err := json.Marshal(envelope{ID: eventID, Type: e.kind, Created: s.now().UnixMilli(), Data: e.data})
	if err != nil {
		return store.QueuedDelivery{}, fmt.Errorf("encode webhook event: %w", err)
	}
	return s.delivery(w, e.key, e.kind, eventID, string(body), ""), nil
}

func (s *Service) delivery(w store.Webhook, key, kind, eventID, body, redeliveryOf string) store.QueuedDelivery {
	id := store.NewID()
	return store.QueuedDelivery{
		Delivery: store.WebhookDelivery{
			ID: id, WebhookID: w.ID, EventID: eventID, Event: kind, Body: body, RedeliveryOf: redeliveryOf,
		},
		Outbox: store.OutboxEntry{
			ID: id, Channel: store.NotificationWebhook, EventKey: w.ID + ":" + key, OwnerID: w.OwnerID,
			Event: kind, Payload: "{}", CreatedAt: s.now(),
		},
	}
}

func (s *Service) redelivery(w store.Webhook, d store.WebhookDelivery) store.QueuedDelivery {
	return s.delivery(w, "redelivery:"+store.NewID(), d.Event, d.EventID, d.Body, d.ID)
}

// publish queues events for the webhooks that want them. A failure here is
// logged and costs those events: the thing they report has already happened
// and is not undone by the receivers not hearing of it.
func (s *Service) publish(ctx context.Context, events ...webhookEvent) {
	if s.c.Webhooks == nil || len(events) == 0 {
		return
	}
	// Whatever the caller is doing next, including ending the request, the
	// report still goes.
	ctx = context.WithoutCancel(ctx)
	hooks := map[string][]store.Webhook{}
	var out []store.QueuedDelivery
	for _, e := range events {
		list, ok := hooks[e.ownerID]
		if !ok {
			var err error
			if list, err = s.c.Webhooks.List(ctx, e.ownerID); err != nil {
				s.logErr(ctx, "list webhooks for an event", err)
				continue
			}
			hooks[e.ownerID] = list
		}
		eventID := store.NewID()
		for _, w := range list {
			if !e.wantedBy(w) {
				continue
			}
			q, err := s.queued(w, e, eventID)
			if err != nil {
				s.logErr(ctx, "build webhook delivery", err)
				continue
			}
			out = append(out, q)
		}
	}
	if len(out) == 0 {
		return
	}
	added, err := s.c.Webhooks.Enqueue(ctx, out...)
	if err != nil {
		s.logErr(ctx, "queue webhook deliveries", err)
		return
	}
	if added > 0 {
		s.c.Log.LogAttrs(ctx, slog.LevelInfo, "webhook deliveries queued", slog.Int("count", added))
		s.wakeWebhooks()
	}
}

func site(s store.Site) siteRef { return siteRef{ID: s.ID, Name: s.Name} }

func siteEvent(kind string, s store.Site) webhookEvent {
	return webhookEvent{key: kind + ":" + s.ID, kind: kind, ownerID: s.OwnerID, siteID: s.ID, data: siteData{site(s)}}
}

// SiteCreated reports a new site.
func (s *Service) SiteCreated(ctx context.Context, created store.Site) {
	s.publish(ctx, siteEvent(EventSiteCreated, created))
}

// SiteDeleted reports a deleted site, then forgets it: webhooks listing it
// are the last to need it.
func (s *Service) SiteDeleted(ctx context.Context, deleted store.Site) {
	s.publish(ctx, siteEvent(EventSiteDeleted, deleted))
	if s.c.Webhooks != nil {
		s.logErr(ctx, "forget deleted site", s.c.Webhooks.ForgetSite(context.WithoutCancel(ctx), deleted.ID))
	}
}

// DeployActivated reports that site now serves d, because of cause.
func (s *Service) DeployActivated(ctx context.Context, st store.Site, d store.Deploy, cause string) {
	s.publish(ctx, webhookEvent{
		key:  key(EventDeployActivated, d.ID, d.ActivatedAt),
		kind: EventDeployActivated, ownerID: st.OwnerID, siteID: st.ID,
		data: deployData{site(st), deployRef{ID: d.ID, ActivatedAt: d.ActivatedAt.UnixMilli(), Path: d.PathPrefix}, cause},
	})
}

// PreviewDeployed reports that a preview now serves d.
func (s *Service) PreviewDeployed(ctx context.Context, st store.Site, d store.Deploy) {
	s.publish(ctx, webhookEvent{
		key:  key(EventPreviewDeployed, d.ID),
		kind: EventPreviewDeployed, ownerID: st.OwnerID, siteID: st.ID,
		data: previewData{site(st), previewRef{Name: d.Preview, DeployID: d.ID}},
	})
}

// PreviewDeleted reports that the owner deleted a preview, at at.
func (s *Service) PreviewDeleted(ctx context.Context, st store.Site, name string, at time.Time) {
	s.publish(ctx, webhookEvent{
		key:  key(EventPreviewDeleted, st.ID, name, at),
		kind: EventPreviewDeleted, ownerID: st.OwnerID, siteID: st.ID,
		data: previewData{site(st), previewRef{Name: name}},
	})
}

// LinkCreated reports a new share link.
func (s *Service) LinkCreated(ctx context.Context, st store.Site, l store.ShareLink) {
	s.publish(ctx, webhookEvent{
		key:  key(EventLinkCreated, l.ID),
		kind: EventLinkCreated, ownerID: st.OwnerID, siteID: st.ID,
		data: linkData{site(st), linkRef{ID: l.ID, PublicID: l.PublicID, Label: l.Label, Path: l.PathPrefix}},
	})
}

// LinkRevoked reports a revoked share link.
func (s *Service) LinkRevoked(ctx context.Context, st store.Site, l store.ShareLink) {
	s.publish(ctx, webhookEvent{
		key:  key(EventLinkRevoked, l.ID),
		kind: EventLinkRevoked, ownerID: st.OwnerID, siteID: st.ID,
		data: linkData{site(st), linkRef{ID: l.ID, PublicID: l.PublicID, Label: l.Label, Path: l.PathPrefix}},
	})
}

// FileWithheld reports that content screening withheld paths on a site,
// once per key. Unlike the owner's email, it names the site by ID too, for
// webhooks that list sites.
func (s *Service) FileWithheld(ctx context.Context, eventKey, ownerID, siteID, siteName string, paths []string) {
	s.publish(ctx, webhookEvent{
		key: eventKey, kind: EventFileWithheld, ownerID: ownerID, siteID: siteID,
		data: withheldData{siteRef{ID: siteID, Name: siteName}, paths},
	})
}

// noOptOut is the event no owner can turn off: turning off an email says
// nothing about a webhook.
const noOptOut = ""

// detectWebhooks publishes the events the notification scans already read.
func (s *Service) detectWebhooks(ctx context.Context) {
	now := s.now()
	st := s.c.Store
	var events []webhookEvent
	domain := func(n store.NoticeSubject, status store.DomainStatus, at time.Time) {
		events = append(events, webhookEvent{
			key:  key(EventDomainStatusChanged, n.ID, status, n.At),
			kind: EventDomainStatusChanged, ownerID: n.OwnerID, siteID: n.SiteID, at: at,
			data: domainData{siteRef{ID: n.SiteID, Name: n.SiteName}, domainRef{ID: n.ID, Hostname: n.Name, Status: string(status)}},
		})
	}
	verified, err := st.VerifiedDomains(ctx, noOptOut, now.Add(-recent))
	s.logErr(ctx, "read verified domains", err)
	for _, n := range verified {
		domain(n, store.DomainActive, n.At)
	}
	lapsed, err := st.LapsedDomains(ctx, noOptOut, now.Add(-lapsedWithin))
	s.logErr(ctx, "read lapsed domains", err)
	for _, n := range lapsed {
		// n.At is when it began failing; it lapsed this much later.
		domain(n, store.DomainLapsed, n.At.Add(sites.LapseAfter))
	}
	used, err := st.UsedUpLinks(ctx, noOptOut, now.Add(-recent))
	s.logErr(ctx, "read used-up links", err)
	for _, n := range used {
		events = append(events, webhookEvent{
			key:  key(EventLinkUsedUp, n.ID),
			kind: EventLinkUsedUp, ownerID: n.OwnerID, siteID: n.SiteID, at: n.At,
			data: linkData{siteRef{ID: n.SiteID, Name: n.SiteName}, linkRef{ID: n.ID, PublicID: n.PublicID, Label: n.Name}},
		})
	}
	s.publish(ctx, events...)
}
