// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"wardlock.nz/apiclient"
)

// fakeAPI is an in-memory server for the site and deploy operations of
// packages/api-spec/openapi.yaml, with the status codes and error codes
// the real server answers. One owner, authenticated by testToken.
type fakeAPI struct {
	*httptest.Server
	t *testing.T

	mu    sync.Mutex
	sites map[string]*fakeSite
	// taken are names held by another owner's sites.
	taken map[string]bool
	// blobs are hashes the owner has uploaded; content their bodies.
	blobs   map[string]bool
	content map[string][]byte
	// scopes the token has; default deploy and manage.
	scopes []string
	// uploads counts blob PUTs by hash; inFlight and maxInFlight track
	// concurrency.
	uploads               map[string]int
	inFlight, maxInFlight int
	// failUploads makes the next PUTs fail like this, in order: an HTTP
	// status, or dropConnection or hashMismatch.
	failUploads []int
	// barrier, when set, holds each upload until that many are in flight
	// at once, so a test can see real concurrency. A generous fail-safe
	// releases them anyway, so a broken CLI fails the test, not hangs it.
	barrier int
	reached chan struct{}
	created []apiclient.CreateDeployRequest
	nextID  int
	// links are each site's share links, newest first (share_test.go).
	links map[string][]*apiclient.ShareLink
	// domains are each site's custom domains (domain_test.go).
	domains map[string][]*apiclient.Domain
	// verifyPasses is how many more verify calls fail before one passes;
	// verifyCalls counts them; throttle answers the next verify with the
	// cooldown 429, and refuseToken with a refused token's 429.
	verifyPasses, verifyCalls int
	throttle, refuseToken     bool
	// refuseDeploy and refuseSite, when set, answer the next deploy or site
	// creation with that error, as the server's limits and quotas do.
	refuseDeploy, refuseSite *refusal
	// beforeActivate, when set, runs once at the start of the next
	// activation, under the lock: a test makes another deploy go live there
	// to see a path deploy's base go stale.
	beforeActivate func(s *fakeSite)
	// downloads counts blob GETs by hash; failDownloads makes the next
	// GETs fail like this, in order: an HTTP status, dropConnection,
	// corruptBody, truncateBody or a 429 with Retry-After. withheld hashes answer 451,
	// and takenDown sites 409 site_unavailable (pull_test.go).
	downloads     map[string]int
	failDownloads []int
	withheld      map[string]bool
	takenDown     map[string]bool
	// dlInFlight and maxDlInFlight track download concurrency.
	dlInFlight, maxDlInFlight int
}

// refusal is an error answer: an HTTP status and the API's error body.
type refusal struct {
	status int
	body   apiclient.Error
}

type fakeSite struct {
	site     apiclient.Site
	deploys  []*fakeDeploy // oldest first
	policy   apiclient.PolicyVisibility
	previous []string // activation history, newest last
	// pathPolicies are listed before the "/" policy, to push it past the
	// first page.
	pathPolicies int
	// set records the setPolicy bodies received, as sent.
	set []map[string]any
	// allowlist is the "/" policy's allowlist as listed.
	allowlist apiclient.PolicyAllowlist
}

const (
	dropConnection = -1
	hashMismatch   = -2
	// rateLimitedBare is a 429 without Retry-After.
	rateLimitedBare = -3
	// corruptBody sends a body of the right size with other bytes;
	// truncateBody cuts the connection half way through the body.
	corruptBody  = -4
	truncateBody = -5
	// fakePage is the fake's page size, so short lists still page.
	fakePage = 2
)

type fakeDeploy struct {
	apiclient.Deploy
	hashes []string
	// files is the whole manifest; base the deploy a path deploy was built
	// on ("" for none).
	files []apiclient.ManifestFile
	base  string
}

