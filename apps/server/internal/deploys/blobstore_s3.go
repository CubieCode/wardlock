// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package deploys

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// MaxS3BlobSize is the largest blob S3BlobStore stores: the limit of one
// PutObject and one CopyObject.
const MaxS3BlobSize = 5 << 30

// S3 key layout under the prefix: blobs at sha256/ab/cdef…, as on disk,
// and temporary uploads at uploads/<random>.
const (
	s3BlobDir   = "sha256/"
	s3UploadDir = "uploads/"
)

// S3Config configures an S3BlobStore.
type S3Config struct {
	// Endpoint is the service's base URL, such as https://s3.eu-west-1.amazonaws.com.
	Endpoint *url.URL
	Bucket   string
	Region   string
	// Prefix is prepended to every key; "" or ending in "/".
	Prefix string
	// PathStyle and VirtualHosted force the addressing style; with
	// neither, the client picks (virtual-hosted for AWS, path otherwise).
	PathStyle     bool
	VirtualHosted bool
	Creds         *credentials.Credentials
	// Transport, if set, carries every request (tests).
	Transport http.RoundTripper
	// AppName and AppVersion name the application in the User-Agent.
	AppName, AppVersion string
	// Log records which upload path the probe chose; nil discards it.
	Log *slog.Logger
}

// S3BlobStore keeps blobs in an S3-compatible bucket. A blob is either
// complete at its key or absent. Every upload is signed with the blob's
// SHA-256 as its payload hash. When a one-time probe shows the service
// rejects a body that does not match that hash, Put writes straight to the
// blob's key; otherwise it uploads to a temporary key, verifies it and only
// then copies it into place.
type S3BlobStore struct {
	c      *minio.Core
	bucket string
	prefix string
	log    *slog.Logger

	// probeMu guards the probe; probed is set once it has an answer, and
	// direct is that answer.
	probeMu sync.Mutex
	probed  bool
	direct  bool
}

var (
	_ BlobStore = (*S3BlobStore)(nil)
	_ BlobFiles = (*S3BlobStore)(nil)
)

// NewS3BlobStore returns a BlobStore over c. It makes no request; Check
// reaches the bucket.
func NewS3BlobStore(c S3Config) (*S3BlobStore, error) {
	if c.Endpoint == nil {
		return nil, errors.New("s3 blob store: no endpoint")
	}
	lookup := minio.BucketLookupAuto
	switch {
	case c.PathStyle:
		lookup = minio.BucketLookupPath
	case c.VirtualHosted:
		lookup = minio.BucketLookupDNS
	}
	core, err := minio.NewCore(c.Endpoint.Host, &minio.Options{
		Creds:        c.Creds,
		Secure:       c.Endpoint.Scheme == "https",
		Region:       c.Region,
		BucketLookup: lookup,
		Transport:    c.Transport,
	})
	if err != nil {
		return nil, fmt.Errorf("s3 blob store: %w", err)
	}
	if c.AppName != "" {
		core.SetAppInfo(c.AppName, c.AppVersion)
	}
	log := c.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &S3BlobStore{c: core, bucket: c.Bucket, prefix: c.Prefix, log: log}, nil
}

func (s *S3BlobStore) key(h string) (string, error) {
	if !ValidHash(h) {
		return "", ErrInvalidHash
	}
	return s.prefix + s3BlobDir + h[:2] + "/" + h[2:], nil
}

// notFound reports whether err is S3's answer for a missing object.
func notFound(err error) bool {
	r := minio.ToErrorResponse(err)
	return r.StatusCode == http.StatusNotFound || r.Code == minio.NoSuchKey
}

// Check reports whether the bucket is reachable with the configured
// credentials, for readiness.
func (s *S3BlobStore) Check(ctx context.Context) error {
	ok, err := s.c.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("blob bucket: %w", err)
	}
	if !ok {
		return fmt.Errorf("blob bucket %q does not exist", s.bucket)
	}
	return nil
}

// Has reports whether the blob exists, by HeadObject.
func (s *S3BlobStore) Has(ctx context.Context, h string) (bool, error) {
	k, err := s.key(h)
	if err != nil {
		return false, err
	}
	_, err = s.c.StatObject(ctx, s.bucket, k, minio.StatObjectOptions{})
	switch {
	case err == nil:
		return true, nil
	case notFound(err):
		return false, nil
	default:
		return false, fmt.Errorf("stat blob: %w", err)
	}
}

