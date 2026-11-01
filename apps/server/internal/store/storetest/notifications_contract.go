// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"slices"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

// outbox returns a valid email outbox row of ownerID.
func (f *fixture) outbox(ownerID, key string, at time.Time) store.OutboxEntry {
	return store.OutboxEntry{
		ID: store.NewID(), Channel: store.NotificationEmail, EventKey: key, OwnerID: ownerID,
		Event: "domain_verified", Payload: "{}", CreatedAt: at,
	}
}

func (f *fixture) enqueue(e store.OutboxEntry) {
	f.t.Helper()
	n, err := f.r.Notifications.Enqueue(f.t.Context(), e)
	f.must(err)
	if n != 1 {
		f.t.Fatalf("Enqueue(%s) added %d", e.EventKey, n)
	}
}

// due lists the email rows due at at.
func (f *fixture) due(at time.Time) []store.OutboxEntry {
	f.t.Helper()
	rows, err := f.r.Notifications.Due(f.t.Context(), store.NotificationEmail, at, 100)
	f.must(err)
	return rows
}

func subjectIDs(list []store.NoticeSubject) []string {
	out := make([]string, len(list))
	for i, n := range list {
		out[i] = n.ID
	}
	return out
}

func notificationsContract(t *testing.T, open Opener) {
	const event = "an_event"
	hour := time.Hour

	t.Run("OptOuts", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		a, b := f.owner(), f.owner()
		n := f.r.Notifications
		ctx := t.Context()
		f.must(n.SetOptOuts(ctx, a.ID, map[string]bool{"one": true, "two": true}))
		f.must(n.SetOptOuts(ctx, b.ID, map[string]bool{"three": true}))
		// Turning one back on leaves the other, and a repeat changes nothing.
		f.must(n.SetOptOuts(ctx, a.ID, map[string]bool{"one": false, "two": true}))
		got, err := n.OptOuts(ctx, a.ID)
		f.must(err)
		if !slices.Equal(got, []string{"two"}) {
			t.Fatalf("OptOuts(a) = %v", got)
		}
		got, err = n.OptOuts(ctx, b.ID)
		f.must(err)
		if !slices.Equal(got, []string{"three"}) {
			t.Fatalf("OptOuts(b) = %v", got)
		}
	})

	t.Run("Recipient", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		ctx := t.Context()
		r, err := f.r.Notifications.Recipient(ctx, o.ID)
		f.must(err)
		if r.Email != o.Email || r.Suspended {
			t.Fatalf("Recipient = %+v", r)
		}
		f.must(f.r.Owners.Suspend(ctx, o.ID, now))
		if r, err = f.r.Notifications.Recipient(ctx, o.ID); err != nil || !r.Suspended {
			t.Fatalf("Recipient of a suspended owner = %+v, %v", r, err)
		}
		_, err = f.r.Notifications.Recipient(ctx, "nobody")
		wantErr(t, err, store.ErrNotFound)
	})

	t.Run("EnqueueOncePerChannelAndKey", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		first := f.outbox(o.ID, "k", now)
		f.enqueue(first)
		again := f.outbox(o.ID, "k", now.Add(hour))
		other := f.outbox(o.ID, "k2", now)
		// A channel of its own reuses a key: I6.3's webhooks will.
		webhook := f.outbox(o.ID, "k", now)
		webhook.Channel = "webhook"
		added, err := f.r.Notifications.Enqueue(t.Context(), again, other, webhook)
		f.must(err)
		if added != 2 {
			t.Fatalf("Enqueue added %d, want 2", added)
		}
		rows := f.due(now.Add(2 * hour))
		if len(rows) != 2 || rows[0].ID != first.ID || rows[1].ID != other.ID {
			t.Fatalf("Due = %+v", rows)
		}
		got := rows[0]
		if got.Status != store.NotificationPending || got.Payload != "{}" || got.Attempts != 0 || !got.CreatedAt.Equal(now) || !got.NextAttemptAt.Equal(now) {
			t.Fatalf("stored row = %+v", got)
		}
		webhooks, err := f.r.Notifications.Due(t.Context(), "webhook", now, 10)
		f.must(err)
		if len(webhooks) != 1 || webhooks[0].ID != webhook.ID {
			t.Fatalf("webhook Due = %+v", webhooks)
		}
	})

	t.Run("AttemptLifecycle", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		ctx := t.Context()
		n := f.r.Notifications
		e := f.outbox(o.ID, "k", now)
		f.enqueue(e)
		if rows := f.due(now.Add(-time.Millisecond)); len(rows) != 0 {
			t.Fatalf("due before its time: %+v", rows)
		}
		f.must(n.Claim(ctx, e.ID))
		wantErr(t, n.Claim(ctx, e.ID), store.ErrNotFound)
		if rows := f.due(now); len(rows) != 0 {
			t.Fatalf("a row in an attempt is due: %+v", rows)
		}
		next := now.Add(hour)
		f.must(n.Reschedule(ctx, e.ID, next))
		wantErr(t, n.Reschedule(ctx, e.ID, next), store.ErrNotFound)
		if rows := f.due(now); len(rows) != 0 {
			t.Fatalf("due before the retry: %+v", rows)
		}
		rows := f.due(next)
		if len(rows) != 1 || rows[0].Attempts != 1 {
			t.Fatalf("Due after a retry = %+v", rows)
		}
		f.must(n.Claim(ctx, e.ID))
		f.must(n.Settle(ctx, e.ID, store.NotificationSent, next))
		wantErr(t, n.Settle(ctx, e.ID, store.NotificationFailed, next), store.ErrNotFound)
		wantErr(t, n.Claim(ctx, e.ID), store.ErrNotFound)
		sent, err := n.CountSent(ctx, o.ID, store.NotificationEmail, next)
		f.must(err)
		if sent != 1 {
			t.Fatalf("CountSent = %d", sent)
		}
		if sent, err = n.CountSent(ctx, o.ID, store.NotificationEmail, next.Add(time.Millisecond)); err != nil || sent != 0 {
			t.Fatalf("CountSent later = %d, %v", sent, err)
		}
		if sent, err = n.CountSent(ctx, f.owner().ID, store.NotificationEmail, now); err != nil || sent != 0 {
			t.Fatalf("CountSent of another owner = %d, %v", sent, err)
		}
	})

	t.Run("FailInterrupted", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		ctx := t.Context()
		n := f.r.Notifications
		stuck, waiting := f.outbox(o.ID, "stuck", now), f.outbox(o.ID, "waiting", now)
		stuck.Payload = `{"a":1}`
		f.enqueue(stuck)
		f.enqueue(waiting)
		f.must(n.Claim(ctx, stuck.ID))
		got, err := n.FailInterrupted(ctx, store.NotificationEmail, now)
		f.must(err)
		if got != 1 {
			t.Fatalf("FailInterrupted = %d", got)
		}
		rows := f.due(now)
		if len(rows) != 1 || rows[0].ID != waiting.ID {
			t.Fatalf("Due = %+v", rows)
		}
		wantErr(t, n.Claim(ctx, stuck.ID), store.ErrNotFound)
	})

	t.Run("HoldAndSummarise", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o, other := f.owner(), f.owner()
		ctx := t.Context()
		n := f.r.Notifications
		a, b := f.outbox(o.ID, "a", now), f.outbox(o.ID, "b", now.Add(hour))
		c := f.outbox(other.ID, "c", now)
		for _, e := range []store.OutboxEntry{a, b, c} {
			f.enqueue(e)
			f.must(n.Settle(ctx, e.ID, store.NotificationHeld, now))
		}
		owners, err := n.HeldOwners(ctx, store.NotificationEmail)
		f.must(err)
		want := []string{o.ID, other.ID}
		slices.Sort(want)
		if !slices.Equal(owners, want) {
			t.Fatalf("HeldOwners = %v, want %v", owners, want)
		}
		held, err := n.Held(ctx, o.ID, store.NotificationEmail)
		f.must(err)
		if len(held) != 2 || held[0].ID != a.ID || held[1].ID != b.ID {
			t.Fatalf("Held = %+v", held)
		}
		summary := f.outbox(o.ID, "summary", now)
		f.must(n.Summarise(ctx, summary, []string{a.ID, b.ID}, now))
		if held, err = n.Held(ctx, o.ID, store.NotificationEmail); err != nil || len(held) != 0 {
			t.Fatalf("Held after Summarise = %+v, %v", held, err)
		}
		rows := f.due(now)
		if len(rows) != 1 || rows[0].ID != summary.ID {
			t.Fatalf("Due after Summarise = %+v", rows)
		}
		if held, err = n.Held(ctx, other.ID, store.NotificationEmail); err != nil || len(held) != 1 {
			t.Fatalf("another owner's held rows = %+v, %v", held, err)
		}
	})

	t.Run("Prune", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		o := f.owner()
		ctx := t.Context()
		n := f.r.Notifications
		done, held, waiting, fresh := f.outbox(o.ID, "done", now), f.outbox(o.ID, "held", now), f.outbox(o.ID, "waiting", now), f.outbox(o.ID, "fresh", now)
		for _, e := range []store.OutboxEntry{done, held, waiting, fresh} {
			f.enqueue(e)
		}
		f.must(n.Settle(ctx, done.ID, store.NotificationDropped, now))
		f.must(n.Settle(ctx, held.ID, store.NotificationHeld, now))
		f.must(n.Settle(ctx, fresh.ID, store.NotificationDropped, now.Add(hour)))
		got, err := n.Prune(ctx, now)
		f.must(err)
		if got != 1 {
			t.Fatalf("Prune = %d, want only the finished row", got)
		}
		// A pruned key can be queued again; a kept one cannot.
		added, err := n.Enqueue(ctx, f.outbox(o.ID, "done", now), f.outbox(o.ID, "fresh", now))
		f.must(err)
		if added != 1 {
			t.Fatalf("Enqueue after Prune added %d", added)
		}
	})

	// Each scan: a matching row is listed with its owner, and it leaves
	// the list for a suspended owner, an owner who turned the event off
	// and, where a site is involved, a taken-down site.
	domain := func(f *fixture, ownerID, siteID string, change func(*store.Domain)) store.Domain {
		f.t.Helper()
		d := f.validDomain(siteID)
		f.must(f.r.Domains.Create(f.t.Context(), store.ActorFor(ownerID), d))
		next := d
		next.Status = store.DomainActive
		next.LastCheckedAt = now
		next.VerifiedAt = now
		change(&next)
		if !next.FailingSince.IsZero() {
			next.LastFailureReason = store.DomainNoRecord
		}
		f.must(f.r.Domains.RecordCheck(f.t.Context(), d, next))
		return next
	}
	link := func(f *fixture, ownerID, siteID string, change func(*store.ShareLink)) store.ShareLink {
		f.t.Helper()
		l := f.validLink(siteID)
		change(&l)
		f.must(f.r.Links.Create(f.t.Context(), store.ActorFor(ownerID), l))
		return l
	}
	token := func(f *fixture, ownerID string, created, expires time.Time) store.APIToken {
		f.t.Helper()
		tok := store.APIToken{
			ID: store.NewID(), OwnerID: ownerID, PublicID: f.publicID(), TokenHash: f.hash(), Name: "ci",
			ScopeDeploy: true, CreatedAt: created, ExpiresAt: expires,
		}
		f.must(f.r.APITokens.Create(f.t.Context(), tok))
		return tok
	}

	scans := []struct {
		name  string
		build func(f *fixture, o store.Owner, s store.Site) string
		scan  func(f *fixture) ([]store.NoticeSubject, error)
	}{
		{
			name: "VerifiedDomains",
			build: func(f *fixture, o store.Owner, s store.Site) string {
				domain(f, o.ID, s.ID, func(d *store.Domain) { d.VerifiedAt = now.Add(-2 * hour) })
				return domain(f, o.ID, s.ID, func(*store.Domain) {}).ID
			},
			scan: func(f *fixture) ([]store.NoticeSubject, error) {
				return f.r.Notifications.VerifiedDomains(f.t.Context(), event, now.Add(-hour))
			},
		},
		{
			name: "FailingDomains",
			build: func(f *fixture, o store.Owner, s store.Site) string {
				// Its failed checks span less than a day.
				domain(f, o.ID, s.ID, func(d *store.Domain) { d.FailingSince = now.Add(-23 * hour) })
				return domain(f, o.ID, s.ID, func(d *store.Domain) { d.FailingSince = now.Add(-24 * hour) }).ID
			},
			scan: func(f *fixture) ([]store.NoticeSubject, error) {
				return f.r.Notifications.FailingDomains(f.t.Context(), event, 24*hour)
			},
		},
		{
			name: "LapsedDomains",
			build: func(f *fixture, o store.Owner, s store.Site) string {
				domain(f, o.ID, s.ID, func(d *store.Domain) {
					d.Status, d.FailingSince = store.DomainLapsed, now.Add(-30*24*hour)
				})
				return domain(f, o.ID, s.ID, func(d *store.Domain) {
					d.Status, d.FailingSince = store.DomainLapsed, now.Add(-72*hour)
				}).ID
			},
			scan: func(f *fixture) ([]store.NoticeSubject, error) {
				return f.r.Notifications.LapsedDomains(f.t.Context(), event, now.Add(-7*24*hour))
			},
		},
		{
			name: "UsedUpLinks",
			build: func(f *fixture, o store.Owner, s store.Site) string {
				ctx := f.t.Context()
				spent := link(f, o.ID, s.ID, func(l *store.ShareLink) { l.MaxUses = 2 })
				f.must(f.r.Links.Use(ctx, spent.ID, spent.TokenHash, now))
				unspent := link(f, o.ID, s.ID, func(l *store.ShareLink) { l.MaxUses = 2 })
				f.must(f.r.Links.Use(ctx, unspent.ID, unspent.TokenHash, now))
				unlimited := link(f, o.ID, s.ID, func(*store.ShareLink) {})
				f.must(f.r.Links.Use(ctx, unlimited.ID, unlimited.TokenHash, now))
				revoked := link(f, o.ID, s.ID, func(l *store.ShareLink) { l.MaxUses = 1 })
				f.must(f.r.Links.Use(ctx, revoked.ID, revoked.TokenHash, now))
				f.must(f.r.Links.Revoke(ctx, store.ActorFor(o.ID), s.ID, revoked.PublicID, now))
				f.must(f.r.Links.Use(ctx, spent.ID, spent.TokenHash, now))
				return spent.ID
			},
			scan: func(f *fixture) ([]store.NoticeSubject, error) {
				return f.r.Notifications.UsedUpLinks(f.t.Context(), event, now.Add(-hour))
			},
		},
		{
			name: "ExpiringLinks",
			build: func(f *fixture, o store.Owner, s store.Site) string {
				early := now.Add(-48 * hour)
				link(f, o.ID, s.ID, func(l *store.ShareLink) { l.CreatedAt, l.ExpiresAt = early, now.Add(72*hour) })
				link(f, o.ID, s.ID, func(l *store.ShareLink) { l.CreatedAt, l.ExpiresAt = now, now.Add(30*time.Minute) })
				link(f, o.ID, s.ID, func(l *store.ShareLink) {
					l.CreatedAt, l.ExpiresAt = early, now.Add(hour)
					l.MaxUses, l.UseCount = 1, 1
				})
				return link(f, o.ID, s.ID, func(l *store.ShareLink) { l.CreatedAt, l.ExpiresAt = early, now.Add(hour) }).ID
			},
			scan: func(f *fixture) ([]store.NoticeSubject, error) {
				return f.r.Notifications.ExpiringLinks(f.t.Context(), event, now, now.Add(48*hour), hour)
			},
		},
	}
	for _, sc := range scans {
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, open)
			o := f.owner()
			s := f.site(o.ID)
			id := sc.build(f, o, s)
			list, err := sc.scan(f)
			f.must(err)
			if len(list) != 1 || list[0].ID != id || list[0].OwnerID != o.ID || list[0].SiteName != s.Name || list[0].At.IsZero() {
				t.Fatalf("scan = %+v, want only %s", list, id)
			}
			f.must(f.r.Notifications.SetOptOuts(t.Context(), o.ID, map[string]bool{"other": true}))
			if list, err = sc.scan(f); err != nil || len(list) != 1 {
				t.Fatalf("with another event off: %v, %v", subjectIDs(list), err)
			}
			f.must(f.r.Notifications.SetOptOuts(t.Context(), o.ID, map[string]bool{event: true}))
			if list, err = sc.scan(f); err != nil || len(list) != 0 {
				t.Fatalf("with the event off: %v, %v", subjectIDs(list), err)
			}
			f.must(f.r.Notifications.SetOptOuts(t.Context(), o.ID, map[string]bool{event: false}))
			f.must(f.r.Sites.TakeDown(t.Context(), s.ID, now))
			if list, err = sc.scan(f); err != nil || len(list) != 0 {
				t.Fatalf("site taken down: %v, %v", subjectIDs(list), err)
			}
			f.must(f.r.Sites.Restore(t.Context(), s.ID))
			f.must(f.r.Owners.Suspend(t.Context(), o.ID, now))
			if list, err = sc.scan(f); err != nil || len(list) != 0 {
				t.Fatalf("owner suspended: %v, %v", subjectIDs(list), err)
			}
		})
	}

	t.Run("ExpiringTokens", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		ctx := t.Context()
		n := f.r.Notifications
		o := f.owner()
		early, by := now.Add(-30*24*hour), now.Add(7*24*hour)
		want := token(f, o.ID, early, now.Add(hour))
		token(f, o.ID, early, by.Add(hour))             // not yet
		token(f, o.ID, early, time.Time{})              // never
		token(f, o.ID, now.Add(-hour), now.Add(hour))   // short-lived
		token(f, o.ID, early, now.Add(-hour))           // expired
		revoked := token(f, o.ID, early, now.Add(hour)) // revoked
		f.must(f.r.APITokens.Revoke(ctx, o.ID, revoked.ID, now))
		list, err := n.ExpiringTokens(ctx, event, now, by, 7*24*hour)
		f.must(err)
		if len(list) != 1 || list[0].ID != want.ID || list[0].Name != "ci" || list[0].SiteName != "" || !list[0].At.Equal(want.ExpiresAt) {
			t.Fatalf("ExpiringTokens = %+v", list)
		}
		f.must(n.SetOptOuts(ctx, o.ID, map[string]bool{event: true}))
		if list, err = n.ExpiringTokens(ctx, event, now, by, 7*24*hour); err != nil || len(list) != 0 {
			t.Fatalf("with the event off: %+v, %v", list, err)
		}
		f.must(n.SetOptOuts(ctx, o.ID, map[string]bool{event: false}))
		f.must(f.r.Owners.Suspend(ctx, o.ID, now))
		if list, err = n.ExpiringTokens(ctx, event, now, by, 7*24*hour); err != nil || len(list) != 0 {
			t.Fatalf("owner suspended: %+v, %v", list, err)
		}
	})

	t.Run("ExpiringPreviews", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		ctx := t.Context()
		n := f.r.Notifications
		o := f.owner()
		s := f.site(o.ID)
		deployed := func(name string, days int, at time.Time) {
			d := f.validDeploy(s.ID)
			d.Preview, d.PreviewDays, d.CreatedAt = name, &days, at
			f.must(f.r.Deploys.Create(ctx, store.ActorFor(o.ID), d, []store.DeployFile{{Path: "index.html", SHA256: sha('b'), Size: 5}}, store.Quotas{}))
			f.uploadAll(o.ID, s.ID, d.ID)
			_, err := f.r.Deploys.Activate(ctx, store.ActorFor(o.ID), s.ID, d.ID, at)
			f.must(err)
		}
		deployed("soon", 7, now.Add(-6*24*hour))
		deployed("later", 14, now.Add(-6*24*hour))
		deployed("short", 1, now.Add(-12*hour))
		deployed("never", 0, now)
		by := now.Add(2 * 24 * hour)
		list, err := n.ExpiringPreviews(ctx, event, now, by, 2*24*hour)
		f.must(err)
		if len(list) != 1 || list[0].Name != "soon" || list[0].SiteID != s.ID || list[0].SiteName != s.Name || list[0].OwnerID != o.ID {
			t.Fatalf("ExpiringPreviews = %+v", list)
		}
		f.must(n.SetOptOuts(ctx, o.ID, map[string]bool{event: true}))
		if list, err = n.ExpiringPreviews(ctx, event, now, by, 2*24*hour); err != nil || len(list) != 0 {
			t.Fatalf("with the event off: %+v, %v", list, err)
		}
		f.must(n.SetOptOuts(ctx, o.ID, map[string]bool{event: false}))
		f.must(f.r.Sites.TakeDown(ctx, s.ID, now))
		if list, err = n.ExpiringPreviews(ctx, event, now, by, 2*24*hour); err != nil || len(list) != 0 {
			t.Fatalf("site taken down: %+v, %v", list, err)
		}
	})
}
