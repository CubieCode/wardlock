// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package access

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
	"wardlock.nz/server/internal/store/storetest"
)

func TestLimiter(t *testing.T) {
	now := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	l := newLimiter(func() time.Time { return now })
	r := Rate{N: 2, Per: time.Minute}
	for i := range 2 {
		if ok, _ := l.allow("a", r); !ok {
			t.Fatalf("event %d refused", i+1)
		}
	}
	if over, wait := l.exceeded("a", r); !over || wait != time.Minute {
		t.Fatalf("exceeded = %v %v after N events", over, wait)
	}
	if ok, _ := l.allow("a", r); ok {
		t.Fatal("event N+1 allowed")
	}
	now = now.Add(time.Minute)
	if over, _ := l.exceeded("a", r); over {
		t.Fatal("limit did not reset")
	}
}

// TestLimiterFull: a full bounded table refuses to count a new key but
// never reports an untracked key as over; expired windows are evicted.
func TestLimiterFull(t *testing.T) {
	now := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	l := newLimiter(func() time.Time { return now })
	l.maxKeys = 2
	r := Rate{N: 5, Per: time.Minute}
	l.allow("a", r)
	l.allow("b", r)
	if ok, wait := l.allow("c", r); ok || wait < time.Second {
		t.Fatalf("a new key was counted in a full table (%v %v)", ok, wait)
	}
	if over, _ := l.exceeded("c", r); over {
		t.Fatal("an untracked key was reported over: a full table must not lock clients out")
	}
	if ok, _ := l.allow("a", r); !ok {
		t.Fatal("a tracked key under its limit was refused")
	}
	now = now.Add(2 * time.Minute) // windows over, sweep due
	if ok, _ := l.allow("c", r); !ok {
		t.Fatal("c refused after expired windows were evicted")
	}
	if len(l.windows) != 1 {
		t.Fatalf("expired windows kept: %v", l.windows)
	}

	u := newServerKeyLimiter(func() time.Time { return now })
	for i := range defaultMaxKeys + 10 {
		if ok, _ := u.allow(fmt.Sprintf("site-%d", i), r); !ok {
			t.Fatalf("the unbounded limiter refused key %d", i)
		}
	}
}

// syncNotifier delivers at once, so policy changes drop the cache.
type syncNotifier struct{ subs []func(string) }

func (n *syncNotifier) Notify(_ context.Context, siteID string) {
	for _, fn := range n.subs {
		fn(siteID)
	}
}

func (n *syncNotifier) Subscribe(fn func(string)) func() {
	n.subs = append(n.subs, fn)
	return func() {}
}

// fullIPTable returns a service whose IP table is full of other clients'
// failures, and a protected site with a share link.
func fullIPTable(t *testing.T) (s *Service, siteID, token string) {
	t.Helper()
	db := storetest.New(t)
	s, err := New(Config{
		Sites: db.Sites(), Policies: db.Policies(), Sessions: db.Sessions(), Links: db.Links(), PasswordSessions: db.PasswordSessions(), Notifier: &syncNotifier{},
		Secret: []byte("limit-test-server-key-0123456789abcdef"), Now: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	o := store.Owner{ID: store.NewID(), Email: "owner@example.test", CreatedAt: time.Now()}
	site := store.Site{ID: store.NewID(), OwnerID: o.ID, Name: "fresh", CreatedAt: time.Now()}
	if err := db.Owners().Create(t.Context(), o); err != nil {
		t.Fatal(err)
	}
	if err := db.Sites().Create(t.Context(), site); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Set(t.Context(), o.ID, "fresh", Rule{PathPrefix: "/", Visibility: store.Protected, Links: true}); err != nil {
		t.Fatal(err)
	}
	_, token, err = s.CreateLink(t.Context(), o.ID, "fresh", NewLink{})
	if err != nil {
		t.Fatal(err)
	}
	s.limiter.maxKeys = 3
	for i := range 3 {
		s.limiter.allow(tokenIPKey(fmt.Sprintf("2001:db8:%x::1", i)), s.limits.TokenFailuresPerIP)
	}
	return s, site.ID, token
}

// TestFullIPTableKeepsValidTokensWorking: once the IP table is full, a
// valid token from a fresh IP still opens a fresh site, and a new failed
// guess is refused (limited) rather than silently uncounted.
func TestFullIPTableKeepsValidTokensWorking(t *testing.T) {
	s, siteID, token := fullIPTable(t)
	d, err := s.Decide(t.Context(), Request{SiteID: siteID, Path: "/", Token: token, ClientIP: "198.51.100.1"})
	if err != nil || d.Outcome != Allow || !d.TokenAccepted {
		t.Fatalf("valid token with a full IP table: %+v %v", d, err)
	}
	_, err = s.checkToken(t.Context(), Request{SiteID: siteID, Token: "wl_junk", ClientIP: "198.51.100.2"})
	if le, ok := errors.AsType[*limitedError](err); !ok || le.RetryAfter <= 0 {
		t.Fatalf("a guess the full table cannot count: %v", err)
	}
	if d, _ := s.Decide(t.Context(), Request{SiteID: siteID, Path: "/", Token: "wl_junk", ClientIP: "198.51.100.3"}); d.Outcome != RateLimited {
		t.Fatalf("an uncountable guess was not limited: %+v", d)
	}
}

// TestSiteLimitSurvivesAFullIPTable: guesses from addresses the full IP
// table cannot hold still count against the site, and close it.
func TestSiteLimitSurvivesAFullIPTable(t *testing.T) {
	s, siteID, token := fullIPTable(t)
	for i := range s.limits.TokenFailuresPerSite.N {
		ip := fmt.Sprintf("203.0.113.%d", i%250)
		if _, err := s.checkToken(t.Context(), Request{SiteID: siteID, Token: "wl_junk", ClientIP: ip}); err == nil {
			t.Fatal("junk accepted")
		}
	}
	d, err := s.Decide(t.Context(), Request{SiteID: siteID, Path: "/", Token: token, ClientIP: "198.51.100.1"})
	if err != nil || d.Outcome != RateLimited || d.RetryAfter <= 0 {
		t.Fatalf("the site limit with a full IP table: %+v %v", d, err)
	}
}