// Put stores r as blob h, signing the upload with h as its payload hash.
// On a service that verifies that hash (see uploadsDirect) it is one
// PutObject to the blob's key. Otherwise it uploads to a temporary key,
// copies the temporary object to the blob's key and deletes it, so content
// the service did not check never reaches a blob's key.
//
// Either way the bytes are hashed as they go, and the read that would
// complete the body fails instead when they do not hash to h or r holds
// more than size bytes, so no upload of the wrong content completes.
//
// As with FSBlobStore, an existing blob takes the same requests as a new
// one (the upload or copy rewrites identical bytes), so timing does not
// reveal that another owner stored the content.
func (s *S3BlobStore) Put(ctx context.Context, h string, size int64, r io.Reader) error {
	k, err := s.key(h)
	if err != nil {
		return err
	}
	if size < 0 {
		return ErrHashMismatch
	}
	if size > MaxS3BlobSize {
		return fmt.Errorf("blob of %d bytes exceeds the S3 limit of %d", size, int64(MaxS3BlobSize))
	}
	vr := &verifyingReader{r: r, want: h, left: size, sum: sha256.New()}
	if size == 0 {
		// No read completes an empty body, so check it first.
		if err := vr.complete(); err != nil {
			return err
		}
	}
	direct, err := s.uploadsDirect(ctx)
	if err != nil {
		return err
	}
	if direct {
		return s.upload(ctx, k, h, size, vr)
	}
	tmp, err := s.uploadKey()
	if err != nil {
		return err
	}
	// The temporary key goes whatever happens: copied, rejected, or
	// partly stored by a failed upload.
	defer s.removeTemp(ctx, tmp)
	if err := s.upload(ctx, tmp, h, size, vr); err != nil {
		return err
	}
	if _, err := s.c.CopyObject(ctx, s.bucket, tmp, s.bucket, k, nil,
		minio.CopySrcOptions{}, minio.PutObjectOptions{}); err != nil {
		return fmt.Errorf("copy blob: %w", err)
	}
	return nil
}

// upload is one PutObject of vr to key, signed with h.
func (s *S3BlobStore) upload(ctx context.Context, key, h string, size int64, vr *verifyingReader) error {
	_, err := s.c.PutObject(ctx, s.bucket, key, vr, size, "", h, minio.PutObjectOptions{
		DisableContentSha256: true, // sign with h rather than stream-sign
		ContentType:          "application/octet-stream",
	})
	switch {
	case vr.mismatch, err != nil && checksumRejected(err):
		return ErrHashMismatch
	case err != nil:
		return fmt.Errorf("upload blob: %w", err)
	case !vr.done:
		// The service took fewer bytes than size; never expected.
		return fmt.Errorf("upload blob: body not fully sent")
	}
	return nil
}

// checksumRejectedCodes are the S3 error codes for a body that does not
// match its signed payload hash.
var checksumRejectedCodes = []string{"XAmzContentSHA256Mismatch", "BadDigest"}

// checksumRejected reports whether err is the service refusing a body that
// does not match its payload hash.
func checksumRejected(err error) bool {
	r := minio.ToErrorResponse(err)
	return r.StatusCode == http.StatusBadRequest && slices.Contains(checksumRejectedCodes, r.Code)
}

// probeBody is what the probe uploads, signed with the hash of other
// bytes.
const probeBody = "wardlock payload hash probe\n"

