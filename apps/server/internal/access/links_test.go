// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package access_test

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/access"
	"wardlock.nz/server/internal/store"
)

var tokenShape = regexp.MustCompile(`^wl_[a-z2-7]{10}_[A-Za-z0-9_-]{43}$`)

func TestCreateLink(t *testing.T) {
	h := newHarness(t)
	o := h.owner()
	h.site(o, "docs")
	for _, tc := range []struct {
		name   string
		n      access.NewLink
		target string // "" for success
	}{
		{name: "defaults", n: access.NewLink{}},
		{name: "everything", n: access.NewLink{Label: "Linear ENG-123", PathPrefix: "/docs/", ExpiresAt: start.Add(time.Hour), MaxUses: 1}},
		{name: "200-character label", n: access.NewLink{Label: strings.Repeat("é", 200)}},
		{name: "201-character label", n: access.NewLink{Label: strings.Repeat("l", 201)}, target: "label"},
		{name: "control character", n: access.NewLink{Label: "sent to\nAlice"}, target: "label"},
		{name: "invisible character", n: access.NewLink{Label: "sent\u200bto"}, target: "label"},
		{name: "past expiry", n: access.NewLink{ExpiresAt: start}, target: "expiresAt"},
		{name: "negative uses", n: access.NewLink{MaxUses: -1}, target: "maxUses"},
		{name: "too many uses", n: access.NewLink{MaxUses: access.MaxLinkUses + 1}, target: "maxUses"},
		{name: "relative path", n: access.NewLink{PathPrefix: "docs/"}, target: "path"},
		{name: "reserved path", n: access.NewLink{PathPrefix: "/.wardlock/"}, target: "path"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, token, err := h.svc.CreateLink(t.Context(), o, "docs", tc.n)
			if tc.target != "" {
				var te *access.TargetError
				if !errors.As(err, &te) || te.Target != tc.target {
					t.Fatalf("error %v, want target %s", err, tc.target)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !tokenShape.MatchString(token) || !strings.HasPrefix(token, "wl_"+l.PublicID+"_") {
				t.Fatalf("token %q for public ID %s", token, l.PublicID)
			}
			if strings.Contains(l.TokenHash, token) || len(l.TokenHash) != 64 {
				t.Fatalf("stored hash %q", l.TokenHash)
			}
			if l.PathPrefix == "" || l.Label != tc.n.Label || l.MaxUses != tc.n.MaxUses || !l.ExpiresAt.Equal(tc.n.ExpiresAt) {
				t.Fatalf("stored %+v for %+v", l, tc.n)
			}
		})
	}
	if strings.Contains(h.log.String(), "wl_") {
		t.Fatalf("a token reached the log:\n%s", h.log.String())
	}
}

// TestLinkOwnerIsolation: another owner's site and link answer exactly like
// missing ones.
func TestLinkOwnerIsolation(t *testing.T) {
	ctx := t.Context()
	h := newHarness(t)
	a, b := h.owner(), h.owner()
	h.site(a, "docs")
	h.site(b, "mine")
	l, _, err := h.svc.CreateLink(ctx, a, "docs", access.NewLink{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name            string
		owner, site, id string
	}{
		{"another owner's site", b, "docs", l.PublicID},
		{"missing site", a, "nope", l.PublicID},
		{"own site, another site's link", b, "mine", l.PublicID},
		{"missing link", a, "docs", "aaaaaaaaaa"},
		{"malformed link ID", a, "docs", "../x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := h.svc.RevokeLink(ctx, tc.owner, tc.site, tc.id); !errors.Is(err, access.ErrNotFound) {
				t.Fatalf("revoke: %v", err)
			}
			if _, _, err := h.svc.RotateLink(ctx, tc.owner, tc.site, tc.id); !errors.Is(err, access.ErrNotFound) {
				t.Fatalf("rotate: %v", err)
			}
		})
	}
	if _, _, err := h.svc.CreateLink(ctx, b, "docs", access.NewLink{}); !errors.Is(err, access.ErrNotFound) {
		t.Fatalf("create on another owner's site: %v", err)
	}
	if _, err := h.svc.ListLinks(ctx, b, "docs", time.Time{}, "", 10); !errors.Is(err, access.ErrNotFound) {
		t.Fatalf("list another owner's site: %v", err)
	}
	ls, err := h.svc.ListLinks(ctx, a, "docs", time.Time{}, "", 10)
	if err != nil || len(ls) != 1 || ls[0].ID != l.ID {
		t.Fatalf("owner's list: %v %v", ls, err)
	}
}

func TestLinkStatus(t *testing.T) {
	h := newHarness(t)
	for _, tc := range []struct {
		l    store.ShareLink
		want access.LinkStatus
	}{
		{store.ShareLink{}, access.LinkActive},
		{store.ShareLink{ExpiresAt: start.Add(time.Millisecond), MaxUses: 2, UseCount: 1}, access.LinkActive},
		{store.ShareLink{ExpiresAt: start}, access.LinkExpired},
		{store.ShareLink{MaxUses: 2, UseCount: 2}, access.LinkUsedUp},
	} {
		if got := h.svc.Status(tc.l); got != tc.want {
			t.Fatalf("Status(%+v) = %s, want %s", tc.l, got, tc.want)
		}
	}
}

