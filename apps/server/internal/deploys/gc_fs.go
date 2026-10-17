// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package deploys

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var _ BlobFiles = (*FSBlobStore)(nil)

// WalkFiles reports every blob file and temporary upload file under the
// store last modified before cutoff. Other files are left alone.
func (s *FSBlobStore) WalkFiles(ctx context.Context, cutoff time.Time, fn func(BlobFile) error) error {
	root := filepath.Join(s.dir, "sha256")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		prefix := filepath.Base(filepath.Dir(p))
		name := d.Name()
		var f BlobFile
		switch {
		case strings.HasPrefix(name, ".upload-"):
		case ValidHash(prefix+name) && len(prefix) == 2:
			f.SHA256 = prefix + name
		default:
			return nil
		}
		info, err := d.Info()
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.ModTime().Before(cutoff) {
			return nil
		}
		f.Size, f.path = info.Size(), p
		return fn(f)
	})
	if err != nil {
		return fmt.Errorf("walk blob files: %w", err)
	}
	return nil
}

// RemoveFile removes a file WalkFiles reported.
func (s *FSBlobStore) RemoveFile(ctx context.Context, f BlobFile) error {
	if f.SHA256 != "" {
		return s.Delete(ctx, f.SHA256)
	}
	if err := os.Remove(f.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove temporary upload: %w", err)
	}
	return nil
}
