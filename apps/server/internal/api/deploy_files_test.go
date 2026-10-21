// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/api"
	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/deploys/s3test"
	"wardlock.nz/server/internal/owners"
	"wardlock.nz/server/internal/store"
)

// Reading a deploy's files back (C1.14).

// get sends a GET with header as owner.
func (hs *harness) get(owner, path string, header http.Header) response {
	hs.t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v0"+path, nil)
	for k, v := range header {
		req.Header[k] = v
	}
	if owner != "" {
		req.Header.Set(ownerHeader, owner)
	}
	rec := httptest.NewRecorder()
	hs.h.ServeHTTP(rec, req)
	return response{rec.Code, rec.Body.Bytes(), rec.Result().Header}
}

func blobPath(site, deployID, hash string) string {
	return "/sites/" + site + "/deploys/" + deployID + "/blobs/" + hash
}

// files lists every file of a deploy, limit at a time.
func (hs *harness) files(owner, site, deployID string, limit int) ([]api.DeployFile, int) {
	hs.t.Helper()
	var all []api.DeployFile
	pages := 0
	for cursor := ""; ; pages++ {
		path := fmt.Sprintf("/sites/%s/deploys/%s/files?limit=%d", site, deployID, limit)
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		res := hs.get(owner, path, nil)
		res.want(hs.t, http.StatusOK, "")
		var page api.DeployFilePage
		res.decode(hs.t, &page)
		all = append(all, page.Items...)
		if page.NextCursor == nil {
			return all, pages + 1
		}
		cursor = *page.NextCursor
	}
}

// downloadHeaders are on every body a download sends, whatever its status.
var downloadHeaders = map[string]string{
	"Content-Disposition":          "attachment",
	"X-Content-Type-Options":       "nosniff",
	"Content-Security-Policy":      "default-src 'none'; frame-ancestors 'none'; sandbox",
	"Cross-Origin-Resource-Policy": "same-origin",
	"Referrer-Policy":              "no-referrer",
	"X-Robots-Tag":                 "noindex",
	"Cache-Control":                "private, no-store",
}

