// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package notify_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"wardlock.nz/server/internal/mail"
	"wardlock.nz/server/internal/notify"
	"wardlock.nz/server/internal/store"
	"wardlock.nz/server/internal/store/storetest"
)

const (
	hour = time.Hour
	day  = 24 * time.Hour

	dashboard = "https://app.example.test"
	// secret stands in for a share link's or API token's secret, stored
	// only as a hash in the rows under test.
	secret = "wl_shh00000001_notInAnyEmail"
)

// mailbox is a fake mailer that fails while err is set.
type mailbox struct {
	mu   sync.Mutex
	sent []mail.Message
	err  error
}

func (m *mailbox) Send(_ context.Context, msg mail.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.sent = append(m.sent, msg)
	return nil
}

func (m *mailbox) fail(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err
}

func (m *mailbox) subjects() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.sent))
	for i, msg := range m.sent {
		out[i] = msg.To.Address + ": " + msg.Subject
	}
	return out
}

// world is a real database, a fake clock and a fake mailer. Service returns
// a new Service over them, as a restart would.
type world struct {
	t     *testing.T
	db    *store.DB
	box   *mailbox
	clock time.Time
	cap   int64
	n     int
}

func newWorld(t *testing.T) *world {
	return &world{
		t: t, db: storetest.New(t), box: &mailbox{}, cap: notify.DefaultCap,
		clock: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
	}
}

func (w *world) must(err error) {
	w.t.Helper()
	if err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) service() *notify.Service {
	return notify.New(notify.Config{
		Store: w.db.Notifications(), Mailer: w.box, DashboardURL: dashboard, Cap: w.cap,
		Now: func() time.Time { return w.clock },
	})
}

// pass is one run of the worker from a fresh start.
func (w *world) pass() {
	s := w.service()
	s.Recover(w.t.Context())
	s.Detect(w.t.Context())
	s.Deliver(w.t.Context())
}

func (w *world) owner(name string) store.Owner {
	w.t.Helper()
	o := store.Owner{ID: store.NewID(), Email: name + "@example.test", CreatedAt: w.clock.Add(-90 * day)}
	w.must(w.db.Owners().Create(w.t.Context(), o))
	return o
}

func (w *world) site(ownerID, name string) store.Site {
	w.t.Helper()
	s := store.Site{ID: store.NewID(), OwnerID: ownerID, Name: name, CreatedAt: w.clock.Add(-90 * day)}
	w.must(w.db.Sites().Create(w.t.Context(), s, store.Quotas{}))
	return s
}

func (w *world) link(ownerID, siteID string, change func(*store.ShareLink)) store.ShareLink {
	w.t.Helper()
	w.n++
	l := store.ShareLink{
		ID: store.NewID(), SiteID: siteID, PublicID: fmt.Sprintf("p%09d", w.n), TokenHash: fmt.Sprintf("hash-%d", w.n),
		PathPrefix: "/", CreatedAt: w.clock.Add(-30 * day),
	}
	change(&l)
	w.must(w.db.Links().Create(w.t.Context(), ownerID, l))
	return l
}

func (w *world) usedUpLink(ownerID, siteID, label string) store.ShareLink {
	w.t.Helper()
	l := w.link(ownerID, siteID, func(l *store.ShareLink) { l.MaxUses, l.Label = 1, label })
	w.must(w.db.Links().Use(w.t.Context(), l.ID, l.TokenHash, w.clock))
	return l
}

func (w *world) domain(ownerID, siteID, hostname string, change func(*store.Domain)) store.Domain {
	w.t.Helper()
	d := store.Domain{
		ID: store.NewID(), SiteID: siteID, Hostname: hostname, Status: store.DomainPending,
		VerificationToken: "verify-" + hostname, CreatedAt: w.clock.Add(-30 * day),
	}
	w.must(w.db.Domains().Create(w.t.Context(), ownerID, d))
	next := d
	next.Status, next.LastCheckedAt, next.VerifiedAt = store.DomainActive, w.clock, w.clock.Add(-20*day)
	change(&next)
	if !next.FailingSince.IsZero() {
		next.LastFailureReason = store.DomainNoRecord
	}
	w.must(w.db.Domains().RecordCheck(w.t.Context(), d, next))
	return next
}

