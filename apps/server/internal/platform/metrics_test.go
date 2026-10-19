// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package platform_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/platform"
)

func TestMetricsExposition(t *testing.T) {
	m := platform.NewMetrics(time.UnixMilli(1_800_000_000_500))
	h := m.Instrument(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/missing":
			http.NotFound(w, r)
		case "/broken":
			w.WriteHeader(http.StatusBadGateway)
		}
	}))
	for _, p := range []string{"/", "/", "/missing", "/broken"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, p, nil))
	}
	m.Collect(func(_ context.Context, e *platform.Exposition) {
		e.Gauge("test_label_escaping", "Help with a \\ backslash\nand a newline.",
			platform.Sample{Labels: []string{"name", "a\"b\\c\nd"}, Value: 0.5})
		e.Counter("test_empty_total", "Families without samples are left out.")
	})
	ready := platform.NewReadiness(slog.New(slog.DiscardHandler),
		platform.Check{Name: "good", Run: func(context.Context) error { return nil }},
		platform.Check{Name: "bad", Run: func(context.Context) error { return errors.New("down") }},
	)
	rec := httptest.NewRecorder()
	m.Handler(ready).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if ct := rec.Header().Get("Content-Type"); ct != "text/plain; version=0.0.4; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"# HELP wardlock_build_info Version of the running wardlockd.\n# TYPE wardlock_build_info gauge\nwardlock_build_info{version=\"dev\"} 1\n",
		"wardlock_start_time_seconds 1.8000000005e+09\n",
		"# TYPE wardlock_http_requests_total counter\n",
		"wardlock_http_requests_total{code=\"2xx\"} 2\n",
		"wardlock_http_requests_total{code=\"4xx\"} 1\n",
		"wardlock_http_requests_total{code=\"5xx\"} 1\n",
		"wardlock_http_requests_in_flight 0\n",
		"# TYPE wardlock_http_request_duration_seconds histogram\n",
		"wardlock_http_request_duration_seconds_bucket{le=\"10\"} 4\n",
		"wardlock_http_request_duration_seconds_bucket{le=\"+Inf\"} 4\n",
		"wardlock_http_request_duration_seconds_count 4\n",
		"wardlock_ready{check=\"good\"} 1\nwardlock_ready{check=\"bad\"} 0\n",
		"# HELP test_label_escaping Help with a \\\\ backslash\\nand a newline.\n",
		"test_label_escaping{name=\"a\\\"b\\\\c\\nd\"} 0.5\n",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics lack %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "test_empty_total") || strings.Contains(body, `code="3xx"`) {
		t.Errorf("empty families or classes rendered:\n%s", body)
	}
	// Buckets are cumulative and every line is a comment or a sample.
	for _, line := range strings.Split(strings.TrimSuffix(body, "\n"), "\n") {
		if !strings.HasPrefix(line, "# ") && len(strings.Fields(line)) != 2 {
			t.Errorf("malformed line %q", line)
		}
	}
}

func TestReadiness(t *testing.T) {
	var logs bytes.Buffer
	failing := true
	r := platform.NewReadiness(slog.New(slog.NewTextHandler(&logs, nil)),
		platform.Check{Name: "database", Run: func(context.Context) error { return nil }},
		platform.Check{Name: "blobs", Run: func(context.Context) error {
			if failing {
				return errors.New("blob directory not writable")
			}
			return nil
		}},
	)
	get := func() (int, string) {
		rec := httptest.NewRecorder()
		r.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		return rec.Code, rec.Body.String()
	}
	code, body := get()
	if code != http.StatusServiceUnavailable || !strings.Contains(body, `"status":"unready"`) ||
		!strings.Contains(body, `"blobs":"blob directory not writable"`) || !strings.Contains(body, `"database":"ok"`) {
		t.Errorf("failing readiness = %d %s", code, body)
	}
	get()
	if n := strings.Count(logs.String(), "readiness check failing"); n != 1 {
		t.Errorf("failure logged %d times, want once per transition:\n%s", n, logs.String())
	}
	failing = false
	if code, body := get(); code != http.StatusOK || !strings.Contains(body, `"status":"ready"`) {
		t.Errorf("passing readiness = %d %s", code, body)
	}
	if !strings.Contains(logs.String(), "readiness check recovered") {
		t.Errorf("recovery not logged:\n%s", logs.String())
	}
}

func TestDirWritable(t *testing.T) {
	dir := t.TempDir()
	check := platform.DirWritable(dir)
	if err := check(t.Context()); err != nil {
		t.Fatalf("writable directory: %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("probe left %v behind", entries)
	}
	if err := platform.DirWritable(filepath.Join(dir, "missing"))(t.Context()); err == nil {
		t.Error("missing directory passed")
	}
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := platform.DirWritable(file)(t.Context()); err == nil {
		t.Error("a file passed as a directory")
	}
}

func TestOperatorHandler(t *testing.T) {
	ready := platform.NewReadiness(slog.New(slog.DiscardHandler))
	h := platform.OperatorHandler(ready, platform.NewMetrics(time.Now()))
	for _, tc := range []struct {
		method, path string
		code         int
	}{
		{http.MethodGet, "/healthz", 200},
		{http.MethodGet, "/readyz", 200},
		{http.MethodGet, "/metrics", 200},
		{http.MethodHead, "/metrics", 200},
		{http.MethodPost, "/metrics", 405},
		{http.MethodGet, "/", 404},
		{http.MethodGet, "/api/v0/health", 404},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != tc.code {
			body, _ := io.ReadAll(rec.Body)
			t.Errorf("%s %s = %d %s, want %d", tc.method, tc.path, rec.Code, body, tc.code)
		}
	}
}
