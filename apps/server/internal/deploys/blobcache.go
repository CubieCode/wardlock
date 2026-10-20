// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package deploys

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// CachedBlobs is what a CachingBlobStore wraps: a blob store and its files.
type CachedBlobs interface {
	BlobStore
	BlobFiles
}

// BlobCacheConfig configures a CachingBlobStore.
type BlobCacheConfig struct {
	// Blobs is the store the cache reads through, normally S3BlobStore.
	Blobs CachedBlobs
	// Dir holds the cache; MaxBytes caps what it keeps.
	Dir      string
	MaxBytes int64
	Log      *slog.Logger
	// Now stamps cached files as they are used; nil is time.Now.
	Now func() time.Time
}

// Cache layout under Dir: blobs at sha256/ab/cdef…, as in FSBlobStore, and
// fills in progress under fill/.
const (
	cacheBlobDir = "sha256"
	cacheFillDir = "fill"
)

// CachingBlobStore keeps local copies of the blobs it serves, so a warm
// blob costs the wrapped store no request. Copies are content-addressed,
// so one can never be stale, only present or absent.
//
// A miss reads through the wrapped store and, when the read starts at the
// beginning and runs to the end, writes the bytes to a temporary file as
// they pass; the file is kept only if they hash to the blob's SHA-256 at
// its size. Only one fill per blob runs at a time; concurrent misses read
// through without filling. A read that seeks (a Range request) never
// fills, so it fetches only the range it asked for.
//
// Blobs used least recently are evicted beyond MaxBytes. Use is tracked
// in memory and stamped on each file's modification time, from which the
// order is rebuilt when the store starts. A copy found missing (the
// directory was wiped) is a miss and is filled again. Delete and
// RemoveFile remove the copy along with the blob. Writes (Has, Put) go
// straight to the wrapped store.
type CachingBlobStore struct {
	inner CachedBlobs
	dir   string
	max   int64
	log   *slog.Logger
	now   func() time.Time

	mu sync.Mutex
	// lru holds *cacheEntry, most recently used first; index finds them.
	lru   *list.List
	index map[string]*list.Element
	used  int64
	// fills are the fills in progress, by hash.
	fills map[string]*cacheFill
}

type cacheEntry struct {
	sha256 string
	size   int64
}

var (
	_ BlobStore = (*CachingBlobStore)(nil)
	_ BlobFiles = (*CachingBlobStore)(nil)
)

// NewCachingBlobStore returns a cache over c.Blobs in c.Dir. It creates the
// directory, removes fills a previous process left and indexes the copies
// already there, oldest use first, evicting beyond c.MaxBytes.
func NewCachingBlobStore(c BlobCacheConfig) (*CachingBlobStore, error) {
	if c.MaxBytes < 1 {
		return nil, errors.New("blob cache: MaxBytes must be at least 1")
	}
	if c.Log == nil {
		c.Log = slog.New(slog.DiscardHandler)
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	s := &CachingBlobStore{
		inner: c.Blobs, dir: c.Dir, max: c.MaxBytes, log: c.Log, now: c.Now,
		lru: list.New(), index: map[string]*list.Element{}, fills: map[string]*cacheFill{},
	}
	if err := os.RemoveAll(filepath.Join(c.Dir, cacheFillDir)); err != nil {
		return nil, fmt.Errorf("blob cache: remove old fills: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(c.Dir, cacheBlobDir), 0o700); err != nil {
		return nil, fmt.Errorf("blob cache: %w", err)
	}
	if err := s.rebuild(); err != nil {
		return nil, fmt.Errorf("blob cache: %w", err)
	}
	return s, nil
}

// rebuild indexes the copies on disk by modification time.
func (s *CachingBlobStore) rebuild() error {
	type found struct {
		entry cacheEntry
		mod   time.Time
	}
	var all []found
	root := filepath.Join(s.dir, cacheBlobDir)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		prefix, rest, ok := strings.Cut(filepath.ToSlash(rel), "/")
		h := prefix + rest
		if !ok || len(prefix) != 2 || !ValidHash(h) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		all = append(all, found{cacheEntry{sha256: h, size: info.Size()}, info.ModTime()})
		return nil
	})
	if err != nil {
		return fmt.Errorf("index cached blobs: %w", err)
	}
	slices.SortFunc(all, func(a, b found) int { return a.mod.Compare(b.mod) })
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range all {
		s.index[f.entry.sha256] = s.lru.PushFront(&f.entry)
		s.used += f.entry.size
	}
	s.evictLocked()
	return nil
}

