// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package deploys

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"wardlock.nz/server/internal/store"
)

// GC defaults.
const (
	// DefaultGCGrace covers uploads in progress: a blob first stored or
	// claimed, or a pending deploy created, within it is never collected.
	DefaultGCGrace = 24 * time.Hour
	// DefaultDeployHistory is how many deploys each site keeps.
	DefaultDeployHistory = 50
	// gcBatch is how many rows one listing returns.
	gcBatch = 500
)

// BlobFiles lists and removes what a BlobStore holds on disk, so GC can
// remove bytes that no blob record describes: a crash between storing a
// blob and recording it, or an abandoned temporary upload file.
type BlobFiles interface {
	// WalkFiles calls fn for every stored file last modified before cutoff.
	WalkFiles(ctx context.Context, cutoff time.Time, fn func(BlobFile) error) error
	// RemoveFile removes a file WalkFiles reported; a missing file succeeds.
	RemoveFile(ctx context.Context, f BlobFile) error
}

// BlobFile is a file in a blob store.
type BlobFile struct {
	// SHA256 is the blob's hash, or "" for a temporary upload file.
	SHA256 string
	Size   int64
	path   string
}

// GCConfig holds a GC's dependencies.
type GCConfig struct {
	Repo  store.GC
	Blobs BlobStore
	// Files, if set, lets GC remove stored files that have no blob record.
	Files BlobFiles
	// Grace protects recent uploads and pending deploys; History is how
	// many deploys each site keeps. Zero values take the defaults.
	Grace   time.Duration
	History int
	Log     *slog.Logger
	Now     func() time.Time
}

// GC removes deploys beyond each site's history limit and then blobs that
// no deploy references. It is instance-wide: it runs from wardlockd serve
// on an interval and from `wardlockd gc`, never for one owner.
//
// A blob is deleted only inside a store transaction that re-checks it is
// unreferenced and old, and the file is removed before that transaction
// commits. So a deploy created, or an upload recorded, for the same hash
// is ordered either before the check (the blob is kept) or after the file
// is gone (the deploy lists it as missing and the owner uploads it again).
// Deleting a blob removes every owner's claim to it with the record, so no
// owner is told they hold content that is gone. Owners' claims to a blob
// that is kept are left alone: GC never changes what one owner may skip
// uploading because of another owner's deploys.
type GC struct{ c GCConfig }

// NewGC returns a GC over c.
func NewGC(c GCConfig) *GC {
	if c.Grace <= 0 {
		c.Grace = DefaultGCGrace
	}
	if c.History <= 0 {
		c.History = DefaultDeployHistory
	}
	return &GC{c: c}
}

// GCResult counts what one run removed.
type GCResult struct {
	DeploysPruned int
	BlobsDeleted  int
	// FilesDeleted counts stored files with no blob record and abandoned
	// temporary uploads.
	FilesDeleted int
	// BytesFreed is the size of every blob and file removed.
	BytesFreed int64
}

// Run prunes deploy history, then deletes unreferenced blobs, then (with
// Files) unrecorded files, and logs the result. It stops at the first
// error, returning what was removed until then.
func (g *GC) Run(ctx context.Context) (GCResult, error) {
	start := g.c.Now()
	cutoff := start.Add(-g.c.Grace)
	var res GCResult
	err := g.pruneDeploys(ctx, cutoff, &res)
	if err == nil {
		err = g.deleteBlobs(ctx, cutoff, &res)
	}
	if err == nil && g.c.Files != nil {
		err = g.deleteUnrecorded(ctx, cutoff, &res)
	}
	attrs := []any{
		slog.Int("deploys_pruned", res.DeploysPruned),
		slog.Int("blobs_deleted", res.BlobsDeleted),
		slog.Int("files_deleted", res.FilesDeleted),
		slog.Int64("bytes_freed", res.BytesFreed),
		slog.Duration("duration", g.c.Now().Sub(start)),
	}
	if err != nil && ctx.Err() != nil {
		g.c.Log.InfoContext(ctx, "garbage collection interrupted", attrs...)
		return res, err
	}
	if err != nil {
		g.c.Log.ErrorContext(ctx, "garbage collection failed", append(attrs, slog.Any("error", err))...)
		return res, err
	}
	g.c.Log.InfoContext(ctx, "garbage collection finished", attrs...)
	return res, nil
}

func (g *GC) pruneDeploys(ctx context.Context, pendingBefore time.Time, res *GCResult) error {
	var after store.DeployCursor
	for {
		batch, err := g.c.Repo.PrunableDeploys(ctx, g.c.History, pendingBefore, after, gcBatch)
		if err != nil {
			return fmt.Errorf("list prunable deploys: %w", err)
		}
		for _, d := range batch {
			deleted, err := g.c.Repo.DeletePrunableDeploy(ctx, d.ID, pendingBefore)
			if err != nil {
				return fmt.Errorf("prune deploy: %w", err)
			}
			if deleted {
				res.DeploysPruned++
			}
			after = store.DeployCursor{CreatedAt: d.CreatedAt, ID: d.ID}
		}
		if len(batch) < gcBatch {
			return nil
		}
	}
}

func (g *GC) deleteBlobs(ctx context.Context, createdBefore time.Time, res *GCResult) error {
	after := ""
	for {
		batch, err := g.c.Repo.UnreferencedBlobs(ctx, createdBefore, after, gcBatch)
		if err != nil {
			return fmt.Errorf("list unreferenced blobs: %w", err)
		}
		for _, b := range batch {
			size, deleted, err := g.c.Repo.DeleteBlob(ctx, b.SHA256, createdBefore, func(ctx context.Context) error {
				return g.c.Blobs.Delete(ctx, b.SHA256)
			})
			if err != nil {
				return fmt.Errorf("delete blob: %w", err)
			}
			if deleted {
				res.BlobsDeleted++
				res.BytesFreed += size
			}
			after = b.SHA256
		}
		if len(batch) < gcBatch {
			return nil
		}
	}
}

func (g *GC) deleteUnrecorded(ctx context.Context, before time.Time, res *GCResult) error {
	return g.c.Files.WalkFiles(ctx, before, func(f BlobFile) error {
		remove := func(ctx context.Context) error { return g.c.Files.RemoveFile(ctx, f) }
		deleted := true
		if f.SHA256 == "" {
			if err := remove(ctx); err != nil {
				return fmt.Errorf("remove temporary upload: %w", err)
			}
		} else {
			var err error
			if deleted, err = g.c.Repo.DeleteUnrecorded(ctx, f.SHA256, remove); err != nil {
				return fmt.Errorf("remove unrecorded blob: %w", err)
			}
		}
		if deleted {
			res.FilesDeleted++
			res.BytesFreed += f.Size
		}
		return nil
	})
}

// RunEvery runs g now and then every interval until ctx is done. A failed
// run is logged and retried at the next interval.
func (g *GC) RunEvery(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		_, _ = g.Run(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
