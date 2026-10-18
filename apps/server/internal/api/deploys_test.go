// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"wardlock.nz/server/internal/access"
	"wardlock.nz/server/internal/api"
	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/sites"
	"wardlock.nz/server/internal/store"
	"wardlock.nz/server/internal/store/storetest"
)

const baseDomain = "wardlock.example.test"

// harness is the API over a real database and blob store with two owners.
// Requests carry the owner in a test header that the wrapper below turns
// into the verified owner context, standing in for C1.1's middleware.
type harness struct {
	t        *testing.T
	h        http.Handler
	db       *store.DB
	blobDir  string
	blobs    *deploys.FSBlobStore
	ownerA   string
	ownerB   string
	mu       sync.Mutex
	notified []string
}

const ownerHeader = "X-Test-Owner"

func newHarness(t *testing.T, limits deploys.Limits) *harness {
	t.Helper()
	db := storetest.New(t)
	hs := &harness{t: t, db: db, blobDir: filepath.Join(t.TempDir(), "blobs")}
	hs.blobs = deploys.NewFSBlobStore(hs.blobDir)
	for _, id := range []*string{&hs.ownerA, &hs.ownerB} {
		*id = store.NewID()
		o := store.Owner{ID: *id, Email: *id + "@example.test", CreatedAt: time.Now()}
		if err := db.Owners().Create(t.Context(), o); err != nil {
			t.Fatal(err)
		}
	}
	notifier := deploys.NewLocalNotifier()
	notifier.Subscribe(func(siteID string) {
		hs.mu.Lock()
		defer hs.mu.Unlock()
		hs.notified = append(hs.notified, siteID)
	})
	policies, err := access.New(access.Config{
		Sites: db.Sites(), Policies: db.Policies(), Sessions: db.Sessions(), Links: db.Links(), Notifier: notifier,
		Secret: []byte("api-test-server-key-0123456789abcdef-01"), Now: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(policies.Close)
	srv := &api.Server{
		Access: policies,
		Sites: sites.NewService(sites.Config{
			Sites: db.Sites(), Notifier: notifier, BaseDomain: baseDomain,
			DashboardHost: "console." + baseDomain, Now: time.Now,
		}),
		Deploys: deploys.NewService(deploys.Config{
			Sites: db.Sites(), Deploys: db.Deploys(), Blobs: hs.blobs, Notifier: notifier,
			Limits: limits, Now: time.Now,
		}),
		SiteURL: func(host string) string { return "http://" + host + ":8080/" },
	}
	inner := srv.Handler()
	hs.h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := r.Header.Get(ownerHeader); id != "" {
			r = r.WithContext(api.WithOwner(r.Context(), id))
		}
		inner.ServeHTTP(w, r)
	})
	return hs
}

type response struct {
	status int
	body   []byte
}

func (r response) decode(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.body, v); err != nil {
		t.Fatalf("decode %s: %v", r.body, err)
	}
}

func (r response) err(t *testing.T) api.Error {
	t.Helper()
	var e api.Error
	r.decode(t, &e)
	return e
}

func (hs *harness) do(owner, method, path string, body any) response {
	hs.t.Helper()
	var r io.Reader
	contentType := "application/json"
	switch b := body.(type) {
	case nil:
	case []byte:
		r, contentType = bytes.NewReader(b), "application/octet-stream"
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			hs.t.Fatal(err)
		}
		r = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, "/api/v0"+path, r)
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}
	if owner != "" {
		req.Header.Set(ownerHeader, owner)
	}
	rec := httptest.NewRecorder()
	hs.h.ServeHTTP(rec, req)
	return response{rec.Code, rec.Body.Bytes()}
}

// want fails unless r has status and, for errors, code.
func (r response) want(t *testing.T, status int, code string) {
	t.Helper()
	if r.status != status {
		t.Fatalf("status %d, want %d; body %s", r.status, status, r.body)
	}
	if code != "" {
		if got := r.err(t).Code; got != code {
			t.Fatalf("code %q, want %q; body %s", got, code, r.body)
		}
	}
}

func (hs *harness) createSite(owner, name string) api.Site {
	hs.t.Helper()
	res := hs.do(owner, http.MethodPost, "/sites", api.CreateSiteRequest{Name: name})
	res.want(hs.t, http.StatusCreated, "")
	var s api.Site
	res.decode(hs.t, &s)
	return s
}

// file is one deploy file with its content.
type file struct {
	path, content string
}

func (f file) hash() string {
	sum := sha256.Sum256([]byte(f.content))
	return hex.EncodeToString(sum[:])
}