func wantDownloadHeaders(t *testing.T, res response) {
	t.Helper()
	for k, v := range downloadHeaders {
		if got := res.header.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
}

func TestListDeployFiles(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	a := hs.ownerA
	hs.createSite(a, "docs")
	want := []file{
		{"B.txt", "upper"}, {"a/b.html", "nested"}, {"a0.css", "after a/"},
		{"copy.html", index.content}, {"index.html", index.content}, {"zz/ünï.txt", "unicode"},
	}
	act := hs.deploy(a, "docs", want...)

	for _, limit := range []int{1, 2, 4, 100} {
		got, pages := hs.files(a, "docs", act.Deploy.Id, limit)
		// The last full page knows there is no more, so no empty page follows.
		if wantPages := (len(want) + limit - 1) / limit; pages != wantPages {
			t.Errorf("limit %d: %d pages", limit, pages)
		}
		if len(got) != len(want) {
			t.Fatalf("limit %d: listed %+v", limit, got)
		}
		// Bytewise path order, each with its hash and size.
		for i, f := range want {
			if got[i] != (api.DeployFile{Path: f.path, Sha256: f.hash(), Size: int64(len(f.content))}) {
				t.Errorf("limit %d: item %d = %+v, want %s", limit, i, got[i], f.path)
			}
		}
	}

	// A path deploy lists the whole merged site.
	page := file{"index.html", "<p>mockup</p>"}
	c := hs.createDeploy(a, "docs", api.CreateDeployRequest{Path: new("/mockup/"), Files: manifest(page).Files})
	hs.upload(a, "docs", c.Deploy.Id, page).want(t, http.StatusNoContent, "")
	got, _ := hs.files(a, "docs", c.Deploy.Id, 100)
	if len(got) != len(want)+1 || got[len(got)-2].Path != "mockup/index.html" {
		t.Fatalf("path deploy lists %+v", got)
	}

	// Pending and earlier deploys list too.
	pending := hs.createDeploy(a, "docs", manifest(app))
	if got, _ := hs.files(a, "docs", pending.Deploy.Id, 10); len(got) != 1 || got[0].Path != app.path {
		t.Fatalf("pending deploy lists %+v", got)
	}

	base := "/sites/docs/deploys/" + act.Deploy.Id + "/files"
	for _, q := range []string{"?limit=0", "?limit=101", "?cursor=!!", "?cursor=" + strings.Repeat("A", 1400)} {
		hs.get(a, base+q, nil).want(t, http.StatusBadRequest, "invalid_request")
	}
	hs.get("", base, nil).want(t, http.StatusUnauthorized, "unauthenticated")
}

func TestDownloadDeployBlob(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	a := hs.ownerA
	hs.createSite(a, "docs")
	page := file{"index.html", "<script>alert(1)</script><h1>Hello</h1>"}
	empty := file{"empty.txt", ""}
	act := hs.deploy(a, "docs", page, empty, app)
	path := blobPath("docs", act.Deploy.Id, page.hash())

	res := hs.get(a, path, nil)
	res.want(t, http.StatusOK, "")
	if string(res.body) != page.content || res.header.Get("Content-Type") != "application/octet-stream" ||
		res.header.Get("Content-Length") != fmt.Sprint(len(page.content)) || res.header.Get("ETag") != `"`+page.hash()+`"` {
		t.Fatalf("download = %q, headers %v", res.body, res.header)
	}
	wantDownloadHeaders(t, res)

	res = hs.get(a, path, http.Header{"Range": {"bytes=8-12"}})
	res.want(t, http.StatusPartialContent, "")
	if string(res.body) != "alert" || res.header.Get("Content-Range") != fmt.Sprintf("bytes 8-12/%d", len(page.content)) {
		t.Fatalf("range = %q, headers %v", res.body, res.header)
	}
	wantDownloadHeaders(t, res)
	res = hs.get(a, path, http.Header{"Range": {"bytes=0-0,8-12"}})
	res.want(t, http.StatusPartialContent, "")
	if !strings.HasPrefix(res.header.Get("Content-Type"), "multipart/byteranges") || !strings.Contains(string(res.body), "alert") {
		t.Fatalf("multiple ranges = %q, headers %v", res.body, res.header)
	}
	res = hs.get(a, path, http.Header{"Range": {"bytes=-6"}})
	if res.status != http.StatusPartialContent || string(res.body) != "o</h1>" {
		t.Fatalf("suffix range = %d %q", res.status, res.body)
	}
	res = hs.get(a, path, http.Header{"Range": {"bytes=5000-"}})
	if res.status != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("range past the end = %d", res.status)
	}
	res = hs.get(a, path, http.Header{"If-None-Match": {`"` + page.hash() + `"`}})
	if res.status != http.StatusNotModified || len(res.body) != 0 {
		t.Fatalf("If-None-Match = %d %q", res.status, res.body)
	}

	res = hs.get(a, blobPath("docs", act.Deploy.Id, empty.hash()), nil)
	res.want(t, http.StatusOK, "")
	if len(res.body) != 0 {
		t.Fatalf("empty file = %q", res.body)
	}

	// HEAD answers the headers alone.
	req := httptest.NewRequest(http.MethodHead, "/api/v0"+path, nil)
	req.Header.Set(ownerHeader, a)
	rec := httptest.NewRecorder()
	hs.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 || rec.Header().Get("Content-Disposition") != "attachment" {
		t.Fatalf("HEAD = %d %q %v", rec.Code, rec.Body, rec.Header())
	}

	// An earlier deploy's files stay downloadable after another goes live.
	hs.deploy(a, "docs", file{"index.html", "<p>two</p>"})
	if res := hs.get(a, path, nil); res.status != http.StatusOK || string(res.body) != page.content {
		t.Fatalf("earlier deploy = %d %q", res.status, res.body)
	}

	for name, p := range map[string]string{
		"a hash the deploy does not list": blobPath("docs", act.Deploy.Id, file{"x", "unlisted"}.hash()),
		"not a hash":                      blobPath("docs", act.Deploy.Id, "abc"),
		"an upper-case hash":              blobPath("docs", act.Deploy.Id, strings.ToUpper(page.hash())),
		"an unknown deploy":               blobPath("docs", store.NewID(), page.hash()),
		"an unknown site":                 blobPath("nope", act.Deploy.Id, page.hash()),
	} {
		if res := hs.get(a, p, nil); res.status != http.StatusNotFound || res.err(t).Code != "not_found" {
			t.Errorf("%s = %d %s", name, res.status, res.body)
		}
	}
	hs.get("", path, nil).want(t, http.StatusUnauthorized, "unauthenticated")
}

