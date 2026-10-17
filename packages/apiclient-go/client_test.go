// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package apiclient_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"wardlock.nz/apiclient"
)

func TestGetHealthDecodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v0/health" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","version":"9.9.9"}`))
	}))
	defer srv.Close()

	c, err := apiclient.NewClientWithResponses(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.GetHealthWithResponse(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if res.JSON200 == nil || res.JSON200.Version != "9.9.9" || res.JSON200.Status != apiclient.HealthStatusOk {
		t.Fatalf("unexpected response %d %s", res.StatusCode(), res.Body)
	}
}

func TestGetSiteBindsPathAndDecodesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v0/sites/docs" {
			t.Errorf("request %s %s, want GET /api/v0/sites/docs", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":"not_found","message":"Not found."}`))
	}))
	defer srv.Close()

	c, err := apiclient.NewClientWithResponses(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.GetSiteWithResponse(t.Context(), "docs")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode() != http.StatusNotFound || res.JSON404 == nil || res.JSON404.Code != "not_found" {
		t.Fatalf("unexpected response %d %s", res.StatusCode(), res.Body)
	}
}