// uploadsDirect reports whether Put may write straight to a blob's key: the
// service rejects a body that does not match its signed payload hash. The
// first call asks the service once, by uploading probeBody to a temporary
// key with a deliberately wrong hash, and the answer holds for the life of
// the process. A rejection with a checksum error means direct; an accepted
// upload (deleted again) or any other client error means the temporary-key
// path. Authorisation, server and network errors give no answer: Put
// fails, and the next Put probes again. The provider's name never decides.
// Only an accepted probe is deleted; one stored by a failed request is
// collected by GC after the grace period.
func (s *S3BlobStore) uploadsDirect(ctx context.Context) (bool, error) {
	s.probeMu.Lock()
	defer s.probeMu.Unlock()
	if s.probed {
		return s.direct, nil
	}
	tmp, err := s.uploadKey()
	if err != nil {
		return false, err
	}
	wrong := sha256.Sum256([]byte("not the probe body"))
	_, err = s.c.PutObject(ctx, s.bucket, tmp, strings.NewReader(probeBody), int64(len(probeBody)), "",
		hex.EncodeToString(wrong[:]), minio.PutObjectOptions{DisableContentSha256: true, ContentType: "application/octet-stream"})
	resp := minio.ToErrorResponse(err)
	switch {
	case err == nil:
		s.removeTemp(ctx, tmp)
		s.direct = false
		s.log.InfoContext(ctx, "s3 upload path", slog.String("path", "temporary key"),
			slog.String("reason", "the service accepted a body that did not match its payload hash"))
	case checksumRejected(err):
		s.direct = true
		s.log.InfoContext(ctx, "s3 upload path", slog.String("path", "direct"),
			slog.String("reason", "the service rejects a body that does not match its payload hash"))
	case resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusUnauthorized:
		// Some other refusal: keep the path that verifies by itself.
		s.direct = false
		s.log.InfoContext(ctx, "s3 upload path", slog.String("path", "temporary key"),
			slog.String("reason", "the payload hash probe was refused without a checksum error"),
			slog.Int("status", resp.StatusCode), slog.String("code", resp.Code))
	default:
		return false, fmt.Errorf("probe upload path: %w", err)
	}
	s.probed = true
	return s.direct, nil
}

// uploadKey returns a fresh temporary upload key.
func (s *S3BlobStore) uploadKey() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("temporary upload key: %w", err)
	}
	return s.prefix + s3UploadDir + hex.EncodeToString(b[:]), nil
}

// removeTemp deletes a temporary upload, even when ctx has ended; one left
// behind is collected by GC after the grace period.
func (s *S3BlobStore) removeTemp(ctx context.Context, key string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	_ = s.c.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
}

// verifyingReader passes exactly left bytes of r through while hashing
// them. The Read that would complete the body first checks that r ends
// there and that the bytes hash to want; if not, it withholds them and
// fails with ErrHashMismatch (setting mismatch), so the request never
// carries a whole body of the wrong content. A short r is a mismatch too.
type verifyingReader struct {
	r        io.Reader
	want     string
	left     int64
	sum      hash.Hash
	mismatch bool
	// done is set once every byte has been read and verified.
	done bool
}

func (v *verifyingReader) Read(p []byte) (int, error) {
	if v.done {
		return 0, io.EOF
	}
	if v.mismatch {
		return 0, ErrHashMismatch
	}
	if int64(len(p)) > v.left {
		p = p[:v.left]
	}
	n, err := v.r.Read(p)
	v.sum.Write(p[:n])
	v.left -= int64(n)
	if v.left == 0 {
		if err := v.complete(); err != nil {
			return 0, err
		}
		return n, nil
	}
	if errors.Is(err, io.EOF) {
		v.mismatch = true
		return 0, ErrHashMismatch
	}
	return n, err
}

// complete checks, once left bytes are read, that r ends and the hash
// matches.
func (v *verifyingReader) complete() error {
	var b [1]byte
	for {
		n, err := v.r.Read(b[:])
		if n > 0 {
			v.mismatch = true
			return ErrHashMismatch
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read blob: %w", err)
		}
	}
	if hex.EncodeToString(v.sum.Sum(nil)) != v.want {
		v.mismatch = true
		return ErrHashMismatch
	}
	v.done = true
	return nil
}

// Open returns blob h, of size bytes, as a reader that fetches what is
// read with ranged GETs, starting one only on the first Read after a Seek.
// Open itself makes no request, so serving a HEAD costs none and a Range
// never downloads the whole object; a missing blob is ErrBlobNotFound
// from the first Read.
func (s *S3BlobStore) Open(ctx context.Context, h string, size int64) (io.ReadSeekCloser, error) {
	k, err := s.key(h)
	if err != nil {
		return nil, err
	}
	if size < 0 {
		return nil, fmt.Errorf("open blob: negative size")
	}
	return &s3Reader{ctx: ctx, s: s, key: k, size: size}, nil
}

