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