func (s *CachingBlobStore) path(h string) string {
	return filepath.Join(s.dir, cacheBlobDir, h[:2], h[2:])
}

// Has asks the wrapped store.
func (s *CachingBlobStore) Has(ctx context.Context, h string) (bool, error) {
	return s.inner.Has(ctx, h)
}

// Put writes to the wrapped store; the cache fills when the blob is read.
func (s *CachingBlobStore) Put(ctx context.Context, h string, size int64, r io.Reader) error {
	return s.inner.Put(ctx, h, size, r)
}

// Open returns the cached copy of blob h, or reads it through the wrapped
// store, filling the cache if the read covers the whole blob.
func (s *CachingBlobStore) Open(ctx context.Context, h string, size int64) (io.ReadSeekCloser, error) {
	if !ValidHash(h) {
		return nil, ErrInvalidHash
	}
	if f := s.hit(h, size); f != nil {
		return f, nil
	}
	rc, err := s.inner.Open(ctx, h, size)
	if err != nil {
		return nil, err
	}
	if size < 1 || size > s.max {
		return rc, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, busy := s.fills[h]; busy {
		return rc, nil
	}
	fill := &cacheFill{s: s, rc: rc, sha256: h, size: size, sum: sha256.New()}
	s.fills[h] = fill
	return fill, nil
}

// hit opens the cached copy of h, marking it used, or returns nil. A copy
// that is gone or the wrong size is dropped.
func (s *CachingBlobStore) hit(h string, size int64) *os.File {
	s.mu.Lock()
	el, ok := s.index[h]
	if ok {
		s.lru.MoveToFront(el)
	}
	s.mu.Unlock()
	if !ok {
		return nil
	}
	p := s.path(h)
	f, err := os.Open(filepath.Clean(p))
	if err == nil {
		info, serr := f.Stat()
		if serr == nil && info.Size() == size {
			now := s.now()
			_ = os.Chtimes(p, now, now)
			return f
		}
		_ = f.Close()
		_ = os.Remove(p)
	} else if !errors.Is(err, fs.ErrNotExist) {
		s.log.Warn("open cached blob", slog.String("sha256", h), slog.Any("error", err))
	}
	s.forget(h)
	return nil
}

// forget drops h from the index without touching the disk.
func (s *CachingBlobStore) forget(h string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if el, ok := s.index[h]; ok {
		s.used -= el.Value.(*cacheEntry).size
		s.lru.Remove(el)
		delete(s.index, h)
	}
}

// evictLocked removes least recently used copies until the cache fits.
func (s *CachingBlobStore) evictLocked() {
	for s.used > s.max {
		el := s.lru.Back()
		e := el.Value.(*cacheEntry)
		if err := os.Remove(s.path(e.sha256)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			s.log.Warn("evict cached blob", slog.String("sha256", e.sha256), slog.Any("error", err))
		}
		s.used -= e.size
		s.lru.Remove(el)
		delete(s.index, e.sha256)
	}
}

// keep moves a verified fill into place and indexes it, unless the blob
// was deleted while it filled.
func (s *CachingBlobStore) keep(f *cacheFill, tmp string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f.cancelled {
		return errors.New("blob deleted during fill")
	}
	p := s.path(f.sha256)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		return err
	}
	now := s.now()
	_ = os.Chtimes(p, now, now)
	if el, ok := s.index[f.sha256]; ok {
		s.used -= el.Value.(*cacheEntry).size
		s.lru.Remove(el)
	}
	s.index[f.sha256] = s.lru.PushFront(&cacheEntry{sha256: f.sha256, size: f.size})
	s.used += f.size
	s.evictLocked()
	return nil
}

// drop removes the copy of h and stops a fill of it in progress.
func (s *CachingBlobStore) drop(h string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f, ok := s.fills[h]; ok {
		f.cancelled = true
	}
	if el, ok := s.index[h]; ok {
		s.used -= el.Value.(*cacheEntry).size
		s.lru.Remove(el)
		delete(s.index, h)
	}
	if err := os.Remove(s.path(h)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove cached blob: %w", err)
	}
	return nil
}