func manifest(files ...file) api.CreateDeployRequest {
	req := api.CreateDeployRequest{}
	for _, f := range files {
		req.Files = append(req.Files, api.ManifestFile{Path: f.path, Sha256: f.hash(), Size: int64(len(f.content))})
	}
	return req
}

func (hs *harness) createDeploy(owner, site string, req api.CreateDeployRequest) api.CreatedDeploy {
	hs.t.Helper()
	res := hs.do(owner, http.MethodPost, "/sites/"+site+"/deploys", req)
	res.want(hs.t, http.StatusCreated, "")
	var c api.CreatedDeploy
	res.decode(hs.t, &c)
	return c
}

func (hs *harness) upload(owner, site, deployID string, f file) response {
	hs.t.Helper()
	return hs.do(owner, http.MethodPut, "/sites/"+site+"/deploys/"+deployID+"/blobs/"+f.hash(), []byte(f.content))
}

// deploy runs the whole protocol and returns the activated deploy.
func (hs *harness) deploy(owner, site string, files ...file) api.Activation {
	hs.t.Helper()
	c := hs.createDeploy(owner, site, manifest(files...))
	for _, f := range files {
		if slices.Contains(c.Missing, f.hash()) {
			hs.upload(owner, site, c.Deploy.Id, f).want(hs.t, http.StatusNoContent, "")
		}
	}
	return hs.activate(owner, site, c.Deploy.Id)
}

func (hs *harness) activate(owner, site, deployID string) api.Activation {
	hs.t.Helper()
	res := hs.do(owner, http.MethodPost, "/sites/"+site+"/deploys/"+deployID+"/activate", nil)
	res.want(hs.t, http.StatusOK, "")
	var a api.Activation
	res.decode(hs.t, &a)
	return a
}

func (hs *harness) notifications() []string {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	return slices.Clone(hs.notified)
}

var (
	index = file{"index.html", "<h1>Hello</h1>"}
	app   = file{"assets/app.js", "console.log(1);"}
)

func TestDeployProtocol(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	a := hs.ownerA
	site := hs.createSite(a, "docs")
	if site.Hostname != "docs."+baseDomain || site.Url != "http://docs."+baseDomain+":8080/" || site.ActiveDeployId != nil {
		t.Fatalf("created site = %+v", site)
	}

	c := hs.createDeploy(a, "docs", manifest(index, app, file{"copy.html", index.content}))
	// Counts and sizes are per path, so the duplicated content counts twice.
	wantTotal := int64(2*len(index.content) + len(app.content))
	if c.Deploy.Status != "pending" || c.Deploy.FileCount != 3 || c.Deploy.TotalSize != wantTotal || c.Deploy.ActivatedAt != nil {
		t.Fatalf("created deploy = %+v", c.Deploy)
	}
	want := []string{index.hash(), app.hash()}
	slices.Sort(want)
	if !slices.Equal(c.Missing, want) {
		t.Fatalf("missing = %v, want each distinct hash once: %v", c.Missing, want)
	}
	hs.upload(a, "docs", c.Deploy.Id, index).want(t, http.StatusNoContent, "")
	hs.upload(a, "docs", c.Deploy.Id, index).want(t, http.StatusNoContent, "") // retries are safe
	hs.upload(a, "docs", c.Deploy.Id, app).want(t, http.StatusNoContent, "")

	act := hs.activate(a, "docs", c.Deploy.Id)
	if act.Deploy.Status != "active" || act.Deploy.ActivatedAt == nil {
		t.Fatalf("activated deploy = %+v", act.Deploy)
	}
	if act.Site.ActiveDeployId == nil || *act.Site.ActiveDeployId != c.Deploy.Id || *act.Site.ActivatedAt != *act.Deploy.ActivatedAt {
		t.Fatalf("activated site = %+v", act.Site)
	}
	if got := hs.notifications(); !slices.Equal(got, []string{site.Id}) {
		t.Fatalf("notified %v, want the site once", got)
	}
	rc, err := hs.blobs.Open(t.Context(), app.hash())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(body) != app.content {
		t.Fatalf("stored blob = %q", body)
	}

	var got api.Site
	res := hs.do(a, http.MethodGet, "/sites/docs", nil)
	res.want(t, http.StatusOK, "")
	res.decode(t, &got)
	if !reflect.DeepEqual(got, act.Site) {
		t.Fatalf("GET site = %+v, want %+v", got, act.Site)
	}
}

