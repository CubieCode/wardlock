// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"wardlock.nz/server/internal/access"
	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/sites"
	"wardlock.nz/server/internal/sites/sitestest"
	"wardlock.nz/server/internal/store"
)

// operatorSites is the sites service as `wardlockd admin` builds it, over
// p's database and notifier.
func (p *policyEdge) operatorSites(dns sites.Resolver) *sites.Service {
	return sites.NewService(sites.Config{
		Sites: p.db.Sites(), Domains: p.db.Domains(), Notifier: p.notifier, Resolver: dns,
		BaseDomain: "wardlock.localhost", DashboardHost: "wardlock.localhost", Now: p.now, Log: p.log,
		CheckTimeout: 50 * time.Millisecond,
	})
}

// unavailable fails t unless r is the unavailable page: 403, never stored
// or indexed, framing refused, and none of the site's content.
func (r result) unavailable(t *testing.T) {
	t.Helper()
	r.expect(t, http.StatusForbidden, "This site is unavailable", "Home page")
	for k, want := range map[string]string{
		"Cache-Control": "no-store", "X-Robots-Tag": "noindex", "Referrer-Policy": "no-referrer",
		"X-Frame-Options": "DENY", "X-Content-Type-Options": "nosniff", "Content-Type": "text/html; charset=utf-8",
	} {
		if got := r.h.Get(k); got != want {
			t.Fatalf("unavailable page %s = %q, want %q", k, got, want)
		}
	}
	// What the site left in the browser goes; its cookies stay, so the
	// page says nothing it would not say to a fresh visitor.
	if r.h.Get("Clear-Site-Data") != `"cache", "storage"` {
		t.Fatalf("unavailable page Clear-Site-Data = %q", r.h.Get("Clear-Site-Data"))
	}
	if r.h.Get("Set-Cookie") != "" || !strings.Contains(r.h.Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatalf("unavailable page headers %v", r.h)
	}
}