const fakeBase = "wardlock.example.test"

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	f := &fakeAPI{t: t, sites: map[string]*fakeSite{}, taken: map[string]bool{}, blobs: map[string]bool{},
		uploads: map[string]int{}, scopes: []string{"deploy", "manage"},
		content: map[string][]byte{}, downloads: map[string]int{}, withheld: map[string]bool{}, takenDown: map[string]bool{}}
	mux := http.NewServeMux()
	handle := func(pattern, scope string, h func(w http.ResponseWriter, r *http.Request)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+testToken {
				reply(w, http.StatusUnauthorized, apiErr("unauthenticated", "Sign in or send a valid API token."))
				return
			}
			f.mu.Lock()
			ok := scope == "" || slices.Contains(f.scopes, scope)
			f.mu.Unlock()
			if !ok {
				reply(w, http.StatusForbidden, apiErr("forbidden", "This token lacks the "+scope+" scope."))
				return
			}
			h(w, r)
		})
	}
	mux.HandleFunc("GET /api/v0/health", func(w http.ResponseWriter, _ *http.Request) {
		reply(w, http.StatusOK, apiclient.Health{Status: "ok", Version: "0.0.1"})
	})
	handle("GET /api/v0/sites", "", f.listSites)
	handle("POST /api/v0/sites", "deploy", f.createSite)
	handle("GET /api/v0/sites/{site}", "", f.withSite(func(w http.ResponseWriter, _ *http.Request, s *fakeSite) {
		reply(w, http.StatusOK, s.site)
	}))
	handle("DELETE /api/v0/sites/{site}", "manage", f.withSite(func(w http.ResponseWriter, _ *http.Request, s *fakeSite) {
		delete(f.sites, s.site.Name)
		w.WriteHeader(http.StatusNoContent)
	}))
	handle("GET /api/v0/sites/{site}/policies", "", f.withSite(func(w http.ResponseWriter, r *http.Request, s *fakeSite) {
		items := []apiclient.Policy{}
		for i := range s.pathPolicies {
			items = append(items, apiclient.Policy{Id: f.id(), Path: fmt.Sprintf("/p%d/", i), Visibility: apiclient.PolicyVisibilityPublic, Methods: []apiclient.PolicyMethod{}, Version: 1})
		}
		if s.policy != "" {
			items = append(items, apiclient.Policy{Id: f.id(), Path: "/", Visibility: s.policy, Methods: []apiclient.PolicyMethod{}, Allowlist: s.allowlist, Version: 1})
		}
		page, next := paginate(r, items)
		reply(w, http.StatusOK, apiclient.PolicyPage{Items: page, NextCursor: next})
	}))
	handle("PUT /api/v0/sites/{site}/policies", "manage", f.withSite(f.setPolicy))
	handle("GET /api/v0/sites/{site}/deploys", "", f.withSite(f.listDeploys))
	handle("POST /api/v0/sites/{site}/deploys", "deploy", f.withSite(f.createDeploy))
	handle("PUT /api/v0/sites/{site}/deploys/{deployId}/blobs/{sha256}", "deploy", f.upload)
	handle("POST /api/v0/sites/{site}/deploys/{deployId}/activate", "deploy", f.withSite(f.activate))
	handle("GET /api/v0/sites/{site}/deploys/{deployId}/files", "", f.withSite(f.listFiles))
	handle("GET /api/v0/sites/{site}/deploys/{deployId}/blobs/{sha256}", "", f.download)
	handle("POST /api/v0/sites/{site}/rollback", "deploy", f.withSite(f.rollback))
	f.handleLinks(handle)
	f.handleDomains(handle)
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func reply(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func apiErr(code, message string) apiclient.Error {
	return apiclient.Error{Code: code, Message: message}
}

var notFound = apiErr("not_found", "Not found.")

func (f *fakeAPI) id() string {
	f.nextID++
	return fmt.Sprintf("01928c5e-7a1b-7c3d-9e4f-%012x", 0x5a6b7c8d0000+f.nextID)
}

// withSite looks up {site} under the lock; another owner's site is as
// missing as an unknown one.
func (f *fakeAPI) withSite(h func(w http.ResponseWriter, r *http.Request, s *fakeSite)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		s, ok := f.sites[r.PathValue("site")]
		if !ok {
			reply(w, http.StatusNotFound, notFound)
			return
		}
		h(w, r, s)
	}
}

// addSite creates a site directly, for tests that start with one.
func (f *fakeAPI) addSite(name string, policy apiclient.PolicyVisibility) *fakeSite {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.newSite(name, policy)
}

func (f *fakeAPI) newSite(name string, policy apiclient.PolicyVisibility) *fakeSite {
	s := &fakeSite{policy: policy, site: apiclient.Site{
		Id: f.id(), Name: name, Hostname: name + "." + fakeBase,
		Url: "https://" + name + "." + fakeBase + "/", CreatedAt: 1_800_000_000_000,
	}}
	f.sites[name] = s
	return s
}

func (f *fakeAPI) listSites(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var names []string
	for n := range f.sites {
		names = append(names, n)
	}
	slices.Sort(names)
	if host := r.URL.Query().Get("hostname"); host != "" {
		items := []apiclient.Site{}
		for _, n := range names {
			if f.sites[n].site.Hostname == host {
				items = append(items, f.sites[n].site)
			}
		}
		reply(w, http.StatusOK, apiclient.SitePage{Items: items})
		return
	}
	var sites []apiclient.Site
	for _, n := range names {
		sites = append(sites, f.sites[n].site)
	}
	items, next := paginate(r, sites)
	reply(w, http.StatusOK, apiclient.SitePage{Items: items, NextCursor: next})
}