func TestUnchangedRedeployUploadsNothing(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	a := hs.ownerA
	hs.createSite(a, "docs")
	hs.deploy(a, "docs", index, app)

	c := hs.createDeploy(a, "docs", manifest(index, app))
	if len(c.Missing) != 0 {
		t.Fatalf("missing = %v, want none", c.Missing)
	}
	act := hs.activate(a, "docs", c.Deploy.Id)
	if *act.Site.ActiveDeployId != c.Deploy.Id {
		t.Fatalf("active = %s, want %s", *act.Site.ActiveDeployId, c.Deploy.Id)
	}
	// A partly changed deploy asks only for the new file.
	changed := file{"index.html", "<h1>Changed</h1>"}
	c = hs.createDeploy(a, "docs", manifest(changed, app))
	if !slices.Equal(c.Missing, []string{changed.hash()}) {
		t.Fatalf("missing = %v, want only the changed file", c.Missing)
	}
	// The JSON is a list even when empty.
	res := hs.do(a, http.MethodPost, "/sites/docs/deploys", manifest(app))
	if !bytes.Contains(res.body, []byte(`"missing":[]`)) {
		t.Fatalf("body %s, want an empty missing list", res.body)
	}
}

func TestUploadRejections(t *testing.T) {
	limits := deploys.DefaultLimits()
	limits.MaxFileSize = 64
	hs := newHarness(t, limits)
	a := hs.ownerA
	hs.createSite(a, "docs")
	big := file{"big.bin", strings.Repeat("x", 64)}
	c := hs.createDeploy(a, "docs", manifest(index, big))
	put := func(hash string, body []byte) response {
		return hs.do(a, http.MethodPut, "/sites/docs/deploys/"+c.Deploy.Id+"/blobs/"+hash, body)
	}

	tests := []struct {
		name   string
		hash   string
		body   string
		status int
		code   string
	}{
		{"different content", index.hash(), "<h1>Hellx</h1>", http.StatusBadRequest, "hash_mismatch"},
		{"short body", index.hash(), index.content[:3], http.StatusBadRequest, "hash_mismatch"},
		{"long body", index.hash(), index.content + "!", http.StatusBadRequest, "hash_mismatch"},
		{"empty body", index.hash(), "", http.StatusBadRequest, "hash_mismatch"},
		{"over the file limit", big.hash(), big.content + "y", http.StatusRequestEntityTooLarge, "limit_exceeded"},
		{"far over the file limit", index.hash(), strings.Repeat("y", 1000), http.StatusRequestEntityTooLarge, "limit_exceeded"},
		{"hash not in manifest", app.hash(), app.content, http.StatusBadRequest, "unexpected_blob"},
		{"malformed hash", "ABC", "x", http.StatusBadRequest, "unexpected_blob"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			put(tt.hash, []byte(tt.body)).want(t, tt.status, tt.code)
		})
	}
	// Nothing was stored, so both are still missing and activation fails.
	for _, f := range []file{index, big} {
		if ok, err := hs.blobs.Has(t.Context(), f.hash()); err != nil || ok {
			t.Fatalf("Has(%s) = %v, %v after rejected uploads", f.path, ok, err)
		}
	}
	_ = filepath.WalkDir(hs.blobDir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			t.Errorf("rejected uploads left %s", p)
		}
		return nil
	})
	res := hs.do(a, http.MethodPost, "/sites/docs/deploys/"+c.Deploy.Id+"/activate", nil)
	res.want(t, http.StatusConflict, "blobs_missing")

	put(big.hash(), []byte(big.content)).want(t, http.StatusNoContent, "") // exactly at the limit
	hs.do(a, http.MethodPost, "/sites/docs/deploys/"+c.Deploy.Id+"/activate", nil).want(t, http.StatusConflict, "blobs_missing")
	put(index.hash(), []byte(index.content)).want(t, http.StatusNoContent, "")
	hs.activate(a, "docs", c.Deploy.Id)
	hs.do(a, http.MethodPost, "/sites/docs/deploys/"+c.Deploy.Id+"/activate", nil).want(t, http.StatusConflict, "deploy_not_pending")

	// Unknown deploy and deploy of another site: 404.
	hs.createSite(a, "blog")
	hs.do(a, http.MethodPut, "/sites/docs/deploys/"+store.NewID()+"/blobs/"+index.hash(), []byte(index.content)).want(t, http.StatusNotFound, "not_found")
	hs.do(a, http.MethodPut, "/sites/blog/deploys/"+c.Deploy.Id+"/blobs/"+index.hash(), []byte(index.content)).want(t, http.StatusNotFound, "not_found")
	hs.do(a, http.MethodPost, "/sites/blog/deploys/"+c.Deploy.Id+"/activate", nil).want(t, http.StatusNotFound, "not_found")
}