// s3Reader reads one object. body, when open, is a GET response positioned
// at off.
type s3Reader struct {
	ctx  context.Context
	s    *S3BlobStore
	key  string
	size int64
	off  int64
	body io.ReadCloser
}

func (r *s3Reader) Read(p []byte) (int, error) {
	if r.off >= r.size {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	if r.body == nil {
		var opts minio.GetObjectOptions
		if r.off > 0 {
			if err := opts.SetRange(r.off, 0); err != nil {
				return 0, fmt.Errorf("read blob: %w", err)
			}
		}
		body, _, _, err := r.s.c.GetObject(r.ctx, r.s.bucket, r.key, opts)
		if err != nil {
			if notFound(err) {
				return 0, fmt.Errorf("read blob: %w", ErrBlobNotFound)
			}
			return 0, fmt.Errorf("read blob: %w", err)
		}
		r.body = body
	}
	n, err := r.body.Read(p)
	r.off += int64(n)
	if errors.Is(err, io.EOF) && r.off < r.size {
		err = io.ErrUnexpectedEOF
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return n, fmt.Errorf("read blob: %w", err)
	}
	return n, err
}

func (r *s3Reader) Seek(offset int64, whence int) (int64, error) {
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = r.off + offset
	case io.SeekEnd:
		abs = r.size + offset
	default:
		return 0, errors.New("seek blob: invalid whence")
	}
	if abs < 0 {
		return 0, errors.New("seek blob: negative position")
	}
	if abs != r.off {
		r.closeBody()
		r.off = abs
	}
	return abs, nil
}

func (r *s3Reader) closeBody() {
	if r.body != nil {
		_ = r.body.Close()
		r.body = nil
	}
}

func (r *s3Reader) Close() error {
	r.closeBody()
	return nil
}

// Delete removes blob h; deleting a missing blob succeeds.
func (s *S3BlobStore) Delete(ctx context.Context, h string) error {
	k, err := s.key(h)
	if err != nil {
		return err
	}
	if err := s.c.RemoveObject(ctx, s.bucket, k, minio.RemoveObjectOptions{}); err != nil && !notFound(err) {
		return fmt.Errorf("delete blob: %w", err)
	}
	return nil
}

// WalkFiles reports every blob object and temporary upload under the
// prefix last modified before cutoff. Other keys are left alone.
func (s *S3BlobStore) WalkFiles(ctx context.Context, cutoff time.Time, fn func(BlobFile) error) error {
	for _, dir := range []string{s3BlobDir, s3UploadDir} {
		if err := s.walk(ctx, s.prefix+dir, cutoff, fn); err != nil {
			return fmt.Errorf("walk blob objects: %w", err)
		}
	}
	return nil
}

func (s *S3BlobStore) walk(ctx context.Context, prefix string, cutoff time.Time, fn func(BlobFile) error) error {
	// Cancelling stops the listing goroutine when fn fails.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	for obj := range s.c.Client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if obj.Err != nil {
			return obj.Err
		}
		if !obj.LastModified.Before(cutoff) {
			continue
		}
		rel := strings.TrimPrefix(obj.Key, s.prefix)
		f := BlobFile{Size: obj.Size, path: obj.Key}
		switch {
		case strings.HasPrefix(rel, s3UploadDir) && !strings.Contains(rel[len(s3UploadDir):], "/"):
		case len(rel) == len(s3BlobDir)+65 && rel[len(s3BlobDir)+2] == '/':
			h := rel[len(s3BlobDir):len(s3BlobDir)+2] + rel[len(s3BlobDir)+3:]
			if !ValidHash(h) {
				continue
			}
			f.SHA256 = h
		default:
			continue
		}
		if err := fn(f); err != nil {
			return err
		}
	}
	return ctx.Err()
}

// RemoveFile removes an object WalkFiles reported.
func (s *S3BlobStore) RemoveFile(ctx context.Context, f BlobFile) error {
	if f.SHA256 != "" {
		return s.Delete(ctx, f.SHA256)
	}
	if err := s.c.RemoveObject(ctx, s.bucket, f.path, minio.RemoveObjectOptions{}); err != nil && !notFound(err) {
		return fmt.Errorf("remove temporary upload: %w", err)
	}
	return nil
}
