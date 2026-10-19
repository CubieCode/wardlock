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
			nth:   func(i int) attempt { return attempt{host, fmt.Sprintf("192.0.2.%d", i+1), ""} },
			next:  attempt{host, "198.51.100.1", ""}, other: attempt{otherHost, "198.51.100.1", ""},
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
				return attempt{host, fmt.Sprintf("192.0.2.%d", i+1), fmt.Sprintf("v%d@example.test", i)}
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
				return attempt{host, fmt.Sprintf("192.0.2.%d", i+1), fmt.Sprintf("v%d@example.test", i)}
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
