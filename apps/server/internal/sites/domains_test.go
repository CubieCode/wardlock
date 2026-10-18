// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package sites_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"wardlock.nz/server/internal/sites"
	"wardlock.nz/server/internal/sites/sitestest"
	"wardlock.nz/server/internal/store"
	"wardlock.nz/server/internal/store/storetest"
)

const base = "sites.test"

var t0 = time.UnixMilli(1_800_000_000_000).UTC()

// clock is a settable test clock.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

func (c *clock) advance(d time.Duration) { c.set(c.Now().Add(d)) }

// notifications records Notify and RemoveCertificate calls.
type notifications struct {
	mu      sync.Mutex
	sites   []string
	removed []string
}

func (n *notifications) Notify(_ context.Context, siteID string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.sites = append(n.sites, siteID)
}

func (n *notifications) RemoveCertificate(_ context.Context, hostname string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.removed = append(n.removed, hostname)
	return nil
}

func (n *notifications) take() (sites, removed []string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	sites, removed = n.sites, n.removed
	n.sites, n.removed = nil, nil
	return sites, removed
}

type env struct {
	db    *store.DB
	svc   *sites.Service
	dns   *sitestest.Resolver
	clock *clock
	note  *notifications
	logs  *bytes.Buffer
	owner store.Owner
	site  store.Site
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{
		db:    storetest.New(t),
		dns:   sitestest.NewResolver("docs."+base, "other."+base),
		clock: &clock{t: t0},
		note:  &notifications{},
		logs:  &bytes.Buffer{},
	}
	e.svc = sites.NewService(sites.Config{
		Sites:         e.db.Sites(),
		Domains:       e.db.Domains(),
		Notifier:      e.note,
		Resolver:      e.dns,
		Certs:         e.note,
		BaseDomain:    base,
		DashboardHost: "wardlock.example.com",
		Now:           e.clock.Now,
		CheckTimeout:  50 * time.Millisecond,
		Log:           slog.New(slog.NewTextHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	e.owner = e.newOwner(t)
	var err error
	if e.site, err = e.svc.Create(t.Context(), e.owner.ID, "docs"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Create(t.Context(), e.newOwner(t).ID, "other"); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) newOwner(t *testing.T) store.Owner {
	t.Helper()
	o := store.Owner{ID: store.NewID(), Email: store.NewID() + "@example.test", CreatedAt: t0}
	if err := e.db.Owners().Create(t.Context(), o); err != nil {
		t.Fatal(err)
	}
	return o
}

// noToken fails t if any verification token issued so far reached the log.
func (e *env) noToken(t *testing.T, tokens ...string) {
	t.Helper()
	for _, tok := range tokens {
		if tok != "" && strings.Contains(e.logs.String(), tok) {
			t.Fatalf("a verification token was logged:\n%s", e.logs)
		}
	}
}

var errServFail = &net.DNSError{Err: "server misbehaving", Name: "x", IsTemporary: true}

// TestVerification adds a hostname, publishes DNS, checks it on request and
// compares the outcome.
func TestVerification(t *testing.T) {
	tests := []struct {
		name   string
		host   string
		dns    func(r *sitestest.Resolver, host, token string)
		status store.DomainStatus
		reason store.DomainFailure
		detail string
	}{
		{"CNAME direct", "www.example.com", func(r *sitestest.Resolver, h, _ string) {
			r.CNAME(h, "docs."+base)
		}, store.DomainActive, "", ""},
		{"CNAME with a trailing dot and capitals", "www.example.com", func(r *sitestest.Resolver, h, _ string) {
			r.CNAME(h, "Docs."+strings.ToUpper(base)+".")
		}, store.DomainActive, "", ""},
		{"CNAME chained", "www.example.com", func(r *sitestest.Resolver, h, _ string) {
			r.CNAME(h, "edge.cdn.example.net")
			r.CNAME("edge.cdn.example.net", "alias.example.org")
			r.CNAME("alias.example.org", "docs."+base)
		}, store.DomainActive, "", ""},
		{"CNAME to another site", "www.example.com", func(r *sitestest.Resolver, h, _ string) {
			r.CNAME(h, "other."+base)
		}, store.DomainPending, store.DomainCNAMEElsewhere, "points to other." + base},
		{"CNAME elsewhere", "www.example.com", func(r *sitestest.Resolver, h, _ string) {
			r.CNAME(h, "pages.example.net")
			r.Addr("pages.example.net")
		}, store.DomainPending, store.DomainCNAMEElsewhere, "points to pages.example.net"},
		{"CNAME chain through another site", "www.example.com", func(r *sitestest.Resolver, h, _ string) {
			r.CNAME(h, "other."+base)
			r.CNAME("other."+base, "docs."+base)
		}, store.DomainActive, "", ""},
		{"TXT fallback at an apex", "example.com", func(r *sitestest.Resolver, h, tok string) {
			r.Addr(h)
			r.TXT("_wardlock."+h, sites.TXTPrefix+tok)
		}, store.DomainActive, "", ""},
		{"TXT fallback behind a CDN proxy", "www.example.com", func(r *sitestest.Resolver, h, tok string) {
			r.Addr(h) // the proxy hides the CNAME behind its own addresses
			r.TXT("_wardlock."+h, "v=spf1 -all", " "+sites.TXTPrefix+tok+" ")
		}, store.DomainActive, "", ""},
		{"TXT fallback beside a CNAME elsewhere", "www.example.com", func(r *sitestest.Resolver, h, tok string) {
			r.CNAME(h, "pages.example.net")
			r.Addr("pages.example.net")
			r.TXT("_wardlock."+h, sites.TXTPrefix+tok)
		}, store.DomainActive, "", ""},
		{"TXT with a wrong token", "example.com", func(r *sitestest.Resolver, h, _ string) {
			r.Addr(h)
			r.TXT("_wardlock."+h, sites.TXTPrefix+"WRONGTOKENWRONGTOKENWRONGTO")
		}, store.DomainPending, store.DomainTXTMismatch, "holds a different token"},
		{"TXT at the hostname itself", "example.com", func(r *sitestest.Resolver, h, tok string) {
			r.Addr(h)
			r.TXT(h, sites.TXTPrefix+tok)
		}, store.DomainPending, store.DomainNoRecord, "No CNAME or TXT record found"},
		{"no records", "www.example.com", func(*sitestest.Resolver, string, string) {},
			store.DomainPending, store.DomainNoRecord, "No CNAME or TXT record found for www.example.com."},
		{"addresses but no CNAME or TXT", "example.com", func(r *sitestest.Resolver, h, _ string) {
			r.Addr(h)
		}, store.DomainPending, store.DomainNoRecord, "No CNAME or TXT record found"},
		{"DNS error", "www.example.com", func(r *sitestest.Resolver, h, _ string) {
			r.Fail(h, errServFail)
			r.Fail("_wardlock."+h, errServFail)
		}, store.DomainPending, store.DomainDNSError, "failed or timed out"},
		{"DNS error on the TXT lookup only", "example.com", func(r *sitestest.Resolver, h, _ string) {
			r.Addr(h)
			r.Fail("_wardlock."+h, errServFail)
		}, store.DomainPending, store.DomainDNSError, "failed or timed out"},
		{"timeout", "www.example.com", func(r *sitestest.Resolver, h, _ string) {
			r.Fail(h, sitestest.ErrHang)
			r.Fail("_wardlock."+h, sitestest.ErrHang)
		}, store.DomainPending, store.DomainDNSError, "failed or timed out"},
		{"timeout on the CNAME, TXT answers", "www.example.com", func(r *sitestest.Resolver, h, tok string) {
			r.Fail(h, sitestest.ErrHang)
			r.TXT("_wardlock."+h, sites.TXTPrefix+tok)
		}, store.DomainActive, "", ""},
		{"timeout on the TXT lookup, no CNAME", "example.com", func(r *sitestest.Resolver, h, _ string) {
			r.Addr(h)
			r.Fail("_wardlock."+h, sitestest.ErrHang)
		}, store.DomainPending, store.DomainDNSError, "failed or timed out"},
		{"the sites wildcard is itself a CNAME", "www.example.com", func(r *sitestest.Resolver, h, _ string) {
			r.CNAME("docs."+base, "lb.example.net")
			r.Addr("lb.example.net")
			r.CNAME(h, "docs."+base)
		}, store.DomainPending, store.DomainCNAMEElsewhere, "cannot confirm"},
		{"the sites wildcard is a CNAME, with the TXT record", "www.example.com", func(r *sitestest.Resolver, h, tok string) {
			r.CNAME("docs."+base, "lb.example.net")
			r.Addr("lb.example.net")
			r.CNAME(h, "docs."+base)
			r.TXT("_wardlock."+h, sites.TXTPrefix+tok)
		}, store.DomainActive, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			d, err := e.svc.AddDomain(t.Context(), e.owner.ID, "docs", tt.host)
			if err != nil {
				t.Fatal(err)
			}
			if d.Status != store.DomainPending || d.LastCheckedAt != t0 || d.LastFailureReason != store.DomainNoRecord {
				t.Fatalf("added %+v; want pending, checked straight away, no record", d)
			}
			tt.dns(e.dns, tt.host, d.VerificationToken)
			e.clock.advance(time.Minute)
			got, err := e.svc.VerifyDomain(t.Context(), e.owner.ID, "docs", tt.host)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != tt.status || got.LastFailureReason != tt.reason || !strings.Contains(got.FailureDetail, tt.detail) {
				t.Fatalf("got %s %q %q; want %s %q containing %q", got.Status, got.LastFailureReason, got.FailureDetail, tt.status, tt.reason, tt.detail)
			}
			if got.LastCheckedAt != e.clock.Now() {
				t.Fatalf("LastCheckedAt = %v, want now", got.LastCheckedAt)
			}
			if tt.status == store.DomainActive {
				if got.VerifiedAt != e.clock.Now() || !got.FailingSince.IsZero() || got.FailureDetail != "" {
					t.Fatalf("verified %+v; want VerifiedAt now and no failure", got)
				}
				if _, err := e.db.Domains().GetActiveByHostname(t.Context(), tt.host); err != nil {
					t.Fatalf("an active domain is not routed: %v", err)
				}
			} else if got.FailingSince != t0 {
				t.Fatalf("FailingSince = %v, want the first failure, %v", got.FailingSince, t0)
			}
			stored, err := e.svc.GetDomain(t.Context(), e.owner.ID, "docs", tt.host)
			if err != nil || stored != got {
				t.Fatalf("stored %+v, %v; want %+v", stored, err, got)
			}
			e.noToken(t, d.VerificationToken)
		})
	}
}

func TestAddDomain(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	e.dns.CNAME("www.example.com", "docs."+base)

	d, err := e.svc.AddDomain(ctx, e.owner.ID, "docs", "WWW.Example.com.")
	if err != nil {
		t.Fatal(err)
	}
	if d.Hostname != "www.example.com" || d.SiteID != e.site.ID || d.Status != store.DomainActive || d.CreatedAt != t0 || d.VerifiedAt != t0 {
		t.Fatalf("added %+v; want www.example.com active at once", d)
	}
	if len(d.VerificationToken) < 16 {
		t.Fatalf("token %q is too short", d.VerificationToken)
	}
	if notified, _ := e.note.take(); !slices.Contains(notified, e.site.ID) {
		t.Fatal("activation was not announced to the edge")
	}
	if got := e.svc.RoutingTarget("docs"); got != "docs."+base {
		t.Fatalf("RoutingTarget = %q", got)
	}

	t.Run("refused hostnames", func(t *testing.T) {
		for _, h := range []string{"192.0.2.1", "*.example.com", "docs." + base, "auth." + base, base, "wardlock.example.com", "localhost"} {
			_, err := e.svc.AddDomain(ctx, e.owner.ID, "docs", h)
			if !errors.Is(err, sites.ErrInvalidHostname) {
				t.Errorf("AddDomain(%q) = %v, want ErrInvalidHostname", h, err)
			}
		}
	})
	t.Run("an unknown site", func(t *testing.T) {
		_, err := e.svc.AddDomain(ctx, e.owner.ID, "missing", "a.example.com")
		if !errors.Is(err, sites.ErrNotFound) {
			t.Fatalf("got %v, want ErrNotFound", err)
		}
	})
	t.Run("domain_taken reveals no site or owner", func(t *testing.T) {
		// The same owner's other site, and another owner's site.
		if _, err := e.svc.Create(ctx, e.owner.ID, "blog"); err != nil {
			t.Fatal(err)
		}
		other := e.newOwner(t)
		if _, err := e.svc.Create(ctx, other.ID, "mine"); err != nil {
			t.Fatal(err)
		}
		for _, attempt := range []struct{ owner, site, host string }{
			{e.owner.ID, "docs", "www.example.com"},
			{e.owner.ID, "blog", "www.example.com"},
			{other.ID, "mine", "Www.Example.Com."},
		} {
			_, err := e.svc.AddDomain(ctx, attempt.owner, attempt.site, attempt.host)
			if !errors.Is(err, sites.ErrDomainTaken) {
				t.Fatalf("%+v: got %v, want ErrDomainTaken", attempt, err)
			}
			for _, leak := range []string{"docs", e.site.ID, e.owner.ID, e.owner.Email} {
				if strings.Contains(err.Error(), leak) {
					t.Fatalf("domain_taken error %q names %q", err, leak)
				}
			}
		}
	})
	t.Run("a re-claim after removal needs a new token", func(t *testing.T) {
		h := "claim.example.com"
		first, err := e.svc.AddDomain(ctx, e.owner.ID, "docs", h)
		if err != nil {
			t.Fatal(err)
		}
		e.dns.Addr(h)
		e.dns.TXT("_wardlock."+h, sites.TXTPrefix+first.VerificationToken)
		e.clock.advance(time.Minute)
		if d, err := e.svc.VerifyDomain(ctx, e.owner.ID, "docs", h); err != nil || d.Status != store.DomainActive {
			t.Fatalf("first claim: %+v, %v", d, err)
		}
		if err := e.svc.RemoveDomain(ctx, e.owner.ID, "docs", h); err != nil {
			t.Fatal(err)
		}
		// Someone else claims it; the old TXT record proves nothing.
		other := e.newOwner(t)
		if _, err := e.svc.Create(ctx, other.ID, "claimer"); err != nil {
			t.Fatal(err)
		}
		e.dns.CNAME("claimer."+base, "docs."+base) // irrelevant to the TXT check
		second, err := e.svc.AddDomain(ctx, other.ID, "claimer", h)
		if err != nil {
			t.Fatal(err)
		}
		if second.VerificationToken == first.VerificationToken || second.ID == first.ID {
			t.Fatal("a new claim reused the old token")
		}
		if second.Status != store.DomainPending || second.LastFailureReason != store.DomainTXTMismatch {
			t.Fatalf("the stale TXT record verified a new claim: %+v", second)
		}
		e.noToken(t, first.VerificationToken, second.VerificationToken)
	})
}

func TestVerifyCooldown(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	if _, err := e.svc.AddDomain(ctx, e.owner.ID, "docs", "www.example.com"); err != nil {
		t.Fatal(err)
	}
	e.clock.advance(4 * time.Second)
	_, err := e.svc.VerifyDomain(ctx, e.owner.ID, "docs", "www.example.com")
	var soon *sites.CheckTooSoonError
	if !errors.As(err, &soon) || !errors.Is(err, sites.ErrCheckTooSoon) || soon.RetryAfter != 6*time.Second {
		t.Fatalf("got %v, want CheckTooSoonError after 6s", err)
	}
	e.clock.advance(6 * time.Second)
	if _, err := e.svc.VerifyDomain(ctx, e.owner.ID, "docs", "www.example.com"); err != nil {
		t.Fatalf("after the cooldown: %v", err)
	}
}

// TestOwnerIsolation: owner B cannot list, get, verify or remove owner A's
// domains, and every attempt looks exactly like a missing domain.
func TestOwnerIsolation(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	e.dns.CNAME("www.example.com", "docs."+base)
	if _, err := e.svc.AddDomain(ctx, e.owner.ID, "docs", "www.example.com"); err != nil {
		t.Fatal(err)
	}
	b := e.newOwner(t)
	if _, err := e.svc.Create(ctx, b.ID, "bsite"); err != nil {
		t.Fatal(err)
	}
	e.clock.advance(time.Hour)
	missing := func(what string, err error) {
		t.Helper()
		if !errors.Is(err, sites.ErrNotFound) {
			t.Fatalf("%s: got %v, want ErrNotFound", what, err)
		}
	}
	_, err := e.svc.ListDomains(ctx, b.ID, "docs", "", 10)
	missing("list A's site", err)
	for _, site := range []string{"docs", "bsite"} {
		_, err = e.svc.GetDomain(ctx, b.ID, site, "www.example.com")
		missing("get via "+site, err)
		_, err = e.svc.VerifyDomain(ctx, b.ID, site, "www.example.com")
		missing("verify via "+site, err)
		missing("remove via "+site, e.svc.RemoveDomain(ctx, b.ID, site, "www.example.com"))
	}
	list, err := e.svc.ListDomains(ctx, b.ID, "bsite", "", 10)
	if err != nil || len(list) != 0 {
		t.Fatalf("B's own list = %v, %v", list, err)
	}
	// The same calls for a hostname nobody holds answer the same way.
	_, err = e.svc.GetDomain(ctx, b.ID, "bsite", "nobody.example.com")
	missing("get unknown", err)
	_, err = e.svc.GetDomain(ctx, e.owner.ID, "docs", "not a hostname")
	missing("get malformed", err)
	// A's domain is untouched.
	d, err := e.svc.GetDomain(ctx, e.owner.ID, "docs", "WWW.example.com.")
	if err != nil || d.Status != store.DomainActive {
		t.Fatalf("A's domain: %+v, %v", d, err)
	}
	if _, removed := e.note.take(); len(removed) != 0 {
		t.Fatalf("certificates removed: %v", removed)
	}
}

func TestListAndRemove(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	for _, h := range []string{"c.example.com", "a.example.com", "b.example.com"} {
		if _, err := e.svc.AddDomain(ctx, e.owner.ID, "docs", h); err != nil {
			t.Fatal(err)
		}
	}
	list, err := e.svc.ListDomains(ctx, e.owner.ID, "docs", "a.example.com", 1)
	if err != nil || len(list) != 1 || list[0].Hostname != "b.example.com" {
		t.Fatalf("page after a = %v, %v", list, err)
	}
	e.note.take()
	if err := e.svc.RemoveDomain(ctx, e.owner.ID, "docs", "B.Example.com."); err != nil {
		t.Fatal(err)
	}
	notified, removed := e.note.take()
	if !slices.Equal(notified, []string{e.site.ID}) || !slices.Equal(removed, []string{"b.example.com"}) {
		t.Fatalf("notified %v, removed certificates %v", notified, removed)
	}
	if _, err := e.svc.GetDomain(ctx, e.owner.ID, "docs", "b.example.com"); !errors.Is(err, sites.ErrNotFound) {
		t.Fatalf("after removal: %v", err)
	}
	if err := e.svc.RemoveDomain(ctx, e.owner.ID, "docs", "b.example.com"); !errors.Is(err, sites.ErrNotFound) {
		t.Fatalf("second removal: %v", err)
	}

	// Teardown takes the remaining domains' certificates with it.
	if err := e.svc.Delete(ctx, e.owner.ID, "docs"); err != nil {
		t.Fatal(err)
	}
	if _, removed := e.note.take(); !slices.Equal(removed, []string{"a.example.com", "c.example.com"}) {
		t.Fatalf("teardown removed certificates %v", removed)
	}
}

func TestNextCheck(t *testing.T) {
	pending := store.Domain{Status: store.DomainPending, CreatedAt: t0}
	if at, ok := sites.NextCheck(pending); !ok || at != t0 {
		t.Fatalf("a new domain: %v %v, want now", at, ok)
	}
	// Each check waits as long as the domain is old: 1, 2, 4 … 32
	// minutes, then hourly, until the 48-hour window closes.
	var waits []time.Duration
	d := pending
	d.LastCheckedAt = t0
	for {
		at, ok := sites.NextCheck(d)
		if !ok {
			break
		}
		waits = append(waits, at.Sub(d.LastCheckedAt))
		d.LastCheckedAt = at
	}
	want := []time.Duration{1, 1, 2, 4, 8, 16, 32}
	for i := range want {
		want[i] *= time.Minute
	}
	if !slices.Equal(waits[:len(want)], want) {
		t.Fatalf("first waits %v, want %v", waits[:len(want)], want)
	}
	for _, w := range waits[len(want):] {
		if w != time.Hour {
			t.Fatalf("waits after the first hour %v, want hourly", waits[len(want):])
		}
	}
	if last := d.LastCheckedAt.Sub(t0); last > sites.CheckWindow || last < sites.CheckWindow-time.Hour {
		t.Fatalf("last automatic check %v after adding, want within the last hour of 48", last)
	}

	for _, status := range []store.DomainStatus{store.DomainActive, store.DomainLapsed} {
		d := store.Domain{Status: status, CreatedAt: t0, LastCheckedAt: t0.Add(5 * 24 * time.Hour)}
		if at, ok := sites.NextCheck(d); !ok || at != d.LastCheckedAt.Add(24*time.Hour) {
			t.Fatalf("%s: %v %v, want 24 hours after the last check", status, at, ok)
		}
	}
}

// TestWorkerBackoff drives the worker with the clock: a pending domain is
// checked exactly when NextCheck says, and not at all after 48 hours.
func TestWorkerBackoff(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	d, err := e.svc.AddDomain(ctx, e.owner.ID, "docs", "www.example.com")
	if err != nil {
		t.Fatal(err)
	}
	run := func() int {
		t.Helper()
		n, err := e.svc.CheckDue(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := run(); n != 0 {
		t.Fatalf("checked %d straight after adding; the add already did", n)
	}
	for _, step := range []struct {
		at   time.Duration
		want int
	}{
		{59 * time.Second, 0},
		{time.Minute, 1},
		{2*time.Minute - time.Millisecond, 0},
		{2 * time.Minute, 1},
		{4 * time.Minute, 1},
		{7 * time.Minute, 0},
		{8 * time.Minute, 1},
	} {
		e.clock.set(t0.Add(step.at))
		if n := run(); n != step.want {
			t.Fatalf("at +%v checked %d, want %d", step.at, n, step.want)
		}
	}
	got, _ := e.svc.GetDomain(ctx, e.owner.ID, "docs", d.Hostname)
	if got.LastCheckedAt != t0.Add(8*time.Minute) || got.FailingSince != t0 {
		t.Fatalf("after the backoff steps %+v", got)
	}
	// Past the window it is checked only on request.
	e.clock.set(t0.Add(49 * time.Hour))
	if n := run(); n != 0 {
		t.Fatalf("checked %d after 48 hours", n)
	}
	if _, ok := sites.NextCheck(got); !ok {
		t.Fatal("NextCheck reported nothing due inside the window")
	}
	e.dns.CNAME(d.Hostname, "docs."+base)
	if v, err := e.svc.VerifyDomain(ctx, e.owner.ID, "docs", d.Hostname); err != nil || v.Status != store.DomainActive {
		t.Fatalf("on request after the window: %+v, %v", v, err)
	}
}

// TestLapseAndRecovery: an active domain whose record disappears is
// re-checked daily, lapses after 72 hours of failures (unrouted, its
// certificate removed, the edge told), and recovers by itself.
func TestLapseAndRecovery(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	h := "www.example.com"
	e.dns.CNAME(h, "docs."+base)
	if _, err := e.svc.AddDomain(ctx, e.owner.ID, "docs", h); err != nil {
		t.Fatal(err)
	}
	e.note.take()
	step := func(at time.Duration, wantChecks int, want store.DomainStatus) store.Domain {
		t.Helper()
		e.clock.set(t0.Add(at))
		n, err := e.svc.CheckDue(ctx)
		if err != nil || n != wantChecks {
			t.Fatalf("at +%v: %d checks, %v; want %d", at, n, err, wantChecks)
		}
		d, err := e.svc.GetDomain(ctx, e.owner.ID, "docs", h)
		if err != nil || d.Status != want {
			t.Fatalf("at +%v: %+v, %v; want %s", at, d, err, want)
		}
		return d
	}
	day := 24 * time.Hour
	step(day-time.Millisecond, 0, store.DomainActive)
	step(day, 1, store.DomainActive)
	e.dns.Remove(h)
	d := step(2*day, 1, store.DomainActive)
	if d.FailingSince != t0.Add(2*day) || d.LastFailureReason != store.DomainNoRecord {
		t.Fatalf("first failure %+v", d)
	}
	step(3*day, 1, store.DomainActive)
	step(4*day, 1, store.DomainActive)
	if notified, removed := e.note.take(); len(notified)+len(removed) != 0 {
		t.Fatalf("before the lapse: notified %v, removed %v", notified, removed)
	}
	if _, err := e.db.Domains().GetActiveByHostname(ctx, h); err != nil {
		t.Fatal("still active domain is not routed")
	}
	step(5*day-time.Millisecond, 0, store.DomainActive)
	d = step(5*day, 1, store.DomainLapsed) // 72 hours after the first failure
	if d.FailingSince != t0.Add(2*day) {
		t.Fatalf("lapsed %+v", d)
	}
	notified, removed := e.note.take()
	if !slices.Equal(notified, []string{e.site.ID}) || !slices.Equal(removed, []string{h}) {
		t.Fatalf("on lapse: notified %v, removed certificates %v", notified, removed)
	}
	if _, err := e.db.Domains().GetActiveByHostname(ctx, h); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a lapsed domain is still routed: %v", err)
	}
	step(6*day, 1, store.DomainLapsed)

	// The owner fixes DNS (with the TXT record this time); the next daily
	// check restores it without them asking.
	e.dns.Addr(h)
	e.dns.TXT("_wardlock."+h, sites.TXTPrefix+d.VerificationToken)
	d = step(7*day, 1, store.DomainActive)
	if d.VerifiedAt != t0.Add(7*day) || !d.FailingSince.IsZero() || d.LastFailureReason != "" {
		t.Fatalf("recovered %+v", d)
	}
	if notified, _ := e.note.take(); !slices.Equal(notified, []string{e.site.ID}) {
		t.Fatalf("recovery notified %v", notified)
	}
	if _, err := e.db.Domains().GetActiveByHostname(ctx, h); err != nil {
		t.Fatalf("a recovered domain is not routed: %v", err)
	}
	if !strings.Contains(e.logs.String(), "custom domain status changed") {
		t.Fatalf("status changes were not logged:\n%s", e.logs)
	}
	e.noToken(t, d.VerificationToken)
}

// TestCheckDueMany: one pass reads candidates a page at a time and checks
// every due domain.
func TestCheckDueMany(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	const n = 230 // more than one page
	for i := range n {
		if _, err := e.svc.AddDomain(ctx, e.owner.ID, "docs", fmt.Sprintf("h%03d.example.com", i)); err != nil {
			t.Fatal(err)
		}
	}
	e.clock.advance(time.Minute)
	got, err := e.svc.CheckDue(ctx)
	if err != nil || got != n {
		t.Fatalf("CheckDue = %d, %v; want %d", got, err, n)
	}
	if got, err := e.svc.CheckDue(ctx); err != nil || got != 0 {
		t.Fatalf("a second pass at once = %d, %v; want none due", got, err)
	}
}
