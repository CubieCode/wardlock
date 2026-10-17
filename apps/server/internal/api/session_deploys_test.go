// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/api"
	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/owners"
	"wardlock.nz/server/internal/sites"
	"wardlock.nz/server/internal/store/storetest"
)

// TestSessionDeploysEndToEnd runs C1.2's deploy protocol with a real
// dashboard session through C1.1's middleware, so the two lanes are tested
// together rather than through C1.2's test-only owner header.
func TestSessionDeploysEndToEnd(t *testing.T) {
	db := storetest.New(t)
	limits := deploys.DefaultLimits()
	limits.MaxFileSize = 64
	h := newOwnerHarnessWith(t, db, owners.Limits{}, func(s *api.Server) {
		notifier := deploys.NewLocalNotifier()
		s.Sites = sites.NewService(sites.Config{
			Sites: db.Sites(), Notifier: notifier, BaseDomain: "sites.example.test", DashboardHost: dashHost, Now: time.Now,
		})
		s.Deploys = deploys.NewService(deploys.Config{
			Sites: db.Sites(), Deploys: db.Deploys(), Blobs: deploys.NewFSBlobStore(t.TempDir()), Notifier: notifier,
			Limits: limits, Now: time.Now,
		})
	})
	token := h.owner("owner@example.test")

	// raw sends a cookie-authenticated request from the dashboard origin.
	raw := func(method, path, contentType string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		r.Host = dashHost
		r.AddCookie(&http.Cookie{Name: api.SessionCookie, Value: token})
		if contentType != "" {
			r.Header.Set("Content-Type", contentType)
		}
		if headers == nil {
			headers = map[string]string{"Origin": dashOrig, "Sec-Fetch-Site": "same-origin"}
		}
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.h.ServeHTTP(rec, r)
		return rec
	}
	jsonReq := func(method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		return raw(method, path, "application/json", b, headers)
	}
	decode := func(rec *httptest.ResponseRecorder, v any) {
		t.Helper()
		if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
			t.Fatalf("decode %s: %v", rec.Body, err)
		}
	}

	rec := jsonReq(http.MethodPost, "/api/v0/sites", api.CreateSiteRequest{Name: "docs"}, nil)
	wantStatus(t, rec, http.StatusCreated)

	index := file{"index.html", "<h1>Hello</h1>"}
	big := file{"big.bin", strings.Repeat("b", 65)}
	deployPath := "/api/v0/sites/docs/deploys"

	// Cross-site unsafe requests are refused before reaching C1.2.
	for _, hdr := range []map[string]string{
		{"Origin": "http://evil.example.test"},
		{"Sec-Fetch-Site": "cross-site"},
		{},
	} {
		wantError(t, jsonReq(http.MethodPost, deployPath, manifest(index), hdr), http.StatusForbidden, "forbidden")
	}
	// Without the cookie there is no owner: 401 from C1.2's handler.
	r := httptest.NewRequest(http.MethodPost, deployPath, strings.NewReader(`{"files":[]}`))
	r.Host = dashHost
	r.Header.Set("Content-Type", "application/json")
	noCookie := httptest.NewRecorder()
	h.h.ServeHTTP(noCookie, r)
	wantError(t, noCookie, http.StatusUnauthorized, "unauthenticated")

	// A manifest over C1.2's per-file limit is refused by C1.2.
	wantError(t, jsonReq(http.MethodPost, deployPath, manifest(big), nil), http.StatusRequestEntityTooLarge, "limit_exceeded")

	rec = jsonReq(http.MethodPost, deployPath, manifest(index), nil)
	wantStatus(t, rec, http.StatusCreated)
	var created api.CreatedDeploy
	decode(rec, &created)
	if !slices.Equal(created.Missing, []string{index.hash()}) {
		t.Fatalf("missing = %v", created.Missing)
	}
	blobPath := deployPath + "/" + created.Deploy.Id + "/blobs/" + index.hash()

	// The blob route is exempt from the JSON body cap but C1.2 still bounds
	// it: a body longer than the manifest entry (and the file limit) fails.
	wantError(t, raw(http.MethodPut, blobPath, "application/octet-stream", []byte(index.content+strings.Repeat("x", 100)), nil),
		http.StatusRequestEntityTooLarge, "limit_exceeded")
	wantError(t, raw(http.MethodPut, blobPath, "application/octet-stream", []byte(index.content), map[string]string{"Origin": "http://evil.example.test"}),
		http.StatusForbidden, "forbidden")
	wantStatus(t, raw(http.MethodPut, blobPath, "application/octet-stream", []byte(index.content), nil), http.StatusNoContent)

	rec = raw(http.MethodPost, deployPath+"/"+created.Deploy.Id+"/activate", "", nil, nil)
	wantStatus(t, rec, http.StatusOK)
	var act api.Activation
	decode(rec, &act)
	if act.Site.ActiveDeployId == nil || *act.Site.ActiveDeployId != created.Deploy.Id {
		t.Fatalf("activation = %+v", act)
	}

	// Another owner's session sees none of it.
	other := h.owner("other@example.test")
	r = httptest.NewRequest(http.MethodGet, "/api/v0/sites/docs", nil)
	r.Host = dashHost
	r.AddCookie(&http.Cookie{Name: api.SessionCookie, Value: other})
	rec = httptest.NewRecorder()
	h.h.ServeHTTP(rec, r)
	wantError(t, rec, http.StatusNotFound, "not_found")
}
