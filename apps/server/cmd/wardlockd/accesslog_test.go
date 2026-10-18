// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"wardlock.nz/server/internal/access"
	"wardlock.nz/server/internal/access/accesslog"
	"wardlock.nz/server/internal/edge"
	"wardlock.nz/server/internal/store"
	wltls "wardlock.nz/server/internal/tls"
)

var page = []byte("<p>hello</p>")

type oneSite struct{}

func (oneSite) SiteForHost(_ context.Context, host string) (edge.ServedSite, error) {
	if host != "docs.wardlock.localhost" {
		return edge.ServedSite{}, edge.ErrNoSite
	}
	return edge.ServedSite{ID: "site-docs", Name: "docs", ActiveDeployID: "dep-1", ActivatedAt: time.Now()}, nil
}

func (oneSite) ActiveFiles(context.Context, edge.ServedSite) ([]edge.ServedFile, error) {
	sum := sha256.Sum256(page)
	return []edge.ServedFile{{Path: "index.html", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(page))}}, nil
}

type pageBlobs struct{}

func (pageBlobs) Open(context.Context, string) (io.ReadSeekCloser, error) {
	return struct {
		io.ReadSeeker
		io.Closer
	}{bytes.NewReader(page), io.NopCloser(nil)}, nil
}

// publicSite lets everyone in, as a public default policy does.
type publicSite struct{}

func (publicSite) Decide(context.Context, access.Request) (access.Decision, error) {
	return access.Decision{Outcome: access.Allow, Via: access.ViaPublic}, nil
}

func (publicSite) EndSession(context.Context, string, string) error { return nil }

func (publicSite) IssueSession(context.Context, access.NewSession) (access.Cookie, error) {
	return access.Cookie{}, errors.New("public sites issue no sessions")
}

func (publicSite) View(context.Context, string, string) (access.Viewed, error) {
	return access.Viewed{}, access.ErrTicket
}

func (publicSite) PasswordLogin(context.Context, access.PasswordAttempt) (access.Cookie, error) {
	return access.Cookie{}, access.ErrWrongPassword
}

func (publicSite) SendEmailCode(context.Context, access.EmailCodeRequest) error { return nil }

func (publicSite) EmailCodeLogin(context.Context, access.EmailCodeAttempt) (access.Cookie, error) {
	return access.Cookie{}, access.ErrInvalidCode
}

func (publicSite) OAuthProviders() []access.ProviderInfo { return nil }

func (publicSite) StartOAuth(context.Context, access.OAuthStart) (access.OAuthStarted, error) {
	return access.OAuthStarted{}, access.ErrUnknownProvider
}

func (publicSite) FinishOAuth(context.Context, access.OAuthCallback) (access.OAuthFinished, error) {
	return access.OAuthFinished{}, access.ErrOAuthState
}

func (publicSite) RedeemOAuth(context.Context, access.OAuthRedeem) (access.OAuthRedeemed, error) {
	return access.OAuthRedeemed{}, access.ErrOAuthCode
}

type noInvalidations struct{}

func (noInvalidations) Subscribe(func(string)) func() { return func() {} }

// slowStore takes delay over every batch.
type slowStore struct {
	delay   time.Duration
	mu      sync.Mutex
	entries []store.AccessEntry
}

func (s *slowStore) Append(ctx context.Context, entries []store.AccessEntry) error {
	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
		return ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, entries...)
	return nil
}

func (*slowStore) Prune(context.Context, time.Time, int) (int64, error) { return 0, nil }

// TestSlowStoreDoesNotSlowServing serves through the real edge and the
// real writer, as serve wires them, over a store that takes two seconds per
// batch: requests stay fast, and shutdown writes every entry.
func TestSlowStoreDoesNotSlowServing(t *testing.T) {
	db := &slowStore{delay: 2 * time.Second}
	// The first entry goes out alone and holds the writer while the rest
	// buffer.
	w := accesslog.Start(accesslog.Config{Repo: db, Truncation: accesslog.DefaultTruncation, FlushInterval: time.Millisecond})
	handler := newTestEdge(t, w)

	const n = 200
	began := time.Now()
	for range n {
		req := httptest.NewRequest(http.MethodGet, "http://docs.wardlock.localhost/?t=wl_abcdefgh23_c2VjcmV0LXRva2Vu", nil)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != 200 || res.Body.String() != string(page) {
			t.Fatalf("GET / = %d %q", res.Code, res.Body.String())
		}
	}
	// Waiting on even one write would take two seconds.
	if took := time.Since(began); took > time.Second {
		t.Fatalf("%d requests took %v against a slow store", n, took)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	if err := w.Close(ctx); err != nil {
		t.Fatalf("Close() = %v", err)
	}
	if len(db.entries) != n || w.Dropped() != 0 {
		t.Fatalf("stored %d entries with %d dropped, want %d and 0", len(db.entries), w.Dropped(), n)
	}
	e := db.entries[0]
	if e.SiteID != "site-docs" || e.Path != "/" || e.Status != 200 || e.Via != store.ViaPublic || e.IP != "192.0.2.0" ||
		strings.Contains(e.Path, "c2VjcmV0") {
		t.Fatalf("stored %+v", e)
	}
}

func newTestEdge(t *testing.T, w *accesslog.Writer) *edge.Edge {
	t.Helper()
	e, err := edge.New(edge.Config{
		DashboardHost: "wardlock.localhost",
		Dashboard:     http.NotFoundHandler(),
		Health:        http.NotFoundHandler(),
		Sites:         oneSite{},
		Blobs:         pageBlobs{},
		Invalidator:   noInvalidations{},
		Access:        publicSite{},
		Recorder:      accessRecorder{w},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	return e
}

// TestAccessLogIPIsTheResolvedClient wraps the edge in the trusted-proxy
// handler as serve does: behind a trusted proxy the forwarded client is
// logged, and a client's own X-Forwarded-For is ignored.
func TestAccessLogIPIsTheResolvedClient(t *testing.T) {
	db := &slowStore{}
	w := accesslog.Start(accesslog.Config{Repo: db, Truncation: accesslog.Truncation{IPv4Prefix: 32, IPv6Prefix: 128}})
	h := wltls.TrustedProxies{netip.MustParsePrefix("10.0.0.0/8")}.Handler(newTestEdge(t, w))
	for _, tt := range []struct{ peer, forwarded string }{
		{"10.0.0.5:4000", "198.51.100.7"},    // through the proxy
		{"203.0.113.9:4000", "198.51.100.7"}, // spoofed by a direct client
	} {
		req := httptest.NewRequest(http.MethodGet, "http://docs.wardlock.localhost/", nil)
		req.RemoteAddr = tt.peer
		req.Header.Set("X-Forwarded-For", tt.forwarded)
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	if err := w.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(db.entries) != 2 || db.entries[0].IP != "198.51.100.7" || db.entries[1].IP != "203.0.113.9" {
		t.Fatalf("stored %+v, want the proxied client then the direct peer", db.entries)
	}
}
