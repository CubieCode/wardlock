// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package platform_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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

func TestInstrumentCountsPanicsAs500(t *testing.T) {
	m := platform.NewMetrics(time.Now())
	h := m.Instrument(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
	func() {
		defer func() {
			if p := recover(); p != "boom" {
				t.Errorf("recovered %v, want the handler's panic to carry on", p)
			}
		}()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	}()
	rec := httptest.NewRecorder()
	m.Handler(nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	if !strings.Contains(body, `wardlock_http_requests_total{code="5xx"} 1`) || strings.Contains(body, `code="2xx"`) ||
		!strings.Contains(body, "wardlock_http_requests_in_flight 0\n") {
		t.Errorf("panic not counted as one finished 500:\n%s", body)
	}
}

// TestReadinessHungCheck uses a check that ignores its context and never
// returns, as DirWritable does on a stuck network mount.
func TestReadinessHungCheck(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	var started atomic.Int32
	r := platform.NewReadiness(slog.New(slog.DiscardHandler),
		platform.Check{Name: "database", Run: func(context.Context) error { return nil }},
		platform.Check{Name: "blobs", Run: func(context.Context) error {
			started.Add(1)
			<-release
			return nil
		}},
	)
	for range 3 {
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		begin := time.Now()
		rec := httptest.NewRecorder()
		r.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil).WithContext(ctx))
		cancel()
		if took := time.Since(begin); took > 2*time.Second {
			t.Fatalf("/readyz took %v with a hung check", took)
		}
		if body := rec.Body.String(); rec.Code != http.StatusServiceUnavailable ||
			!strings.Contains(body, `"blobs":"timeout"`) || !strings.Contains(body, `"database":"ok"`) {
			t.Errorf("/readyz with a hung check = %d %s", rec.Code, body)
		}
	}
	m := platform.NewMetrics(time.Now())
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	rec := httptest.NewRecorder()
	m.Handler(r).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil).WithContext(ctx))
	if !strings.Contains(rec.Body.String(), `wardlock_ready{check="blobs"} 0`) {
		t.Errorf("metrics with a hung check:\n%s", rec.Body.String())
	}
	if n := started.Load(); n != 1 {
		t.Errorf("hung check started %d times, want once while it is still running", n)
	}
}

func TestReadinessDuringShutdown(t *testing.T) {
	ready := platform.NewReadiness(slog.New(slog.DiscardHandler),
		platform.Check{Name: "database", Run: func(context.Context) error { return nil }})
	listen := func() net.Listener {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		return ln
	}
	// The main server has a request in flight, so its graceful shutdown
	// waits; the operator server, listed last, still answers meanwhile.
	inFlight, finish := make(chan struct{}), make(chan struct{})
	mainLn, opLn := listen(), listen()
	mainSrv := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(inFlight)
		<-finish
	})}
	opSrv := &http.Server{ReadHeaderTimeout: time.Second, Handler: platform.OperatorHandler(ready, platform.NewMetrics(time.Now()))}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- platform.Serve(ctx, slog.New(slog.DiscardHandler), ready,
			platform.Listener{Server: mainSrv, Listener: mainLn}, platform.Listener{Server: opSrv, Listener: opLn})
	}()
	readyz := func() int {
		t.Helper()
		res, err := http.Get("http://" + opLn.Addr().String() + "/readyz")
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		return res.StatusCode
	}
	if code := readyz(); code != http.StatusOK {
		t.Fatalf("/readyz before shutdown = %d", code)
	}
	go func() {
		if res, err := http.Get("http://" + mainLn.Addr().String() + "/"); err == nil {
			_ = res.Body.Close()
		}
	}()
	<-inFlight
	cancel()
	deadline := time.Now().Add(5 * time.Second)
	for code := readyz(); code != http.StatusServiceUnavailable; code = readyz() {
		if time.Now().After(deadline) {
			t.Fatalf("/readyz during shutdown = %d, want 503", code)
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(finish)
	if err := <-done; err != nil {
		t.Fatalf("Serve = %v", err)
	}
}
