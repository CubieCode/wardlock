// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/platform"
	"wardlock.nz/server/internal/store"
)

const gcUsage = `Usage:
  wardlockd gc

Prunes deploy history beyond WARDLOCK_DEPLOY_HISTORY per site (never the
active deploy) and removes blobs no deploy references, older than
WARDLOCK_GC_GRACE_HOURS, from the blob directory or bucket, along with
abandoned temporary uploads. Safe to run while the server is running.
`

// gcCommand runs `wardlockd gc`: one collection against the configured data
// directory, then a summary on stdout.
func gcCommand(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) error {
	if len(args) > 0 {
		if args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
			fmt.Fprint(stdout, gcUsage)
			return nil
		}
		fmt.Fprint(stderr, gcUsage)
		return errUsage
	}
	cfg, err := platform.LoadConfig(getenv)
	if err != nil {
		return err
	}
	log := platform.NewLogger(stderr, cfg)
	db, err := openStore(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	blobs, _, err := openBlobs(cfg, log)
	if err != nil {
		return err
	}
	res, err := newGC(cfg, log, db, blobs).Run(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Pruned %d deploys, deleted %d blobs and %d unrecorded files, freed %d bytes.\n",
		res.DeploysPruned, res.BlobsDeleted, res.FilesDeleted, res.BytesFreed)
	return nil
}

func newGC(cfg platform.Config, log *slog.Logger, db *store.DB, blobs blobStore) *deploys.GC {
	return deploys.NewGC(deploys.GCConfig{
		Repo:    db.GC(),
		Blobs:   blobs,
		Files:   blobs,
		Grace:   cfg.GCGrace,
		History: cfg.DeployHistory,
		Log:     log,
		Now:     time.Now,
	})
}

// startGC runs garbage collection every cfg.GCInterval (none when zero).
// The returned stop cancels a run in progress and waits for it; call it
// before the database closes.
func startGC(ctx context.Context, cfg platform.Config, log *slog.Logger, db *store.DB, blobs blobStore) func() {
	if cfg.GCInterval <= 0 {
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	gc := newGC(cfg, log, db, blobs)
	wg.Go(func() { gc.RunEvery(ctx, cfg.GCInterval) })
	return func() {
		cancel()
		wg.Wait()
	}
}