func TestConcurrentActivations(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	a := hs.ownerA
	hs.createSite(a, "docs")
	const n = 6
	var ids []string
	for i := range n {
		f := file{"index.html", fmt.Sprintf("version %d", i)}
		c := hs.createDeploy(a, "docs", manifest(f))
		hs.upload(a, "docs", c.Deploy.Id, f).want(t, http.StatusNoContent, "")
		ids = append(ids, c.Deploy.Id)
	}
	statuses := make([]int, 2*n)
	var wg sync.WaitGroup
	for i := range 2 * n {
		wg.Go(func() {
			statuses[i] = hs.do(a, http.MethodPost, "/sites/docs/deploys/"+ids[i%n]+"/activate", nil).status
		})
	}
	wg.Wait()
	for i := range n {
		pair := []int{statuses[i], statuses[i+n]}
		slices.Sort(pair)
		if !slices.Equal(pair, []int{http.StatusOK, http.StatusConflict}) {
			t.Fatalf("deploy %d activated twice at once answered %v, want one 200 and one 409", i, pair)
		}
	}
	var site api.Site
	res := hs.do(a, http.MethodGet, "/sites/docs", nil)
	res.decode(t, &site)
	if site.ActiveDeployId == nil || !slices.Contains(ids, *site.ActiveDeployId) {
		t.Fatalf("active deploy = %v, want one of %v", site.ActiveDeployId, ids)
	}
	var page api.DeployPage
	hs.do(a, http.MethodGet, "/sites/docs/deploys", nil).decode(t, &page)
	active := 0
	for _, d := range page.Items {
		if d.Status == "active" {
			active++
		}
		if d.Status == "pending" {
			t.Errorf("deploy %s still pending", d.Id)
		}
	}
	if active != 1 {
		t.Fatalf("%d active deploys, want 1", active)
	}
}

func TestRollback(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	a := hs.ownerA
	site := hs.createSite(a, "docs")
	rollback := func(body any) response {
		return hs.do(a, http.MethodPost, "/sites/docs/rollback", body)
	}
	rollback(nil).want(t, http.StatusConflict, "no_previous_deploy")
	var acts []api.Activation
	for i := range 4 {
		acts = append(acts, hs.deploy(a, "docs", file{"index.html", fmt.Sprintf("v%d", i)}))
	}
	pending := hs.createDeploy(a, "docs", manifest(file{"index.html", "pending"}))
	before := len(hs.notifications())

	activated := func(res response) api.Activation {
		t.Helper()
		res.want(t, http.StatusOK, "")
		var act api.Activation
		res.decode(t, &act)
		if act.Deploy.Status != "active" || *act.Site.ActiveDeployId != act.Deploy.Id {
			t.Fatalf("rollback answered %+v", act)
		}
		return act
	}
	// Without a body: the deploy active before the current one.
	if got := activated(rollback(nil)); got.Deploy.Id != acts[2].Deploy.Id {
		t.Fatalf("rollback went to %s, want %s", got.Deploy.Id, acts[2].Deploy.Id)
	}
	// An empty body behaves the same: back to v3, which was active before v2.
	if got := activated(rollback(api.RollbackRequest{})); got.Deploy.Id != acts[3].Deploy.Id {
		t.Fatalf("rollback went to %s, want %s", got.Deploy.Id, acts[3].Deploy.Id)
	}
	// Any earlier deploy by ID, with no upload.
	for _, i := range []int{0, 1, 3, 2, 0} {
		id := acts[i].Deploy.Id
		if got := activated(rollback(api.RollbackRequest{DeployId: &id})); got.Deploy.Id != id {
			t.Fatalf("rollback to %d went to %s", i, got.Deploy.Id)
		}
	}
	if got := len(hs.notifications()) - before; got != 7 {
		t.Fatalf("%d notifications for 7 rollbacks", got)
	}
	// Back to the active deploy: no change, no notification.
	id := acts[0].Deploy.Id
	activated(rollback(api.RollbackRequest{DeployId: &id}))
	if got := len(hs.notifications()) - before; got != 7 {
		t.Fatalf("rollback to the active deploy notified")
	}
	rollback(api.RollbackRequest{DeployId: &pending.Deploy.Id}).want(t, http.StatusConflict, "deploy_not_activated")
	unknown := store.NewID()
	rollback(api.RollbackRequest{DeployId: &unknown}).want(t, http.StatusNotFound, "not_found")
	if got := hs.notifications(); got[len(got)-1] != site.Id {
		t.Fatalf("notified %v, want the site", got)
	}
}