// Delete removes the cached copy and then the blob.
func (s *CachingBlobStore) Delete(ctx context.Context, h string) error {
	if !ValidHash(h) {
		return ErrInvalidHash
	}
	if err := s.drop(h); err != nil {
		return err
	}
	return s.inner.Delete(ctx, h)
}

// WalkFiles lists the wrapped store's files; the cache holds none of its
// own.
func (s *CachingBlobStore) WalkFiles(ctx context.Context, cutoff time.Time, fn func(BlobFile) error) error {
	return s.inner.WalkFiles(ctx, cutoff, fn)
}

// RemoveFile removes a file of the wrapped store and any cached copy.
func (s *CachingBlobStore) RemoveFile(ctx context.Context, f BlobFile) error {
	if f.SHA256 != "" {
		if err := s.drop(f.SHA256); err != nil {
			return err
		}
	}
	return s.inner.RemoveFile(ctx, f)
}

// cacheFill reads a blob through the wrapped store, copying what it reads
// to a temporary file while the reads run in order from the start. It
// stops filling at the first read elsewhere, a write error, Close before
// the end, or a hash mismatch; the reader itself carries on.
type cacheFill struct {
	s      *CachingBlobStore
	rc     io.ReadSeekCloser
	sha256 string
	size   int64
	sum    hash.Hash
	// off is the reader's position; written how much tmp holds.
	off, written int64
	tmp          *os.File
	// stopped ends filling; cancelled (under s.mu) means the blob was
	// deleted.
	stopped   bool
	cancelled bool
}

func (f *cacheFill) Read(p []byte) (int, error) {
	if !f.stopped && f.off != f.written {
		f.stop()
	}
	n, err := f.rc.Read(p)
	if n > 0 && !f.stopped {
		f.copy(p[:n])
	}
	f.off += int64(n)
	return n, err
}

// copy appends b to the fill, keeping it once it is complete.
func (f *cacheFill) copy(b []byte) {
	if f.tmp == nil {
		dir := filepath.Join(f.s.dir, cacheFillDir)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			f.fail(err)
			return
		}
		tmp, err := os.CreateTemp(dir, "blob-*")
		if err != nil {
			f.fail(err)
			return
		}
		f.tmp = tmp
	}
	if _, err := f.tmp.Write(b); err != nil {
		f.fail(err)
		return
	}
	f.sum.Write(b)
	f.written += int64(len(b))
	if f.written < f.size {
		return
	}
	if f.written > f.size || hex.EncodeToString(f.sum.Sum(nil)) != f.sha256 {
		f.s.log.Warn("cached blob does not match its hash", slog.String("sha256", f.sha256))
		f.stop()
		return
	}
	// Durable before it is named, so a crash never leaves a short copy.
	if err := f.tmp.Sync(); err != nil {
		f.fail(err)
		return
	}
	if err := f.tmp.Close(); err != nil {
		f.fail(err)
		return
	}
	tmp := f.tmp.Name()
	f.tmp = nil
	if err := f.s.keep(f, tmp); err != nil {
		_ = os.Remove(tmp)
		f.s.log.Info("keep cached blob", slog.String("sha256", f.sha256), slog.Any("error", err))
	}
	f.stop()
}

// fail stops filling after a cache write error, which never fails a read.
func (f *cacheFill) fail(err error) {
	f.s.log.Warn("fill blob cache", slog.String("sha256", f.sha256), slog.Any("error", err))
	f.stop()
}

// stop ends the fill, removing any partial file, and lets another start.
func (f *cacheFill) stop() {
	if f.stopped {
		return
	}
	f.stopped = true
	if f.tmp != nil {
		_ = f.tmp.Close()
		_ = os.Remove(f.tmp.Name())
		f.tmp = nil
	}
	f.s.mu.Lock()
	if f.s.fills[f.sha256] == f {
		delete(f.s.fills, f.sha256)
	}
	f.s.mu.Unlock()
}

func (f *cacheFill) Seek(offset int64, whence int) (int64, error) {
	off, err := f.rc.Seek(offset, whence)
	if err == nil {
		f.off = off
	}
	return off, err
}

func (f *cacheFill) Close() error {
	f.stop()
	return f.rc.Close()
}