func (w *world) token(ownerID, name string, expires time.Time) store.APIToken {
	w.t.Helper()
	w.n++
	tok := store.APIToken{
		ID: store.NewID(), OwnerID: ownerID, PublicID: fmt.Sprintf("t%09d", w.n), TokenHash: fmt.Sprintf("hash-%d", w.n),
		Name: name, ScopeDeploy: true, CreatedAt: w.clock.Add(-60 * day), ExpiresAt: expires,
	}
	w.must(w.db.APITokens().Create(w.t.Context(), tok))
	return tok
}

func (w *world) preview(ownerID, siteID, name string, days int, at time.Time) {
	w.t.Helper()
	ctx := w.t.Context()
	w.n++
	sha := fmt.Sprintf("%064x", w.n)
	d := store.Deploy{
		ID: store.NewID(), SiteID: siteID, FileCount: 1, TotalSize: 5,
		Preview: name, PreviewDays: &days, CreatedAt: at,
	}
	w.must(w.db.Deploys().Create(ctx, ownerID, d, []store.DeployFile{{Path: "index.html", SHA256: sha, Size: 5}}, store.Quotas{}))
	w.must(w.db.Deploys().RecordUpload(ctx, ownerID, store.Blob{SHA256: sha, Size: 5, CreatedAt: at}))
	_, err := w.db.Deploys().Activate(ctx, ownerID, siteID, d.ID, at)
	w.must(err)
}

func (w *world) setOff(ownerID string, events ...string) {
	w.t.Helper()
	on := map[string]bool{}
	for _, e := range events {
		on[e] = false
	}
	w.must(w.service().SetSettings(w.t.Context(), ownerID, on))
}

// events gives each event one way to happen for an owner, the event an
// owner turns off to stop it and the subject its email has.
var events = []struct {
	name    string
	setting string
	happen  func(w *world, o store.Owner, s store.Site)
	subject string
	path    string
}{
	{
		name: "domain verified", setting: notify.DomainVerified,
		happen: func(w *world, o store.Owner, s store.Site) {
			w.domain(o.ID, s.ID, "docs.example.test", func(d *store.Domain) { d.VerifiedAt = w.clock })
		},
		subject: "docs.example.test is verified", path: "/sites/docs/domains",
	},
	{
		name: "domain failing for a day", setting: notify.DomainFailing,
		happen: func(w *world, o store.Owner, s store.Site) {
			w.domain(o.ID, s.ID, "docs.example.test", func(d *store.Domain) {
				d.FailingSince = w.clock.Add(-day)
			})
		},
		subject: "docs.example.test is failing its DNS checks", path: "/sites/docs/domains",
	},
	{
		name: "domain lapsed", setting: notify.DomainFailing,
		happen: func(w *world, o store.Owner, s store.Site) {
			w.domain(o.ID, s.ID, "docs.example.test", func(d *store.Domain) {
				d.Status, d.FailingSince = store.DomainLapsed, w.clock.Add(-3*day)
			})
		},
		subject: "docs.example.test is no longer served", path: "/sites/docs/domains",
	},
	{
		name: "link used up", setting: notify.LinkUsedUp,
		happen:  func(w *world, o store.Owner, s store.Site) { w.usedUpLink(o.ID, s.ID, "Launch review") },
		subject: "A share link on docs is used up", path: "/sites/docs/links",
	},
	{
		name: "links expiring", setting: notify.LinksExpiring,
		happen: func(w *world, o store.Owner, s store.Site) {
			w.link(o.ID, s.ID, func(l *store.ShareLink) { l.ExpiresAt = w.clock.Add(47 * hour) })
		},
		subject: "A share link expires soon", path: "/sites/docs/links",
	},
	{
		name: "token expiring", setting: notify.TokenExpiring,
		happen:  func(w *world, o store.Owner, _ store.Site) { w.token(o.ID, "ci-deploy", w.clock.Add(6*day)) },
		subject: "An API token expires soon", path: "/tokens",
	},
	{
		name: "preview expiring", setting: notify.PreviewExpiring,
		happen: func(w *world, o store.Owner, s store.Site) {
			w.preview(o.ID, s.ID, "pr-1", 7, w.clock.Add(-6*day))
		},
		subject: "The preview pr-1 of docs expires soon", path: "/sites/docs/previews",
	},
}