func TestTeardown(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	a, b := hs.ownerA, hs.ownerB
	site := hs.createSite(a, "docs")
	act := hs.deploy(a, "docs", index, app)
	before := len(hs.notifications())

	hs.do(a, http.MethodDelete, "/sites/docs", nil).want(t, http.StatusNoContent, "")
	if got := hs.notifications()[before:]; !slices.Equal(got, []string{site.Id}) {
		t.Fatalf("teardown notified %v, want the site", got)
	}
	hs.do(a, http.MethodGet, "/sites/docs", nil).want(t, http.StatusNotFound, "not_found")
	hs.do(a, http.MethodGet, "/sites/docs/deploys", nil).want(t, http.StatusNotFound, "not_found")
	hs.do(a, http.MethodDelete, "/sites/docs", nil).want(t, http.StatusNotFound, "not_found")
	if files, err := hs.db.Deploys().Files(t.Context(), *act.Site.ActiveDeployId); err != nil || len(files) != 0 {
		t.Fatalf("manifest after teardown = %v, %v", files, err)
	}
	// The name is free again, for anyone.
	hs.createSite(b, "docs")
}

func TestLimits(t *testing.T) {
	limits := deploys.Limits{MaxFileSize: 10, MaxFiles: 3, MaxDeploySize: 25}
	hs := newHarness(t, limits)
	a := hs.ownerA
	hs.createSite(a, "docs")
	f := func(path string, size int) file { return file{path, strings.Repeat("x", size)} }
	tests := []struct {
		name   string
		files  []file
		target string
	}{
		{"too many files", []file{f("a", 1), f("b", 1), f("c", 1), f("d", 1)}, "files"},
		{"file over the size limit", []file{f("a", 1), f("big", 11)}, "big"},
		{"deploy over the total limit", []file{f("a", 10), f("b", 10), f("c", 6)}, "files"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := hs.do(a, http.MethodPost, "/sites/docs/deploys", manifest(tt.files...))
			res.want(t, http.StatusRequestEntityTooLarge, "limit_exceeded")
			if e := res.err(t); e.Target == nil || *e.Target != tt.target || e.Message == "" {
				t.Fatalf("error = %+v, want target %q", e, tt.target)
			}
		})
	}
	// Exactly at every limit is fine.
	hs.deploy(a, "docs", f("a", 10), f("b", 10), f("c", 5))
}

func TestRejectedPaths(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	a := hs.ownerA
	hs.createSite(a, "docs")
	for _, p := range []string{
		"", "/index.html", "index.html/", "a//b", "../secret", "a/../../b", "a/..", "./a", "a/./b", ".",
		`a\..\b`, `\index.html`, "a\x00b", "a\x1fb", "a\tb", "a\nb", "a\x7fb", "a\u0085b",
		".wardlock/health", ".wardlock", ".Wardlock/login", `.wardlock\health`,
		"a\u200bb", "evil\u202Elmth.js", "\ufeffindex.html",
		strings.Repeat("a", 1025),
	} {
		t.Run(fmt.Sprintf("%q", p), func(t *testing.T) {
			res := hs.do(a, http.MethodPost, "/sites/docs/deploys", manifest(file{p, "x"}, index))
			res.want(t, http.StatusBadRequest, "invalid_path")
			if e := res.err(t); p != "" && (e.Target == nil || *e.Target != p) {
				t.Fatalf("target = %v, want %q", e.Target, p)
			}
		})
	}
	for name, req := range map[string]api.CreateDeployRequest{
		"no files":                    {Files: []api.ManifestFile{}},
		"duplicate path":              manifest(index, index),
		"duplicate after normalising": manifest(file{`assets\app.js`, "1"}, file{"assets/app.js", "2"}),
		"uppercase hash":              {Files: []api.ManifestFile{{Path: "a", Sha256: strings.ToUpper(index.hash()), Size: 1}}},
		"short hash":                  {Files: []api.ManifestFile{{Path: "a", Sha256: "abc", Size: 1}}},
		"negative size":               {Files: []api.ManifestFile{{Path: "a", Sha256: index.hash(), Size: -1}}},
		"long message":                {Files: manifest(index).Files, Message: new(strings.Repeat("m", 501))},
	} {
		t.Run(name, func(t *testing.T) {
			hs.do(a, http.MethodPost, "/sites/docs/deploys", req).want(t, http.StatusBadRequest, "invalid_manifest")
		})
	}
	// One hash with two sizes is rejected, naming the later path.
	conflict := api.CreateDeployRequest{Files: []api.ManifestFile{
		{Path: "a.html", Sha256: index.hash(), Size: 1},
		{Path: "b.html", Sha256: index.hash(), Size: 2},
	}}
	res := hs.do(a, http.MethodPost, "/sites/docs/deploys", conflict)
	res.want(t, http.StatusBadRequest, "invalid_manifest")
	if e := res.err(t); e.Target == nil || *e.Target != "b.html" {
		t.Fatalf("conflicting sizes: target = %v, want b.html", e.Target)
	}
	// Accepted forms, with backslashes normalised.
	c := hs.createDeploy(a, "docs", manifest(
		file{`docs\install.html`, "1"}, file{".well-known/security.txt", "2"}, file{"wardlock/x", "3"},
		file{"x/.wardlock/y", "4"}, file{"..foo", "5"}, file{"café/ünï.html", "6"},
	))
	got, err := hs.db.Deploys().Files(t.Context(), c.Deploy.Id)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range got {
		paths = append(paths, f.Path)
	}
	want := []string{"..foo", ".well-known/security.txt", "café/ünï.html", "docs/install.html", "wardlock/x", "x/.wardlock/y"}
	if !slices.Equal(paths, want) {
		t.Fatalf("stored paths %q, want %q", paths, want)
	}
}

