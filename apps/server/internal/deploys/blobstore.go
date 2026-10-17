// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package deploys

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Blob errors.
var (
	ErrBlobNotFound = errors.New("blob not found")
	ErrHashMismatch = errors.New("blob hash mismatch")
	// ErrInvalidHash means a hash is not 64 lowercase hex characters.
	ErrInvalidHash = errors.New("invalid blob hash")
)

// BlobStore holds file bodies by SHA-256.
type BlobStore interface {
	Has(ctx context.Context, sha256 string) (bool, error)
	// Put streams r to a temp file, fsyncs, verifies SHA-256 and size, and
	// renames into place. A mismatch is ErrHashMismatch and stores nothing.
	// Putting an existing blob succeeds.
	Put(ctx context.Context, sha256 string, size int64, r io.Reader) error
	// Open returns the blob for reading with Seek (for Range). A missing
	// blob is ErrBlobNotFound.
	Open(ctx context.Context, sha256 string) (io.ReadSeekCloser, error)
	Delete(ctx context.Context, sha256 string) error
}

// FSBlobStore keeps blobs as files under dir/sha256/ab/cdef…, so a blob is
// either complete or absent.
type FSBlobStore struct {
	dir string
	// sync makes a file or directory durable; tests observe it.
	sync func(*os.File) error
}

var _ BlobStore = (*FSBlobStore)(nil)

// NewFSBlobStore returns a BlobStore rooted at dir (normally
// <data dir>/blobs), created on first Put.
func NewFSBlobStore(dir string) *FSBlobStore {
	return &FSBlobStore{dir: dir, sync: (*os.File).Sync}
}

// ValidHash reports whether h is a SHA-256 as 64 lowercase hex characters.
func ValidHash(h string) bool {
	if len(h) != 64 {
		return false
	}
	for i := range len(h) {
		if c := h[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func (s *FSBlobStore) path(h string) (string, error) {
	if !ValidHash(h) {
		return "", ErrInvalidHash
	}
	return filepath.Join(s.dir, "sha256", h[:2], h[2:]), nil
}

// Has reports whether the blob exists.
func (s *FSBlobStore) Has(_ context.Context, h string) (bool, error) {
	p, err := s.path(h)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(p)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	default:
		return false, fmt.Errorf("stat blob: %w", err)
	}
}

// Put stores r as blob h. An existing blob is not rewritten, but r is still
// read and verified, so success always means the caller sent the content.
//
// Put does the same work whether or not the blob exists: write a temp file,
// fsync it, then rename it into place or remove it, and fsync the
// directory. Skipping the write and syncs for an existing blob would make
// that upload measurably faster, telling one owner from the timing that
// another owner already stored the same content.
func (s *FSBlobStore) Put(ctx context.Context, h string, size int64, r io.Reader) error {
	p, err := s.path(h)
	if err != nil {
		return err
	}
	if size < 0 {
		return ErrHashMismatch
	}
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create blob directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return fmt.Errorf("create temp blob: %w", err)
	}
	renamed := false
	defer func() {
		_ = tmp.Close()
		if !renamed {
			_ = os.Remove(tmp.Name())
		}
	}()
	if err := verify(h, size, r, tmp); err != nil {
		return err
	}
	if err := s.sync(tmp); err != nil {
		return fmt.Errorf("sync blob: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close blob: %w", err)
	}
	exists, err := s.Has(ctx, h)
	if err != nil {
		return err
	}
	if exists {
		// Keep the stored file; the deferred remove drops the copy.
		if err := os.Remove(tmp.Name()); err != nil {
			return fmt.Errorf("remove temp blob: %w", err)
		}
	} else {
		if err := os.Rename(tmp.Name(), p); err != nil {
			return fmt.Errorf("rename blob: %w", err)
		}
		renamed = true
	}
	return s.syncDir(dir)
}

// verify copies r to w, failing with ErrHashMismatch unless r holds exactly
// size bytes hashing to h.
func verify(h string, size int64, r io.Reader, w io.Writer) error {
	sum := sha256.New()
	n, err := io.Copy(io.MultiWriter(w, sum), io.LimitReader(r, size+1))
	if err != nil {
		return fmt.Errorf("read blob: %w", err)
	}
	if n != size || hex.EncodeToString(sum.Sum(nil)) != h {
		return ErrHashMismatch
	}
	return nil
}

// syncDir makes a rename or remove in dir durable.
func (s *FSBlobStore) syncDir(dir string) error {
	d, err := os.Open(filepath.Clean(dir))
	if err != nil {
		return fmt.Errorf("open blob directory: %w", err)
	}
	defer func() { _ = d.Close() }()
	if err := s.sync(d); err != nil && !errors.Is(err, errors.ErrUnsupported) {
		return fmt.Errorf("sync blob directory: %w", err)
	}
	return nil
}

// Open opens blob h for reading.
func (s *FSBlobStore) Open(_ context.Context, h string) (io.ReadSeekCloser, error) {
	p, err := s.path(h)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(filepath.Clean(p))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrBlobNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("open blob: %w", err)
	}
	return f, nil
}

// Delete removes blob h; deleting a missing blob succeeds.
func (s *FSBlobStore) Delete(_ context.Context, h string) error {
	p, err := s.path(h)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("delete blob: %w", err)
	}
	return nil
}
