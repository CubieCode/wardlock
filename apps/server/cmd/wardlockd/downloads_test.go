// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"wardlock.nz/server/internal/deploys/s3test"
)

// fetched is a response fetch read in full.
type fetched struct {
	StatusCode int
	Header     http.Header
	cookies    []*http.Cookie
}

func (f fetched) Cookies() []*http.Cookie { return f.cookies }

// fetch sends method path to host on the server with header and cookies,
// and nothing else: no session or Origin is added.
func (s *testServer) fetch(method, host, path string, header http.Header, cookies ...*http.Cookie) (fetched, string) {
	s.t.Helper()
	req, err := http.NewRequest(method, "http://"+s.addr+path, nil)
	if err != nil {
		s.t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(s.addr)
	req.Host = host + ":" + port
	for k, v := range header {
		req.Header[k] = v
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	res, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	return fetched{res.StatusCode, res.Header, res.Cookies()}, string(body)
}

// TestDeployDownloadsThroughTheServer reads a deploy's files back through
// a real server (C1.14), with blobs on disk and in a bucket: the dashboard
// session and API tokens of either scope work; a protected site's share
// link, its site session cookie and the dashboard cookie on a site host do
// not; every body carries the attachment headers through the edge; and no
// raw token reaches the log.
func TestDeployDownloadsThroughTheServer(t *testing.T) {
	for _, backend := range []string{"fs", "s3"} {
		t.Run(backend, func(t *testing.T) {
			var extra map[string]string
			if backend == "s3" {
				bucket := s3test.New(t, "wardlock-blobs", "AKIATEST")
				extra = map[string]string{
					"WARDLOCK_BLOB_BACKEND":  "s3",
					"WARDLOCK_S3_ENDPOINT":   bucket.URL,
					"WARDLOCK_S3_BUCKET":     "wardlock-blobs",
					"WARDLOCK_S3_REGION":     "us-east-1",
					"WARDLOCK_S3_PATH_STYLE": "true",
					"AWS_ACCESS_KEY_ID":      "AKIATEST",
					"AWS_SECRET_ACCESS_KEY":  s3test.SecretKey,
				}
			}
			s := startServerEnv(t, t.TempDir(), extra)
			page := "<script>document.title='x'</script><p>Pulled back</p>"
			s.setupAndPublish("docs", page)
			sum := sha256.Sum256([]byte(page))
			hash := hex.EncodeToString(sum[:])
			const dash = "wardlock.localhost"

			var site struct {
				ActiveDeployID string `json:"activeDeployId"`
			}
			if err := json.Unmarshal(s.call(http.MethodGet, dash, "/api/v0/sites/docs", nil, http.StatusOK), &site); err != nil {
				t.Fatal(err)
			}
			files := "/api/v0/sites/docs/deploys/" + site.ActiveDeployID + "/files"
			blob := "/api/v0/sites/docs/deploys/" + site.ActiveDeployID + "/blobs/" + hash
			var listed struct {
				Items []struct {
					Path   string `json:"path"`
					Sha256 string `json:"sha256"`
					Size   int    `json:"size"`
				} `json:"items"`
			}
			if err := json.Unmarshal(s.call(http.MethodGet, dash, files, nil, http.StatusOK), &listed); err != nil {
				t.Fatal(err)
			}
			if len(listed.Items) != 1 || listed.Items[0].Path != "index.html" || listed.Items[0].Sha256 != hash || listed.Items[0].Size != len(page) {
				t.Fatalf("listing = %+v", listed)
			}

			token := func(scope string) string {
				var created struct {
					Token string `json:"token"`
				}
				raw := s.call(http.MethodPost, dash, "/api/v0/tokens", map[string]any{"name": scope, "scopes": []string{scope}}, http.StatusCreated)
				if err := json.Unmarshal(raw, &created); err != nil {
					t.Fatal(err)
				}
				return created.Token
			}
			deployTok, manageTok := token("deploy"), token("manage")

			// A share link on the protected site, and the site session it
			// gives a visitor.
			s.call(http.MethodPut, dash, "/api/v0/sites/docs/policies", map[string]any{"path": "/", "visibility": "protected", "methods": []string{"links"}}, http.StatusOK)
			var link struct {
				Token string `json:"token"`
			}
			if err := json.Unmarshal(s.call(http.MethodPost, dash, "/api/v0/sites/docs/links", map[string]any{}, http.StatusCreated), &link); err != nil {
				t.Fatal(err)
			}
			navigate := http.Header{"Accept": {"text/html"}, "Sec-Fetch-Mode": {"navigate"}}
			res, body := s.fetch(http.MethodGet, "docs.wardlock.localhost", "/?t="+link.Token, navigate)
			if res.StatusCode != http.StatusOK || !strings.Contains(body, "Pulled back") || len(res.Cookies()) == 0 {
				t.Fatalf("share link visit = %d %v", res.StatusCode, res.Cookies())
			}
			siteCookie := res.Cookies()[0]

			bearer := func(tok string) http.Header { return http.Header{"Authorization": {"Bearer " + tok}} }
			for name, tc := range map[string]struct {
				header  http.Header
				cookies []*http.Cookie
			}{
				"dashboard session": {cookies: []*http.Cookie{s.session}},
				"deploy token":      {header: bearer(deployTok)},
				"manage token":      {header: bearer(manageTok)},
			} {
				res, _ := s.fetch(http.MethodGet, dash, files, tc.header, tc.cookies...)
				if res.StatusCode != http.StatusOK {
					t.Errorf("%s: listing = %d", name, res.StatusCode)
				}
				res, body := s.fetch(http.MethodGet, dash, blob, tc.header, tc.cookies...)
				if res.StatusCode != http.StatusOK || body != page {
					t.Errorf("%s: download = %d %q", name, res.StatusCode, body)
				}
			}

			for name, tc := range map[string]struct {
				header  http.Header
				cookies []*http.Cookie
			}{
				"share-link token as bearer":  {header: bearer(link.Token)},
				"site session cookie":         {cookies: []*http.Cookie{siteCookie}},
				"share-link token in the URL": {},
			} {
				path := blob
				if name == "share-link token in the URL" {
					path += "?t=" + link.Token
				}
				res, body := s.fetch(http.MethodGet, dash, path, tc.header, tc.cookies...)
				if res.StatusCode != http.StatusUnauthorized || strings.Contains(body, "Pulled back") {
					t.Errorf("%s: download = %d %q", name, res.StatusCode, body)
				}
			}
			// The dashboard session is no credential on a site's host.
			res, body = s.fetch(http.MethodGet, "docs.wardlock.localhost", blob, nil, s.session)
			if res.StatusCode == http.StatusOK && body == page {
				t.Error("the dashboard cookie downloaded on the site's host")
			}

			wantHeaders := map[string]string{
				"Content-Disposition":          "attachment",
				"X-Content-Type-Options":       "nosniff",
				"Content-Security-Policy":      "default-src 'none'; frame-ancestors 'none'; sandbox",
				"Cross-Origin-Resource-Policy": "same-origin",
				"Referrer-Policy":              "no-referrer",
				"X-Robots-Tag":                 "noindex",
				"Cache-Control":                "private, no-store",
			}
			for name, tc := range map[string]struct {
				header http.Header
				status int
				body   string
			}{
				"whole file":    {nil, http.StatusOK, page},
				"one range":     {http.Header{"Range": {"bytes=38-43"}}, http.StatusPartialContent, "Pulled"},
				"not modified":  {http.Header{"If-None-Match": {`"` + hash + `"`}}, http.StatusNotModified, ""},
				"outside range": {http.Header{"Range": {"bytes=9000-"}}, http.StatusRequestedRangeNotSatisfiable, ""},
			} {
				h := bearer(deployTok)
				for k, v := range tc.header {
					h[k] = v
				}
				res, body := s.fetch(http.MethodGet, dash, blob, h)
				if res.StatusCode != tc.status || (tc.body != "" && body != tc.body) {
					t.Errorf("%s = %d %q", name, res.StatusCode, body)
					continue
				}
				if tc.status == http.StatusOK || tc.status == http.StatusPartialContent {
					if ct := res.Header.Get("Content-Type"); ct != "application/octet-stream" {
						t.Errorf("%s: Content-Type %q", name, ct)
					}
					for k, v := range wantHeaders {
						if got := res.Header.Get(k); got != v {
							t.Errorf("%s: %s = %q, want %q", name, k, got, v)
						}
					}
				}
			}

			s.stop()
			for _, secret := range []string{deployTok, manageTok, link.Token, s.session.Value} {
				if strings.Contains(s.logs.String(), secret) {
					t.Fatalf("log contains a raw credential:\n%s", s.logs)
				}
			}
		})
	}
}