// TestDeployFilesOwnerIsolation: another owner's site, deploy or a hash
// only another owner uploaded answers exactly like a missing one.
func TestDeployFilesOwnerIsolation(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	a, b := hs.ownerA, hs.ownerB
	hs.createSite(a, "alpha")
	secret := file{"secret.html", "<p>owner A only</p>"}
	actA := hs.deploy(a, "alpha", secret)
	hs.createSite(b, "beta")
	actB := hs.deploy(b, "beta", file{"index.html", "<p>owner B</p>"})

	missing := hs.get(a, blobPath("alpha", store.NewID(), secret.hash()), nil)
	missing.want(t, http.StatusNotFound, "not_found")
	missingList := hs.get(a, "/sites/alpha/deploys/"+store.NewID()+"/files", nil)
	missingList.want(t, http.StatusNotFound, "not_found")

	// B lists A's hash in a pending deploy of its own and never uploads it.
	pending := hs.createDeploy(b, "beta", manifest(file{"stolen.html", secret.content}))
	if len(pending.Missing) != 1 {
		t.Fatalf("B is told it holds A's content: missing %v", pending.Missing)
	}
	for name, res := range map[string]response{
		"A's site, as B":                    hs.get(b, blobPath("alpha", actA.Deploy.Id, secret.hash()), nil),
		"A's deploy under B's site":         hs.get(b, blobPath("beta", actA.Deploy.Id, secret.hash()), nil),
		"A's hash in B's pending deploy":    hs.get(b, blobPath("beta", pending.Deploy.Id, secret.hash()), nil),
		"A's hash in B's active deploy":     hs.get(b, blobPath("beta", actB.Deploy.Id, secret.hash()), nil),
		"B's deploy under A's site, as A":   hs.get(a, blobPath("alpha", actB.Deploy.Id, secret.hash()), nil),
		"A's files, as B":                   hs.get(b, "/sites/alpha/deploys/"+actA.Deploy.Id+"/files", nil),
		"A's deploy's files under B's site": hs.get(b, "/sites/beta/deploys/"+actA.Deploy.Id+"/files", nil),
	} {
		if res.status != missing.status || string(res.body) != string(missing.body) {
			t.Errorf("%s = %d %s, want the missing answer %d %s", name, res.status, res.body, missing.status, missing.body)
		}
	}
	// B's pending deploy lists the file, but B holds none of it.
	got, _ := hs.files(b, "beta", pending.Deploy.Id, 10)
	if len(got) != 1 || got[0].Sha256 != secret.hash() {
		t.Fatalf("B's own listing = %+v", got)
	}
	// Once B uploads the same bytes itself, it may read its own copy back.
	hs.upload(b, "beta", pending.Deploy.Id, file{"stolen.html", secret.content}).want(t, http.StatusNoContent, "")
	hs.get(b, blobPath("beta", pending.Deploy.Id, secret.hash()), nil).want(t, http.StatusOK, "")
}

func TestDeployFilesOfTakenDownSite(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	a := hs.ownerA
	hs.createSite(a, "docs")
	act := hs.deploy(a, "docs", index)
	if err := hs.db.Sites().TakeDown(t.Context(), act.Site.Id, time.Now()); err != nil {
		t.Fatal(err)
	}
	// The owner too: the content is kept for the operator, not handed out.
	hs.get(a, "/sites/docs/deploys/"+act.Deploy.Id+"/files", nil).want(t, http.StatusConflict, "site_unavailable")
	hs.get(a, blobPath("docs", act.Deploy.Id, index.hash()), nil).want(t, http.StatusConflict, "site_unavailable")
	hs.get(hs.ownerB, blobPath("docs", act.Deploy.Id, index.hash()), nil).want(t, http.StatusNotFound, "not_found")

	if err := hs.db.Sites().Restore(t.Context(), act.Site.Id); err != nil {
		t.Fatal(err)
	}
	hs.get(a, blobPath("docs", act.Deploy.Id, index.hash()), nil).want(t, http.StatusOK, "")
}

// quarantine is a fixed set of withheld hashes.
type quarantine map[string]bool

func (q quarantine) Has(h string) bool { return q[h] }

func TestDownloadWithheldFile(t *testing.T) {
	bad := file{"bad.png", "withheld bytes"}
	hs := newHarnessWith(t, deploys.DefaultLimits(), store.Quotas{}, func(c *deploys.Config) {
		c.Withheld = quarantine{bad.hash(): true}
	})
	a := hs.ownerA
	hs.createSite(a, "docs")
	act := hs.deploy(a, "docs", index, bad)
	// Listed, but not downloadable; the rest of the deploy is.
	if got, _ := hs.files(a, "docs", act.Deploy.Id, 10); len(got) != 2 {
		t.Fatalf("listing = %+v", got)
	}
	hs.get(a, blobPath("docs", act.Deploy.Id, bad.hash()), nil).want(t, http.StatusUnavailableForLegalReasons, "file_withheld")
	hs.get(a, blobPath("docs", act.Deploy.Id, bad.hash()), http.Header{"Range": {"bytes=0-1"}}).want(t, http.StatusUnavailableForLegalReasons, "file_withheld")
	hs.get(a, blobPath("docs", act.Deploy.Id, index.hash()), nil).want(t, http.StatusOK, "")
	// Another owner still learns nothing.
	hs.get(hs.ownerB, blobPath("docs", act.Deploy.Id, bad.hash()), nil).want(t, http.StatusNotFound, "not_found")
}

