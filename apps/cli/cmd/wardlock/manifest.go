// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

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
	"runtime"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// localFile is one file of a deploy.
type localFile struct {
	// path is the manifest path: relative, forward slashes, NFC.
	path string
	// file is where it is in the deployed directory, with this
	// computer's separators.
	file   string
	sha256 string
	size   int64
}

// scan lists the files in dir (opened as r, and named root) that a deploy
// includes, in lexical order, applying the default ignore rules and
// .wardlockignore. Symbolic links and other non-regular files are skipped
// and returned in skipped; reading through r means nothing outside the
// directory is ever read.
func scan(r *os.Root, root string) (files []localFile, skipped []string, err error) {
	rules, err := r.ReadFile(ignoreFile)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, nil, fmt.Errorf("read %s: %w", ignoreFile, err)
	}
	ig, err := newIgnorer(string(rules))
	if err != nil {
		return nil, nil, err
	}
	seen := map[string]string{}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		mp, err := manifestPath(rel, os.PathSeparator)
		if err != nil {
			return err
		}
		if ig.ignored(mp, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		switch {
		case d.IsDir():
			return nil
		case !d.Type().IsRegular():
			skipped = append(skipped, mp)
			return nil
		}
		if other, dup := seen[mp]; dup {
			return fmt.Errorf("%q and %q are the same path once Unicode-normalised; rename one", other, rel)
		}
		seen[mp] = rel
		files = append(files, localFile{path: mp, file: rel})
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return files, skipped, nil
}

// manifestPath turns rel, a relative path using sep as its separator, into
// a manifest path: forward slashes, valid UTF-8, NFC (docs/SPEC.md
// "Deploys and rollback"). macOS may report names decomposed (NFD); NFC
// makes them match what browsers request.
func manifestPath(rel string, sep byte) (string, error) {
	if sep != '/' {
		rel = strings.ReplaceAll(rel, string(sep), "/")
	} else if strings.Contains(rel, `\`) {
		return "", fmt.Errorf("%q: a file name containing \\ cannot be deployed; rename it", rel)
	}
	if !utf8.ValidString(rel) {
		return "", fmt.Errorf("%q: file name is not valid UTF-8; rename it", rel)
	}
	return norm.NFC.String(rel), nil
}

// hashFiles fills in each file's SHA-256 and size, reading several files
// at once. done is called after each file.
func hashFiles(ctx context.Context, r *os.Root, files []localFile, done func(size int64)) error {
	return forEach(ctx, len(files), min(runtime.NumCPU(), 8), func(ctx context.Context, i int) error {
		f := &files[i]
		sum, size, err := hashFile(ctx, r, f.file)
		if err != nil {
			return fmt.Errorf("read %s: %w", f.path, err)
		}
		f.sha256, f.size = sum, size
		done(size)
		return nil
	})
}

func hashFile(ctx context.Context, r *os.Root, name string) (string, int64, error) {
	f, err := r.Open(name)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, ctxReader{ctx, f})
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// ctxReader stops reading once ctx ends.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (r ctxReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

// countingReader adds the bytes read to n and total.
type countingReader struct {
	r        io.Reader
	n, total *atomic.Int64
}

func (c countingReader) Read(p []byte) (int, error) {
	k, err := c.r.Read(p)
	c.n.Add(int64(k))
	c.total.Add(int64(k))
	return k, err
}