func TestDecideWithTokens(t *testing.T) {
	ctx := t.Context()
	h := newHarness(t)
	o := h.owner()
	s := h.site(o, "docs")
	other := h.site(o, "other")
	// A path policy whose only method is a password (A2.3): links refused.
	// Created before protect, whose change notification drops the cache.
	pw := store.Policy{
		ID: store.NewID(), SiteID: s.ID, PathPrefix: "/pw/", Visibility: store.Protected,
		PasswordEnabled: true, PasswordHash: "$argon2id$stand-in", Version: 1, CreatedAt: start, UpdatedAt: start,
	}
	if err := h.db.Policies().Create(ctx, o, pw); err != nil {
		t.Fatal(err)
	}
	h.protect(o, "docs")
	h.protect(o, "other")
	mk := func(site string, n access.NewLink) (store.ShareLink, string) {
		l, token, err := h.svc.CreateLink(ctx, o, site, n)
		if err != nil {
			t.Fatal(err)
		}
		return l, token
	}
	whole, wholeToken := mk("docs", access.NewLink{})
	_, scopedToken := mk("docs", access.NewLink{PathPrefix: "/docs/"})
	_, onceToken := mk("docs", access.NewLink{MaxUses: 1})
	_, otherToken := mk("other", access.NewLink{})
	wrongSecret := wholeToken[:len(wholeToken)-2] + "AA"
	if wrongSecret == wholeToken {
		wrongSecret = wholeToken[:len(wholeToken)-2] + "BB"
	}

	for _, tc := range []struct {
		name    string
		path    string
		token   string
		outcome access.Outcome
	}{
		{"valid", "/", wholeToken, access.Allow},
		{"valid deep", "/a/b.html", wholeToken, access.Allow},
		{"scoped inside", "/docs/x", scopedToken, access.Allow},
		{"scoped directory", "/docs", scopedToken, access.Allow},
		{"scoped outside", "/admin/", scopedToken, access.Login},
		{"use-limited", "/", onceToken, access.Interstitial},
		{"another site's", "/", otherToken, access.Login},
		{"wrong secret", "/", wrongSecret, access.Login},
		{"malformed", "/", "wl_short", access.Login},
		{"uppercase public ID", "/", strings.ToUpper(wholeToken[:13]) + wholeToken[13:], access.Login},
		{"policy without links", "/pw/x", wholeToken, access.Login},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := h.decide(access.Request{SiteID: s.ID, Path: tc.path, Token: tc.token, Return: tc.path, Browser: true, ClientIP: "192.0.2.9"})
			if d.Outcome != tc.outcome {
				t.Fatalf("%+v, want outcome %d", d, tc.outcome)
			}
			switch tc.outcome {
			case access.Allow:
				if d.Via != access.ViaLink || d.LinkPublicID == "" || !d.TokenAccepted || d.Issue == nil || d.Issue.ShareLinkID == "" {
					t.Fatalf("allowed without the link: %+v", d)
				}
			case access.Interstitial:
				if d.Ticket == "" || d.Issue != nil || d.Via != access.ViaNone || strings.Contains(d.Ticket, onceToken) {
					t.Fatalf("interstitial %+v", d)
				}
			default:
				if d.TokenAccepted || d.Issue != nil || d.Via != access.ViaNone {
					t.Fatalf("refused but %+v", d)
				}
			}
		})
	}

	// A session from the token lets later requests in without it, and a
	// token request that already carries it earns no second session.
	d := h.decide(access.Request{SiteID: s.ID, Path: "/", Token: wholeToken})
	c, err := h.svc.IssueSession(ctx, *d.Issue)
	if err != nil {
		t.Fatal(err)
	}
	if d := h.decide(access.Request{SiteID: s.ID, Path: "/x", Cookie: c.Value}); d.Outcome != access.Allow || d.LinkPublicID != whole.PublicID {
		t.Fatalf("session: %+v", d)
	}
	if d := h.decide(access.Request{SiteID: s.ID, Path: "/", Cookie: c.Value, Token: wholeToken}); d.Outcome != access.Allow || d.Issue != nil || !d.TokenAccepted {
		t.Fatalf("token with its session: %+v", d)
	}
	// A refused token falls back to a good cookie.
	if d := h.decide(access.Request{SiteID: s.ID, Path: "/", Cookie: c.Value, Token: wrongSecret}); d.Outcome != access.Allow || d.TokenAccepted {
		t.Fatalf("bad token with a good cookie: %+v", d)
	}
	// Another site never accepts it, even as a cookie.
	if d := h.decide(access.Request{SiteID: other.ID, Path: "/", Cookie: c.Value}); d.Outcome == access.Allow || !d.ClearCookie {
		t.Fatalf("cookie on another site: %+v", d)
	}
	if out := h.log.String(); strings.Contains(out, wholeToken[14:]) || !strings.Contains(out, "public_id=") {
		t.Fatalf("log output:\n%s", out)
	}
}
