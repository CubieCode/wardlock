// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package edge_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"wardlock.nz/server/internal/access"
	"wardlock.nz/server/internal/edge"
)

type fakeRecorder struct {
	mu      sync.Mutex
	records []edge.AccessRecord
}

func (r *fakeRecorder) Record(rec edge.AccessRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, rec)
}

func (r *fakeRecorder) take() []edge.AccessRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	got := r.records
	r.records = nil
	return got
}

// newRecordingHarness is newHarness with an access recorder.
func newRecordingHarness(t *testing.T, a edge.Access) (*harness, *fakeRecorder) {
	t.Helper()
	if a == nil {
		a = publicAccess{}
	}
	h := newHarness(t, a)
	return h, recordWith(t, h, a, h.fx.blobs)
}

// recordWith replaces h's edge with one that records, opening blobs
// through blobs.
func recordWith(t *testing.T, h *harness, a edge.Access, blobs edge.BlobOpener) *fakeRecorder {
	t.Helper()
	rec := &fakeRecorder{}
	e, err := edge.New(edge.Config{
		DashboardHost: base,
		Dashboard:     text("dashboard"),
		Health:        text("healthy"),
		Sites:         h.src,
		Blobs:         blobs,
		Invalidator:   h.notifier,
		Access:        a,
		Recorder:      rec,
	})
	if err != nil {
		t.Fatal(err)
	}
	h.edge = e
	t.Cleanup(h.edge.Close)
	return rec
}

func TestAccessRecords(t *testing.T) {
	const token = "wl_abcdefgh23_Zm9vYmFy_c2VjcmV0-dGVzdC1vbmx5LW5vdC1yZWFsx"
	// The decision for the requested path is recorded, not the second one
	// a fallback file (here 404.html) gets.
	decide := &fakeAccess{fn: func(r access.Request) (access.Decision, error) {
		switch {
		case strings.HasPrefix(r.Path, "/about/"):
			return deny(r), nil
		case r.Path == "/docs/notes":
			d := allow(true)
			d.LinkPublicID = "abcdefgh23"
			return d, nil
		case r.Path == "/404.html":
			return access.Decision{Outcome: access.Allow, Protected: true, Via: access.ViaPassword}, nil
		}
		return access.Decision{Outcome: access.Allow, Via: access.ViaPublic}, nil
	}}
	h, rec := newRecordingHarness(t, decide)
	tests := []struct {
		name, method, host, target string
		header                     http.Header
		status                     int
		path                       string
		via                        access.Via
		link                       string
	}{
		{"served", http.MethodGet, "basic." + base, "/guide", nil, 200, "/guide", access.ViaPublic, ""},
		{"query string is not passed on", http.MethodGet, "basic." + base, "/guide?t=" + token + "&x=1", nil, 200, "/guide", access.ViaPublic, ""},
		{"cleaned path, canonical host", http.MethodGet, "Basic.Wardlock.Localhost.:8080", "/assets/../guide", nil, 200, "/guide", access.ViaPublic, ""},
		{"custom domain", http.MethodGet, "docs.example.test", "/guide", nil, 200, "/guide", access.ViaPublic, ""},
		{"HEAD", http.MethodHead, "basic." + base, "/guide", nil, 200, "/guide", access.ViaPublic, ""},
		{"not found", http.MethodGet, "basic." + base, "/missing", nil, 404, "/missing", access.ViaPublic, ""},
		{"redirect", http.MethodGet, "basic." + base, "/about", nil, 301, "/about", access.ViaPublic, ""},
		{"range", http.MethodGet, "basic." + base, "/assets/site.css", http.Header{"Range": {"bytes=0-3"}}, 206, "/assets/site.css", access.ViaPublic, ""},
		{"not modified", http.MethodGet, "basic." + base, "/guide", http.Header{"If-Modified-Since": {"Wed, 01 Jul 2099 00:00:00 GMT"}}, 304, "/guide", access.ViaPublic, ""},
		{"denied", http.MethodGet, "basic." + base, "/about/", nil, 401, "/about/", access.ViaNone, ""},
		{"denied browser gets the login page", http.MethodGet, "basic." + base, "/about/", browser, 401, "/about/", access.ViaNone, ""},
		{"share link", http.MethodGet, "basic." + base, "/docs/notes", nil, 200, "/docs/notes", access.ViaLink, "abcdefgh23"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			header := http.Header{"User-Agent": {"test-agent/1"}}
			for k, v := range tt.header {
				header[k] = v
			}
			if res := h.do(tt.method, tt.host, tt.target, header); res.Code != tt.status {
				t.Fatalf("status = %d, want %d", res.Code, tt.status)
			}
			got := rec.take()
			if len(got) != 1 {
				t.Fatalf("%d records, want 1", len(got))
			}
			r := got[0]
			wantHost := "basic." + base
			if tt.host == "docs.example.test" {
				wantHost = tt.host
			}
			if r.SiteID != "site-basic" || r.Host != wantHost || r.Path != tt.path || r.Status != tt.status ||
				r.Via != tt.via || r.LinkPublicID != tt.link || r.RemoteIP != "192.0.2.1" || r.UserAgent != "test-agent/1" || r.Time.IsZero() {
				t.Fatalf("record = %+v", r)
			}
			if strings.Contains(r.Path, "wl_") {
				t.Fatalf("record path carries the token: %q", r.Path)
			}
		})
	}
}

