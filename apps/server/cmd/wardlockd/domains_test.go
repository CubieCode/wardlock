// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"wardlock.nz/server/internal/access"
	"wardlock.nz/server/internal/edge"
	"wardlock.nz/server/internal/platform"
	"wardlock.nz/server/internal/sites"
	"wardlock.nz/server/internal/sites/sitestest"
	"wardlock.nz/server/internal/store"
	wltls "wardlock.nz/server/internal/tls"
)

// removedCerts records RemoveCertificate calls.
type removedCerts struct {
	mu    sync.Mutex
	hosts []string
}

func (r *removedCerts) RemoveCertificate(_ context.Context, hostname string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hosts = append(r.hosts, hostname)
	return nil
}

func (r *removedCerts) list() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.hosts)
}

// TestCustomDomainsThroughTheEdge drives a custom domain through its life
// with the real store, sites service, edge, trusted-proxy middleware,
// tls-ask and on-demand decision as serve wires them in proxy mode: a
// pending or lapsed hostname is answered exactly like an unknown host and
// gets no certificate, an active one serves the site with its policies,
// and every change applies on the next request.
func TestCustomDomainsThroughTheEdge(t *testing.T) {
	p := newPolicyEdge(t)
	ctx := t.Context()
	p.publish("docs")
	p.publish("other")
	const custom = "www.example.com"
	target := host // docs.wardlock.localhost

	dns := sitestest.NewResolver(target)
	certs := &removedCerts{}
	svc := sites.NewService(sites.Config{
		Sites: p.db.Sites(), Domains: p.db.Domains(), Notifier: p.notifier, Resolver: dns, Certs: certs,
		BaseDomain: "wardlock.localhost", DashboardHost: "wardlock.localhost", Now: p.now, Log: p.log,
		CheckTimeout: 50 * time.Millisecond,
	})
	cfg := platform.Config{BaseDomain: "wardlock.localhost", DashboardHost: "wardlock.localhost", TLS: platform.TLSConfig{Mode: platform.TLSProxy}}
	e, err := edge.New(edge.Config{
		DashboardHost: "wardlock.localhost",
		Dashboard:     http.NotFoundHandler(),
		Health:        http.NotFoundHandler(),
		Sites:         siteSource{baseDomain: "wardlock.localhost", owners: p.db.Owners(), sites: p.db.Sites(), domains: p.db.Domains(), deploys: p.db.Deploys(), previews: p.db.Previews(), renditions: p.db.Renditions()},
		Blobs:         p.blobs,
		Invalidator:   p.notifier,
		Access:        p.acc,
		TLSAsk:        tlsAsk(cfg, p.db, p.log),
		Log:           p.log,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	handler := wltls.TrustedProxies{netip.MustParsePrefix("127.0.0.1/32")}.Handler(e)
	decision := wltls.OnDemand{BaseDomain: "wardlock.localhost", DashboardHost: "wardlock.localhost", Domains: p.db.Domains()}

	get := func(h, target, ip string, header http.Header) result {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.Host = h
		req.RemoteAddr = ip + ":4711"
		maps.Copy(req.Header, header)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return result{rec.Code, rec.Body.String(), rec.Result().Header}
	}
	const client, proxy = "192.0.2.7", "127.0.0.1"
	unknown := get("unknown.example.org", "/", client, nil)
	if unknown.code != http.StatusNotFound {
		t.Fatalf("unknown host %d", unknown.code)
	}
	served := func(want bool) {
		t.Helper()
		for _, path := range []string{"/", "/assets/site.css", "/.wardlock/health", "/missing"} {
			got := get(custom, path, client, nil)
			gotUnknown := got.code == unknown.code && got.body == unknown.body && maps.EqualFunc(got.h, unknown.h, slices.Equal)
			if want && (gotUnknown || (path == "/" && !strings.Contains(got.body, "Home page"))) {
				t.Fatalf("%s%s is not served: %d %q", custom, path, got.code, got.body)
			}
			if !want && !gotUnknown {
				t.Fatalf("%s%s = %d %v %q; want exactly the unknown-host answer", custom, path, got.code, got.h, got.body)
			}
		}
		allowErr := decision.Allow(ctx, custom)
		ask := get("anything.example", wltls.AskPath+"?domain="+custom, proxy, nil)
		if want != (allowErr == nil) || want != (ask.code == http.StatusOK) {
			t.Fatalf("served %v: on-demand decision %v, tls-ask %d", want, allowErr, ask.code)
		}
		if !want && !errors.Is(allowErr, wltls.ErrNotAllowed) {
			t.Fatalf("decision error %v", allowErr)
		}
		// tls-ask never answers anyone but the proxy itself: not a client
		// connecting directly, nor a visitor's request the proxy forwards
		// (as Caddy's reverse_proxy does, from the trusted address, with
		// forwarding headers), on any host.
		if ask := get(custom, wltls.AskPath+"?domain="+custom, client, nil); ask.code != http.StatusNotFound {
			t.Fatalf("tls-ask from a client = %d", ask.code)
		}
		for _, h := range []string{custom, host, "anything.example"} {
			viaProxy := http.Header{"X-Forwarded-For": {client}, "X-Forwarded-Proto": {"https"}}
			if ask := get(h, wltls.AskPath+"?domain="+custom, proxy, viaProxy); ask.code != http.StatusNotFound {
				t.Fatalf("tls-ask forwarded from a visitor on %s = %d", h, ask.code)
			}
		}
	}

	// Pending: added, no DNS yet.
	d, err := svc.AddDomain(ctx, p.owner.ID, "docs", custom)
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != store.DomainPending {
		t.Fatalf("added %+v", d)
	}
	served(false)

	// Active once the CNAME is in place, on the next request.
	dns.CNAME(custom, target)
	p.advance(time.Minute)
	if d, err = svc.VerifyDomain(ctx, p.owner.ID, "docs", custom); err != nil || d.Status != store.DomainActive {
		t.Fatalf("verify: %+v, %v", d, err)
	}
	served(true)

	// The site's policies apply on the custom domain too, and share links
	// work there.
	p.set("docs", "/", store.Protected, true)
	if got := get(custom, "/", client, nil); got.code != http.StatusUnauthorized || strings.Contains(got.body, "Home page") {
		t.Fatalf("protected site on its custom domain: %d %q", got.code, got.body)
	}
	_, token := p.link("docs", access.NewLink{PathPrefix: "/"})
	if got := get(custom, withT("/", token), client, nil); got.code != http.StatusOK || !strings.Contains(got.body, "Home page") {
		t.Fatalf("share link on the custom domain: %d", got.code)
	}
	if got := get(custom, "/", client, bearer(token)); got.code != http.StatusOK || !strings.Contains(got.body, "Home page") {
		t.Fatalf("bearer share link on the custom domain: %d", got.code)
	}
	p.set("docs", "/", store.Public, false)
	served(true)

	// The record disappears: still served through the re-checks, lapsed
	// 72 hours after the first failure, and then unserved at once even
	// though the host was cached.
	dns.Remove(custom)
	for day := 1; day <= 3; day++ {
		p.advance(24 * time.Hour)
		if _, err := svc.CheckDue(ctx); err != nil {
			t.Fatal(err)
		}
		served(true)
	}
	p.advance(24 * time.Hour)
	if _, err := svc.CheckDue(ctx); err != nil {
		t.Fatal(err)
	}
	if d, _ = svc.GetDomain(ctx, p.owner.ID, "docs", custom); d.Status != store.DomainLapsed {
		t.Fatalf("after 72 hours of failures: %+v", d)
	}
	served(false)
	if !slices.Equal(certs.list(), []string{custom}) {
		t.Fatalf("certificates removed on lapse: %v", certs.list())
	}

	// It recovers by itself when the record returns.
	dns.CNAME(custom, target)
	p.advance(24 * time.Hour)
	if _, err := svc.CheckDue(ctx); err != nil {
		t.Fatal(err)
	}
	served(true)

	// Removed: unserved at once, certificate deleted.
	if err := svc.RemoveDomain(ctx, p.owner.ID, "docs", custom); err != nil {
		t.Fatal(err)
	}
	served(false)
	if !slices.Equal(certs.list(), []string{custom, custom}) {
		t.Fatalf("certificates removed: %v", certs.list())
	}
	// Verification tokens never reach the logs.
	if strings.Contains(p.logs.String(), d.VerificationToken) {
		t.Fatalf("a verification token was logged:\n%s", p.logs)
	}
	p.noRawTokens(t, p.accessRecords())
}

// TestTLSAskOnlyInProxyMode: outside proxy mode the edge has no tls-ask.
func TestTLSAskOnlyInProxyMode(t *testing.T) {
	for _, mode := range []string{platform.TLSLocal, platform.TLSAuto} {
		if h := tlsAsk(platform.Config{TLS: platform.TLSConfig{Mode: mode}}, nil, nil); h != nil {
			t.Errorf("%s mode has a tls-ask handler", mode)
		}
	}
}
