// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"wardlock.nz/server/internal/api"
)

func TestGetHealth(t *testing.T) {
	h := (&api.Server{Version: "1.2.3"}).Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v0/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got api.Health
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != api.HealthStatusOk || got.Version != "1.2.3" {
		t.Fatalf("body = %+v", got)
	}
}