// paginate answers one page of fakePage items, with the cursor for the
// next.
func paginate[T any](r *http.Request, all []T) ([]T, *string) {
	start, _ := strconv.Atoi(r.URL.Query().Get("cursor"))
	start = min(start, len(all))
	page := append([]T{}, all[start:min(start+fakePage, len(all))]...)
	if start+fakePage < len(all) {
		return page, ptr(strconv.Itoa(start + fakePage))
	}
	return page, nil
}

func (f *fakeAPI) createSite(w http.ResponseWriter, r *http.Request) {
	var body apiclient.CreateSiteRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !siteNamePattern.MatchString(body.Name) {
		reply(w, http.StatusBadRequest, apiErr("invalid_request", "Invalid site name."))
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if r := f.refuseSite; r != nil {
		f.refuseSite = nil
		reply(w, r.status, r.body)
		return
	}
	if f.taken[body.Name] || f.sites[body.Name] != nil {
		reply(w, http.StatusConflict, apiErr("site_name_taken", "That site name is taken."))
		return
	}
	reply(w, http.StatusCreated, f.newSite(body.Name, "").site)
}

func (f *fakeAPI) listDeploys(w http.ResponseWriter, r *http.Request, s *fakeSite) {
	var all []apiclient.Deploy
	for i := len(s.deploys) - 1; i >= 0; i-- {
		all = append(all, s.deploys[i].Deploy)
	}
	items, next := paginate(r, all)
	reply(w, http.StatusOK, apiclient.DeployPage{Items: items, NextCursor: next})
}

func (f *fakeAPI) createDeploy(w http.ResponseWriter, r *http.Request, s *fakeSite) {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var body apiclient.CreateDeployRequest
	if err := dec.Decode(&body); err != nil || (len(body.Files) == 0 && body.Path == nil) {
		reply(w, http.StatusBadRequest, apiErr("invalid_request", "Invalid manifest."))
		return
	}
	if p := body.Path; p != nil && (len(*p) < 3 || !strings.HasPrefix(*p, "/") || !strings.HasSuffix(*p, "/")) {
		reply(w, http.StatusBadRequest, apiclient.Error{Code: "invalid_path", Message: "Invalid path.", Target: ptr("path")})
		return
	}
	if r := f.refuseDeploy; r != nil {
		f.refuseDeploy = nil
		reply(w, r.status, r.body)
		return
	}
	f.created = append(f.created, body)
	d := &fakeDeploy{Deploy: apiclient.Deploy{Id: f.id(), Status: apiclient.DeployStatusPending, CreatedAt: 1_800_000_000_000,
		Message: body.Message, Spa: body.Spa != nil && *body.Spa,
		ImmutableAssets: body.ImmutableAssets != nil && *body.ImmutableAssets}}
	files := body.Files
	if body.Path != nil {
		// A path deploy: the active deploy's files outside the prefix plus
		// the upload under it, with the flags following the base.
		dir := strings.TrimPrefix(*body.Path, "/")
		d.Path = body.Path
		files = nil
		for _, file := range body.Files {
			file.Path = dir + file.Path
			files = append(files, file)
		}
		if id := s.site.ActiveDeployId; id != nil {
			for _, x := range s.deploys {
				if x.Id != *id {
					continue
				}
				d.base = x.Id
				for _, file := range x.files {
					if !strings.HasPrefix(file.Path, dir) {
						files = append(files, file)
					}
				}
				d.Spa = cmpOr(body.Spa, x.Spa)
				d.ImmutableAssets = cmpOr(body.ImmutableAssets, x.ImmutableAssets)
			}
		}
	}
	d.files = files
	d.FileCount = int32(len(files))
	missing := []string{}
	for _, file := range files {
		if strings.HasPrefix(strings.ToLower(file.Path), ".wardlock/") || strings.Contains(file.Path, `\`) {
			reply(w, http.StatusBadRequest, apiclient.Error{Code: "invalid_path", Message: "Invalid path.", Target: &file.Path})
			return
		}
		d.TotalSize += file.Size
		if !slices.Contains(d.hashes, file.Sha256) {
			d.hashes = append(d.hashes, file.Sha256)
			if !f.blobs[file.Sha256] {
				missing = append(missing, file.Sha256)
			}
		}
	}
	s.deploys = append(s.deploys, d)
	reply(w, http.StatusCreated, apiclient.CreatedDeploy{Deploy: d.Deploy, Missing: missing})
}

func (f *fakeAPI) upload(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.inFlight++
	f.maxInFlight = max(f.maxInFlight, f.inFlight)
	if f.barrier > 0 && f.inFlight == f.barrier {
		close(f.reached)
		f.barrier = 0
	}
	reached := f.reached
	var fail int
	if len(f.failUploads) > 0 {
		fail, f.failUploads = f.failUploads[0], f.failUploads[1:]
	}
	f.mu.Unlock()
	if reached != nil {
		select {
		case <-reached:
		case <-time.After(30 * time.Second):
		}
	}
	body, err := io.ReadAll(r.Body)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inFlight--
	if err != nil {
		return
	}
	switch fail {
	case 0:
	case dropConnection:
		conn, _, err := http.NewResponseController(w).Hijack()
		if err == nil {
			_ = conn.Close()
		}
		return
	case hashMismatch:
		reply(w, http.StatusBadRequest, apiErr("hash_mismatch", "The body does not match its hash."))
		return
	case http.StatusTooManyRequests:
		w.Header().Set("Retry-After", "120")
		reply(w, fail, apiErr("rate_limited", "Too many requests."))
		return
	case rateLimitedBare:
		reply(w, http.StatusTooManyRequests, apiErr("rate_limited", "Too many requests."))
		return
	default:
		reply(w, fail, apiErr("internal", "Internal server error."))
		return
	}
	s, ok := f.sites[r.PathValue("site")]
	var d *fakeDeploy
	if ok {
		for _, x := range s.deploys {
			if x.Id == r.PathValue("deployId") {
				d = x
			}
		}
	}
	if d == nil {
		reply(w, http.StatusNotFound, notFound)
		return
	}
	hash := r.PathValue("sha256")
	if !slices.Contains(d.hashes, hash) {
		reply(w, http.StatusBadRequest, apiErr("unexpected_blob", "Not in the manifest."))
		return
	}
	if sum := sha256.Sum256(body); hex.EncodeToString(sum[:]) != hash {
		reply(w, http.StatusBadRequest, apiErr("hash_mismatch", "The body does not match its hash."))
		return
	}
	f.blobs[hash] = true
	f.content[hash] = body
	f.uploads[hash]++
	w.WriteHeader(http.StatusNoContent)
}

func (f *fakeAPI) activate(w http.ResponseWriter, r *http.Request, s *fakeSite) {
	for _, d := range s.deploys {
		if d.Id != r.PathValue("deployId") {
			continue
		}
		if d.Status != apiclient.DeployStatusPending {
			reply(w, http.StatusConflict, apiErr("deploy_not_pending", "The deploy is not pending."))
			return
		}
		if hook := f.beforeActivate; hook != nil {
			f.beforeActivate = nil
			hook(s)
		}
		if active := s.site.ActiveDeployId; d.Path != nil && d.base != deref(active) {
			reply(w, http.StatusConflict, apiErr("stale_base", "Another deploy went live on this site after this path deploy was created; create it again."))
			return
		}
		for _, h := range d.hashes {
			if !f.blobs[h] {
				reply(w, http.StatusConflict, apiErr("blobs_missing", "Upload every file first."))
				return
			}
		}
		f.setActive(s, d)
		reply(w, http.StatusOK, apiclient.Activation{Site: s.site, Deploy: d.Deploy})
		return
	}
	reply(w, http.StatusNotFound, notFound)
}

// cmpOr is *given, or else base.
func cmpOr(given *bool, base bool) bool {
	if given != nil {
		return *given
	}
	return base
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (f *fakeAPI) setActive(s *fakeSite, d *fakeDeploy) {
	for _, x := range s.deploys {
		if x.Status == apiclient.DeployStatusActive {
			x.Status = apiclient.DeployStatusInactive
		}
	}
	d.Status = apiclient.DeployStatusActive
	d.ActivatedAt = ptr(int64(1_800_000_060_000))
	s.site.ActiveDeployId = &d.Id
	s.site.ActivatedAt = d.ActivatedAt
	s.previous = append(s.previous, d.Id)
}

func (f *fakeAPI) rollback(w http.ResponseWriter, r *http.Request, s *fakeSite) {
	var body apiclient.RollbackRequest
	if r.ContentLength != 0 {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	want := ""
	if body.DeployId != nil {
		want = *body.DeployId
	} else {
		if len(s.previous) < 2 {
			reply(w, http.StatusConflict, apiErr("no_previous_deploy", "There is no earlier deploy to return to."))
			return
		}
		want = s.previous[len(s.previous)-2]
	}
	for _, d := range s.deploys {
		if d.Id == want {
			if d.ActivatedAt == nil {
				reply(w, http.StatusConflict, apiErr("deploy_not_activated", "That deploy was never activated."))
				return
			}
			f.setActive(s, d)
			reply(w, http.StatusOK, apiclient.Activation{Site: s.site, Deploy: d.Deploy})
			return
		}
	}
	reply(w, http.StatusNotFound, notFound)
}

// deployOf finds one of s's deploys by ID.
func (s *fakeSite) deployOf(id string) *fakeDeploy {
	for _, d := range s.deploys {
		if d.Id == id {
			return d
		}
	}
	return nil
}

func (f *fakeAPI) listFiles(w http.ResponseWriter, r *http.Request, s *fakeSite) {
	if f.takenDown[s.site.Name] {
		reply(w, http.StatusConflict, apiErr("site_unavailable", "The operator of this server has taken this site down."))
		return
	}
	d := s.deployOf(r.PathValue("deployId"))
	if d == nil {
		reply(w, http.StatusNotFound, notFound)
		return
	}
	var all []apiclient.DeployFile
	for _, file := range d.files {
		all = append(all, apiclient.DeployFile(file))
	}
	slices.SortFunc(all, func(a, b apiclient.DeployFile) int { return strings.Compare(a.Path, b.Path) })
	items, next := paginate(r, all)
	if items == nil {
		items = []apiclient.DeployFile{}
	}
	reply(w, http.StatusOK, apiclient.DeployFilePage{Items: items, NextCursor: next})
}

func (f *fakeAPI) download(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.dlInFlight++
	f.maxDlInFlight = max(f.maxDlInFlight, f.dlInFlight)
	if f.barrier > 0 && f.dlInFlight == f.barrier {
		close(f.reached)
		f.barrier = 0
	}
	reached := f.reached
	var fail int
	if len(f.failDownloads) > 0 {
		fail, f.failDownloads = f.failDownloads[0], f.failDownloads[1:]
	}
	f.mu.Unlock()
	if reached != nil {
		select {
		case <-reached:
		case <-time.After(30 * time.Second):
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dlInFlight--
	hash := r.PathValue("sha256")
	f.downloads[hash]++
	s, ok := f.sites[r.PathValue("site")]
	var d *fakeDeploy
	if ok {
		d = s.deployOf(r.PathValue("deployId"))
	}
	body, held := f.content[hash]
	switch {
	case !ok || d == nil || !slices.Contains(d.hashes, hash) || !held:
		reply(w, http.StatusNotFound, notFound)
		return
	case f.takenDown[s.site.Name]:
		reply(w, http.StatusConflict, apiErr("site_unavailable", "The operator of this server has taken this site down."))
		return
	case f.withheld[hash]:
		reply(w, http.StatusUnavailableForLegalReasons, apiErr("file_withheld", "The operator of this server has withheld this file, so it cannot be downloaded."))
		return
	}
	switch fail {
	case 0:
	case dropConnection:
		conn, _, err := http.NewResponseController(w).Hijack()
		if err == nil {
			_ = conn.Close()
		}
		return
	case corruptBody:
		body = bytes.Repeat([]byte("x"), len(body))
	case truncateBody:
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write(body[:len(body)/2])
		_ = http.NewResponseController(w).Flush()
		panic(http.ErrAbortHandler)
	case http.StatusTooManyRequests:
		w.Header().Set("Retry-After", "120")
		reply(w, fail, apiErr("rate_limited", "Too many requests."))
		return
	default:
		reply(w, fail, apiErr("internal", "Internal server error."))
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	_, _ = w.Write(body)
}

// publish adds an active deploy of files (slash paths to bodies) to s, as
// if deployed and activated, and returns it.
func (f *fakeAPI) publish(s *fakeSite, files map[string]string) *fakeDeploy {
	f.mu.Lock()
	defer f.mu.Unlock()
	d := &fakeDeploy{Deploy: apiclient.Deploy{Id: f.id(), Status: apiclient.DeployStatusPending, CreatedAt: 1_800_000_000_000}}
	for _, p := range slices.Sorted(maps.Keys(files)) {
		sum := sha256.Sum256([]byte(files[p]))
		h := hex.EncodeToString(sum[:])
		d.files = append(d.files, apiclient.ManifestFile{Path: p, Sha256: h, Size: int64(len(files[p]))})
		if !slices.Contains(d.hashes, h) {
			d.hashes = append(d.hashes, h)
		}
		f.blobs[h], f.content[h] = true, []byte(files[p])
		d.TotalSize += int64(len(files[p]))
	}
	d.FileCount = int32(len(d.files))
	s.deploys = append(s.deploys, d)
	f.setActive(s, d)
	return d
}
