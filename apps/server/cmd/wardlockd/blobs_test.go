// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/deploys/s3test"
)

// s3SessionToken is a synthetic AWS_SESSION_TOKEN the logs must not show.
const s3SessionToken = "s3test-session-token-do-not-log"

// siteRequest sends method path to the docs site with header and returns
// the status, the ETag and the body.
func (s *testServer) siteRequest(method, path string, header http.Header) (int, string, string) {
	s.t.Helper()
	req, err := http.NewRequest(method, "http://"+s.addr+path, nil)
	if err != nil {
		s.t.Fatal(err)
	}
	req.Host = "docs.wardlock.localhost"
	maps.Copy(req.Header, header)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header.Get("ETag"), string(body)
}

// pageViewCosts checks the bucket requests one page view of each kind
// makes, with getCost what a full GET costs.
func pageViewCosts(t *testing.T, s *testServer, bucket *s3test.Server, page string, getCost map[string]int) {
	t.Helper()
	bucket.Requests()
	code, etag, body := s.siteRequest(http.MethodGet, "/", nil)
	if code != http.StatusOK || body != page {
		t.Fatalf("GET = %d %q", code, body)
	}
	if got := s3test.Count(bucket.Requests()); !maps.Equal(got, getCost) {
		t.Errorf("GET made %v, want %v", got, getCost)
	}
	for name, header := range map[string]http.Header{
		"If-None-Match":     {"If-None-Match": {etag}},
		"If-Modified-Since": {"If-Modified-Since": {time.Now().Add(time.Hour).UTC().Format(http.TimeFormat)}},
	} {
		if code, _, _ := s.siteRequest(http.MethodGet, "/", header); code != http.StatusNotModified {
			t.Errorf("%s = %d, want 304", name, code)
		}
		if got := bucket.Requests(); len(got) != 0 {
			t.Errorf("%s 304 made %v, want none", name, s3test.Count(got))
		}
	}
	if code, _, _ := s.siteRequest(http.MethodHead, "/", nil); code != http.StatusOK {
		t.Errorf("HEAD = %d", code)
	}
	if got := bucket.Requests(); len(got) != 0 {
		t.Errorf("HEAD made %v, want none", s3test.Count(got))
	}
}

// TestServeS3Blobs runs the server with blobs in an S3 bucket: readiness
// reaches the bucket, a published page is stored there in one PutObject
// (the fake checks payload hashes) and served with one GetObject per full
// or ranged response and none for a 304 or HEAD, `wardlockd gc` sweeps
// stale temporary uploads, and no credential reaches the log.
func TestServeS3Blobs(t *testing.T) {
	bucket := s3test.New(t, "wardlock-blobs", "AKIATEST")
	dataDir := t.TempDir()
	s := startServerEnv(t, dataDir, map[string]string{
		"WARDLOCK_BLOB_BACKEND":  "s3",
		"WARDLOCK_S3_ENDPOINT":   bucket.URL,
		"WARDLOCK_S3_BUCKET":     "wardlock-blobs",
		"WARDLOCK_S3_REGION":     "us-east-1",
		"WARDLOCK_S3_PREFIX":     "prod",
		"WARDLOCK_S3_PATH_STYLE": "true",
		"AWS_ACCESS_KEY_ID":      "AKIATEST",
		"AWS_SECRET_ACCESS_KEY":  s3test.SecretKey,
		"AWS_SESSION_TOKEN":      s3SessionToken,
	})

	code, ready := s.do(s.opAddr, http.MethodGet, "operator", "/readyz", nil)
	if code != http.StatusOK || !strings.Contains(string(ready), `"blobs":"ok"`) {
		t.Fatalf("/readyz = %d %s", code, ready)
	}

	page := "<p>Served from a bucket</p>"
	bucket.Requests()
	s.setupAndPublish("docs", page)
	sum := sha256.Sum256([]byte(page))
	h := hex.EncodeToString(sum[:])
	if got, ok := bucket.Object("prod/sha256/" + h[:2] + "/" + h[2:]); !ok || string(got) != page {
		t.Fatalf("bucket holds %v", bucket.Keys())
	}
	for _, k := range bucket.Keys() {
		if strings.HasPrefix(k, "prod/uploads/") {
			t.Fatalf("temporary upload %s left behind", k)
		}
	}
	// One probe, then one PutObject per blob (the page and its
	// renditions), and never a copy.
	var puts []string
	for _, r := range bucket.Requests() {
		if r.Op == s3test.CopyObject {
			t.Errorf("publishing copied %s", r.Key)
		}
		if r.Op == s3test.PutObject {
			puts = append(puts, r.Key)
		}
	}
	if len(puts) < 2 || !strings.HasPrefix(puts[0], "prod/uploads/") || !strings.HasPrefix(puts[1], "prod/sha256/") {
		t.Errorf("publishing put %v, want a probe and then blobs", puts)
	}
	for _, k := range puts[1:] {
		if !strings.HasPrefix(k, "prod/sha256/") {
			t.Errorf("an upload went to %s, not a blob's key", k)
		}
	}
	pageViewCosts(t, s, bucket, page, map[string]int{s3test.GetObject: 1})
	code, _, body := s.siteRequest(http.MethodGet, "/", http.Header{"Range": {"bytes=3-8"}})
	if code != http.StatusPartialContent || body != "Served" {
		t.Fatalf("ranged GET = %d %q", code, body)
	}
	if reqs := bucket.Requests(); len(reqs) != 1 || reqs[0].Op != s3test.GetObject || reqs[0].Range != "bytes=3-" {
		t.Errorf("ranged GET made %v, want one GetObject from byte 3", reqs)
	}

	bucket.PutObject("prod/uploads/abandoned", []byte("partial"), time.Now().Add(-48*time.Hour))
	bucket.PutObject("prod/uploads/in-progress", []byte("partial"), time.Now())
	var out, gcLogs bytes.Buffer
	if err := run(t.Context(), []string{"gc"}, s.getenv, &out, &gcLogs); err != nil {
		t.Fatalf("gc: %v\n%s", err, gcLogs.String())
	}
	if !strings.Contains(out.String(), "0 blobs and 1 unrecorded files") {
		t.Fatalf("gc output %q", out.String())
	}
	if _, ok := bucket.Object("prod/uploads/abandoned"); ok {
		t.Fatal("gc kept the stale temporary upload")
	}
	if _, ok := bucket.Object("prod/uploads/in-progress"); !ok {
		t.Fatal("gc removed a young temporary upload")
	}
	if _, ok := bucket.Object("prod/sha256/" + h[:2] + "/" + h[2:]); !ok {
		t.Fatal("gc removed the served blob")
	}

	s.stop()
	logs := s.logs.String() + gcLogs.String()
	if !strings.Contains(logs, `"blob_bucket":{"endpoint":"`+bucket.URL+`","bucket":"wardlock-blobs"`) {
		t.Errorf("startup log lacks the bucket:\n%s", s.logs)
	}
	for _, secret := range []string{s3test.SecretKey, s3SessionToken} {
		if strings.Contains(logs, secret) {
			t.Errorf("log contains a credential:\n%s", logs)
		}
	}
}

