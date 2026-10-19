// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// testServer is a real `wardlockd serve` on free ports.
type testServer struct {
	t       *testing.T
	addr    string // the edge: sites, dashboard and API
	opAddr  string // the operator listener
	dataDir string
	logs    *syncBuffer
	stop    func()
	session *http.Cookie
}

func (s *testServer) env() map[string]string {
	return map[string]string{
		"WARDLOCK_HTTP_ADDR":     s.addr,
		"WARDLOCK_OPERATOR_ADDR": s.opAddr,
		"WARDLOCK_DATA_DIR":      s.dataDir,
		"WARDLOCK_BASE_DOMAIN":   "wardlock.localhost",
		"WARDLOCK_SECRET":        testSecret,
	}
}

func (s *testServer) getenv(k string) string { return s.env()[k] }

func startServer(t *testing.T, dataDir string) *testServer {
	t.Helper()
	s := &testServer{t: t, addr: freeAddr(t), opAddr: freeAddr(t), dataDir: dataDir, logs: &syncBuffer{}}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- run(ctx, []string{"serve"}, s.getenv, io.Discard, s.logs) }()
	var once sync.Once
	s.stop = func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("serve returned %v\n%s", err, s.logs)
				}
			case <-time.After(15 * time.Second):
				t.Error("serve did not shut down")
			}
		})
	}
	t.Cleanup(s.stop)
	deadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case err := <-done:
			t.Fatalf("serve exited: %v\n%s", err, s.logs)
		default:
		}
		if res, err := http.Get("http://" + s.opAddr + "/healthz"); err == nil {
			_ = res.Body.Close()
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not start:\n%s", s.logs)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// do sends a request to addr with Host host; the dashboard host gets the
// owner's session and a matching Origin, as a browser sends them.
func (s *testServer) do(addr, method, host, path string, body any) (int, []byte) {
	s.t.Helper()
	code, out, cookies, err := s.try(addr, method, host, path, body)
	if err != nil {
		s.t.Fatal(err)
	}
	for _, c := range cookies {
		if c.Name == "__Host-wardlock_session" {
			s.session = c
		}
	}
	return code, out
}

// try is do for other goroutines: it reports errors instead of failing
// the test, and returns cookies rather than recording them.
func (s *testServer) try(addr, method, host, path string, body any) (int, []byte, []*http.Cookie, error) {
	var r io.Reader
	contentType := "application/json"
	switch b := body.(type) {
	case nil:
	case string:
		r, contentType = strings.NewReader(b), "application/octet-stream"
	default:
		raw, _ := json.Marshal(b)
		r = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, "http://"+addr+path, r)
	_, port, _ := net.SplitHostPort(s.addr)
	req.Host = host + ":" + port
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}
	if host == "wardlock.localhost" {
		req.Header.Set("Origin", "http://wardlock.localhost:"+port)
		if s.session != nil {
			req.AddCookie(s.session)
		}
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer func() { _ = res.Body.Close() }()
	out, err := io.ReadAll(res.Body)
	return res.StatusCode, out, res.Cookies(), err
}

func (s *testServer) call(method, host, path string, body any, want int) []byte {
	s.t.Helper()
	code, out := s.do(s.addr, method, host, path, body)
	if code != want {
		s.t.Fatalf("%s %s%s = %d %s, want %d", method, host, path, code, out, want)
	}
	return out
}

// setupAndPublish signs in through the setup URL and publishes a one-page
// site called name.
func (s *testServer) setupAndPublish(name, page string) {
	s.t.Helper()
	m := regexp.MustCompile(`/setup\?code=([A-Za-z0-9_-]+)`).FindStringSubmatch(s.logs.String())
	if m == nil {
		s.t.Fatalf("no setup code in the log:\n%s", s.logs)
	}
	s.call(http.MethodPost, "wardlock.localhost", "/api/v0/setup", map[string]string{"code": m[1], "email": "owner@example.test"}, http.StatusCreated)
	s.call(http.MethodPost, "wardlock.localhost", "/api/v0/sites", map[string]string{"name": name}, http.StatusCreated)
	sum := sha256.Sum256([]byte(page))
	hash := hex.EncodeToString(sum[:])
	var created struct {
		Deploy struct {
			ID string `json:"id"`
		} `json:"deploy"`
	}
	raw := s.call(http.MethodPost, "wardlock.localhost", "/api/v0/sites/"+name+"/deploys", map[string]any{
		"files": []map[string]any{{"path": "index.html", "sha256": hash, "size": len(page)}},
	}, http.StatusCreated)
	if err := json.Unmarshal(raw, &created); err != nil {
		s.t.Fatal(err)
	}
	s.call(http.MethodPut, "wardlock.localhost", "/api/v0/sites/"+name+"/deploys/"+created.Deploy.ID+"/blobs/"+hash, page, http.StatusNoContent)
	s.call(http.MethodPost, "wardlock.localhost", "/api/v0/sites/"+name+"/deploys/"+created.Deploy.ID+"/activate", nil, http.StatusOK)
}

// siteNames lists every site the signed-in owner has.
func (s *testServer) siteNames() map[string]bool {
	s.t.Helper()
	names := map[string]bool{}
	for cursor := ""; ; {
		path := "/api/v0/sites?limit=100"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		var page struct {
			Items []struct {
				Name string `json:"name"`
			} `json:"items"`
			NextCursor string `json:"nextCursor"`
		}
		if err := json.Unmarshal(s.call(http.MethodGet, "wardlock.localhost", path, nil, http.StatusOK), &page); err != nil {
			s.t.Fatal(err)
		}
		for _, site := range page.Items {
			names[site.Name] = true
		}
		if page.NextCursor == "" {
			return names
		}
		cursor = page.NextCursor
	}
}

// TestBackupAndRestore backs up a running server's database with
// `wardlockd backup` while owners keep creating sites and visitors keep
// adding access-log entries, restores the copy and the blobs into an
// empty data directory as DEPLOYMENT describes, and starts a second
// server on it.
func TestBackupAndRestore(t *testing.T) {
	a := startServer(t, t.TempDir())
	page := "<p>Restored from a backup</p>"
	a.setupAndPublish("docs", page)

	// Writers: sites through the API, access-log entries through the edge.
	var created atomic.Int64
	writeCtx, stopWrites := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	var writeErr atomic.Value
	wg.Go(func() {
		for i := 0; writeCtx.Err() == nil; i++ {
			code, out, _, err := a.try(a.addr, http.MethodPost, "wardlock.localhost", "/api/v0/sites", map[string]string{"name": fmt.Sprintf("load-%05d", i)})
			if err != nil || code != http.StatusCreated {
				writeErr.Store(fmt.Sprintf("create site: %d %s %v", code, out, err))
				return
			}
			created.Add(1)
		}
	})
	wg.Go(func() {
		for writeCtx.Err() == nil {
			if code, _, _, err := a.try(a.addr, http.MethodGet, "docs.wardlock.localhost", "/", nil); err != nil || code != http.StatusOK {
				writeErr.Store(fmt.Sprintf("visit: %d %v", code, err))
				return
			}
		}
	})
	waitUntil(t, func() bool { return created.Load() >= 20 })

	backup := filepath.Join(t.TempDir(), "wardlock-backup.db")
	before := created.Load()
	var out bytes.Buffer
	if err := run(t.Context(), []string{"backup", backup}, a.getenv, &out, io.Discard); err != nil {
		t.Fatalf("backup: %v", err)
	}
	if !strings.Contains(out.String(), "Backed up") {
		t.Errorf("backup output %q", out.String())
	}
	// Keep writing past the backup, so the copy is not simply the end state.
	target := created.Load() + 10
	waitUntil(t, func() bool { return created.Load() >= target })
	stopWrites()
	wg.Wait()
	if msg := writeErr.Load(); msg != nil {
		t.Fatal(msg)
	}
	after := created.Load()
	// A second backup to the same path is refused.
	if err := run(t.Context(), []string{"backup", backup}, a.getenv, io.Discard, io.Discard); err == nil {
		t.Error("backup over an existing file succeeded")
	}

	// Restore: the database and the blobs into an empty data directory.
	restoreDir := t.TempDir()
	copyFile(t, backup, filepath.Join(restoreDir, "wardlock.db"))
	copyTree(t, filepath.Join(a.dataDir, "blobs"), filepath.Join(restoreDir, "blobs"))
	b := startServer(t, restoreDir)
	if strings.Contains(b.logs.String(), "setup_url") {
		t.Error("restored server asked for setup: owners were not restored")
	}
	// The owner's dashboard session is in the database, so it carries over
	// (the same WARDLOCK_SECRET is kept with the backup).
	b.session = a.session
	names := b.siteNames()
	got := int64(len(names)) - 1 // less docs
	if got < before || got >= after {
		t.Errorf("restored %d load sites; %d existed when the backup started and %d by the end", got, before, after)
	}
	for i := range got {
		if !names[fmt.Sprintf("load-%05d", i)] {
			t.Errorf("restored copy lacks load-%05d but has %d load sites: not a consistent snapshot", i, got)
		}
	}
	if body := b.call(http.MethodGet, "docs.wardlock.localhost", "/", nil, http.StatusOK); !strings.Contains(string(body), "Restored from a backup") {
		t.Errorf("restored site body %q", body)
	}
	if code, body := b.do(b.opAddr, http.MethodGet, "localhost", "/readyz", nil); code != http.StatusOK {
		t.Errorf("restored /readyz = %d %s", code, body)
	}
	// The restored instance takes writes.
	b.call(http.MethodPost, "wardlock.localhost", "/api/v0/sites", map[string]string{"name": "after-restore"}, http.StatusCreated)
}

func TestBackupCommandErrors(t *testing.T) {
	env := map[string]string{"WARDLOCK_DATA_DIR": t.TempDir()}
	getenv := func(k string) string { return env[k] }
	if err := run(t.Context(), []string{"backup"}, getenv, io.Discard, io.Discard); !errors.Is(err, errUsage) {
		t.Errorf("backup without a path = %v, want usage", err)
	}
	var out bytes.Buffer
	if err := run(t.Context(), []string{"backup", "--help"}, getenv, &out, io.Discard); err != nil || !strings.Contains(out.String(), "online backup") {
		t.Errorf("backup --help = %v %q", err, out.String())
	}
	dst := filepath.Join(t.TempDir(), "b.db")
	err := run(t.Context(), []string{"backup", dst}, getenv, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "no database") {
		t.Errorf("backup with no database = %v", err)
	}
	if _, err := os.Stat(filepath.Join(env["WARDLOCK_DATA_DIR"], "wardlock.db")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("backup created a database: %v", err)
	}
}

// TestOperatorListener checks health, readiness and metrics answer only on
// WARDLOCK_OPERATOR_ADDR: the dashboard, sites and unknown hosts on the
// edge listener never serve them.
func TestOperatorListener(t *testing.T) {
	s := startServer(t, t.TempDir())
	s.setupAndPublish("docs", "<p>a site</p>")
	s.call(http.MethodGet, "docs.wardlock.localhost", "/", nil, http.StatusOK)

	for _, host := range []string{"wardlock.localhost", "docs.wardlock.localhost", "nothing.wardlock.localhost", "127.0.0.1"} {
		for _, path := range []string{"/metrics", "/readyz", "/.wardlock/metrics", "/api/v0/metrics"} {
			code, body := s.do(s.addr, http.MethodGet, host, path, nil)
			if strings.Contains(string(body), "wardlock_") || strings.Contains(string(body), `"checks"`) {
				t.Errorf("edge %s%s = %d served operator data:\n%s", host, path, code, body)
			}
		}
	}

	logged := len(s.logs.String())
	code, body := s.do(s.opAddr, http.MethodGet, "localhost", "/metrics", nil)
	metrics := string(body)
	if code != http.StatusOK {
		t.Fatalf("operator /metrics = %d", code)
	}
	for _, want := range []string{
		`wardlock_build_info{version="dev"} 1`,
		`# TYPE wardlock_http_requests_total counter`,
		`wardlock_http_requests_total{code="2xx"} `,
		`wardlock_http_request_duration_seconds_bucket{le="+Inf"} `,
		`wardlock_ready{check="database"} 1`,
		`wardlock_ready{check="blobs"} 1`,
		`wardlock_access_log_dropped_total 0`,
		`wardlock_database_size_bytes `,
		`go_goroutines `,
	} {
		if !strings.Contains(metrics, want) {
			t.Errorf("metrics lack %q:\n%s", want, metrics)
		}
	}
	if strings.Contains(metrics, "docs") || strings.Contains(metrics, "owner@") {
		t.Errorf("metrics name a site or an owner:\n%s", metrics)
	}
	if code, body := s.do(s.opAddr, http.MethodGet, "localhost", "/readyz", nil); code != http.StatusOK ||
		!strings.Contains(string(body), `"status":"ready"`) || !strings.Contains(string(body), `"database":"ok"`) {
		t.Errorf("operator /readyz = %d %s", code, body)
	}
	if code, _ := s.do(s.opAddr, http.MethodGet, "localhost", "/healthz", nil); code != http.StatusOK {
		t.Errorf("operator /healthz = %d", code)
	}
	// The operator listener serves nothing else: no dashboard, API or site.
	for _, tc := range []struct{ host, path string }{
		{"wardlock.localhost", "/"}, {"wardlock.localhost", "/api/v0/health"}, {"docs.wardlock.localhost", "/"},
	} {
		if code, _ := s.do(s.opAddr, http.MethodGet, tc.host, tc.path, nil); code != http.StatusNotFound {
			t.Errorf("operator %s%s = %d, want 404", tc.host, tc.path, code)
		}
	}
	if strings.Contains(s.logs.String()[logged:], `"msg":"request"`) {
		t.Error("operator scrapes were logged as requests")
	}

	// Readiness fails when the blob directory goes away.
	if err := os.RemoveAll(filepath.Join(s.dataDir, "blobs")); err != nil {
		t.Fatal(err)
	}
	if code, body := s.do(s.opAddr, http.MethodGet, "localhost", "/readyz", nil); code != http.StatusServiceUnavailable || !strings.Contains(string(body), `"status":"unready"`) {
		t.Errorf("/readyz without blobs = %d %s", code, body)
	}
	if _, body := s.do(s.opAddr, http.MethodGet, "localhost", "/metrics", nil); !strings.Contains(string(body), `wardlock_ready{check="blobs"} 0`) {
		t.Errorf("metrics without blobs:\n%s", body)
	}
	if !strings.Contains(s.logs.String(), "readiness check failing") {
		t.Error("failing readiness check was not logged")
	}
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 20s")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o700)
		}
		copyFile(t, p, filepath.Join(dst, rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
