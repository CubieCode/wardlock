// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package edge_test

import (
	"bytes"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"wardlock.nz/server/internal/access"
	"wardlock.nz/server/internal/edge"
)

// syncBuffer is a log sink safe for the edge's goroutines.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// withLog rebuilds h's edge over a to log into the returned buffer.
func withLog(t *testing.T, h *harness, a edge.Access) *syncBuffer {
	t.Helper()
	var logs syncBuffer
	e, err := edge.New(edge.Config{
		DashboardHost: base,
		Dashboard:     text("dashboard"),
		Health:        text("healthy"),
		Sites:         h.src,
		Blobs:         h.fx.blobs,
		Invalidator:   h.notifier,
		Access:        a,
		Log:           slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{ReplaceAttr: dropTime})),
	})
	if err != nil {
		t.Fatal(err)
	}
	h.edge = e
	t.Cleanup(e.Close)
	return &logs
}

func dropTime(_ []string, a slog.Attr) slog.Attr {
	if a.Key == slog.TimeKey {
		return slog.Attr{}
	}
	return a
}

// TestBodylessResponsesNeverOpen: 304, 412, 416 and HEAD are answered from
// the manifest, so the blob is never opened, and a 304 carries exactly the
// headers of the 200 it stands for, less the body's (as http.ServeContent
// has always sent them).
func TestBodylessResponsesNeverOpen(t *testing.T) {
	h, _ := withAssets(t, nil, true)
	const app = "app." + base
	bodyHeaders := []string{"Content-Type", "Content-Length", "Content-Encoding", "Last-Modified", "Accept-Ranges"}
	for _, tc := range []struct {
		name, host, target string
		header             http.Header
	}{
		{"plain file", "basic." + base, "/assets/site.css", nil},
		{"html page with renditions", "basic." + base, "/guide", nil},
		{"precompressed sibling", "basic." + base, "/assets/app.js", http.Header{"Accept-Encoding": {"gzip"}}},
		{"immutable asset", app, "/assets/index-BdK3j2Lp.js", http.Header{"Accept-Encoding": {"br"}}},
		{"rendition", app, "/assets/page-AbCdEf12.html?format=md", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			full := h.do(http.MethodGet, tc.host, tc.target, tc.header)
			if full.Code != http.StatusOK {
				t.Fatalf("GET = %d", full.Code)
			}
			etag, lastMod := full.Header().Get("ETag"), full.Header().Get("Last-Modified")
			want := full.Header().Clone()
			for _, k := range bodyHeaders {
				want.Del(k)
			}
			for name, cond := range map[string]http.Header{
				"If-None-Match":     {"If-None-Match": {etag}},
				"If-Modified-Since": {"If-Modified-Since": {lastMod}},
			} {
				opens := h.fx.blobs.openCount()
				header := maps.Clone(tc.header)
				if header == nil {
					header = http.Header{}
				}
				maps.Copy(header, cond)
				rec := h.do(http.MethodGet, tc.host, tc.target, header)
				if rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
					t.Fatalf("%s = %d %q, want an empty 304", name, rec.Code, rec.Body.String())
				}
				if !equalHeaders(rec.Header(), want) {
					t.Errorf("%s 304 headers\n%v\nwant\n%v", name, rec.Header(), want)
				}
				if got := h.fx.blobs.openCount() - opens; got != 0 {
					t.Errorf("%s opened the blob %d times", name, got)
				}
			}
			for name, req := range map[string]struct {
				method string
				header http.Header
				status int
			}{
				"HEAD":           {http.MethodHead, nil, http.StatusOK},
				"If-Match fails": {http.MethodGet, http.Header{"If-Match": {`"other"`}}, http.StatusPreconditionFailed},
				"bad Range":      {http.MethodGet, http.Header{"Range": {"bytes=99999-"}}, http.StatusRequestedRangeNotSatisfiable},
			} {
				opens := h.fx.blobs.openCount()
				header := maps.Clone(tc.header)
				if header == nil {
					header = http.Header{}
				}
				maps.Copy(header, req.header)
				if rec := h.do(req.method, tc.host, tc.target, header); rec.Code != req.status {
					t.Errorf("%s = %d, want %d", name, rec.Code, req.status)
				}
				if got := h.fx.blobs.openCount() - opens; got != 0 {
					t.Errorf("%s opened the blob %d times", name, got)
				}
			}
			opens := h.fx.blobs.openCount()
			h.do(http.MethodGet, tc.host, tc.target, tc.header)
			if got := h.fx.blobs.openCount() - opens; got != 1 {
				t.Errorf("GET opened the blob %d times, want once", got)
			}
		})
	}
}

func equalHeaders(a, b http.Header) bool {
	return maps.EqualFunc(a, b, slices.Equal)
}

// TestFirstReadFailureIs500: a blob whose first read fails (a missing
// bucket object, opened lazily) answers exactly as a failed open does,
// with the same log line, and never a partial 200.
func TestFirstReadFailureIs500(t *testing.T) {
	for _, tc := range []struct {
		name, target, logged string
		header               http.Header
	}{
		{"page", "/", "open blob", nil},
		{"404.html", "/missing", "open blob", nil},
		{"range", "/assets/site.css", "open blob", http.Header{"Range": {"bytes=2-5"}}},
		{"multiple ranges", "/assets/site.css", "open blob", http.Header{"Range": {"bytes=0-1,4-5"}}},
		{"precompressed sibling", "/assets/app.js", "open blob", http.Header{"Accept-Encoding": {"br"}}},
		{"rendition", "/assets/page-AbCdEf12.html?format=md", "open rendition", nil},
		{"share-link page", "/?t=wl_abcdefgh23_Zm9vYmFy_c2VjcmV0-dGVzdC1vbmx5LW5vdC1yZWFsx", "open blob", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			type outcome struct {
				status int
				header http.Header
				body   string
				logs   string
			}
			run := func(failOpen bool) outcome {
				h, _ := withAssets(t, nil, false)
				logs := withLog(t, h, &fakeAccess{fn: func(access.Request) (access.Decision, error) {
					d := allow(true)
					d.TokenAccepted = true
					return d, nil
				}})
				h.fx.blobs.fail, h.fx.blobs.failRead = failOpen, !failOpen
				host := "basic." + base
				if strings.Contains(tc.target, "page-AbCdEf12") {
					host = "app." + base
				}
				rec := h.do(http.MethodGet, host, tc.target, tc.header)
				return outcome{rec.Code, rec.Header(), rec.Body.String(), logs.String()}
			}
			open, read := run(true), run(false)
			if read.status != http.StatusInternalServerError || read.body != "Internal Server Error\n" {
				t.Fatalf("first-read failure = %d %q", read.status, read.body)
			}
			if !strings.Contains(read.logs, "msg=\""+tc.logged+"\"") || !strings.Contains(read.logs, "site_id=site-") {
				t.Errorf("logs:\n%s\nwant %q", read.logs, tc.logged)
			}
			if open.status != read.status || open.body != read.body || !equalHeaders(open.header, read.header) || open.logs != read.logs {
				t.Errorf("first-read failure\n%+v\ndiffers from failed open\n%+v", read, open)
			}
		})
	}
}