func TestEvents(t *testing.T) {
	for _, ev := range events {
		t.Run(ev.name, func(t *testing.T) {
			t.Run("sent once, naming the page and the settings", func(t *testing.T) {
				t.Parallel()
				w := newWorld(t)
				o := w.owner("one")
				ev.happen(w, o, w.site(o.ID, "docs"))
				w.pass()
				if got := w.box.subjects(); !slices.Equal(got, []string{"one@example.test: " + ev.subject}) {
					t.Fatalf("sent %q", got)
				}
				msg := w.box.sent[0]
				for _, want := range []string{dashboard + ev.path, dashboard + "/notifications"} {
					if !strings.Contains(msg.Text, want) || !strings.Contains(msg.HTML, want) {
						t.Fatalf("no %s in\n%s\n%s", want, msg.Text, msg.HTML)
					}
				}
				// A later pass and a restart change nothing.
				w.clock = w.clock.Add(time.Minute)
				w.pass()
				w.pass()
				if n := len(w.box.subjects()); n != 1 {
					t.Fatalf("sent %d times", n)
				}
			})

			t.Run("retried after a mail failure", func(t *testing.T) {
				t.Parallel()
				w := newWorld(t)
				o := w.owner("one")
				ev.happen(w, o, w.site(o.ID, "docs"))
				w.box.fail(errors.New("smtp down"))
				w.pass()
				w.pass() // Not due yet, restart or not.
				w.box.fail(nil)
				w.pass()
				if n := len(w.box.subjects()); n != 0 {
					t.Fatalf("sent %d before the backoff ended", n)
				}
				w.clock = w.clock.Add(notify.DeliverInterval)
				w.pass()
				if n := len(w.box.subjects()); n != 1 {
					t.Fatalf("sent %d after the backoff", n)
				}
			})

			t.Run("respects the setting", func(t *testing.T) {
				t.Parallel()
				w := newWorld(t)
				o := w.owner("one")
				ev.happen(w, o, w.site(o.ID, "docs"))
				w.setOff(o.ID, ev.setting)
				w.pass()
				if got := w.box.subjects(); len(got) != 0 {
					t.Fatalf("sent %q", got)
				}
			})

			t.Run("respects a setting changed once queued", func(t *testing.T) {
				t.Parallel()
				w := newWorld(t)
				o := w.owner("one")
				ev.happen(w, o, w.site(o.ID, "docs"))
				s := w.service()
				s.Detect(t.Context())
				w.setOff(o.ID, ev.setting)
				s.Deliver(t.Context())
				if got := w.box.subjects(); len(got) != 0 {
					t.Fatalf("sent %q", got)
				}
			})

			t.Run("nothing for a suspended owner", func(t *testing.T) {
				t.Parallel()
				w := newWorld(t)
				o := w.owner("one")
				ev.happen(w, o, w.site(o.ID, "docs"))
				s := w.service()
				s.Detect(t.Context()) // Queued first, suspended before it goes.
				w.must(w.db.Owners().Suspend(t.Context(), o.ID, w.clock))
				s.Deliver(t.Context())
				w.pass()
				if got := w.box.subjects(); len(got) != 0 {
					t.Fatalf("sent %q", got)
				}
			})

			t.Run("respects the cap", func(t *testing.T) {
				t.Parallel()
				w := newWorld(t)
				w.cap = 1
				o := w.owner("one")
				s := w.site(o.ID, "docs")
				w.usedUpLink(o.ID, s.ID, "First")
				w.pass()
				before := len(w.box.subjects())
				ev.happen(w, o, s)
				w.pass()
				if got := w.box.subjects(); len(got) != before {
					t.Fatalf("over the cap: %q", got)
				}
			})
		})
	}
}