// TestTakedownThroughTheEdge takes a protected site with a custom domain
// down and restores it with the real store, sites service, access service
// and edge: every path on every host answers the unavailable page whatever
// credential the visitor holds, the files are kept, the owner can neither
// deploy nor delete, and restoring serves the same deploy again.
func TestTakedownThroughTheEdge(t *testing.T) {
	p := newPolicyEdge(t)
	ctx := t.Context()
	site := p.publish("docs")
	p.publish("other")
	const custom = "www.example.com"
	dns := sitestest.NewResolver(host)
	svc := p.operatorSites(dns)
	if _, err := svc.AddDomain(ctx, p.owner.ID, "docs", custom); err != nil {
		t.Fatal(err)
	}
	dns.CNAME(custom, host)
	p.advance(time.Minute)
	if d, err := svc.VerifyDomain(ctx, p.owner.ID, "docs", custom); err != nil || d.Status != store.DomainActive {
		t.Fatalf("verify: %+v, %v", d, err)
	}
	pol := p.set("docs", "/", store.Protected, true)
	_, token := p.link("docs", access.NewLink{Label: "review"})
	cookie := p.issue(site, pol)
	for _, h := range []string{host, custom} {
		p.do(http.MethodGet, h, "/?t="+token, "", nil).expect(t, http.StatusOK, "Home page", "")
	}
	before, err := p.db.Sites().GetByID(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Taken down by name, or by any hostname it answers on.
	if _, err := svc.TakeDown(ctx, custom); err != nil {
		t.Fatal(err)
	}
	browser := http.Header{"Accept": {"text/html"}}
	bearer := http.Header{"Authorization": {"Bearer " + token}}
	for _, h := range []string{host, custom} {
		for _, target := range []string{"/", "/?t=" + token, "/assets/site.css", "/internal/", "/missing", "/.wardlock/login?next=/", "/.wardlock/view", "/.wardlock/callback"} {
			p.do(http.MethodGet, h, target, "", browser).unavailable(t)
			p.do(http.MethodGet, h, target, cookie, nil).unavailable(t)
			if head := p.do(http.MethodHead, h, target, "", nil); head.code != http.StatusForbidden || head.body != "" || head.h.Get("Cache-Control") != "no-store" {
				t.Fatalf("HEAD %s%s on a taken-down site: %d %v", h, target, head.code, head.h)
			}
		}
		p.do(http.MethodGet, h, "/", "", bearer).unavailable(t)
		p.do(http.MethodPost, h, "/.wardlock/login", "", nil).unavailable(t)
		// Health still answers; the report form does not exist while
		// reports are off, and the page offers no link to it.
		p.do(http.MethodGet, h, "/.wardlock/health", "", nil).expect(t, http.StatusOK, `"ok"`, "")
		p.do(http.MethodGet, h, "/.wardlock/report", "", nil).unavailable(t)
		if r := p.do(http.MethodGet, h, "/", "", nil); strings.Contains(r.body, "Report this site") {
			t.Fatal("report link shown with reports off")
		}
	}
	// Other sites are untouched.
	p.do(http.MethodGet, "other.wardlock.localhost", "/", "", nil).expect(t, http.StatusOK, "Home page", "")

	// Deploys and files are kept, and the owner cannot replace or delete
	// them.
	files, err := p.db.Deploys().Files(ctx, before.ActiveDeployID)
	if err != nil || len(files) != len(policyFiles) {
		t.Fatalf("files after takedown: %d, %v", len(files), err)
	}
	for _, f := range files {
		rc, err := p.blobs.Open(ctx, f.SHA256, f.Size)
		if err != nil {
			t.Fatal(err)
		}
		_ = rc.Close()
	}
	if _, _, err := p.deploys.Create(ctx, p.owner.ID, "docs", deploys.Manifest{}); !errors.Is(err, deploys.ErrSiteUnavailable) {
		t.Fatalf("deploy to a taken-down site: %v", err)
	}
	if _, _, err := p.deploys.Rollback(ctx, p.owner.ID, "docs", ""); !errors.Is(err, deploys.ErrSiteUnavailable) {
		t.Fatalf("rollback of a taken-down site: %v", err)
	}
	if err := svc.Delete(ctx, p.owner.ID, "docs"); !errors.Is(err, sites.ErrUnavailable) {
		t.Fatalf("owner delete of a taken-down site: %v", err)
	}
	if _, err := svc.TearDown(ctx, "other"); !errors.Is(err, sites.ErrNotTakenDown) {
		t.Fatalf("teardown of a live site: %v", err)
	}

	// Restored: the same deploy, the same policies, the same link.
	if _, err := svc.Restore(ctx, "docs"); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{host, custom} {
		p.do(http.MethodGet, h, "/?t="+token, "", nil).expect(t, http.StatusOK, "Home page", "")
		p.do(http.MethodGet, h, "/", cookie, nil).expect(t, http.StatusOK, "Home page", "")
		p.do(http.MethodGet, h, "/", "", browser).expect(t, http.StatusUnauthorized, "This page is private", "Home page")
	}
	after, err := p.db.Sites().GetByID(ctx, site.ID)
	if err != nil || after.ActiveDeployID != before.ActiveDeployID {
		t.Fatalf("active deploy after restore: %+v, %v", after, err)
	}

	// Torn down: gone everywhere, name free.
	if _, err := svc.TakeDown(ctx, "docs"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.TearDown(ctx, "docs.wardlock.localhost"); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{host, custom} {
		p.do(http.MethodGet, h, "/", "", nil).expect(t, http.StatusNotFound, "No site", "")
	}
	p.noRawTokens(t, p.accessRecords())
	if strings.Contains(p.logs.String(), token) || strings.Contains(p.logs.String(), cookie) {
		t.Fatal("a credential reached the log")
	}
}

// fakeNotifier records notified site IDs.
type fakeNotifier struct {
	mu  sync.Mutex
	ids []string
}

func (n *fakeNotifier) Notify(_ context.Context, id string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.ids = append(n.ids, id)
}

func (n *fakeNotifier) seen() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return slices.Clone(n.ids)
}

// TestSuspendedOwnerSitesStopServing suspends an owner from "another
// process" (straight in the database, as `wardlockd admin` does) and
// checks the watch tells the edge, which then answers the unavailable page
// on all their sites until they are reinstated.
func TestSuspendedOwnerSitesStopServing(t *testing.T) {
	p := newPolicyEdge(t)
	ctx := t.Context()
	docs := p.publish("docs")
	p.publish("blog")
	other := store.Owner{ID: store.NewID(), Email: "other@example.test", CreatedAt: p.now()}
	if err := p.db.Owners().Create(ctx, other); err != nil {
		t.Fatal(err)
	}
	// Cached by the edge before the suspension.
	p.do(http.MethodGet, host, "/", "", nil).expect(t, http.StatusOK, "Home page", "")

	watchCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Go(func() {
		(&unavailableWatch{log: p.log, sites: p.db.Sites(), domains: p.db.Domains(), notify: p.notifier}).run(watchCtx, 5*time.Millisecond)
	})
	t.Cleanup(func() { cancel(); wg.Wait() })
	eventually := func(what string, ok func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !ok() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s", what)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	served := func(h string) bool { return p.do(http.MethodGet, h, "/", "", nil).code == http.StatusOK }

	time.Sleep(20 * time.Millisecond) // let the watch read its baseline
	if err := p.db.Owners().Suspend(ctx, p.owner.ID, p.now()); err != nil {
		t.Fatal(err)
	}
	eventually("the suspended owner's sites to stop", func() bool {
		return !served(host) && !served("blog.wardlock.localhost")
	})
	p.do(http.MethodGet, host, "/", "", nil).unavailable(t)
	p.do(http.MethodGet, "blog.wardlock.localhost", "/assets/site.css", "", nil).unavailable(t)

	if err := p.db.Owners().Reinstate(ctx, p.owner.ID); err != nil {
		t.Fatal(err)
	}
	eventually("the reinstated owner's sites to serve", func() bool {
		return served(host) && served("blog.wardlock.localhost")
	})

	// The watch reports exactly the sites that changed.
	n := &fakeNotifier{}
	nctx, ncancel := context.WithCancel(ctx)
	var nwg sync.WaitGroup
	nwg.Go(func() {
		(&unavailableWatch{log: p.log, sites: p.db.Sites(), domains: p.db.Domains(), notify: n}).run(nctx, 5*time.Millisecond)
	})
	time.Sleep(20 * time.Millisecond)
	if err := p.db.Sites().TakeDown(ctx, docs.ID, p.now()); err != nil {
		t.Fatal(err)
	}
	eventually("the takedown to be noticed", func() bool { return slices.Contains(n.seen(), docs.ID) })
	ncancel()
	nwg.Wait()
	if got := n.seen(); !slices.Equal(got, []string{docs.ID}) {
		t.Fatalf("notified %v, want only %s", got, docs.ID)
	}
}

func TestChanged(t *testing.T) {
	for _, tc := range []struct{ a, b, want []string }{
		{nil, nil, nil},
		{nil, []string{"a"}, []string{"a"}},
		{[]string{"a", "b"}, []string{"b", "c"}, []string{"a", "c"}},
		{[]string{"a", "b"}, []string{"a", "b"}, nil},
	} {
		if got := changed(tc.a, tc.b); !slices.Equal(got, tc.want) {
			t.Errorf("changed(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// dashHost serves the report form (A2.13).
const dashHost = "wardlock.localhost"

// reportPost sends the report form for the site at site from the
// dashboard host, as a browser showing that form would.
func (p *policyEdge) reportPost(site, ip string, form url.Values, header http.Header) result {
	p.t.Helper()
	form = maps.Clone(form)
	if form == nil {
		form = url.Values{}
	}
	if _, ok := form["site"]; !ok {
		form.Set("site", site)
	}
	req := httptest.NewRequest(http.MethodPost, "/report", strings.NewReader(form.Encode()))
	req.Host = dashHost
	req.RemoteAddr = ip + ":40000"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://"+dashHost)
	for k, v := range header {
		req.Header[k] = v
	}
	rec := httptest.NewRecorder()
	p.h.ServeHTTP(rec, req)
	return result{rec.Code, rec.Body.String(), rec.Result().Header}
}

// reports returns the abuse reports waiting in the dev:// sink.
func (p *policyEdge) reports() []string {
	p.t.Helper()
	files, _ := filepath.Glob(filepath.Join(p.mailDir, "*.eml"))
	var out []string
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err == nil && strings.Contains(string(b), "\r\nTo: <abuse@example.test>\r\n") {
			out = append(out, strings.ReplaceAll(string(b), "=\r\n", ""))
		}
	}
	return out
}

// TestAbuseReports runs the report form through the real edge, access
// service and dev:// mail sink. The form lives on the dashboard host, so
// the reported site's scripts cannot reach it: where it is linked from,
// which hosts it exists for, same-origin posting, field checks, share-link
// tokens removed from what is sent, the operator's text above the
// reporter's quoted words, rate limits counting only valid reports, and
// logs with IDs only.
func TestAbuseReports(t *testing.T) {
	p := newPolicyEdge(t, func(c *access.Config) {
		c.AbuseEmail = "abuse@example.test"
		c.Limits = access.DefaultLimits()
		c.Limits.ReportsPerIP = access.Rate{N: 2, Per: time.Hour}
		c.Limits.ReportsPerNetwork = access.Rate{N: 3, Per: time.Hour}
		c.Limits.ReportsTotal = access.Rate{N: 5, Per: time.Hour}
	})
	ctx := t.Context()
	site := p.publish("docs")
	p.publish("other")
	unpublished := store.Site{ID: store.NewID(), OwnerID: p.owner.ID, Name: "drafts", CreatedAt: p.now()}
	if err := p.db.Sites().Create(ctx, unpublished); err != nil {
		t.Fatal(err)
	}
	p.set("docs", "/internal/", store.Protected, true)
	_, token := p.link("docs", access.NewLink{Label: "review", MaxUses: 1})

	// Linked from the login page and the interstitial, to the dashboard
	// host, for the site and page the visitor was on.
	browser := http.Header{"Accept": {"text/html"}}
	link := `href="http://wardlock.localhost/report?page=%2Finternal%2F&amp;site=docs.wardlock.localhost"`
	p.do(http.MethodGet, host, "/internal/", "", browser).expect(t, http.StatusUnauthorized, link, "")
	p.do(http.MethodGet, host, "/internal/?t="+token, "", browser).expect(t, http.StatusOK, link, token)
	// Never on the site's own pages, and never served on the site's origin.
	p.do(http.MethodGet, host, "/", "", browser).expect(t, http.StatusOK, "Home page", "Report this site")
	p.do(http.MethodGet, host, "/.wardlock/report?page=/", "", nil).expect(t, http.StatusNotFound, "", "Report this site")
	p.reportPostOnSite(t)
	dashboardHits := p.dashboardHits

	form := p.do(http.MethodGet, dashHost, "/report?site=DOCS.wardlock.localhost.&page=/internal/", "", nil)
	form.expect(t, http.StatusOK, `value="docs.wardlock.localhost/internal/"`, "")
	for _, want := range []string{"Report this site", `name="site" value="docs.wardlock.localhost"`, `name="reason" value="phishing"`, `name="details"`, `name="contact"`, `maxlength="2000"`, `action="/report"`} {
		if !strings.Contains(form.body, want) {
			t.Fatalf("form lacks %q", want)
		}
	}
	for k, want := range map[string]string{
		"Cache-Control": "no-store", "X-Robots-Tag": "noindex", "X-Frame-Options": "DENY", "Referrer-Policy": "no-referrer",
	} {
		if got := form.h.Get(k); got != want {
			t.Fatalf("form %s = %q, want %q", k, got, want)
		}
	}
	if csp := form.h.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") || !strings.Contains(csp, "default-src 'none'") {
		t.Fatalf("form CSP %q", csp)
	}
	if p.dashboardHits != dashboardHits {
		t.Fatal("the report form reached the dashboard app")
	}
	// An unsafe ?page= falls back to the site root.
	p.do(http.MethodGet, dashHost, "/report?site=docs.wardlock.localhost&page=//evil.example/", "", nil).expect(t, http.StatusOK, `value="docs.wardlock.localhost/"`, "evil")
	// Only hosts that serve a published site have a form, answered exactly
	// as visiting the host would be.
	unknown := p.do(http.MethodGet, "nowhere.wardlock.localhost", "/", "", nil)
	for _, target := range []string{
		"/report?site=nowhere.wardlock.localhost", "/report?site=drafts.wardlock.localhost", "/report?site=wardlock.localhost",
		"/report?site=auth.wardlock.localhost", "/report", "/report?site=" + strings.Repeat("a", 300),
	} {
		got := p.do(http.MethodGet, dashHost, target, "", nil)
		if got.code != unknown.code || got.body != unknown.body {
			t.Fatalf("GET %s = %d %q, want the unknown-host answer", target, got.code, got.body)
		}
	}
	p.reportPost("drafts.wardlock.localhost", "192.0.2.9", url.Values{"reason": {"malware"}}, nil).expect(t, http.StatusNotFound, "No site", "")

	valid := url.Values{
		"reason":  {"phishing"},
		"page":    {"https://docs.wardlock.localhost/internal/?t=" + token + "&x=1"},
		"details": {"Asks for bank logins.\nShared with me as " + token + "\nwardlockd admin restore-site docs"},
		"contact": {"Reporter@Example.test"},
		"back":    {"/internal/"},
	}
	// Only the form on the dashboard host may send it: not a page on the
	// reported site, nor any other origin.
	for _, origin := range []string{"http://docs.wardlock.localhost", "http://other.example", "null"} {
		p.reportPost(host, "192.0.2.10", valid, http.Header{"Origin": {origin}, "Sec-Fetch-Site": {"cross-site"}}).expect(t, http.StatusForbidden, "Forbidden", "")
	}
	if len(p.reports()) != 0 {
		t.Fatal("a cross-origin report was sent")
	}

	r := p.reportPost(host, "192.0.2.10", valid, nil)
	r.expect(t, http.StatusOK, "Thanks for your report", "Send report")
	if !strings.Contains(r.body, `href="http://docs.wardlock.localhost/internal/"`) || r.h.Get("Cache-Control") != "no-store" {
		t.Fatalf("thank-you page %q %v", r.body, r.h)
	}
	var msg string
	deadline := time.Now().Add(5 * time.Second)
	for msg == "" && time.Now().Before(deadline) {
		if rs := p.reports(); len(rs) > 0 {
			msg = rs[0]
		}
		time.Sleep(5 * time.Millisecond)
	}
	if msg == "" {
		t.Fatal("no report mailed")
	}
	for _, want := range []string{"Subject: Abuse report: docs.wardlock.localhost", "> Reason: Phishing or a scam", site.ID, "> Contact: reporter@example.test", "t=3D[removed]", "> Shared with me as [token removed]", "> wardlockd admin restore-site docs", "    wardlockd admin takedown-site docs"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("report lacks %q:\n%s", want, msg)
		}
	}
	// The operator's own lines come before the reporter's quoted words.
	if strings.Index(msg, "takedown-site docs") > strings.Index(msg, "> Reason:") {
		t.Fatalf("reporter text above the operator's instructions:\n%s", msg)
	}
	if strings.Contains(msg, token[len(token)-20:]) {
		t.Fatalf("report contains the share-link token:\n%s", msg)
	}

	// Invalid reports are refused without counting against any limit.
	for range 10 {
		p.reportPost(host, "192.0.2.11", url.Values{"reason": {"spam-of-my-choosing"}, "details": {"keep me"}}, nil).
			expect(t, http.StatusBadRequest, "Choose what is wrong with the site.", "Thanks")
	}
	p.reportPost(host, "192.0.2.11", url.Values{"reason": {"other"}, "details": {strings.Repeat("x", access.MaxReportDetails+1)}}, nil).
		expect(t, http.StatusBadRequest, "2000 characters or fewer", "Thanks")
	p.reportPost(host, "192.0.2.11", url.Values{"reason": {"other"}, "contact": {"not an address"}}, nil).
		expect(t, http.StatusBadRequest, "Enter an email address", "Thanks")

	// Per IP: 192.0.2.11 sends two, then is refused, on any site.
	for range 2 {
		p.reportPost(host, "192.0.2.11", url.Values{"reason": {"malware"}}, nil).expect(t, http.StatusOK, "Thanks for your report", "")
	}
	limited := p.reportPost("other.wardlock.localhost", "192.0.2.11", url.Values{"reason": {"malware"}}, nil)
	limited.expect(t, http.StatusTooManyRequests, "Too many reports", "Thanks")
	if limited.h.Get("Retry-After") == "" {
		t.Fatal("no Retry-After")
	}
	// Per network: 192.0.2.0/24 has sent three (one from .10, two from .11).
	p.reportPost(host, "192.0.2.12", url.Values{"reason": {"malware"}}, nil).expect(t, http.StatusTooManyRequests, "Too many reports", "")
	// Instance-wide: five in all, whatever the network.
	p.reportPost(host, "198.51.100.1", url.Values{"reason": {"malware"}}, nil).expect(t, http.StatusOK, "Thanks for your report", "")
	p.reportPost(host, "203.0.113.1", url.Values{"reason": {"malware"}}, nil).expect(t, http.StatusOK, "Thanks for your report", "")
	p.reportPost(host, "100.64.0.1", url.Values{"reason": {"malware"}}, nil).expect(t, http.StatusTooManyRequests, "Too many reports", "")
	// The windows end.
	p.advance(time.Hour + time.Minute)
	p.reportPost(host, "192.0.2.11", url.Values{"reason": {"malware"}}, nil).expect(t, http.StatusOK, "Thanks for your report", "")

	// The unavailable page links to the form, which still works.
	svc := p.operatorSites(sitestest.NewResolver(host))
	if _, err := svc.TakeDown(ctx, "docs"); err != nil {
		t.Fatal(err)
	}
	down := p.do(http.MethodGet, host, "/guide", "", nil)
	down.unavailable(t)
	if !strings.Contains(down.body, `href="http://wardlock.localhost/report?page=%2Fguide&amp;site=docs.wardlock.localhost"`) {
		t.Fatalf("unavailable page without the report link:\n%s", down.body)
	}
	p.do(http.MethodGet, dashHost, "/report?site=docs.wardlock.localhost&page=/guide", "", nil).expect(t, http.StatusOK, "Report this site", "")
	p.reportPost(host, "198.51.100.2", url.Values{"reason": {"illegal"}}, nil).expect(t, http.StatusOK, "Thanks for your report", "")

	// Logs name sites by ID, never reporters, tokens or what they wrote.
	logs := p.logs.String()
	for _, secret := range []string{token, "reporter@example.test", "bank logins", "192.0.2.10"} {
		if strings.Contains(logs, secret) {
			t.Fatalf("log contains %q:\n%s", secret, logs)
		}
	}
	if !strings.Contains(logs, "abuse report sent") || !strings.Contains(logs, site.ID) {
		t.Fatalf("report not logged by site ID:\n%s", logs)
	}
}

// reportPostOnSite checks a POST to the old path on a site's own origin
// sends nothing: the site's scripts cannot send reports in its visitors'
// names.
func (p *policyEdge) reportPostOnSite(t *testing.T) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/.wardlock/report", strings.NewReader("reason=malware"))
	req.Host = host
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://"+host)
	rec := httptest.NewRecorder()
	p.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound || len(p.reports()) != 0 {
		t.Fatalf("POST /.wardlock/report on the site = %d, %d reports", rec.Code, len(p.reports()))
	}
}

// TestAdminOperatorCommands runs the operator commands as the binary
// does, against a data directory, and checks what they print and log.
func TestAdminOperatorCommands(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{
		"WARDLOCK_DATA_DIR": dir, "WARDLOCK_SECRET": testSecret,
	}
	getenv := func(k string) string { return env[k] }
	var logs bytes.Buffer
	admin := func(args ...string) (string, error) {
		t.Helper()
		var out bytes.Buffer
		err := run(t.Context(), append([]string{"admin"}, args...), getenv, &out, &logs)
		return out.String(), err
	}
	must := func(args ...string) string {
		t.Helper()
		out, err := admin(args...)
		if err != nil {
			t.Fatalf("admin %v: %v\n%s", args, err, logs.String())
		}
		return out
	}
	must("create", "--email", "owner@example.test")
	must("create", "--email", "second@example.test")

	// A site to act on, written as the API would.
	db, err := store.Open(t.Context(), filepath.Join(dir, "wardlock.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	o, err := db.Owners().GetByEmail(t.Context(), "owner@example.test")
	if err != nil {
		t.Fatal(err)
	}
	site := store.Site{ID: store.NewID(), OwnerID: o.ID, Name: "docs", CreatedAt: time.Now()}
	if err := db.Sites().Create(t.Context(), site); err != nil {
		t.Fatal(err)
	}

	if out := must("list-owners"); !strings.Contains(out, "owner@example.test") || !strings.Contains(out, "invited") || !strings.Contains(out, o.ID) {
		t.Fatalf("list-owners:\n%s", out)
	}
	if out := must("list-sites"); !strings.Contains(out, "docs") || !strings.Contains(out, "nothing published") {
		t.Fatalf("list-sites:\n%s", out)
	}

	if out := must("suspend-owner", "Owner@Example.test"); !strings.Contains(out, "Suspended owner@example.test") {
		t.Fatalf("suspend-owner:\n%s", out)
	}
	if out := must("list-owners"); !strings.Contains(out, "suspended") {
		t.Fatalf("list-owners after suspension:\n%s", out)
	}
	if out := must("list-sites"); !strings.Contains(out, "owner suspended") {
		t.Fatalf("list-sites after suspension:\n%s", out)
	}
	if ids, err := db.Sites().UnavailableIDs(t.Context()); err != nil || !slices.Equal(ids, []string{site.ID}) {
		t.Fatalf("unavailable after suspension: %v, %v", ids, err)
	}
	must("reinstate-owner", "owner@example.test")

	if out := must("takedown-site", "docs.wardlock.localhost"); !strings.Contains(out, "Took down docs") {
		t.Fatalf("takedown-site:\n%s", out)
	}
	if out := must("list-sites"); !strings.Contains(out, "taken down") {
		t.Fatalf("list-sites after takedown:\n%s", out)
	}
	must("restore-site", "docs")
	if _, err := admin("teardown-site", "docs"); err == nil || !strings.Contains(err.Error(), "not taken down") {
		t.Fatalf("teardown of a live site: %v", err)
	}
	must("takedown-site", "docs")
	must("teardown-site", "docs")
	if _, err := db.Sites().GetByID(t.Context(), site.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("site after teardown: %v", err)
	}

	for _, bad := range [][]string{
		{"suspend-owner", "nobody@example.test"}, {"suspend-owner", "not an address"}, {"takedown-site", "missing"},
	} {
		if _, err := admin(bad...); err == nil || errors.Is(err, errUsage) {
			t.Errorf("admin %v = %v, want an error", bad, err)
		}
	}
	for _, bad := range [][]string{{"suspend-owner"}, {"takedown-site", "a", "b"}, {"list-sites", "x"}, {"delete"}} {
		if _, err := admin(bad...); !errors.Is(err, errUsage) {
			t.Errorf("admin %v = %v, want usage", bad, err)
		}
	}

	// Operator actions are logged, by ID only.
	l := logs.String()
	for _, want := range []string{"operator suspended owner", "operator reinstated owner", "operator took down site", "operator restored site", "operator tore down site", o.ID, site.ID} {
		if !strings.Contains(l, want) {
			t.Errorf("log lacks %q", want)
		}
	}
	for _, line := range strings.Split(l, "\n") {
		if strings.Contains(line, "operator ") && strings.Contains(line, "@example.test") {
			t.Errorf("operator action logged an address: %s", line)
		}
	}
}

// TestUnpublishedTakenDownSiteLooksMissing checks a taken-down site with
// nothing published answers exactly as an unknown host does.
func TestUnpublishedTakenDownSiteLooksMissing(t *testing.T) {
	p := newPolicyEdge(t)
	ctx := t.Context()
	drafts := store.Site{ID: store.NewID(), OwnerID: p.owner.ID, Name: "drafts", CreatedAt: p.now()}
	if err := p.db.Sites().Create(ctx, drafts); err != nil {
		t.Fatal(err)
	}
	if _, err := p.operatorSites(sitestest.NewResolver()).TakeDown(ctx, "drafts"); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"/", "/index.html", "/.wardlock/health", "/.wardlock/login"} {
		unknown := p.do(http.MethodGet, "nowhere.wardlock.localhost", target, "", nil)
		got := p.do(http.MethodGet, "drafts.wardlock.localhost", target, "", nil)
		if got.code != unknown.code || got.body != unknown.body || !maps.EqualFunc(got.h, unknown.h, slices.Equal) {
			t.Fatalf("%s on an unpublished taken-down site = %d %v %q; want the unknown-host answer %d %v %q",
				target, got.code, got.h, got.body, unknown.code, unknown.h, unknown.body)
		}
	}
}

// fakeCerts records removed certificates.
type fakeCerts struct {
	mu      sync.Mutex
	removed []string
}

func (c *fakeCerts) RemoveCertificate(_ context.Context, h string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.removed = append(c.removed, h)
	return nil
}

func (c *fakeCerts) list() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.removed)
}

// TestWatchRemovesTornDownCertificates tears a site down from "another
// process" and checks the serving process's watch removes its custom
// domains' certificates, and only on teardown, not on restore.
func TestWatchRemovesTornDownCertificates(t *testing.T) {
	p := newPolicyEdge(t)
	ctx := t.Context()
	p.publish("docs")
	p.publish("other")
	dns := sitestest.NewResolver(host, "other.wardlock.localhost")
	svc := p.operatorSites(dns)
	for site, h := range map[string]string{"docs": "www.example.com", "other": "other.example.com"} {
		if _, err := svc.AddDomain(ctx, p.owner.ID, site, h); err != nil {
			t.Fatal(err)
		}
	}
	certs := &fakeCerts{}
	w := &unavailableWatch{log: p.log, sites: p.db.Sites(), domains: p.db.Domains(), notify: p.notifier, certs: certs}
	wctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Go(func() { w.run(wctx, 5*time.Millisecond) })
	t.Cleanup(func() { cancel(); wg.Wait() })
	settle := func() { time.Sleep(50 * time.Millisecond) }

	settle()
	// Taken down and restored: nothing removed.
	if err := p.db.Sites().TakeDown(ctx, p.siteID("other"), p.now()); err != nil {
		t.Fatal(err)
	}
	settle()
	if err := p.db.Sites().Restore(ctx, p.siteID("other")); err != nil {
		t.Fatal(err)
	}
	settle()
	// Taken down, then torn down: its domain's certificate goes.
	if err := p.db.Sites().TakeDown(ctx, p.siteID("docs"), p.now()); err != nil {
		t.Fatal(err)
	}
	settle()
	if err := p.db.Sites().TearDown(ctx, p.siteID("docs")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(certs.list()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := certs.list(); !slices.Equal(got, []string{"www.example.com"}) {
		t.Fatalf("removed certificates %v, want only www.example.com", got)
	}
}

func (p *policyEdge) siteID(name string) string {
	p.t.Helper()
	s, err := p.db.Sites().GetByName(p.t.Context(), name)
	if err != nil {
		p.t.Fatal(err)
	}
	return s.ID
}
