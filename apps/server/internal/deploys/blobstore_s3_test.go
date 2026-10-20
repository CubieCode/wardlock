// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package deploys_test

import (
	"errors"
	"io"
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
	reqs := srv.Requests()
	if got, want := strings.Join(s3Calls(reqs), "; "), "PUT uploads/*; PUT "+key+"; DELETE uploads/*"; got != want {
		t.Fatalf("Put made %s, want %s", got, want)
	}
	// The upload is signed with the blob's hash, so S3 itself rejects
	// other content; the copy names the temporary key.
	if got := reqs[0].Header.Get("X-Amz-Content-Sha256"); got != h {
		t.Errorf("upload X-Amz-Content-Sha256 = %q, want the blob hash", got)
	}
	if src := reqs[1].Header.Get("X-Amz-Copy-Source"); !strings.Contains(src, "uploads/") || reqs[2].Key != reqs[0].Key {
		t.Errorf("copy source %q, upload %q, delete %q", src, reqs[0].Key, reqs[2].Key)
	}

	rc, err := bs.Open(t.Context(), h)
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
	// Open is one HEAD; each read after a seek is one GET from there on;
	// sequential reads share a GET.
	want := "HEAD " + key + "; GET " + key + " bytes=150-; GET " + key + " bytes=5-; GET " + key
	if got := strings.Join(s3Calls(srv.Requests()), "; "); got != want {
		t.Fatalf("reads made %s, want %s", got, want)
	}

	// A HEAD response only needs the size.
	rc2, err := bs.Open(t.Context(), h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rc2.Seek(0, io.SeekEnd); err != nil {
		t.Fatal(err)
	}
	_ = rc2.Close()
	if got := s3Calls(srv.Requests()); len(got) != 1 || got[0] != "HEAD "+key {
		t.Fatalf("open and close made %v, want one HEAD", got)
	}
}

// TestS3BlobStoreVanishedBlob: a blob deleted after Open reads as
// ErrBlobNotFound.
func TestS3BlobStoreVanishedBlob(t *testing.T) {
	srv := s3test.New(t, "wardlock-test", "AKIATEST")
	bs := srv.Store(t, "")
	h := hashOf("short-lived")
	if err := bs.Put(t.Context(), h, int64(len("short-lived")), strings.NewReader("short-lived")); err != nil {
		t.Fatal(err)
	}
	rc, err := bs.Open(t.Context(), h)
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