func TestEventsThatDoNotFire(t *testing.T) {
	cases := []struct {
		name   string
		happen func(w *world, o store.Owner, s store.Site)
	}{
		{"pending domain", func(w *world, o store.Owner, s store.Site) {
			w.must(w.db.Domains().Create(w.t.Context(), o.ID, store.Domain{
				ID: store.NewID(), SiteID: s.ID, Hostname: "new.example.test", Status: store.DomainPending,
				VerificationToken: "a-verification-token", CreatedAt: w.clock,
			}))
		}},
		{"domain failing for under a day", func(w *world, o store.Owner, s store.Site) {
			w.domain(o.ID, s.ID, "docs.example.test", func(d *store.Domain) { d.FailingSince = w.clock.Add(-23 * hour) })
		}},
		{"link with uses left", func(w *world, o store.Owner, s store.Site) {
			l := w.link(o.ID, s.ID, func(l *store.ShareLink) { l.MaxUses = 2 })
			w.must(w.db.Links().Use(w.t.Context(), l.ID, l.TokenHash, w.clock))
		}},
		{"link used up before the upgrade", func(w *world, o store.Owner, s store.Site) {
			w.link(o.ID, s.ID, func(l *store.ShareLink) { l.MaxUses, l.UseCount = 1, 1 })
		}},
		{"link expiring in three days", func(w *world, o store.Owner, s store.Site) {
			w.link(o.ID, s.ID, func(l *store.ShareLink) { l.ExpiresAt = w.clock.Add(3 * day) })
		}},
		{"one-hour link", func(w *world, o store.Owner, s store.Site) {
			w.link(o.ID, s.ID, func(l *store.ShareLink) {
				l.CreatedAt, l.ExpiresAt = w.clock, w.clock.Add(30*time.Minute)
			})
		}},
		{"token expiring in eight days", func(w *world, o store.Owner, _ store.Site) { w.token(o.ID, "later", w.clock.Add(8*day)) }},
		{"token that never expires", func(w *world, o store.Owner, _ store.Site) { w.token(o.ID, "forever", time.Time{}) }},
		{"preview made to last a day", func(w *world, o store.Owner, s store.Site) {
			w.preview(o.ID, s.ID, "short", 1, w.clock)
		}},
		{"preview expiring in three days", func(w *world, o store.Owner, s store.Site) {
			w.preview(o.ID, s.ID, "later", 7, w.clock.Add(-4*day))
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			o := w.owner("one")
			c.happen(w, o, w.site(o.ID, "docs"))
			w.pass()
			if got := w.box.subjects(); len(got) != 0 {
				t.Fatalf("sent %q", got)
			}
		})
	}
}

// TestDomainFailsThenLapses checks the two emails of one failure, and that
// a second failure after a recovery is a new event.
func TestDomainFailsThenLapses(t *testing.T) {
	w := newWorld(t)
	o := w.owner("one")
	s := w.site(o.ID, "docs")
	ctx := t.Context()
	first := w.clock.Add(-day)
	d := w.domain(o.ID, s.ID, "docs.example.test", func(d *store.Domain) {
		d.FailingSince = first
	})
	w.pass()
	w.clock = w.clock.Add(2 * day)
	lapsed := d
	lapsed.Status, lapsed.LastCheckedAt = store.DomainLapsed, w.clock
	w.must(w.db.Domains().RecordCheck(ctx, d, lapsed))
	w.pass()
	want := []string{
		"one@example.test: docs.example.test is failing its DNS checks",
		"one@example.test: docs.example.test is no longer served",
	}
	if got := w.box.subjects(); !slices.Equal(got, want) {
		t.Fatalf("sent %q", got)
	}
	w.clock = w.clock.Add(day)
	recovered := lapsed
	recovered.Status, recovered.LastCheckedAt, recovered.VerifiedAt = store.DomainActive, w.clock, w.clock
	recovered.FailingSince, recovered.LastFailureReason = time.Time{}, ""
	w.must(w.db.Domains().RecordCheck(ctx, lapsed, recovered))
	w.pass()
	failing := recovered
	failing.FailingSince, failing.LastCheckedAt, failing.LastFailureReason = w.clock.Add(-day), w.clock.Add(time.Second), store.DomainNoRecord
	w.must(w.db.Domains().RecordCheck(ctx, recovered, failing))
	w.pass()
	want = append(want, "one@example.test: docs.example.test is verified", "one@example.test: docs.example.test is failing its DNS checks")
	if got := w.box.subjects(); !slices.Equal(got, want) {
		t.Fatalf("after recovering and failing again: %q", got)
	}
}

