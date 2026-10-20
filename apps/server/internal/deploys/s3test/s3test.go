// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Package s3test is an in-process fake of the S3 API, enough for
// deploys.S3BlobStore: path-style HeadBucket, HeadObject, GetObject with
// Range, PutObject, CopyObject, DeleteObject and ListObjectsV2. It checks
// that requests are signed with the expected access key and that a signed
// payload hash matches the body, as S3 does, but not the signature itself.
package s3test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/minio/minio-go/v7/pkg/credentials"

	"wardlock.nz/server/internal/deploys"
)

// Server is a fake S3 service holding one bucket.
type Server struct {
	*httptest.Server
	Bucket    string
	AccessKey string
	// PageSize is how many keys one ListObjectsV2 response holds.
	PageSize int

	mu       sync.Mutex
	now      func() time.Time
	objects  map[string]object
	requests []Request
}

type object struct {
	data []byte
	mod  time.Time
}

// Request is one request the server answered.
type Request struct {
	Method string
	Key    string
	// Range is the Range header of a GET.
	Range string
	// Header is every request header, for tests that look for leaks.
	Header http.Header
}

// New starts a fake holding an empty bucket, closed when t ends.
func New(t testing.TB, bucket, accessKey string) *Server {
	t.Helper()
	s := &Server{Bucket: bucket, AccessKey: accessKey, PageSize: 1000, now: time.Now, objects: map[string]object{}}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// EndpointURL returns the server's URL as a deploys.S3Config endpoint.
func (s *Server) EndpointURL() *url.URL {
	u, _ := url.Parse(s.URL)
	return u
}

// SetNow sets the clock that stamps stored objects.
func (s *Server) SetNow(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}

// Keys returns every stored key, sorted.
func (s *Server) Keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.objects))
	for k := range s.objects {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// Object returns a stored object's bytes.
func (s *Server) Object(key string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.objects[key]
	return o.data, ok
}

// PutObject stores data at key, last modified at mod.
func (s *Server) PutObject(key string, data []byte, mod time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = object{data: data, mod: mod}
}

// SetModTime changes a stored object's last-modified time.
func (s *Server) SetModTime(key string, mod time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if o, ok := s.objects[key]; ok {
		o.mod = mod
		s.objects[key] = o
	}
}

// Requests returns the requests answered so far and forgets them.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.requests
	s.requests = nil
	return out
}

// SecretKey is the secret every Store signs with. It is not checked; tests
// look for it in logs.
const SecretKey = "s3test-secret-access-key-do-not-log"

// Store returns an S3BlobStore over the fake with keys under prefix.
func (s *Server) Store(t testing.TB, prefix string) *deploys.S3BlobStore {
	t.Helper()
	bs, err := deploys.NewS3BlobStore(deploys.S3Config{
		Endpoint:  s.EndpointURL(),
		Bucket:    s.Bucket,
		Region:    "us-east-1",
		Prefix:    prefix,
		PathStyle: true,
		Creds:     credentials.NewStaticV4(s.AccessKey, SecretKey, ""),
	})
	if err != nil {
		t.Fatal(err)
	}
	return bs
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	bucket, key, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	s.mu.Lock()
	s.requests = append(s.requests, Request{Method: r.Method, Key: key, Range: r.Header.Get("Range"), Header: r.Header.Clone()})
	s.mu.Unlock()
	if !strings.Contains(r.Header.Get("Authorization"), "Credential="+s.AccessKey+"/") {
		writeError(w, r, http.StatusForbidden, "InvalidAccessKeyId", key)
		return
	}
	if bucket != s.Bucket {
		writeError(w, r, http.StatusNotFound, "NoSuchBucket", key)
		return
	}
	switch {
	case key == "" && r.Method == http.MethodHead:
	case key == "" && r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2":
		s.list(w, r)
	case key == "":
		writeError(w, r, http.StatusNotImplemented, "NotImplemented", key)
	case r.Method == http.MethodHead, r.Method == http.MethodGet:
		s.get(w, r, key)
	case r.Method == http.MethodPut && r.Header.Get("X-Amz-Copy-Source") != "":
		s.copy(w, r, key)
	case r.Method == http.MethodPut:
		s.put(w, r, key)
	case r.Method == http.MethodDelete:
		s.mu.Lock()
		delete(s.objects, key)
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, r, http.StatusNotImplemented, "NotImplemented", key)
	}
}