func TestSiteNames(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	a, b := hs.ownerA, hs.ownerB
	for _, name := range []string{"auth", "www", "api", "admin", "app", "dashboard", "mail", "wardlock", "xn--bcher-kva", "xn--1", "console"} {
		hs.do(a, http.MethodPost, "/sites", api.CreateSiteRequest{Name: name}).want(t, http.StatusBadRequest, "reserved_site_name")
	}
	for _, name := range []string{"", "xn--", "-docs", "docs-", "Docs", "do_cs", "do.cs", "dócs", strings.Repeat("a", 64)} {
		hs.do(a, http.MethodPost, "/sites", api.CreateSiteRequest{Name: name}).want(t, http.StatusBadRequest, "invalid_site_name")
	}
	for _, name := range []string{"a", "0", "my-docs", "xn", "consoles", strings.Repeat("a", 63)} {
		hs.createSite(a, name)
	}
	// Taken, whoever holds it.
	hs.do(a, http.MethodPost, "/sites", api.CreateSiteRequest{Name: "my-docs"}).want(t, http.StatusConflict, "site_name_taken")
	hs.do(b, http.MethodPost, "/sites", api.CreateSiteRequest{Name: "my-docs"}).want(t, http.StatusConflict, "site_name_taken")
	hs.do(a, http.MethodPost, "/sites", nil).want(t, http.StatusBadRequest, "")
}