func TestPreviewDeployedAgainWarnsAgain(t *testing.T) {
	w := newWorld(t)
	o := w.owner("one")
	s := w.site(o.ID, "docs")
	w.preview(o.ID, s.ID, "pr-1", 7, w.clock.Add(-6*day))
	w.pass()
	w.clock = w.clock.Add(day - hour)
	w.preview(o.ID, s.ID, "pr-1", 7, w.clock)
	w.pass()
	w.clock = w.clock.Add(6 * day)
	w.pass()
	if n := len(w.box.subjects()); n != 2 {
		t.Fatalf("sent %d, want one for each expiry", n)
	}
}

// TestLinkDigest checks expiring links reach each owner as one email a day
// listing their own links.
func TestLinkDigest(t *testing.T) {
	w := newWorld(t)
	a, b := w.owner("one"), w.owner("two")
	docs, blog, other := w.site(a.ID, "docs"), w.site(a.ID, "blog"), w.site(b.ID, "shop")
	expires := w.clock.Add(40 * hour)
	expiring := func(owner, site, label string) {
		w.link(owner, site, func(l *store.ShareLink) { l.ExpiresAt, l.Label = expires, label })
	}
	expiring(a.ID, docs.ID, "Launch review")
	expiring(a.ID, blog.ID, "")
	expiring(b.ID, other.ID, "Private shop link")
	w.pass()
	if got := w.box.subjects(); !slices.Equal(got, []string{
		"one@example.test: 2 share links expire soon", "two@example.test: A share link expires soon",
	}) {
		t.Fatalf("sent %q", got)
	}
	first := w.box.sent[0]
	for _, want := range []string{`docs: "Launch review", 3 Sep 2026 04:00 UTC`, "blog: p00000000", dashboard + "/\n"} {
		if !strings.Contains(first.Text, want) {
			t.Errorf("no %q in\n%s", want, first.Text)
		}
	}
	if strings.Contains(first.Text, "Private shop link") || strings.Contains(first.HTML, "Private shop link") {
		t.Fatal("another owner's link in the email")
	}
	// A new link the same day waits for the next day's email.
	w.clock = w.clock.Add(hour)
	expiring(a.ID, docs.ID, "Added later")
	w.pass()
	if n := len(w.box.subjects()); n != 2 {
		t.Fatalf("second email in a day: %q", w.box.subjects())
	}
	w.clock = w.clock.Add(12 * hour)
	w.pass()
	if n := len(w.box.subjects()); n != 4 {
		t.Fatalf("next day: %q", w.box.subjects())
	}
	if !strings.Contains(w.box.sent[2].Text+w.box.sent[3].Text, "Added later") {
		t.Fatal("next day's email leaves out the new link")
	}
}

// TestCap checks an owner past the cap gets the rest in one later email,
// and other owners are not affected.
func TestCap(t *testing.T) {
	w := newWorld(t)
	w.cap = 2
	o, other := w.owner("one"), w.owner("two")
	s, shop := w.site(o.ID, "docs"), w.site(other.ID, "shop")
	for i := range 5 {
		w.usedUpLink(o.ID, s.ID, fmt.Sprintf("Link %d", i))
	}
	w.usedUpLink(other.ID, shop.ID, "Other owner")
	w.pass()
	sentTo := func(addr string) int {
		n := 0
		for _, got := range w.box.subjects() {
			if strings.HasPrefix(got, addr) {
				n++
			}
		}
		return n
	}
	if a, b := sentTo("one@"), sentTo("two@"); a != 2 || b != 1 {
		t.Fatalf("sent %d and %d", a, b)
	}
	// Still inside the hour: no summary, even after a restart.
	w.clock = w.clock.Add(30 * time.Minute)
	w.pass()
	if a := sentTo("one@"); a != 2 {
		t.Fatalf("sent %d within the hour", a)
	}
	// The next pass after the hour summarises and sends it.
	w.clock = w.clock.Add(31 * time.Minute)
	w.pass()
	w.pass()
	if a := sentTo("one@"); a != 3 {
		t.Fatalf("sent %d after the hour: %q", a, w.box.subjects())
	}
	last := w.box.sent[len(w.box.sent)-1]
	if last.Subject != "3 more Wardlock notifications" || strings.Count(last.Text, "A share link on docs is used up") != 3 {
		t.Fatalf("summary: %s\n%s", last.Subject, last.Text)
	}
	w.clock = w.clock.Add(2 * hour)
	w.pass()
	if a := sentTo("one@"); a != 3 {
		t.Fatalf("summarised twice: %q", w.box.subjects())
	}
}

