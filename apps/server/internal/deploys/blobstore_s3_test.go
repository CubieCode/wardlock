// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package deploys_test

import (
	"errors"
	"io"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/minio/minio-go/v7/pkg/credentials"

	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/deploys/s3test"
)

// s3Calls renders requests as "METHOD key [range]" with temporary upload
// keys shortened to uploads/*.
func s3Calls(reqs []s3test.Request) []string {
	var out []string
	for _, r := range reqs {
		key := r.Key
		if i := strings.Index(key, "uploads/"); i >= 0 {
			key = key[:i] + "uploads/*"
		}
		c := r.Method + " " + key
		if r.Range != "" {
			c += " " + r.Range
		}
		out = append(out, c)
	}
	return out
}

func TestS3BlobStoreRequests(t *testing.T) {
	srv := s3test.New(t, "wardlock-test", "AKIATEST")
	bs := srv.Store(t, "")
	content := strings.Repeat("0123456789", 20)
	h := hashOf(content)
	key := "sha256/" + h[:2] + "/" + h[2:]

	if err := bs.Put(t.Context(), h, int64(len(content)), strings.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	// The first Put probes once with a wrong payload hash, which S3
	// rejects, then uploads straight to the blob's key, signed with its
	// hash.
	reqs := srv.Requests()
	if got, want := strings.Join(s3Calls(reqs), "; "), "PUT uploads/*; PUT "+key; got != want {
		t.Fatalf("first Put made %s, want %s", got, want)
	}
	if got := reqs[0].Header.Get("X-Amz-Content-Sha256"); got == "" || got == h {
		t.Errorf("probe X-Amz-Content-Sha256 = %q, want a hash of other bytes", got)
	}
	if got := reqs[1].Header.Get("X-Amz-Content-Sha256"); got != h {
		t.Errorf("upload X-Amz-Content-Sha256 = %q, want the blob hash", got)
	}
	if err := bs.Put(t.Context(), h, int64(len(content)), strings.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	if got := s3Calls(srv.Requests()); len(got) != 1 || got[0] != "PUT "+key {
		t.Fatalf("second Put made %v, want one PUT to the blob's key", got)
	}

	// Open makes no request: the manifest gives the size.
	rc, err := bs.Open(t.Context(), h, int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	if _, err := rc.Seek(0, io.SeekEnd); err != nil {
		t.Fatal(err)
	}
	if _, err := rc.Seek(150, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 10)
	for range 3 {
		if _, err := io.ReadFull(rc, buf); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := rc.Seek(5, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(rc, buf); err != nil || string(buf) != "5678901234" {
		t.Fatalf("read at 5 = %q, %v", buf, err)
	}
	if _, err := rc.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(rc); err != nil {
		t.Fatal(err)
	}
	// Each read after a seek is one GET from there on; sequential reads
	// share a GET.
	want := "GET " + key + " bytes=150-; GET " + key + " bytes=5-; GET " + key
	if got := strings.Join(s3Calls(srv.Requests()), "; "); got != want {
		t.Fatalf("reads made %s, want %s", got, want)
	}

	// A HEAD response only needs the size, which Open was given.
	rc2, err := bs.Open(t.Context(), h, int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rc2.Seek(0, io.SeekEnd); err != nil {
		t.Fatal(err)
	}
	_ = rc2.Close()
	if got := s3Calls(srv.Requests()); len(got) != 0 {
		t.Fatalf("open and close made %v, want none", got)
	}
}

// TestS3BlobStoreUploadPaths: the probe picks one PutObject on a service
// that checks payload hashes and the temporary key, copy and delete on one
// that does not, logging the choice once; on either, content that does not
// match its hash never reaches a blob's key.
func TestS3BlobStoreUploadPaths(t *testing.T) {
	for _, tc := range []struct {
		name       string
		ignore     bool
		probe, put map[string]int
		path       string
	}{
		{"service checks hashes", false,
			map[string]int{s3test.PutObject: 1},
			map[string]int{s3test.PutObject: 1}, `path=direct`},
		{"service ignores hashes", true,
			map[string]int{s3test.PutObject: 1, s3test.DeleteObject: 1},
			map[string]int{s3test.PutObject: 1, s3test.CopyObject: 1, s3test.DeleteObject: 1}, `path="temporary key"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := s3test.New(t, "wardlock-test", "AKIATEST")
			srv.IgnorePayloadHashes(tc.ignore)
			var logs strings.Builder
			cfg := srv.Config("")
			cfg.Log = slog.New(slog.NewTextHandler(&logs, nil))
			bs, err := deploys.NewS3BlobStore(cfg)
			if err != nil {
				t.Fatal(err)
			}
			put := func(h, body string, size int) error {
				return bs.Put(t.Context(), h, int64(size), strings.NewReader(body))
			}
			content := "the real content"
			h := hashOf(content)
			if err := put(h, content, len(content)); err != nil {
				t.Fatal(err)
			}
			first := s3test.Count(srv.Requests())
			for op, n := range tc.probe {
				first[op] -= n
				if first[op] == 0 {
					delete(first, op)
				}
			}
			if !maps.Equal(first, tc.put) {
				t.Fatalf("first Put beyond the probe made %v, want %v", first, tc.put)
			}
			for range 2 {
				if err := put(h, content, len(content)); err != nil {
					t.Fatal(err)
				}
				if got := s3test.Count(srv.Requests()); !maps.Equal(got, tc.put) {
					t.Fatalf("later Put made %v, want %v", got, tc.put)
				}
			}
			if got := strings.Count(logs.String(), "s3 upload path"); got != 1 || !strings.Contains(logs.String(), tc.path) {
				t.Fatalf("logs:\n%s\nwant one s3 upload path line with %s", logs.String(), tc.path)
			}
			if strings.Contains(logs.String(), s3test.SecretKey) {
				t.Fatal("logs carry the secret key")
			}

			other := hashOf("some other content")
			for name, body := range map[string]string{
				"wrong content": "some other contenT",
				"too long":      "some other content and more",
				"too short":     "some other",
			} {
				if err := put(other, body, len("some other content")); !errors.Is(err, deploys.ErrHashMismatch) {
					t.Errorf("%s: Put error = %v, want ErrHashMismatch", name, err)
				}
			}
			if got, want := srv.Keys(), []string{"sha256/" + h[:2] + "/" + h[2:]}; !slices.Equal(got, want) {
				t.Fatalf("after mismatches, bucket holds %v, want only %v", got, want)
			}
		})
	}
}

// TestS3BlobStoreProbeRetries: a probe the service refuses for want of
// access gives no answer, so Put fails and the next Put probes again.
func TestS3BlobStoreProbeRetries(t *testing.T) {
	srv := s3test.New(t, "wardlock-test", "AKIATEST")
	cfg := srv.Config("")
	cfg.Creds = credentials.NewStaticV4("AKIAOTHER", s3test.SecretKey, "")
	bs, err := deploys.NewS3BlobStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		err := bs.Put(t.Context(), hashOf("x"), 1, strings.NewReader("x"))
		if err == nil || errors.Is(err, deploys.ErrHashMismatch) {
			t.Fatalf("Put error = %v, want a probe error", err)
		}
		if got := s3test.Count(srv.Requests()); !maps.Equal(got, map[string]int{s3test.PutObject: 1}) {
			t.Fatalf("Put made %v, want one probe", got)
		}
	}
}

// TestS3BlobStoreVanishedBlob: a missing blob opens without a request and
// reads as ErrBlobNotFound.
func TestS3BlobStoreVanishedBlob(t *testing.T) {
	srv := s3test.New(t, "wardlock-test", "AKIATEST")
	bs := srv.Store(t, "")
	h := hashOf("short-lived")
	if err := bs.Put(t.Context(), h, int64(len("short-lived")), strings.NewReader("short-lived")); err != nil {
		t.Fatal(err)
	}
	rc, err := bs.Open(t.Context(), h, int64(len("short-lived")))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	if err := bs.Delete(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(rc); !errors.Is(err, deploys.ErrBlobNotFound) {
		t.Fatalf("read error = %v, want ErrBlobNotFound", err)
	}
}

type failingReader struct{ n int }

func (r *failingReader) Read(p []byte) (int, error) {
	if r.n <= 0 {
		return 0, errors.New("client went away")
	}
	n := min(len(p), r.n)
	r.n -= n
	return n, nil
}

// TestS3BlobStoreFailedUpload: an upload whose body fails is an error, not
// a hash mismatch, and leaves no temporary object.
func TestS3BlobStoreFailedUpload(t *testing.T) {
	srv := s3test.New(t, "wardlock-test", "AKIATEST")
	bs := srv.Store(t, "")
	err := bs.Put(t.Context(), hashOf("whatever"), 100, &failingReader{n: 10})
	if err == nil || errors.Is(err, deploys.ErrHashMismatch) {
		t.Fatalf("Put error = %v, want an upload error", err)
	}
	if keys := srv.Keys(); len(keys) != 0 {
		t.Fatalf("failed upload left %v", keys)
	}
}

func TestS3BlobStoreCheck(t *testing.T) {
	srv := s3test.New(t, "wardlock-test", "AKIATEST")
	if err := srv.Store(t, "").Check(t.Context()); err != nil {
		t.Fatalf("Check = %v", err)
	}
	for name, cfg := range map[string]deploys.S3Config{
		"missing bucket": {Bucket: "no-such-bucket", Creds: credentials.NewStaticV4("AKIATEST", s3test.SecretKey, "")},
		"wrong key":      {Bucket: "wardlock-test", Creds: credentials.NewStaticV4("AKIAOTHER", s3test.SecretKey, "")},
	} {
		t.Run(name, func(t *testing.T) {
			cfg.Endpoint, cfg.Region, cfg.PathStyle = srv.EndpointURL(), "us-east-1", true
			bs, err := deploys.NewS3BlobStore(cfg)
			if err != nil {
				t.Fatal(err)
			}
			err = bs.Check(t.Context())
			if err == nil {
				t.Fatal("Check passed")
			}
			if strings.Contains(err.Error(), s3test.SecretKey) {
				t.Fatalf("Check error contains the secret key: %v", err)
			}
		})
	}
	// Requests carry a signature, never the secret key itself.
	for _, r := range srv.Requests() {
		for name, values := range r.Header {
			if strings.Contains(strings.Join(values, ","), s3test.SecretKey) {
				t.Errorf("header %s carries the secret key", name)
			}
		}
	}
}
