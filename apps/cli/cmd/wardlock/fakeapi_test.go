// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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
	// blobs are hashes the owner has uploaded.
	blobs map[string]bool
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
	// fakePage is the fake's page size, so short lists still page.
	fakePage = 2
)

type fakeDeploy struct {
	apiclient.Deploy
	hashes []string
}

const fakeBase = "wardlock.example.test"

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	f := &fakeAPI{t: t, sites: map[string]*fakeSite{}, taken: map[string]bool{}, blobs: map[string]bool{},
		uploads: map[string]int{}, scopes: []string{"deploy", "manage"}}
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
	if err := dec.Decode(&body); err != nil || len(body.Files) == 0 {
		reply(w, http.StatusBadRequest, apiErr("invalid_request", "Invalid manifest."))
		return
	}
	f.created = append(f.created, body)
	d := &fakeDeploy{Deploy: apiclient.Deploy{Id: f.id(), Status: apiclient.DeployStatusPending, CreatedAt: 1_800_000_000_000,
		FileCount: int32(len(body.Files)), Message: body.Message, Spa: body.Spa != nil && *body.Spa,
		ImmutableAssets: body.ImmutableAssets != nil && *body.ImmutableAssets}}
	missing := []string{}
	for _, file := range body.Files {
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