func TestSummaryLeavesOutWhatWasTurnedOff(t *testing.T) {
	w := newWorld(t)
	w.cap = 1
	o := w.owner("one")
	s := w.site(o.ID, "docs")
	w.usedUpLink(o.ID, s.ID, "First")
	w.usedUpLink(o.ID, s.ID, "Second")
	w.pass()
	w.setOff(o.ID, notify.LinkUsedUp)
	w.clock = w.clock.Add(2 * hour)
	w.pass()
	w.pass()
	if got := w.box.subjects(); len(got) != 1 {
		t.Fatalf("sent %q", got)
	}
}

func TestHeldForSuspendedOwnerIsDropped(t *testing.T) {
	w := newWorld(t)
	w.cap = 1
	o := w.owner("one")
	s := w.site(o.ID, "docs")
	w.usedUpLink(o.ID, s.ID, "First")
	w.usedUpLink(o.ID, s.ID, "Second")
	w.pass()
	w.must(w.db.Owners().Suspend(t.Context(), o.ID, w.clock))
	w.clock = w.clock.Add(2 * hour)
	w.pass()
	w.must(w.db.Owners().Reinstate(t.Context(), o.ID))
	w.pass()
	if got := w.box.subjects(); len(got) != 1 {
		t.Fatalf("sent %q", got)
	}
}

func TestWithheld(t *testing.T) {
	d := mail.Withheld{Site: "docs", Host: "app.example.test", Paths: []string{"img/a.png"}, Contact: "abuse@example.test"}
	queue := func(w *world, s *notify.Service, ownerID, key string) {
		w.t.Helper()
		w.must(s.Withheld(w.t.Context(), key, ownerID, d))
	}
	t.Run("the same email as before, once per key", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		o := w.owner("one")
		s := w.service()
		queue(w, s, o.ID, "file_withheld:a")
		queue(w, s, o.ID, "file_withheld:a")
		s.Deliver(t.Context())
		w.pass()
		want, err := mail.WithheldMessage(w.box.sent[0].To, d)
		w.must(err)
		if len(w.box.sent) != 1 || w.box.sent[0].To.Address != o.Email || w.box.sent[0].Text != want.Text || w.box.sent[0].Subject != want.Subject || w.box.sent[0].HTML != "" {
			t.Fatalf("sent %+v, want %+v", w.box.sent, want)
		}
	})
	t.Run("cannot be turned off and passes the cap", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.cap = 1
		o := w.owner("one")
		st := w.site(o.ID, "docs")
		w.usedUpLink(o.ID, st.ID, "First")
		w.pass()
		for _, e := range notify.Switchable {
			w.setOff(o.ID, e)
		}
		s := w.service()
		queue(w, s, o.ID, "file_withheld:a")
		s.Deliver(t.Context())
		if n := len(w.box.subjects()); n != 2 {
			t.Fatalf("sent %q", w.box.subjects())
		}
	})
	t.Run("nothing for a suspended owner", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		o := w.owner("one")
		s := w.service()
		queue(w, s, o.ID, "file_withheld:a")
		w.must(w.db.Owners().Suspend(t.Context(), o.ID, w.clock))
		s.Deliver(t.Context())
		if got := w.box.subjects(); len(got) != 0 {
			t.Fatalf("sent %q", got)
		}
	})
}