// TestDownloadAfterGC: a body garbage collection removed answers 404, both
// with its deploy pruned and with the deploy still listing it.
func TestDownloadAfterGC(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	a := hs.ownerA
	hs.createSite(a, "docs")
	old := file{"index.html", "<p>old</p>"}
	first := hs.deploy(a, "docs", old)
	hs.deploy(a, "docs", file{"index.html", "<p>new</p>"})
	path := blobPath("docs", first.Deploy.Id, old.hash())
	hs.get(a, path, nil).want(t, http.StatusOK, "")

	gc := deploys.NewGC(deploys.GCConfig{
		Repo: hs.db.GC(), Blobs: hs.blobs, History: 1,
		Log: slog.New(slog.DiscardHandler),
		Now: func() time.Time { return time.Now().Add(48 * time.Hour) },
	})
	res, err := gc.Run(t.Context())
	if err != nil || res.DeploysPruned != 1 || res.BlobsDeleted == 0 {
		t.Fatalf("gc = %+v, %v", res, err)
	}
	hs.get(a, path, nil).want(t, http.StatusNotFound, "not_found")
	hs.get(a, "/sites/docs/deploys/"+first.Deploy.Id+"/files", nil).want(t, http.StatusNotFound, "not_found")

	// A body gone from the store while its deploy still lists it.
	act := hs.deploy(a, "docs", app)
	if err := hs.blobs.Delete(t.Context(), app.hash()); err != nil {
		t.Fatal(err)
	}
	hs.get(a, blobPath("docs", act.Deploy.Id, app.hash()), nil).want(t, http.StatusNotFound, "not_found")
}

// TestDownloadFromS3: the same answers with bodies in a bucket, ranges
// passed on as ranged reads.
func TestDownloadFromS3(t *testing.T) {
	bucket := s3test.New(t, "wardlock-test", "AKIATEST")
	bs := bucket.Store(t, "")
	hs := newHarnessWith(t, deploys.DefaultLimits(), store.Quotas{}, func(c *deploys.Config) { c.Blobs = bs })
	a := hs.ownerA
	hs.createSite(a, "docs")
	page := file{"index.html", "<h1>From a bucket</h1>"}
	act := hs.deploy(a, "docs", page)
	path := blobPath("docs", act.Deploy.Id, page.hash())

	res := hs.get(a, path, nil)
	res.want(t, http.StatusOK, "")
	if string(res.body) != page.content {
		t.Fatalf("download = %q", res.body)
	}
	wantDownloadHeaders(t, res)
	res = hs.get(a, path, http.Header{"Range": {"bytes=4-7"}})
	if res.status != http.StatusPartialContent || string(res.body) != "From" {
		t.Fatalf("range = %d %q", res.status, res.body)
	}
	// A missing object surfaces before any byte is sent.
	if err := bs.Delete(t.Context(), page.hash()); err != nil {
		t.Fatal(err)
	}
	hs.get(a, path, nil).want(t, http.StatusNotFound, "not_found")
}

