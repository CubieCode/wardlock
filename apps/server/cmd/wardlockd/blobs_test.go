// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/deploys/s3test"
)

// s3SessionToken is a synthetic AWS_SESSION_TOKEN the logs must not show.
const s3SessionToken = "s3test-session-token-do-not-log"

// TestServeS3Blobs runs the server with blobs in an S3 bucket: readiness
// reaches the bucket, a published page is stored there and served (with
// ranges), `wardlockd gc` sweeps stale temporary uploads, and no
// credential reaches the log.
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
	if body := s.call(http.MethodGet, "docs.wardlock.localhost", "/", nil, http.StatusOK); string(body) != page {
		t.Fatalf("site body %q", body)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://"+s.addr+"/", nil)
	req.Host = "docs.wardlock.localhost"
	req.Header.Set("Range", "bytes=3-8")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusPartialContent || string(body) != "Served" {
		t.Fatalf("ranged GET = %d %q", res.StatusCode, body)
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