func TestRetries(t *testing.T) {
	t.Run("backs off, doubling to an hour", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		o := w.owner("one")
		w.usedUpLink(o.ID, w.site(o.ID, "docs").ID, "Link")
		var attempts []time.Duration
		start := w.clock
		s := w.service()
		w.box.fail(errors.New("smtp down"))
		s.Detect(t.Context())
		for range 8 {
			s.Deliver(t.Context())
			rows, err := w.db.Notifications().Due(t.Context(), store.NotificationEmail, w.clock.Add(2*hour), 10)
			w.must(err)
			if len(rows) != 1 {
				t.Fatalf("Due = %+v", rows)
			}
			attempts = append(attempts, rows[0].NextAttemptAt.Sub(w.clock))
			w.clock = rows[0].NextAttemptAt
		}
		want := []time.Duration{1, 2, 4, 8, 16, 32, 60, 60}
		for i := range want {
			if attempts[i] != want[i]*time.Minute {
				t.Fatalf("waits %v, want %v minutes (from %v)", attempts, want, start)
			}
		}
	})
	t.Run("gives up after three days", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		o := w.owner("one")
		w.usedUpLink(o.ID, w.site(o.ID, "docs").ID, "Link")
		s := w.service()
		w.box.fail(errors.New("smtp down"))
		s.Detect(t.Context())
		s.Deliver(t.Context())
		w.clock = w.clock.Add(73 * hour)
		s.Deliver(t.Context())
		w.box.fail(nil)
		w.clock = w.clock.Add(2 * hour)
		s.Deliver(t.Context())
		if got := w.box.subjects(); len(got) != 0 {
			t.Fatalf("sent %q", got)
		}
	})
	t.Run("a message that cannot be sent is not retried", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		o := w.owner("one")
		w.usedUpLink(o.ID, w.site(o.ID, "docs").ID, "Link")
		s := w.service()
		w.box.fail(mail.ErrInvalidMessage)
		s.Detect(t.Context())
		s.Deliver(t.Context())
		rows, err := w.db.Notifications().Due(t.Context(), store.NotificationEmail, w.clock.Add(2*hour), 10)
		w.must(err)
		if len(rows) != 0 {
			t.Fatalf("Due = %+v", rows)
		}
	})
	t.Run("a failure stops the pass for mail's sake", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		o := w.owner("one")
		st := w.site(o.ID, "docs")
		w.usedUpLink(o.ID, st.ID, "First")
		w.usedUpLink(o.ID, st.ID, "Second")
		counted := &countingMailer{err: errors.New("smtp down")}
		s := notify.New(notify.Config{Store: w.db.Notifications(), Mailer: counted, DashboardURL: dashboard, Now: func() time.Time { return w.clock }})
		s.Detect(t.Context())
		s.Deliver(t.Context())
		if counted.n != 1 {
			t.Fatalf("%d attempts in one pass", counted.n)
		}
	})
	t.Run("cut short by shutdown, it is retried", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		o := w.owner("one")
		w.usedUpLink(o.ID, w.site(o.ID, "docs").ID, "Link")
		ctx, cancel := context.WithCancel(t.Context())
		s := notify.New(notify.Config{
			Store: w.db.Notifications(), DashboardURL: dashboard, Now: func() time.Time { return w.clock },
			Mailer: mailFunc(func(context.Context, mail.Message) error { cancel(); return context.Canceled }),
		})
		s.Detect(ctx)
		s.Deliver(ctx)
		w.clock = w.clock.Add(hour)
		w.pass()
		if n := len(w.box.subjects()); n != 1 {
			t.Fatalf("sent %d after the restart", n)
		}
	})
}

type mailFunc func(context.Context, mail.Message) error

func (f mailFunc) Send(ctx context.Context, m mail.Message) error { return f(ctx, m) }

type countingMailer struct {
	n   int
	err error
}

func (c *countingMailer) Send(context.Context, mail.Message) error { c.n++; return c.err }

// TestCrashMidSend checks a message whose sending was never recorded is not
// sent twice: it may have gone out.
func TestCrashMidSend(t *testing.T) {
	w := newWorld(t)
	o := w.owner("one")
	w.usedUpLink(o.ID, w.site(o.ID, "docs").ID, "Link")
	s := w.service()
	s.Detect(t.Context())
	rows, err := w.db.Notifications().Due(t.Context(), store.NotificationEmail, w.clock, 10)
	w.must(err)
	w.must(w.db.Notifications().Claim(t.Context(), rows[0].ID))
	w.pass()
	w.clock = w.clock.Add(2 * hour)
	w.pass()
	if got := w.box.subjects(); len(got) != 0 {
		t.Fatalf("sent %q", got)
	}
}

