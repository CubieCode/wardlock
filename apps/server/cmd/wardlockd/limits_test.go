// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"fmt"
	"math"
	"net/http"
	"testing"
	"time"

	"wardlock.nz/server/internal/access"
	"wardlock.nz/server/internal/store"
)

// TestSignInRateLimitsThroughTheEdge drives every visitor sign-in limit
// through the real edge, keyed as SECURITY-MODEL "Brute force and
// enumeration" says: each trips on the attempt after its allowance with a
// 429 that carries Retry-After and the protected headers, leaves other
// keys (another IP, address or site) alone, and resets when its window
// ends. Each case shrinks only the limit under test.
func TestSignInRateLimitsThroughTheEdge(t *testing.T) {
	const n = 3
	type attempt struct{ host, ip, addr string }
	tests := []struct {
		name  string
		limit func(*access.Limits) *access.Rate
		// form is what one attempt posts; nil sends a share-link guess.
		form func(addr string) map[string][]string
		// nth is the i-th attempt within the limit; next is the one that
		// trips it, other one that shares no key with them.
		nth         func(i int) attempt
		next, other attempt
	}{
		{
			name:  "password per IP",
			limit: func(l *access.Limits) *access.Rate { return &l.PasswordPerIP },
			form:  func(string) map[string][]string { return passwordForm("wrong-guess", "/") },
			nth:   func(int) attempt { return attempt{host, "192.0.2.1", ""} },
			next:  attempt{host, "192.0.2.1", ""}, other: attempt{host, "192.0.2.2", ""},
		},
		{
			name:  "password per site",
			limit: func(l *access.Limits) *access.Rate { return &l.PasswordPerSite },
			form:  func(string) map[string][]string { return passwordForm("wrong-guess", "/") },
			// One network each, so no network's share (A2.9) is reached.
			nth:  func(i int) attempt { return attempt{host, fmt.Sprintf("2001:db8:%x::1", i+1), ""} },
			next: attempt{host, "198.51.100.1", ""}, other: attempt{otherHost, "198.51.100.1", ""},
		},
		{
			name:  "email sends per IP",
			limit: func(l *access.Limits) *access.Rate { return &l.EmailSendPerIP },
			form:  func(a string) map[string][]string { return sendForm(a, "/") },
			nth:   func(i int) attempt { return attempt{host, "192.0.2.1", fmt.Sprintf("v%d@example.test", i)} },
			next:  attempt{host, "192.0.2.1", "v9@example.test"}, other: attempt{host, "192.0.2.2", "v10@example.test"},
		},
		{
			name:  "email sends per address",
			limit: func(l *access.Limits) *access.Rate { return &l.EmailSendPerAddress },
			form:  func(a string) map[string][]string { return sendForm(a, "/") },
			nth:   func(i int) attempt { return attempt{host, fmt.Sprintf("192.0.2.%d", i+1), visitorAddr} },
			next:  attempt{host, "198.51.100.1", visitorAddr}, other: attempt{host, "198.51.100.1", strangerAddr},
		},
		{
			name:  "email sends per site",
			limit: func(l *access.Limits) *access.Rate { return &l.EmailSendPerSite },
			form:  func(a string) map[string][]string { return sendForm(a, "/") },
			nth: func(i int) attempt {
				return attempt{host, fmt.Sprintf("2001:db8:%x::1", i+1), fmt.Sprintf("v%d@example.test", i)}
			},
			next: attempt{host, "198.51.100.1", "v9@example.test"}, other: attempt{otherHost, "198.51.100.1", "v9@example.test"},
		},
		{
			name:  "email code entries per IP",
			limit: func(l *access.Limits) *access.Rate { return &l.EmailVerifyPerIP },
			form:  func(a string) map[string][]string { return verifyForm(a, "000000", "/") },
			nth:   func(i int) attempt { return attempt{host, "192.0.2.1", fmt.Sprintf("v%d@example.test", i)} },
			next:  attempt{host, "192.0.2.1", "v9@example.test"}, other: attempt{host, "192.0.2.2", "v10@example.test"},
		},
		{
			name:  "email code entries per address",
			limit: func(l *access.Limits) *access.Rate { return &l.EmailVerifyPerAddress },
			form:  func(a string) map[string][]string { return verifyForm(a, "000000", "/") },
			nth:   func(i int) attempt { return attempt{host, fmt.Sprintf("192.0.2.%d", i+1), visitorAddr} },
			next:  attempt{host, "198.51.100.1", visitorAddr}, other: attempt{host, "198.51.100.1", strangerAddr},
		},
		{
			name:  "email code entries per site",
			limit: func(l *access.Limits) *access.Rate { return &l.EmailVerifyPerSite },
			form:  func(a string) map[string][]string { return verifyForm(a, "000000", "/") },
			nth: func(i int) attempt {
				return attempt{host, fmt.Sprintf("2001:db8:%x::1", i+1), fmt.Sprintf("v%d@example.test", i)}
			},
			next: attempt{host, "198.51.100.1", "v9@example.test"}, other: attempt{otherHost, "198.51.100.1", "v9@example.test"},
		},
		{
			// One IPv6 host usually holds a whole /64: the limit is the
			// /64's, and the next /64 is another client.
			name:  "share-link guesses per IPv6 /64",
			limit: func(l *access.Limits) *access.Rate { return &l.TokenFailuresPerIP },
			nth:   func(i int) attempt { return attempt{host, fmt.Sprintf("2001:db8:0:1::%x", i+1), ""} },
			next:  attempt{host, "2001:db8:0:1:ffff::1", ""}, other: attempt{host, "2001:db8:0:2::1", ""},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			window := 15 * time.Minute
			p := newPolicyEdge(t, func(c *access.Config) {
				c.Limits = access.DefaultLimits()
				for _, r := range []*access.Rate{
					&c.Limits.PasswordPerIP, &c.Limits.PasswordPerSite, &c.Limits.TokenFailuresPerIP, &c.Limits.TokenFailuresPerSite,
					&c.Limits.EmailSendPerIP, &c.Limits.EmailSendPerAddress, &c.Limits.EmailSendPerSite,
					&c.Limits.EmailVerifyPerIP, &c.Limits.EmailVerifyPerAddress, &c.Limits.EmailVerifyPerSite,
				} {
					r.N = math.MaxInt
				}
				r := tt.limit(&c.Limits)
				r.N, window = n, r.Per
			})
			allow := store.Allowlist{Emails: []string{visitorAddr}}
			for _, site := range []string{"docs", "other"} {
				p.publish(site)
				if _, err := p.acc.Set(t.Context(), p.owner.ID, site, access.Rule{
					PathPrefix: "/", Visibility: store.Protected, Links: true, Password: true, NewPassword: sitePassword,
					Email: true, Allowlist: &allow,
				}); err != nil {
					t.Fatal(err)
				}
			}
			try := func(a attempt) result {
				t.Helper()
				if tt.form == nil {
					return p.visit(visit{host: a.host, ip: a.ip, target: withT("/", "wl_aaaaaaaaaa_"+fmt.Sprintf("%043d", 0))})
				}
				return p.post(a.host, a.ip, tt.form(a.addr), nil)
			}
			for i := range n {
				if r := try(tt.nth(i)); r.code == http.StatusTooManyRequests {
					t.Fatalf("attempt %d of %d refused", i+1, n)
				}
			}
			r := try(tt.next)
			if r.code != http.StatusTooManyRequests || r.h.Get("Retry-After") == "" {
				t.Fatalf("attempt %d = %d %v, want 429 with Retry-After", n+1, r.code, r.h)
			}
			for k, want := range map[string]string{"Cache-Control": "no-store", "X-Robots-Tag": "noindex", "Referrer-Policy": "no-referrer"} {
				if got := r.h.Get(k); got != want {
					t.Errorf("429 %s = %q, want %q", k, got, want)
				}
			}
			if r := try(tt.other); r.code == http.StatusTooManyRequests {
				t.Fatalf("a request sharing no key was refused")
			}
			p.advance(window - time.Second)
			if r := try(tt.next); r.code != http.StatusTooManyRequests {
				t.Fatalf("before the window ends = %d, want 429", r.code)
			}
			p.advance(time.Second)
			if r := try(tt.next); r.code == http.StatusTooManyRequests {
				t.Fatalf("after the window = %d, want the limit reset", r.code)
			}
		})
	}
}