// TestDownloadCredentials: listing and downloading accept the dashboard
// session or a token with either scope, and refuse expired, revoked and
// guessed tokens, a guessing client after the limit, and a session cookie
// beside a bad token. The harness fails the test if a raw token reaches
// the log.
func TestDownloadCredentials(t *testing.T) {
	limits := owners.DefaultTokenLimits()
	limits.FailuresPerIP = owners.Rate{N: 3, Per: time.Minute}
	h := newTokenHarness(t, limits)
	session := h.owner("owner@example.test")
	page := file{"index.html", "<h1>Pulled</h1>"}

	raw := func(method, path, cookie string, header map[string]string, body []byte) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		r.Host = dashHost
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: api.SessionCookie, Value: cookie})
		}
		for k, v := range header {
			r.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.h.ServeHTTP(rec, r)
		return rec
	}
	deployTok := createToken(t, h, session, "CI", api.ScopeDeploy).Token
	wantStatus(t, h.bearer(deployTok, req{method: http.MethodPost, path: "/api/v0/sites", body: api.CreateSiteRequest{Name: "docs"}}), http.StatusCreated)
	rec := h.bearer(deployTok, req{method: http.MethodPost, path: "/api/v0/sites/docs/deploys", body: manifest(page)})
	wantStatus(t, rec, http.StatusCreated)
	var created api.CreatedDeploy
	decodeInto(t, rec, &created)
	id := created.Deploy.Id
	wantStatus(t, raw(http.MethodPut, "/api/v0"+blobPath("docs", id, page.hash()), "",
		map[string]string{"Authorization": "Bearer " + deployTok, "Content-Type": "application/octet-stream"}, []byte(page.content)), http.StatusNoContent)
	wantStatus(t, h.bearer(deployTok, req{method: http.MethodPost, path: "/api/v0/sites/docs/deploys/" + id + "/activate"}), http.StatusOK)

	files := "/api/v0/sites/docs/deploys/" + id + "/files"
	blob := "/api/v0" + blobPath("docs", id, page.hash())
	manageTok := createToken(t, h, session, "Admin", api.ScopeManage).Token
	exp := h.clock.Now().Add(time.Hour).UnixMilli()
	rec = h.do(req{method: http.MethodPost, path: "/api/v0/tokens", cookie: session,
		body: api.CreateApiTokenRequest{Name: "short", Scopes: api.Scopes{api.ScopeDeploy}, ExpiresAt: &exp}})
	wantStatus(t, rec, http.StatusCreated)
	var short api.CreatedApiToken
	decodeInto(t, rec, &short)
	h.secret(short.Token)
	revoked := createToken(t, h, session, "Old", api.ScopeDeploy)

	for name, header := range map[string]map[string]string{
		"deploy token": {"Authorization": "Bearer " + deployTok},
		"manage token": {"Authorization": "Bearer " + manageTok},
		"short token":  {"Authorization": "Bearer " + short.Token},
	} {
		wantStatus(t, raw(http.MethodGet, files, "", header, nil), http.StatusOK)
		rec := raw(http.MethodGet, blob, "", header, nil)
		wantStatus(t, rec, http.StatusOK)
		if rec.Body.String() != page.content || rec.Header().Get("Content-Disposition") != "attachment" {
			t.Fatalf("%s: download %q %v", name, rec.Body, rec.Header())
		}
	}
	// The dashboard session, as a same-origin fetch or a plain navigation.
	wantStatus(t, raw(http.MethodGet, blob, session, map[string]string{"Sec-Fetch-Site": "same-origin"}, nil), http.StatusOK)
	wantStatus(t, raw(http.MethodGet, blob, session, nil, nil), http.StatusOK)

	wantStatus(t, h.do(req{method: http.MethodDelete, path: "/api/v0/tokens/" + revoked.ApiToken.Id, cookie: session}), http.StatusNoContent)
	h.clock.advance(time.Hour)
	for name, header := range map[string]map[string]string{
		"expired token":  {"Authorization": "Bearer " + short.Token},
		"revoked token":  {"Authorization": "Bearer " + revoked.Token},
		"no credentials": nil,
	} {
		for _, path := range []string{files, blob} {
			rec := raw(http.MethodGet, path, "", header, nil)
			wantError(t, rec, http.StatusUnauthorized, "unauthenticated")
			if strings.Contains(rec.Body.String(), page.content) {
				t.Fatalf("%s: body leaked", name)
			}
		}
	}
	// A session cookie beside a bad token does not rescue it.
	guess := deployTok[:len(deployTok)-43] + strings.Repeat("A", 43)
	wantError(t, raw(http.MethodGet, blob, session, map[string]string{"Authorization": "Bearer " + guess}, nil), http.StatusUnauthorized, "unauthenticated")
	for range 2 {
		wantError(t, raw(http.MethodGet, files, "", map[string]string{"Authorization": "Bearer " + guess}, nil), http.StatusUnauthorized, "unauthenticated")
	}
	// Guessing is limited: the client is refused, even with a good token,
	// until the window passes.
	rec = raw(http.MethodGet, blob, "", map[string]string{"Authorization": "Bearer " + deployTok}, nil)
	wantError(t, rec, http.StatusTooManyRequests, "rate_limited")
	if rec.Header().Get("Retry-After") == "" || strings.Contains(rec.Body.String(), page.content) {
		t.Fatalf("limited download %v %q", rec.Header(), rec.Body)
	}
	h.clock.advance(time.Minute)
	wantStatus(t, raw(http.MethodGet, blob, "", map[string]string{"Authorization": "Bearer " + deployTok}, nil), http.StatusOK)
}