// TestNoSecretsOrOtherOwners sends every event and checks nothing carries
// a token, a share-link URL or another owner's data.
func TestNoSecretsOrOtherOwners(t *testing.T) {
	w := newWorld(t)
	o, other := w.owner("one"), w.owner("two")
	s, shop := w.site(o.ID, "docs"), w.site(other.ID, "shop")
	w.link(o.ID, s.ID, func(l *store.ShareLink) {
		l.TokenHash, l.ExpiresAt, l.Label = "hash-of-"+secret, w.clock.Add(hour), "Launch review"
	})
	tok := w.token(o.ID, "ci-deploy", w.clock.Add(day))
	w.usedUpLink(o.ID, s.ID, "Used")
	w.domain(o.ID, s.ID, "docs.example.test", func(d *store.Domain) { d.VerifiedAt = w.clock })
	w.domain(o.ID, s.ID, "www.example.test", func(d *store.Domain) { d.FailingSince = w.clock.Add(-day) })
	w.preview(o.ID, s.ID, "pr-1", 7, w.clock.Add(-6*day))
	w.usedUpLink(other.ID, shop.ID, "Private shop link")
	w.pass()
	if len(w.box.sent) != 7 {
		t.Fatalf("sent %q", w.box.subjects())
	}
	for _, m := range w.box.sent {
		all := m.Subject + "\n" + m.Text + "\n" + m.HTML
		for _, bad := range []string{secret, "hash-", tok.TokenHash, "wl_", "?t=", "verify-"} {
			if strings.Contains(all, bad) {
				t.Errorf("%q in %s", bad, all)
			}
		}
		if m.To.Address == o.Email {
			for _, bad := range []string{"shop", "Private shop link", other.Email} {
				if strings.Contains(all, bad) {
					t.Errorf("another owner's %q in %s", bad, all)
				}
			}
		}
	}
}

func TestSettings(t *testing.T) {
	w := newWorld(t)
	a, b := w.owner("one"), w.owner("two")
	s := w.service()
	ctx := t.Context()
	on, err := s.Settings(ctx, a.ID)
	w.must(err)
	if len(on) != len(notify.Switchable) {
		t.Fatalf("Settings = %v", on)
	}
	for e, v := range on {
		if !v {
			t.Fatalf("%s off by default", e)
		}
	}
	w.must(s.SetSettings(ctx, a.ID, map[string]bool{notify.LinksExpiring: false}))
	w.must(s.SetSettings(ctx, a.ID, map[string]bool{notify.LinkUsedUp: false}))
	w.must(s.SetSettings(ctx, a.ID, map[string]bool{notify.LinkUsedUp: true}))
	on, err = s.Settings(ctx, a.ID)
	w.must(err)
	if on[notify.LinksExpiring] || !on[notify.LinkUsedUp] {
		t.Fatalf("Settings = %v", on)
	}
	on, err = s.Settings(ctx, b.ID)
	w.must(err)
	if !on[notify.LinksExpiring] {
		t.Fatalf("another owner's setting changed: %v", on)
	}
	if err := s.SetSettings(ctx, a.ID, map[string]bool{"file_withheld": false}); !errors.Is(err, notify.ErrUnknownEvent) {
		t.Fatalf("turning off a mandatory event: %v", err)
	}
}

// TestRun checks the worker delivers a withheld notice without waiting for
// its minute, and stops with its context.
func TestRun(t *testing.T) {
	w := newWorld(t)
	o := w.owner("one")
	ctx, cancel := context.WithCancel(t.Context())
	s := notify.New(notify.Config{Store: w.db.Notifications(), Mailer: w.box, DashboardURL: dashboard, Cap: 20})
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	w.must(s.Withheld(ctx, "file_withheld:a", o.ID, mail.Withheld{Site: "docs", Host: "app.example.test", Paths: []string{"a.png"}}))
	deadline := time.After(10 * time.Second)
	for len(w.box.subjects()) == 0 {
		select {
		case <-deadline:
			t.Fatal("not delivered")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	<-done
}