// TestNetworkShareThroughTheEdge: once a site is under pressure (half its
// password limit spent), one network gets only its share (a tenth) of the
// site's attempts, answered 429 with Retry-After beyond it, while clients
// on other networks still reach the password check (A2.9).
func TestNetworkShareThroughTheEdge(t *testing.T) {
	const siteN = 20 // pressure at 10, a share of 2
	p := newPolicyEdge(t, func(c *access.Config) {
		c.Limits = access.DefaultLimits()
		c.Limits.PasswordPerSite.N = siteN
	})
	p.publish("docs")
	if _, err := p.acc.Set(t.Context(), p.owner.ID, "docs", access.Rule{
		PathPrefix: "/", Visibility: store.Protected, Password: true, NewPassword: sitePassword,
	}); err != nil {
		t.Fatal(err)
	}
	wrong := passwordForm("wrong-guess", "/")
	for i := range siteN / 2 {
		if r := p.post(host, fmt.Sprintf("2001:db8:%x::1", i+1), wrong, nil); r.code == http.StatusTooManyRequests {
			t.Fatalf("attempt %d before the pressure mark refused", i+1)
		}
	}
	for i := range 2 {
		if r := p.post(host, fmt.Sprintf("2001:db8:aa:%x::1", i), wrong, nil); r.code == http.StatusTooManyRequests {
			t.Fatalf("attempt %d within the network's share refused", i+1)
		}
	}
	r := p.post(host, "2001:db8:aa:ff::1", wrong, nil)
	if r.code != http.StatusTooManyRequests || r.h.Get("Retry-After") == "" || r.h.Get("Cache-Control") != "no-store" {
		t.Fatalf("over the network's share = %d %v, want 429 with Retry-After", r.code, r.h)
	}
	if r := p.post(host, "198.51.100.1", passwordForm(sitePassword, "/"), nil); r.code == http.StatusTooManyRequests || r.h.Get("Set-Cookie") == "" {
		t.Fatalf("another network's right password = %d %v, want a session", r.code, r.h)
	}
	p.advance(15 * time.Minute)
	if r := p.post(host, "2001:db8:aa:ff::1", passwordForm(sitePassword, "/"), nil); r.code == http.StatusTooManyRequests {
		t.Fatalf("after the window = %d, want the share reset", r.code)
	}
}
