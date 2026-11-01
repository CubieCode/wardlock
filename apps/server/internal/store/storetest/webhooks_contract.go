// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

// webhook returns a stored webhook of ownerID for every event and site.
func (f *fixture) webhook(ownerID string) store.Webhook {
	f.t.Helper()
	w := store.Webhook{
		ID: store.NewID(), OwnerID: ownerID, URL: "https://hooks.example.test/" + store.NewID(), Description: "A hook",
		AllEvents: true, AllSites: true, SecretSalt: "salt", CreatedAt: now,
	}
	f.must(f.r.Webhooks.Create(f.t.Context(), w, 100))
	return w
}

// queued returns a new delivery to w with its outbox row.
func (f *fixture) queued(w store.Webhook, key string, at time.Time) store.QueuedDelivery {
	id := store.NewID()
	return store.QueuedDelivery{
		Delivery: store.WebhookDelivery{ID: id, WebhookID: w.ID, EventID: "evt-" + key, Event: "ping", Body: `{"k":"` + key + `"}`},
		Outbox: store.OutboxEntry{
			ID: id, Channel: store.NotificationWebhook, EventKey: w.ID + ":" + key, OwnerID: w.OwnerID,
			Event: "ping", Payload: "{}", CreatedAt: at,
		},
	}
}

func (f *fixture) enqueueDelivery(q store.QueuedDelivery) {
	f.t.Helper()
	n, err := f.r.Webhooks.Enqueue(f.t.Context(), q)
	f.must(err)
	if n != 1 {
		f.t.Fatalf("Enqueue added %d", n)
	}
}

func deliveryIDs(list []store.WebhookDelivery) []string {
	out := make([]string, len(list))
	for i, d := range list {
		out[i] = d.ID
	}
	return out
}

