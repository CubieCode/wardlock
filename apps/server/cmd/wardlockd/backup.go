// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"wardlock.nz/server/internal/platform"
	"wardlock.nz/server/internal/store"
)

const backupUsage = `Usage:
  wardlockd backup PATH

Writes a consistent copy of the database in WARDLOCK_DATA_DIR to PATH, a
new file, using SQLite's online backup API. Safe to run while the server is
running. Back up the blob directory afterwards; see docs/DEPLOYMENT.md.
`

// backupCommand runs `wardlockd backup PATH`.
func backupCommand(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) error {
	var arg string
	if len(args) > 0 {
		arg = args[0]
	}
	switch {
	case arg == "help" || arg == "--help" || arg == "-h":
		fmt.Fprint(stdout, backupUsage)
		return nil
	case arg == "" || len(args) > 1:
		fmt.Fprint(stderr, backupUsage)
		return errUsage
	}
	cfg, err := platform.LoadConfig(getenv)
	if err != nil {
		return err
	}
	src := filepath.Join(cfg.DataDir, "wardlock.db")
	// Opening a missing database would create an empty one.
	if _, err := os.Stat(src); errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("no database at %s: check WARDLOCK_DATA_DIR", src)
	} else if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	dst, err := filepath.Abs(arg)
	if err != nil {
		return fmt.Errorf("backup destination: %w", err)
	}
	db, err := store.Open(ctx, src)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	if err := db.Backup(ctx, dst); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Backed up %s to %s.\n", src, dst)
	return nil
}
