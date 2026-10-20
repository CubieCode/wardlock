// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/access"
	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/mail"
	"wardlock.nz/server/internal/moderation"
	"wardlock.nz/server/internal/moderation/moderationtest"
	"wardlock.nz/server/internal/moderation/testimages"
	"wardlock.nz/server/internal/sites/sitestest"
	"wardlock.nz/server/internal/store"
)

func hashOf(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// publishAs creates a site of ownerID and activates a deploy of files on
// it.
func (p *policyEdge) publishAs(ownerID, name string, spa bool, files map[string]string) {
	p.t.Helper()
	p.publishWith(ownerID, name, deploys.Manifest{SPA: spa}, files)
}

// publishWith is publishAs with m's options.
func (p *policyEdge) publishWith(ownerID, name string, m deploys.Manifest, files map[string]string) {
	t, ctx := p.t, p.t.Context()
	t.Helper()
	if err := p.db.Sites().Create(ctx, store.Site{ID: store.NewID(), OwnerID: ownerID, Name: name, CreatedAt: p.now()}, store.Quotas{}); err != nil {
		t.Fatal(err)
	}
	for path, body := range files {
		m.Files = append(m.Files, store.DeployFile{Path: path, SHA256: hashOf(body), Size: int64(len(body))})
	}
	d, _, err := p.deploys.Create(ctx, ownerID, name, m)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range m.Files {
		if err := p.deploys.Upload(ctx, ownerID, name, d.ID, f.SHA256, strings.NewReader(files[f.Path])); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := p.deploys.Activate(ctx, ownerID, name, d.ID); err != nil {
		t.Fatal(err)
	}
}

// mailsTo returns the bodies of the messages the dev sink wrote to addr,
// soft line breaks removed.
func (p *policyEdge) mailsTo(addr string) []string {
	p.t.Helper()
	files, _ := filepath.Glob(filepath.Join(p.mailDir, "*.eml"))
	var out []string
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err == nil && strings.Contains(string(b), "\r\nTo: <"+addr+">\r\n") {
			out = append(out, strings.ReplaceAll(string(b), "=\r\n", ""))
		}
	}
	return out
}

// withheld fails t unless r is the 451 answer for a quarantined file: the
// neutral page for HTML, a line of text otherwise, never stored, and none
// of the file's content.
func (r result) withheld(t *testing.T, page bool, hidden string) {
	t.Helper()
	body := "This file is unavailable."
	if page {
		body = "This page is unavailable"
	}
	if r.code != http.StatusUnavailableForLegalReasons || (r.body != "" && !strings.Contains(r.body, body)) || strings.Contains(r.body, hidden) {
		t.Fatalf("%d %q, want 451 with %q and never %q", r.code, r.body, body, hidden)
	}
	if r.h.Get("Cache-Control") != "no-store" || r.h.Get("ETag") != "" || r.h.Get("Content-Range") != "" || r.h.Get("Content-Encoding") != "" {
		t.Fatalf("withheld headers %v", r.h)
	}
}

// TestContentScreeningThroughTheEdge screens deploys with the fake
// moderator and the real store, deploys, access service and edge: each
// unique image and page is sent once, exempt owners' files never; a
// quarantined file answers 451 on every host and path that would serve it
// (clean URLs, renditions, precompressed siblings, 404.html, the SPA
// fallback, HEAD, Range, conditional requests, share links) and wherever
// its hash appears, while the rest of the site keeps working; the operator
// and owners are told; and releasing serves it again.
func TestContentScreeningThroughTheEdge(t *testing.T) {
	p := newPolicyEdge(t)
	ctx := t.Context()
	const (
		badPage = "<h1>Notes</h1><p>forbidden-words here</p>"
		minors  = "<p>minors-marker</p>"
		violent = "<p>violent-marker</p>"
	)
	badPNG, okPNG := testimages.PNG("bad-image-bytes", 0), testimages.PNG("fine-image-bytes", 0)
	p.publishAs(p.owner.ID, "docs", false, map[string]string{
		"index.html":       "<p>Home page</p>",
		"bad.html":         badPage,
		"bad.html.gz":      "gzip-bytes-of-the-bad-page",
		"img/bad.png":      badPNG,
		"img/also-bad.PNG": badPNG,
		"img/ok.png":       okPNG,
		"report.html":      violent,
		"site.css":         "body { color: teal }",
	})
	// 404.html and the SPA fallback answer from other paths.
	p.publishAs(p.owner.ID, "lost", false, map[string]string{"index.html": "<p>Lost home</p>", "404.html": badPage})
	p.publishAs(p.owner.ID, "app", true, map[string]string{"index.html": minors})
	// Another owner lists the same image bytes; an exempt owner's files are
	// never sent, however they would be judged.
	other := store.Owner{ID: store.NewID(), Email: "other@example.test", CreatedAt: p.now()}
	exempt := store.Owner{ID: store.NewID(), Email: "trusted@example.test", CreatedAt: p.now()}
	for _, o := range []store.Owner{other, exempt} {
		if err := p.db.Owners().Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	p.publishAs(other.ID, "shop", false, map[string]string{"index.html": "<p>Shop</p>", "logo.png": badPNG})
	if err := p.db.Moderation().SetExempt(ctx, exempt.ID, true); err != nil {
		t.Fatal(err)
	}
	exemptPNG := testimages.PNG("bad-image-bytes but exempt", 0)
	p.publishAs(exempt.ID, "trusted", false, map[string]string{"index.html": "<p>Trusted forbidden-words</p>", "pic.png": exemptPNG})

	// A custom domain on docs.
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

	fake := moderationtest.New()
	fake.Flag("forbidden-words", "sexual")
	fake.Flag("bad-image-bytes", "sexual")
	fake.Flag("minors-marker", "sexual/minors")
	fake.Flag("violent-marker", "violence")
	mod := moderation.New(moderation.Config{
		Store: p.db.Moderation(), Blobs: p.blobs, Moderator: fake,
		Policy:     moderation.Policy{Quarantine: []string{"sexual/minors", "sexual"}},
		Quarantine: p.quarantine,
		Mailer:     mail.NewDevSink(mail.Config{DevDir: p.mailDir, Logger: p.log, Now: p.now}),
		AbuseEmail: "abuse@example.test", DashboardHost: "wardlock.localhost",
		Now: p.now, Jitter: func() float64 { return 0 }, Log: p.log,
	})
	mod.Pass(ctx)
	mod.Pass(ctx)

	// Each unique image and page once: the pages of docs, lost, app and
	// shop (index.html bodies differ) and two images. Nothing of the
	// exempt owner's; nothing of the stylesheet or the precompressed file,
	// neither an image nor served as text.
	sent := fake.Sent()
	var texts, images int
	for _, in := range sent {
		joined := strings.Join(in.Text, " ") + string(in.Image)
		if strings.Contains(joined, "Trusted") || strings.Contains(joined, "exempt") || strings.Contains(joined, "teal") || strings.Contains(joined, "gzip") {
			t.Fatalf("sent %q", joined)
		}
		if len(in.Image) > 0 {
			images++
			if in.ImageType != "image/png" {
				t.Fatalf("image type %q", in.ImageType)
			}
		} else {
			texts++
		}
	}
	if images != 2 || texts != 6 {
		t.Fatalf("sent %d images and %d pages, want 2 and 6 (each unique file once)", images, texts)
	}
	for body, want := range map[string]store.ModerationStatus{
		badPage: store.ModerationQuarantined, badPNG: store.ModerationQuarantined, minors: store.ModerationQuarantined,
		violent: store.ModerationFlagged, okPNG: store.ModerationAllowed, "<p>Home page</p>": store.ModerationAllowed,
	} {
		r, err := p.db.Moderation().Get(ctx, hashOf(body))
		if err != nil || r.Status != want {
			t.Fatalf("verdict for %q = %+v, %v; want %s", body, r, err, want)
		}
	}
	if _, err := p.db.Moderation().Get(ctx, hashOf(exemptPNG)); err == nil {
		t.Fatal("the exempt owner's image was queued")
	}

	// Quarantined files answer 451 on every host and path; the rest serves.
	for _, h := range []string{host, custom} {
		p.do(http.MethodGet, h, "/", "", html).expect(t, http.StatusOK, "Home page", "")
		p.do(http.MethodGet, h, "/img/ok.png", "", nil).expect(t, http.StatusOK, "fine-image-bytes", "")
		p.do(http.MethodGet, h, "/report.html", "", html).expect(t, http.StatusOK, "violent-marker", "") // flagged: still served
		for _, target := range []string{"/bad.html", "/bad", "/bad.html?format=md", "/bad.html?format=txt"} {
			p.do(http.MethodGet, h, target, "", html).withheld(t, true, "forbidden-words")
		}
		p.do(http.MethodGet, h, "/bad.html.gz", "", nil).withheld(t, false, "gzip-bytes")
		p.do(http.MethodGet, h, "/bad.html", "", http.Header{"Accept-Encoding": {"gzip"}}).withheld(t, true, "gzip-bytes")
		for _, target := range []string{"/img/bad.png", "/img/also-bad.PNG"} {
			p.do(http.MethodGet, h, target, "", nil).withheld(t, false, "bad-image-bytes")
			p.do(http.MethodGet, h, target, "", http.Header{"Range": {"bytes=0-3"}}).withheld(t, false, "PNG")
			p.do(http.MethodGet, h, target, "", http.Header{"If-None-Match": {`"` + hashOf(badPNG) + `"`}}).withheld(t, false, "PNG")
			head := p.do(http.MethodHead, h, target, "", nil)
			head.withheld(t, false, "PNG")
			if head.body != "" {
				t.Fatalf("HEAD body %q", head.body)
			}
		}
	}
	p.do(http.MethodGet, "lost.wardlock.localhost", "/missing", "", html).withheld(t, true, "forbidden-words")
	p.do(http.MethodGet, "lost.wardlock.localhost", "/", "", html).expect(t, http.StatusOK, "Lost home", "")
	p.do(http.MethodGet, "app.wardlock.localhost", "/some/route", "", html).withheld(t, true, "minors-marker")
	p.do(http.MethodGet, "shop.wardlock.localhost", "/logo.png", "", nil).withheld(t, false, "bad-image-bytes")
	p.do(http.MethodGet, "shop.wardlock.localhost", "/", "", html).expect(t, http.StatusOK, "Shop", "")
	p.do(http.MethodGet, "trusted.wardlock.localhost", "/pic.png", "", nil).expect(t, http.StatusOK, "exempt", "")

	// On a protected site the policy answers first, so the 451 says nothing
	// to a visitor without access; with a share link, cookie-less or
	// bearer, the file is still withheld.
	p.set("docs", "/", store.Protected, true)
	p.do(http.MethodGet, host, "/bad.html", "", html).expect(t, http.StatusUnauthorized, "This page is private", "unavailable")
	_, token := p.link("docs", access.NewLink{Label: "review"})
	p.do(http.MethodGet, host, withT("/", token), "", html).expect(t, http.StatusOK, "Home page", "")
	p.do(http.MethodGet, host, withT("/bad.html", token), "", html).withheld(t, true, "forbidden-words")
	plain := p.do(http.MethodGet, host, "/img/bad.png", "", bearer(token))
	plain.withheld(t, false, "bad-image-bytes")
	// The plain-text 451 carries the protected-site headers too.
	if plain.h.Get("X-Robots-Tag") != "noindex" || plain.h.Get("Cross-Origin-Resource-Policy") != "same-origin" ||
		plain.h.Get("Referrer-Policy") != "no-referrer" || !slices.Contains(plain.h.Values("Vary"), "Cookie") {
		t.Fatalf("protected 451 headers %v", plain.h)
	}
	p.do(http.MethodGet, custom, withT("/img/bad.png", token), "", nil).withheld(t, false, "bad-image-bytes")
	p.noRawTokens(t, p.accessRecords())

	// The operator hears of each quarantine and the flag, with sites and
	// categories; owners hear which file and site, never why.
	ops := p.mailsTo("abuse@example.test")
	if len(ops) != 4 {
		t.Fatalf("%d operator notices, want 4", len(ops))
	}
	var minorsNotice string
	for _, m := range ops {
		if strings.Contains(m, hashOf(minors)) {
			minorsNotice = m
		}
	}
	if !strings.Contains(minorsNotice, "Preserving and reporting") || !strings.Contains(minorsNotice, "wardlockd admin moderation confirm "+hashOf(minors)) ||
		strings.Contains(minorsNotice, "moderation release") {
		t.Fatalf("minors notice:\n%s", minorsNotice)
	}
	for _, m := range ops {
		if strings.Contains(m, hashOf(badPNG)) && (!strings.Contains(m, "docs  /img/bad.png") || !strings.Contains(m, "shop  /logo.png") || !strings.Contains(m, "Categories: sexual")) {
			t.Fatalf("image notice:\n%s", m)
		}
	}
	owner := p.mailsTo("owner@example.test")
	if len(owner) != 4 { // bad.html on docs, the images on docs, 404.html on lost, index.html on app
		t.Fatalf("%d owner notices, want 4:\n%s", len(owner), strings.Join(owner, "\n---\n"))
	}
	for _, m := range owner {
		for _, never := range []string{"sexual", "minors", "violence", "categor", "forbidden-words", "report.html"} {
			if strings.Contains(m, never) {
				t.Fatalf("owner notice mentions %q:\n%s", never, m)
			}
		}
	}
	if !slices.ContainsFunc(owner, func(m string) bool {
		return strings.Contains(m, "Subject: A file on docs is withheld") && strings.Contains(m, "/img/bad.png") && strings.Contains(m, "/img/also-bad.PNG")
	}) {
		t.Fatalf("owner notices:\n%s", strings.Join(owner, "\n---\n"))
	}
	if got := p.mailsTo("other@example.test"); len(got) != 1 || !strings.Contains(got[0], "/logo.png") {
		t.Fatalf("other owner notices: %q", got)
	}
	if got := p.mailsTo("trusted@example.test"); len(got) != 0 {
		t.Fatalf("exempt owner notices: %q", got)
	}

	// No content reaches the logs.
	for _, never := range []string{"forbidden-words", "bad-image-bytes", "minors-marker", "violent-marker"} {
		if strings.Contains(p.logs.String(), never) {
			t.Fatalf("logs contain %q:\n%s", never, p.logs.String())
		}
	}

	// Released, the image serves again once the server reloads the list.
	if err := mod.Release(ctx, hashOf(badPNG)); err != nil {
		t.Fatal(err)
	}
	if err := p.quarantine.Load(ctx, p.db.Moderation().Quarantined); err != nil {
		t.Fatal(err)
	}
	p.do(http.MethodGet, "shop.wardlock.localhost", "/logo.png", "", nil).expect(t, http.StatusOK, "bad-image-bytes", "")
	p.do(http.MethodGet, host, "/bad.html", "", bearer(token)).withheld(t, true, "forbidden-words")
}

// TestAdminModeration runs `wardlockd admin moderation` as the binary does,
// against a data directory with screened files.
func TestAdminModeration(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{"WARDLOCK_DATA_DIR": dir, "WARDLOCK_SECRET": testSecret}
	getenv := func(k string) string { return env[k] }
	var logs bytes.Buffer
	admin := func(args ...string) (string, error) {
		t.Helper()
		var out bytes.Buffer
		err := run(t.Context(), append([]string{"admin", "moderation"}, args...), getenv, &out, &logs)
		return out.String(), err
	}
	must := func(args ...string) string {
		t.Helper()
		out, err := admin(args...)
		if err != nil {
			t.Fatalf("admin moderation %v: %v\n%s", args, err, logs.String())
		}
		return out
	}
	if out := must("list"); !strings.Contains(out, "No flagged or quarantined files") {
		t.Fatalf("empty list:\n%s", out)
	}
	if err := run(t.Context(), []string{"admin", "create", "--email", "owner@example.test"}, getenv, &bytes.Buffer{}, &logs); err != nil {
		t.Fatal(err)
	}

	// A site serving two screened images, written as the API and worker
	// would.
	db, err := store.Open(t.Context(), filepath.Join(dir, "wardlock.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := t.Context()
	o, err := db.Owners().GetByEmail(ctx, "owner@example.test")
	if err != nil {
		t.Fatal(err)
	}
	site := store.Site{ID: store.NewID(), OwnerID: o.ID, Name: "docs", CreatedAt: time.Now()}
	if err := db.Sites().Create(ctx, site, store.Quotas{}); err != nil {
		t.Fatal(err)
	}
	q, f, u := strings.Repeat("1", 64), strings.Repeat("2", 64), strings.Repeat("4", 64)
	d := store.Deploy{ID: store.NewID(), SiteID: site.ID, FileCount: 3, TotalSize: 15, CreatedAt: time.Now()}
	files := []store.DeployFile{{Path: "q.png", SHA256: q, Size: 5}, {Path: "f.png", SHA256: f, Size: 5}, {Path: "u.avif", SHA256: u, Size: 5}}
	if err := db.Deploys().Create(ctx, o.ID, d, files, store.Quotas{}); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{q, f, u} {
		if err := db.Deploys().RecordUpload(ctx, o.ID, store.Blob{SHA256: h, Size: 5, CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Deploys().Activate(ctx, o.ID, site.ID, d.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Moderation().EnqueueSite(ctx, site.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	for h, v := range map[string]store.ModerationVerdict{
		q: {Status: store.ModerationQuarantined, Categories: []string{"sexual"}, Kind: store.ModerationImage},
		f: {Status: store.ModerationFlagged, Categories: []string{"violence"}, Kind: store.ModerationImage},
		u: {Status: store.ModerationFlagged, Reason: store.ModerationUnscreenable, Kind: store.ModerationImage},
	} {
		if err := db.Moderation().Record(ctx, h, v, time.Now()); err != nil {
			t.Fatal(err)
		}
	}

	out := must("list")
	for _, want := range []string{q, f, u, "quarantined", "flagged", "docs/q.png", "sexual", "violence", "unscreenable", "docs/u.avif"} {
		if !strings.Contains(out, want) {
			t.Fatalf("list lacks %q:\n%s", want, out)
		}
	}
	if out := must("confirm", strings.ToUpper(q)); !strings.Contains(out, "Confirmed "+q) {
		t.Fatalf("confirm:\n%s", out)
	}
	if out := must("list"); !strings.Contains(out, "quarantined (confirmed, held)") {
		t.Fatalf("list after confirm:\n%s", out)
	}
	if out := must("discard", q); !strings.Contains(out, "Discarded") {
		t.Fatalf("discard:\n%s", out)
	}
	if out := must("rescan", q); !strings.Contains(out, "Queued") {
		t.Fatalf("rescan:\n%s", out)
	}
	if out := must("list"); !strings.Contains(out, "quarantined (discarded), rescan queued") {
		t.Fatalf("list after rescan:\n%s", out)
	}
	if out := must("release", f); !strings.Contains(out, "Released") {
		t.Fatalf("release:\n%s", out)
	}
	for args, want := range map[string]string{
		"confirm " + f:                       "is not quarantined",
		"release " + strings.Repeat("3", 64): "no screening result",
		"release abc":                        "is not a SHA-256",
	} {
		if _, err := admin(strings.Fields(args)...); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("admin moderation %s: %v, want %q", args, err, want)
		}
	}

	if out := must("disable", "--owner", "Owner@Example.test"); !strings.Contains(out, "Screening is off for owner@example.test") {
		t.Fatalf("disable:\n%s", out)
	}
	if ex, err := db.Moderation().Exempt(ctx, o.ID); err != nil || !ex {
		t.Fatalf("exempt after disable = %v, %v", ex, err)
	}
	if out := must("enable", "--owner", "owner@example.test"); !strings.Contains(out, "Screening is on") {
		t.Fatalf("enable:\n%s", out)
	}
	if ex, err := db.Moderation().Exempt(ctx, o.ID); err != nil || ex {
		t.Fatalf("exempt after enable = %v, %v", ex, err)
	}
	if _, err := admin("disable", "--owner", "nobody@example.test"); err == nil || !strings.Contains(err.Error(), "no owner") {
		t.Fatalf("disable unknown owner: %v", err)
	}
	for _, bad := range [][]string{{}, {"list", "x"}, {"release"}, {"disable"}, {"disable", "owner@example.test"}, {"frobnicate"}} {
		if _, err := admin(bad...); err == nil {
			t.Fatalf("admin moderation %v succeeded", bad)
		}
	}
	// Operator actions are logged with hashes and IDs only.
	if strings.Contains(logs.String(), "owner@example.test") && strings.Contains(logs.String(), "screening exemption") {
		for line := range strings.SplitSeq(logs.String(), "\n") {
			if strings.Contains(line, "screening exemption") && strings.Contains(line, "owner@example.test") {
				t.Fatalf("exemption log names the address: %s", line)
			}
		}
	}
}

// TestServeWithScreening starts serve with screening on: the start-up log
// names the provider but never the key, and /metrics on the operator
// listener reports the backlog and the withheld count.
func TestServeWithScreening(t *testing.T) {
	const key = "sk-test-never-logged-0123456789"
	s := startServerEnv(t, t.TempDir(), map[string]string{"WARDLOCK_MODERATION": "openai", "WARDLOCK_OPENAI_API_KEY": key})
	var body []byte
	for range 50 {
		var code int
		if code, body = s.do(s.opAddr, http.MethodGet, "localhost", "/metrics", nil); code == http.StatusOK {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, want := range []string{"wardlock_moderation_backlog 0", "wardlock_moderation_quarantined 0", "wardlock_moderation_paused 0", "wardlock_moderation_provider_errors_total 0"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("/metrics lacks %q:\n%s", want, body)
		}
	}
	s.stop()
	if logs := s.logs.String(); strings.Contains(logs, key) || !strings.Contains(logs, `"moderation":"openai"`) {
		t.Fatalf("start-up logs:\n%s", logs)
	}
}

// TestScreeningCacheAndSiblings checks what screening changes in serving
// on a screened site (A2.15): a file not screened yet is never cached for
// long, a gzip sibling is offered only once screening found it
// decompresses to exactly its file (never an abusive one with a benign
// base), and a brotli sibling is never offered; a site of an exempt owner
// is served as before.
func TestScreeningCacheAndSiblings(t *testing.T) {
	p := newPolicyEdge(t)
	ctx := t.Context()
	screened := p.screening()
	gz := func(b string) string {
		var buf bytes.Buffer
		w := gzip.NewWriter(&buf)
		_, _ = w.Write([]byte(b))
		_ = w.Close()
		return buf.String()
	}
	const page, asset, js = "<p>Benign page</p>", "/assets/app-3f2a9c1b.js", "console.log('app')"
	files := map[string]string{"index.html": page, "index.html.gz": gz(page), "index.html.br": "brotli-bytes", asset[1:]: js}
	docs := map[string]string{"other.html": "<p>Other</p>", "other.html.gz": gz("<p>abusive-sibling</p>")}
	maps.Copy(docs, files)
	p.publishWith(p.owner.ID, "docs", deploys.Manifest{ImmutableAssets: true}, docs)
	exempt := store.Owner{ID: store.NewID(), Email: "trusted@example.test", CreatedAt: p.now()}
	if err := p.db.Owners().Create(ctx, exempt); err != nil {
		t.Fatal(err)
	}
	if err := p.db.Moderation().SetExempt(ctx, exempt.ID, true); err != nil {
		t.Fatal(err)
	}
	p.publishWith(exempt.ID, "trusted", deploys.Manifest{ImmutableAssets: true}, files)
	const docsHost, trustedHost = "docs.wardlock.localhost", "trusted.wardlock.localhost"
	both := http.Header{"Accept-Encoding": {"br, gzip"}}
	encoding := func(host, target string, h http.Header, want, body string) {
		t.Helper()
		r := p.do(http.MethodGet, host, target, "", h)
		if r.code != http.StatusOK || r.h.Get("Content-Encoding") != want || (body != "" && r.body != body) {
			t.Fatalf("%s%s: %d, encoding %q, body %.30q; want %q", host, target, r.code, r.h.Get("Content-Encoding"), r.body, want)
		}
	}
	cache := func(host, target, want string) {
		t.Helper()
		if r := p.do(http.MethodGet, host, target, "", nil); r.code != http.StatusOK || r.h.Get("Cache-Control") != want {
			t.Fatalf("%s%s: %d, Cache-Control %q, want %q", host, target, r.code, r.h.Get("Cache-Control"), want)
		}
	}

	// Not screened yet: revalidated on every use, and only the file itself.
	cache(docsHost, asset, "public, no-cache")
	cache(docsHost, "/", "public, no-cache")
	encoding(docsHost, "/", both, "", page)
	// The exempt owner's site is not screened: as before.
	cache(trustedHost, asset, "public, max-age=31536000, immutable")
	encoding(trustedHost, "/", both, "br", "brotli-bytes")

	mod := moderation.New(moderation.Config{
		Store: p.db.Moderation(), Blobs: p.blobs, Moderator: moderationtest.New(),
		Policy: moderation.Policy{Quarantine: []string{"sexual"}}, Quarantine: p.quarantine,
		Invalidate: func(id string) { screened.Notify(ctx, id) },
		Now:        p.now, Jitter: func() float64 { return 0 }, Log: p.log,
	})
	mod.Pass(ctx)

	cache(docsHost, asset, "public, max-age=31536000, immutable")
	cache(docsHost, "/", "public, max-age=0, must-revalidate")
	// The checked gzip sibling is offered; brotli never is.
	encoding(docsHost, "/", both, "gzip", gz(page))
	encoding(docsHost, "/", http.Header{"Accept-Encoding": {"br"}}, "", page)
	// A gzip sibling holding other bytes than its file is never offered.
	r := p.do(http.MethodGet, docsHost, "/other.html", "", both)
	if r.code != http.StatusOK || r.h.Get("Content-Encoding") != "" || r.body != "<p>Other</p>" {
		t.Fatalf("other.html: %d %q %q", r.code, r.h.Get("Content-Encoding"), r.body)
	}
}

// TestServeLoadsQuarantineFirst checks a restarted server withholds a
// quarantined file from its very first request, with screening off: the
// list is loaded before the server listens.
func TestServeLoadsQuarantineFirst(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{"WARDLOCK_DATA_DIR": dir, "WARDLOCK_SECRET": testSecret}
	if err := run(t.Context(), []string{"admin", "create", "--email", "owner@example.test"}, func(k string) string { return env[k] }, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(t.Context(), filepath.Join(dir, "wardlock.db"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	o, err := db.Owners().GetByEmail(ctx, "owner@example.test")
	if err != nil {
		t.Fatal(err)
	}
	site := store.Site{ID: store.NewID(), OwnerID: o.ID, Name: "docs", CreatedAt: time.Now()}
	if err := db.Sites().Create(ctx, site, store.Quotas{}); err != nil {
		t.Fatal(err)
	}
	q := strings.Repeat("1", 64)
	d := store.Deploy{ID: store.NewID(), SiteID: site.ID, FileCount: 1, TotalSize: 5, CreatedAt: time.Now()}
	if err := db.Deploys().Create(ctx, o.ID, d, []store.DeployFile{{Path: "q.png", SHA256: q, Size: 5}}, store.Quotas{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Deploys().RecordUpload(ctx, o.ID, store.Blob{SHA256: q, Size: 5, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Deploys().Activate(ctx, o.ID, site.ID, d.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Moderation().EnqueueSite(ctx, site.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	v := store.ModerationVerdict{Status: store.ModerationQuarantined, Categories: []string{"sexual"}, Kind: store.ModerationImage}
	if err := db.Moderation().Record(ctx, q, v, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s := startServer(t, dir)
	if code, body := s.do(s.addr, http.MethodGet, "docs.wardlock.localhost", "/q.png", nil); code != http.StatusUnavailableForLegalReasons {
		t.Fatalf("first request: %d %q, want 451", code, body)
	}
}