func TestListSites(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	a, b := hs.ownerA, hs.ownerB
	for _, name := range []string{"delta", "alpha", "charlie", "bravo", "echo"} {
		hs.createSite(a, name)
	}
	hs.createSite(b, "foxtrot")
	var names []string
	cursor := ""
	for pages := 0; ; pages++ {
		path := "/sites?limit=2"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		var page api.SitePage
		res := hs.do(a, http.MethodGet, path, nil)
		res.want(t, http.StatusOK, "")
		res.decode(t, &page)
		for _, s := range page.Items {
			names = append(names, s.Name)
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
		if pages > 5 {
			t.Fatal("too many pages")
		}
	}
	if want := []string{"alpha", "bravo", "charlie", "delta", "echo"}; !slices.Equal(names, want) {
		t.Fatalf("listed %v, want %v", names, want)
	}
	for _, q := range []string{"limit=0", "limit=101", "cursor=%21%21", "cursor=" + strings.Repeat("A", 8)} {
		hs.do(a, http.MethodGet, "/sites?"+q, nil).want(t, http.StatusBadRequest, "invalid_request")
	}

	// The hostname filter: base-domain names and custom domains of any status.
	alpha := hs.createSite(a, "golf")
	stored, err := hs.db.Sites().GetByOwnerName(t.Context(), a, "golf")
	if err != nil {
		t.Fatal(err)
	}
	dom := store.Domain{
		ID: store.NewID(), SiteID: stored.ID, Hostname: "docs.example.test", Status: store.DomainPending,
		VerificationToken: "verify-token-000001", CreatedAt: time.Now(),
	}
	if err := hs.db.Domains().Create(t.Context(), a, dom); err != nil {
		t.Fatal(err)
	}
	for host, want := range map[string]string{
		"golf." + baseDomain:        "golf",
		"GOLF." + baseDomain + ".":  "golf",
		"docs.example.test":         "golf",
		"foxtrot." + baseDomain:     "", // another owner's
		"unknown." + baseDomain:     "",
		"a.golf." + baseDomain:      "",
		baseDomain:                  "",
		"unknown.example.test":      "",
		"console." + baseDomain:     "",
		"golf." + baseDomain + ".x": "",
	} {
		var page api.SitePage
		res := hs.do(a, http.MethodGet, "/sites?hostname="+host, nil)
		res.want(t, http.StatusOK, "")
		res.decode(t, &page)
		var got string
		if len(page.Items) == 1 {
			got = page.Items[0].Name
			if !reflect.DeepEqual(page.Items[0], alpha) {
				t.Errorf("hostname %s: %+v, want %+v", host, page.Items[0], alpha)
			}
		}
		if got != want || len(page.Items) > 1 || page.NextCursor != nil {
			t.Errorf("hostname %s: %+v, want %q", host, page, want)
		}
	}
}

func TestListDeploys(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	a := hs.ownerA
	hs.createSite(a, "docs")
	var created []string
	for i := range 5 {
		created = append(created, hs.deploy(a, "docs", file{"index.html", fmt.Sprint(i)}).Deploy.Id)
	}
	msg := "pending one"
	req := manifest(index)
	req.Message, req.Spa = &msg, new(true)
	created = append(created, hs.createDeploy(a, "docs", req).Deploy.Id)
	slices.Reverse(created)

	var items []api.Deploy
	cursor := ""
	for {
		path := "/sites/docs/deploys?limit=4"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		var page api.DeployPage
		res := hs.do(a, http.MethodGet, path, nil)
		res.want(t, http.StatusOK, "")
		res.decode(t, &page)
		items = append(items, page.Items...)
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	var ids, statuses []string
	for _, d := range items {
		ids, statuses = append(ids, d.Id), append(statuses, string(d.Status))
	}
	if !slices.Equal(ids, created) {
		t.Fatalf("listed %v, want newest first %v", ids, created)
	}
	if want := []string{"pending", "active", "inactive", "inactive", "inactive", "inactive"}; !slices.Equal(statuses, want) {
		t.Fatalf("statuses %v, want %v", statuses, want)
	}
	if p := items[0]; p.Message == nil || *p.Message != msg || !p.Spa || p.ActivatedAt != nil {
		t.Fatalf("pending deploy = %+v", p)
	}
	for _, q := range []string{"limit=0", "limit=101", "cursor=%21", "cursor=" + strings.Repeat("A", 8)} {
		hs.do(a, http.MethodGet, "/sites/docs/deploys?"+q, nil).want(t, http.StatusBadRequest, "invalid_request")
	}
}

// TestOwnerIsolation: owner B cannot see, deploy to, roll back or tear down
// owner A's site, and gets exactly the answer a missing site gets.
func TestOwnerIsolation(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	a, b := hs.ownerA, hs.ownerB
	site := hs.createSite(a, "docs")
	first := hs.deploy(a, "docs", index)
	second := hs.deploy(a, "docs", app)
	pending := hs.createDeploy(a, "docs", manifest(file{"new.html", "new"}))
	hs.createSite(b, "blog")
	before := len(hs.notifications())

	type call struct {
		name, method, path string
		body               any
	}
	calls := func(siteName string) []call {
		d := "/sites/" + siteName + "/deploys/"
		return []call{
			{"get site", http.MethodGet, "/sites/" + siteName, nil},
			{"list deploys", http.MethodGet, d[:len(d)-1], nil},
			{"create deploy", http.MethodPost, d[:len(d)-1], manifest(index)},
			{"upload blob", http.MethodPut, d + pending.Deploy.Id + "/blobs/" + file{"new.html", "new"}.hash(), []byte("new")},
			{"activate", http.MethodPost, d + pending.Deploy.Id + "/activate", nil},
			{"rollback", http.MethodPost, "/sites/" + siteName + "/rollback", nil},
			{"rollback to ID", http.MethodPost, "/sites/" + siteName + "/rollback", api.RollbackRequest{DeployId: &first.Deploy.Id}},
			{"teardown", http.MethodDelete, "/sites/" + siteName, nil},
		}
	}
	missing := calls("nosuchsite")
	for i, c := range calls("docs") {
		t.Run(c.name, func(t *testing.T) {
			got := hs.do(b, c.method, c.path, c.body)
			got.want(t, http.StatusNotFound, "not_found")
			m := missing[i]
			want := hs.do(b, m.method, m.path, m.body)
			if !bytes.Equal(got.body, want.body) {
				t.Fatalf("foreign site answered %s, missing site %s", got.body, want.body)
			}
		})
	}
	// B's own site does not reach A's deploys either.
	hs.do(b, http.MethodPost, "/sites/blog/deploys/"+pending.Deploy.Id+"/activate", nil).want(t, http.StatusNotFound, "not_found")
	hs.do(b, http.MethodPut, "/sites/blog/deploys/"+pending.Deploy.Id+"/blobs/"+index.hash(), []byte(index.content)).want(t, http.StatusNotFound, "not_found")
	hs.do(b, http.MethodPost, "/sites/blog/rollback", api.RollbackRequest{DeployId: &first.Deploy.Id}).want(t, http.StatusNotFound, "not_found")

	var page api.SitePage
	hs.do(b, http.MethodGet, "/sites", nil).decode(t, &page)
	if len(page.Items) != 1 || page.Items[0].Name != "blog" {
		t.Fatalf("B lists %+v", page.Items)
	}
	hs.do(b, http.MethodGet, "/sites?hostname=docs."+baseDomain, nil).decode(t, &page)
	if len(page.Items) != 0 {
		t.Fatalf("B resolves A's hostname to %+v", page.Items)
	}

	// A's site is untouched and nothing was notified.
	var got api.Site
	hs.do(a, http.MethodGet, "/sites/docs", nil).decode(t, &got)
	if !reflect.DeepEqual(got, second.Site) || got.Id != site.Id {
		t.Fatalf("A's site is now %+v, want %+v", got, second.Site)
	}
	if n := len(hs.notifications()) - before; n != 0 {
		t.Fatalf("B's calls sent %d notifications", n)
	}
}

// TestBlobPrivacy: owner B cannot learn that owner A's content is on the
// server through createDeploy's missing list, uploadDeployBlob or
// activateDeploy.
func TestBlobPrivacy(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	a, b := hs.ownerA, hs.ownerB
	secret := file{"secret.txt", "owner A's secret"}
	never := file{"never.txt", "content nobody uploaded"}
	hs.createSite(a, "docs")
	hs.deploy(a, "docs", secret)
	path := filepath.Join(hs.blobDir, "sha256", secret.hash()[:2], secret.hash()[2:])
	stored, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	hs.createSite(b, "blog")
	c := hs.createDeploy(b, "blog", manifest(secret, never))
	want := []string{secret.hash(), never.hash()}
	slices.Sort(want)
	if !slices.Equal(c.Missing, want) {
		t.Fatalf("B's missing = %v, want both hashes %v", c.Missing, want)
	}
	// Activation fails the same way as for content nobody has.
	res := hs.do(b, http.MethodPost, "/sites/blog/deploys/"+c.Deploy.Id+"/activate", nil)
	res.want(t, http.StatusConflict, "blobs_missing")
	// A wrong body for A's hash answers exactly as for an unknown hash.
	wrongSecret := hs.do(b, http.MethodPut, "/sites/blog/deploys/"+c.Deploy.Id+"/blobs/"+secret.hash(), []byte("guess"))
	wrongNever := hs.do(b, http.MethodPut, "/sites/blog/deploys/"+c.Deploy.Id+"/blobs/"+never.hash(), []byte("guess"))
	wrongSecret.want(t, http.StatusBadRequest, "hash_mismatch")
	if wrongSecret.status != wrongNever.status || !bytes.Equal(wrongSecret.body, wrongNever.body) {
		t.Fatalf("mismatch for A's blob %d %s, for an unknown blob %d %s", wrongSecret.status, wrongSecret.body, wrongNever.status, wrongNever.body)
	}
	// Uploading A's content proves B holds it; the file is not rewritten.
	hs.upload(b, "blog", c.Deploy.Id, secret).want(t, http.StatusNoContent, "")
	hs.upload(b, "blog", c.Deploy.Id, never).want(t, http.StatusNoContent, "")
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(stored, after) || !after.ModTime().Equal(stored.ModTime()) {
		t.Fatal("re-uploading an existing blob rewrote it")
	}
	hs.activate(b, "blog", c.Deploy.Id)
	if c := hs.createDeploy(b, "blog", manifest(secret)); len(c.Missing) != 0 {
		t.Fatalf("B's missing after uploading = %v", c.Missing)
	}
}

func TestUnauthenticated(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	hs.createSite(hs.ownerA, "docs")
	id := store.NewID()
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/sites", nil},
		{http.MethodPost, "/sites", api.CreateSiteRequest{Name: "blog"}},
		{http.MethodGet, "/sites/docs", nil},
		{http.MethodDelete, "/sites/docs", nil},
		{http.MethodGet, "/sites/docs/deploys", nil},
		{http.MethodPost, "/sites/docs/deploys", manifest(index)},
		{http.MethodPut, "/sites/docs/deploys/" + id + "/blobs/" + index.hash(), []byte(index.content)},
		{http.MethodPost, "/sites/docs/deploys/" + id + "/activate", nil},
		{http.MethodPost, "/sites/docs/rollback", nil},
	} {
		hs.do("", c.method, c.path, c.body).want(t, http.StatusUnauthorized, "unauthenticated")
	}
	if _, err := hs.db.Sites().GetByName(t.Context(), "blog"); err == nil {
		t.Fatal("an unauthenticated request created a site")
	}
}