func TestAccessRecordsOnlySiteAccesses(t *testing.T) {
	h, rec := newRecordingHarness(t, nil)
	for _, tt := range []struct{ name, method, host, target string }{
		{"health", http.MethodGet, "basic." + base, "/healthz"},
		{"dashboard", http.MethodGet, base, "/"},
		{"unknown host", http.MethodGet, "nothing." + base, "/"},
		{"nothing published", http.MethodGet, "empty." + base, "/"},
		{"reserved path", http.MethodGet, "basic." + base, "/.wardlock/health"},
		{"method not allowed", http.MethodPost, "basic." + base, "/guide"},
	} {
		h.do(tt.method, tt.host, tt.target, nil)
		if got := rec.take(); len(got) != 0 {
			t.Errorf("%s: recorded %+v", tt.name, got)
		}
	}
}

// TestRecorderKeepsResponses checks the status-capturing wrapper changes
// nothing a client sees.
func TestRecorderKeepsResponses(t *testing.T) {
	plain := newHarness(t, nil)
	recorded, _ := newRecordingHarness(t, nil)
	for _, target := range []string{"/", "/guide", "/assets/site.css", "/missing", "/about"} {
		a := plain.do(http.MethodGet, "basic."+base, target, nil)
		b := recorded.do(http.MethodGet, "basic."+base, target, nil)
		if a.Code != b.Code || a.Body.String() != b.Body.String() || a.Header().Get("Content-Type") != b.Header().Get("Content-Type") ||
			a.Header().Get("Content-Length") != b.Header().Get("Content-Length") {
			t.Errorf("%s: %d %q vs recorded %d %q", target, a.Code, a.Header(), b.Code, b.Header())
		}
	}
}

// panicBlobs panics on Open, like a handler bug before any byte is sent.
type panicBlobs struct{}

func (panicBlobs) Open(context.Context, string) (io.ReadSeekCloser, error) { panic("blob store bug") }

func TestPanicIsRecordedAs500(t *testing.T) {
	h := newHarness(t, nil)
	rec := recordWith(t, h, publicAccess{}, panicBlobs{})
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		h.do(http.MethodGet, "basic."+base, "/guide", nil)
	}()
	if recovered != "blob store bug" {
		t.Fatalf("recovered %v, want the handler's panic re-raised", recovered)
	}
	got := rec.take()
	if len(got) != 1 || got[0].Status != http.StatusInternalServerError {
		t.Fatalf("records = %+v, want one with status 500", got)
	}
}