func webhooksContract(t *testing.T, open Opener) {
	t.Run("CreateIsLimitedPerOwner", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		a, b := f.owner(), f.owner()
		w := func(ownerID string) store.Webhook {
			return store.Webhook{ID: store.NewID(), OwnerID: ownerID, URL: "https://a.example.test/", AllEvents: true, AllSites: true, SecretSalt: "s", CreatedAt: now}
		}
		ctx := t.Context()
		for range 2 {
			f.must(f.r.Webhooks.Create(ctx, w(a.ID), 2))
		}
		wantErr(t, f.r.Webhooks.Create(ctx, w(a.ID), 2), store.ErrWebhookLimit)
		f.must(f.r.Webhooks.Create(ctx, w(b.ID), 2))
	})

	t.Run("ScopesRoundTrip", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		ctx := t.Context()
		w := store.Webhook{
			ID: store.NewID(), OwnerID: o.ID, URL: "https://a.example.test/", Events: []string{"b.event", "a.event"},
			Sites: []store.WebhookSite{{ID: s.ID}}, SecretSalt: "s", CreatedAt: now,
		}
		f.must(f.r.Webhooks.Create(ctx, w, 10))
		got, err := f.r.Webhooks.Get(ctx, o.ID, w.ID)
		f.must(err)
		if got.AllEvents || got.AllSites || !slices.Equal(got.Events, []string{"a.event", "b.event"}) ||
			len(got.Sites) != 1 || got.Sites[0] != (store.WebhookSite{ID: s.ID, Name: s.Name}) ||
			got.URL != w.URL || got.SecretSalt != "s" || !got.CreatedAt.Equal(now) {
			t.Fatalf("Get = %+v", got)
		}
		plain := f.webhook(o.ID)
		list, err := f.r.Webhooks.List(ctx, o.ID)
		f.must(err)
		if len(list) != 2 || len(list[0].Events) != 2 || len(list[1].Events) != 0 || !list[1].AllEvents || list[1].ID != plain.ID && list[0].ID != plain.ID {
			t.Fatalf("List = %+v", list)
		}
	})

	t.Run("OwnerIsolation", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		a, b := f.owner(), f.owner()
		w := f.webhook(a.ID)
		q := f.queued(w, "k", now)
		f.enqueueDelivery(q)
		ctx := t.Context()
		wr := f.r.Webhooks
		_, err := wr.Get(ctx, b.ID, w.ID)
		wantErr(t, err, store.ErrNotFound)
		list, err := wr.List(ctx, b.ID)
		f.must(err)
		if len(list) != 0 {
			t.Fatalf("List(b) = %+v", list)
		}
		changed := w
		changed.OwnerID, changed.URL = b.ID, "https://stolen.example.test/"
		wantErr(t, wr.Update(ctx, changed), store.ErrNotFound)
		wantErr(t, wr.Delete(ctx, b.ID, w.ID), store.ErrNotFound)
		ds, err := wr.Deliveries(ctx, b.ID, w.ID, 10)
		f.must(err)
		if len(ds) != 0 {
			t.Fatalf("Deliveries(b) = %+v", ds)
		}
		_, err = wr.Delivery(ctx, b.ID, w.ID, q.Outbox.ID)
		wantErr(t, err, store.ErrNotFound)
		if got, err := wr.Get(ctx, a.ID, w.ID); err != nil || got.URL != w.URL {
			t.Fatalf("after b's attempts: %+v, %v", got, err)
		}
	})

	t.Run("UpdateReplacesScopeAndState", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		w := f.webhook(o.ID)
		ctx := t.Context()
		w.URL, w.Description, w.AllEvents, w.Events = "https://new.example.test/", "New", false, []string{"e"}
		w.AllSites, w.Sites = false, []store.WebhookSite{{ID: s.ID}}
		w.DisabledAt, w.DisabledReason = now, store.WebhookDisabledOwner
		f.must(f.r.Webhooks.Update(ctx, w))
		got, err := f.r.Webhooks.Get(ctx, o.ID, w.ID)
		f.must(err)
		if got.URL != w.URL || got.Description != "New" || got.AllEvents || len(got.Events) != 1 || got.AllSites || len(got.Sites) != 1 ||
			!got.DisabledAt.Equal(now) || got.DisabledReason != store.WebhookDisabledOwner {
			t.Fatalf("Get = %+v", got)
		}
		got.AllEvents, got.Events, got.AllSites, got.Sites = true, nil, true, nil
		got.DisabledAt, got.DisabledReason = time.Time{}, ""
		f.must(f.r.Webhooks.Update(ctx, got))
		got, err = f.r.Webhooks.Get(ctx, o.ID, w.ID)
		f.must(err)
		if !got.AllEvents || len(got.Events) != 0 || !got.AllSites || len(got.Sites) != 0 || !got.DisabledAt.IsZero() || got.DisabledReason != "" {
			t.Fatalf("Get = %+v", got)
		}
	})

	t.Run("DeleteTakesDeliveries", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		w := f.webhook(o.ID)
		q := f.queued(w, "k", now)
		f.enqueueDelivery(q)
		ctx := t.Context()
		f.must(f.r.Webhooks.Delete(ctx, o.ID, w.ID))
		_, err := f.r.Webhooks.ToSend(ctx, q.Outbox.ID)
		wantErr(t, err, store.ErrNotFound)
		wantErr(t, f.r.Webhooks.Delete(ctx, o.ID, w.ID), store.ErrNotFound)
		// The outbox row is settled by the worker; dropping a gone delivery
		// is not an error.
		f.must(f.r.Webhooks.Drop(ctx, q.Outbox.ID, now))
	})

	t.Run("EnqueueSkipsARepeatedKey", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		w := f.webhook(o.ID)
		first := f.queued(w, "k", now)
		f.enqueueDelivery(first)
		other := f.webhook(o.ID)
		n, err := f.r.Webhooks.Enqueue(t.Context(), f.queued(w, "k", now), f.queued(other, "k", now))
		f.must(err)
		if n != 1 {
			t.Fatalf("Enqueue added %d, want the other webhook's", n)
		}
		ds, err := f.r.Webhooks.Deliveries(t.Context(), o.ID, w.ID, 10)
		f.must(err)
		if !slices.Equal(deliveryIDs(ds), []string{first.Outbox.ID}) || ds[0].Status != store.WebhookDeliveryPending ||
			ds[0].Body != first.Delivery.Body || ds[0].EventID != "evt-k" || !ds[0].CreatedAt.Equal(now) {
			t.Fatalf("Deliveries = %+v", ds)
		}
		due, err := f.r.Notifications.Due(t.Context(), store.NotificationWebhook, now, 10)
		f.must(err)
		if len(due) != 2 {
			t.Fatalf("Due = %+v", due)
		}
	})

	t.Run("RecordAttemptTracksFailures", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		w := f.webhook(o.ID)
		q := f.queued(w, "k", now)
		f.enqueueDelivery(q)
		ctx := t.Context()
		wr := f.r.Webhooks
		attempt := store.WebhookAttempt{
			DeliveryID: q.Outbox.ID, WebhookID: w.ID, Status: store.WebhookDeliveryPending,
			ResponseCode: 503, Duration: 12 * time.Millisecond, Error: "status", At: now,
		}
		f.must(wr.RecordAttempt(ctx, attempt, 10))
		attempt.At = now.Add(time.Hour)
		f.must(wr.RecordAttempt(ctx, attempt, 10))
		send, err := wr.ToSend(ctx, q.Outbox.ID)
		f.must(err)
		if !send.FailingSince.Equal(now) || send.URL != w.URL || send.Body != q.Delivery.Body || send.SecretSalt != "salt" || send.Disabled {
			t.Fatalf("ToSend = %+v", send)
		}
		d, err := wr.Delivery(ctx, o.ID, w.ID, q.Outbox.ID)
		f.must(err)
		if d.Attempts != 2 || d.ResponseCode != 503 || d.Duration != 12*time.Millisecond || d.Error != "status" || !d.FinishedAt.IsZero() || d.Status != store.WebhookDeliveryPending {
			t.Fatalf("Delivery = %+v", d)
		}
		f.must(wr.RecordAttempt(ctx, store.WebhookAttempt{
			DeliveryID: q.Outbox.ID, WebhookID: w.ID, Status: store.WebhookDeliverySucceeded, OK: true, ResponseCode: 200, At: now.Add(2 * time.Hour),
		}, 10))
		if d, err = wr.Delivery(ctx, o.ID, w.ID, q.Outbox.ID); err != nil || d.Status != store.WebhookDeliverySucceeded || d.Error != "" || d.FinishedAt.IsZero() {
			t.Fatalf("Delivery = %+v, %v", d, err)
		}
		if send, err = wr.ToSend(ctx, q.Outbox.ID); err != nil || !send.FailingSince.IsZero() {
			t.Fatalf("a success keeps the failing mark: %+v, %v", send, err)
		}
	})

	t.Run("RecordAttemptKeepsTheNewest", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		w, other := f.webhook(o.ID), f.webhook(o.ID)
		ctx := t.Context()
		var ids []string
		for i := range 5 {
			q := f.queued(w, fmt.Sprint(i), now.Add(time.Duration(i)*time.Minute))
			f.enqueueDelivery(q)
			ids = append(ids, q.Outbox.ID)
		}
		theirs := f.queued(other, "k", now)
		f.enqueueDelivery(theirs)
		f.must(f.r.Webhooks.RecordAttempt(ctx, store.WebhookAttempt{DeliveryID: theirs.Outbox.ID, WebhookID: other.ID, Status: store.WebhookDeliverySucceeded, OK: true, At: now}, 1))
		// Only finished rows go, however old a pending one is.
		for _, i := range []int{1, 2, 4} {
			f.must(f.r.Webhooks.RecordAttempt(ctx, store.WebhookAttempt{DeliveryID: ids[i], WebhookID: w.ID, Status: store.WebhookDeliveryFailed, At: now}, 2))
		}
		ds, err := f.r.Webhooks.Deliveries(ctx, o.ID, w.ID, 10)
		f.must(err)
		if want := []string{ids[4], ids[3], ids[0]}; !slices.Equal(deliveryIDs(ds), want) {
			t.Fatalf("Deliveries = %v, want %v", deliveryIDs(ds), want)
		}
		if ds, err = f.r.Webhooks.Deliveries(ctx, o.ID, other.ID, 10); err != nil || len(ds) != 1 {
			t.Fatalf("the other webhook's = %+v, %v", ds, err)
		}
		if ds, err = f.r.Webhooks.Deliveries(ctx, o.ID, w.ID, 2); err != nil || len(ds) != 2 {
			t.Fatalf("limited = %+v, %v", ds, err)
		}
	})

	t.Run("DisableOnceWithItsNotice", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		w := f.webhook(o.ID)
		ctx := t.Context()
		f.must(f.r.Webhooks.Disable(ctx, w.ID, now, f.outbox(o.ID, "off", now)))
		got, err := f.r.Webhooks.Get(ctx, o.ID, w.ID)
		f.must(err)
		if !got.DisabledAt.Equal(now) || got.DisabledReason != store.WebhookDisabledFailing {
			t.Fatalf("Get = %+v", got)
		}
		f.must(f.r.Webhooks.Disable(ctx, w.ID, now.Add(time.Hour), f.outbox(o.ID, "off again", now)))
		if got, err = f.r.Webhooks.Get(ctx, o.ID, w.ID); err != nil || !got.DisabledAt.Equal(now) {
			t.Fatalf("Get = %+v, %v", got, err)
		}
		if due := f.due(now); len(due) != 1 || due[0].EventKey != "off" {
			t.Fatalf("Due = %+v", due)
		}
	})

	t.Run("DropEndsOnlyPending", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		w := f.webhook(o.ID)
		pending, done := f.queued(w, "a", now), f.queued(w, "b", now)
		f.enqueueDelivery(pending)
		f.enqueueDelivery(done)
		ctx := t.Context()
		f.must(f.r.Webhooks.RecordAttempt(ctx, store.WebhookAttempt{DeliveryID: done.Outbox.ID, WebhookID: w.ID, Status: store.WebhookDeliverySucceeded, OK: true, At: now}, 10))
		f.must(f.r.Webhooks.Drop(ctx, pending.Outbox.ID, now))
		f.must(f.r.Webhooks.Drop(ctx, done.Outbox.ID, now))
		for id, want := range map[string]store.WebhookDeliveryStatus{pending.Outbox.ID: store.WebhookDeliveryDropped, done.Outbox.ID: store.WebhookDeliverySucceeded} {
			d, err := f.r.Webhooks.Delivery(ctx, o.ID, w.ID, id)
			f.must(err)
			if d.Status != want {
				t.Fatalf("%s = %s, want %s", id, d.Status, want)
			}
		}
	})

	t.Run("ADeletedSiteStaysListedUntilForgotten", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		s := f.site(o.ID)
		ctx := t.Context()
		w := store.Webhook{ID: store.NewID(), OwnerID: o.ID, URL: "https://a.example.test/", AllEvents: true, Sites: []store.WebhookSite{{ID: s.ID}}, SecretSalt: "s", CreatedAt: now}
		f.must(f.r.Webhooks.Create(ctx, w, 10))
		f.must(f.r.Sites.Delete(ctx, store.ActorFor(o.ID), s.ID))
		got, err := f.r.Webhooks.Get(ctx, o.ID, w.ID)
		f.must(err)
		if len(got.Sites) != 1 || got.Sites[0] != (store.WebhookSite{ID: s.ID}) {
			t.Fatalf("after the site went: %+v", got.Sites)
		}
		f.must(f.r.Webhooks.ForgetSite(ctx, s.ID))
		got, err = f.r.Webhooks.Get(ctx, o.ID, w.ID)
		f.must(err)
		if len(got.Sites) != 0 || got.AllSites {
			t.Fatalf("after forgetting it: %+v", got)
		}
	})

	t.Run("RequeueInterruptedIsPerChannel", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		w := f.webhook(o.ID)
		q := f.queued(w, "k", now)
		f.enqueueDelivery(q)
		mail := f.outbox(o.ID, "m", now)
		f.enqueue(mail)
		n := f.r.Notifications
		ctx := t.Context()
		f.must(n.Claim(ctx, q.Outbox.ID))
		f.must(n.Claim(ctx, mail.ID))
		later := now.Add(time.Hour)
		got, err := n.RequeueInterrupted(ctx, store.NotificationWebhook, later)
		f.must(err)
		if got != 1 {
			t.Fatalf("RequeueInterrupted = %d", got)
		}
		due, err := n.Due(ctx, store.NotificationWebhook, later, 10)
		f.must(err)
		if len(due) != 1 || due[0].ID != q.Outbox.ID || due[0].Attempts != 1 {
			t.Fatalf("Due = %+v", due)
		}
		if d := f.due(later); len(d) != 0 {
			t.Fatalf("an email in an attempt was requeued: %+v", d)
		}
	})
}