// TestServeS3BlobCache: with WARDLOCK_BLOB_CACHE_DIR a page costs one
// GetObject the first time and none after, a cold Range one ranged
// GetObject that fills nothing, and a wiped cache fills again.
func TestServeS3BlobCache(t *testing.T) {
	bucket := s3test.New(t, "wardlock-blobs", "AKIATEST")
	cacheDir := filepath.Join(t.TempDir(), "blob-cache")
	s := startServerEnv(t, t.TempDir(), map[string]string{
		"WARDLOCK_BLOB_BACKEND":         "s3",
		"WARDLOCK_S3_ENDPOINT":          bucket.URL,
		"WARDLOCK_S3_BUCKET":            "wardlock-blobs",
		"WARDLOCK_S3_REGION":            "us-east-1",
		"WARDLOCK_S3_PATH_STYLE":        "true",
		"AWS_ACCESS_KEY_ID":             "AKIATEST",
		"AWS_SECRET_ACCESS_KEY":         s3test.SecretKey,
		"WARDLOCK_BLOB_CACHE_DIR":       cacheDir,
		"WARDLOCK_BLOB_CACHE_MAX_BYTES": "1048576",
	})
	page := "<p>Served from a cache</p>"
	s.setupAndPublish("docs", page)
	// Publishing read the page to render it, which filled the cache.
	pageViewCosts(t, s, bucket, page, map[string]int{})

	if err := os.RemoveAll(cacheDir); err != nil {
		t.Fatal(err)
	}
	code, _, body := s.siteRequest(http.MethodGet, "/", http.Header{"Range": {"bytes=3-8"}})
	if code != http.StatusPartialContent || body != "Served" {
		t.Fatalf("ranged GET = %d %q", code, body)
	}
	if reqs := bucket.Requests(); len(reqs) != 1 || reqs[0].Op != s3test.GetObject || reqs[0].Range != "bytes=3-" {
		t.Errorf("cold ranged GET made %v, want one GetObject from byte 3", reqs)
	}
	pageViewCosts(t, s, bucket, page, map[string]int{s3test.GetObject: 1})
	pageViewCosts(t, s, bucket, page, map[string]int{})
	s.stop()
	if !strings.Contains(s.logs.String(), `"blob_cache_dir":"`+cacheDir+`"`) {
		t.Errorf("startup log lacks the cache:\n%s", s.logs)
	}
}

func TestServeRejectsBadS3Config(t *testing.T) {
	env := map[string]string{
		"WARDLOCK_DATA_DIR":     t.TempDir(),
		"WARDLOCK_SECRET":       testSecret,
		"WARDLOCK_BLOB_BACKEND": "s3",
		"WARDLOCK_S3_ENDPOINT":  "https://s3.example.test",
		"WARDLOCK_S3_REGION":    "us-east-1",
	}
	err := run(t.Context(), []string{"serve"}, func(k string) string { return env[k] }, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "WARDLOCK_S3_BUCKET") {
		t.Fatalf("serve = %v, want a WARDLOCK_S3_BUCKET error", err)
	}
}