func (s *Server) get(w http.ResponseWriter, r *http.Request, key string) {
	s.mu.Lock()
	o, ok := s.objects[key]
	s.mu.Unlock()
	if !ok {
		writeError(w, r, http.StatusNotFound, "NoSuchKey", key)
		return
	}
	size := int64(len(o.data))
	start, end := int64(0), size-1
	status := http.StatusOK
	if rg := r.Header.Get("Range"); rg != "" && r.Method == http.MethodGet {
		first, last, ok := strings.Cut(strings.TrimPrefix(rg, "bytes="), "-")
		var err error
		if start, err = strconv.ParseInt(first, 10, 64); !ok || err != nil || start >= size {
			writeError(w, r, http.StatusRequestedRangeNotSatisfiable, "InvalidRange", key)
			return
		}
		if last != "" {
			if end, err = strconv.ParseInt(last, 10, 64); err != nil || end < start {
				writeError(w, r, http.StatusRequestedRangeNotSatisfiable, "InvalidRange", key)
				return
			}
			end = min(end, size-1)
		}
		status = http.StatusPartialContent
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, size))
	}
	h := w.Header()
	h.Set("Content-Length", strconv.FormatInt(end-start+1, 10))
	h.Set("Content-Type", "application/octet-stream")
	h.Set("ETag", etag(o.data))
	h.Set("Last-Modified", o.mod.UTC().Format(http.TimeFormat))
	w.WriteHeader(status)
	if r.Method == http.MethodGet {
		_, _ = w.Write(o.data[start : end+1])
	}
}

func (s *Server) put(w http.ResponseWriter, r *http.Request, key string) {
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "IncompleteBody", key)
		return
	}
	switch claimed := r.Header.Get("X-Amz-Content-Sha256"); {
	case strings.HasPrefix(claimed, "STREAMING-"):
		// aws-chunked bodies are not decoded here; the store never sends one.
		writeError(w, r, http.StatusNotImplemented, "NotImplemented", key)
		return
	case claimed != "UNSIGNED-PAYLOAD":
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != claimed {
			writeError(w, r, http.StatusBadRequest, "XAmzContentSHA256Mismatch", key)
			return
		}
	}
	s.mu.Lock()
	s.objects[key] = object{data: data, mod: s.now()}
	s.mu.Unlock()
	w.Header().Set("ETag", etag(data))
	w.WriteHeader(http.StatusOK)
}

func (s *Server) copy(w http.ResponseWriter, r *http.Request, key string) {
	src, err := url.PathUnescape(r.Header.Get("X-Amz-Copy-Source"))
	srcBucket, srcKey, _ := strings.Cut(strings.TrimPrefix(src, "/"), "/")
	s.mu.Lock()
	o, ok := s.objects[srcKey]
	if ok && err == nil && srcBucket == s.Bucket {
		o.mod = s.now()
		s.objects[key] = o
	}
	s.mu.Unlock()
	if !ok || err != nil || srcBucket != s.Bucket {
		writeError(w, r, http.StatusNotFound, "NoSuchKey", srcKey)
		return
	}
	writeXML(w, http.StatusOK, struct {
		XMLName      xml.Name `xml:"CopyObjectResult"`
		ETag         string
		LastModified string
	}{ETag: etag(o.data), LastModified: o.mod.UTC().Format(time.RFC3339)})
}

type listEntry struct {
	Key          string
	LastModified string
	ETag         string
	Size         int64
	StorageClass string
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	prefix, after := q.Get("prefix"), q.Get("continuation-token")
	if sa := q.Get("start-after"); sa > after {
		after = sa
	}
	var entries []listEntry
	truncated := false
	for _, k := range s.Keys() {
		if !strings.HasPrefix(k, prefix) || k <= after {
			continue
		}
		if len(entries) == s.PageSize {
			truncated = true
			break
		}
		s.mu.Lock()
		o, ok := s.objects[k]
		s.mu.Unlock()
		if ok {
			entries = append(entries, listEntry{Key: k, LastModified: o.mod.UTC().Format("2006-01-02T15:04:05.000Z"),
				ETag: etag(o.data), Size: int64(len(o.data)), StorageClass: "STANDARD"})
		}
	}
	res := struct {
		XMLName               xml.Name `xml:"http://s3.amazonaws.com/doc/2006-03-01/ ListBucketResult"`
		Name                  string
		Prefix                string
		KeyCount              int
		MaxKeys               int
		IsTruncated           bool
		ContinuationToken     string `xml:",omitempty"`
		NextContinuationToken string `xml:",omitempty"`
		Contents              []listEntry
	}{Name: s.Bucket, Prefix: prefix, KeyCount: len(entries), MaxKeys: s.PageSize, IsTruncated: truncated,
		ContinuationToken: q.Get("continuation-token"), Contents: entries}
	if truncated {
		res.NextContinuationToken = entries[len(entries)-1].Key
	}
	writeXML(w, http.StatusOK, res)
}

func etag(data []byte) string {
	sum := sha256.Sum256(data)
	return `"` + hex.EncodeToString(sum[:16]) + `"`
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, key string) {
	if r.Method == http.MethodHead {
		w.WriteHeader(status)
		return
	}
	writeXML(w, status, struct {
		XMLName   xml.Name `xml:"Error"`
		Code      string
		Message   string
		Key       string `xml:",omitempty"`
		RequestID string `xml:"RequestId"`
	}{Code: code, Message: code, Key: key, RequestID: "fake"})
}

func writeXML(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, xml.Header)
	_ = xml.NewEncoder(w).Encode(v)
}
