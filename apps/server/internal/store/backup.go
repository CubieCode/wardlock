// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"

	sqlite "modernc.org/sqlite"
)

// ErrBackupExists means the backup destination already exists; Backup never
// overwrites a file.
var ErrBackupExists = errors.New("backup destination already exists")

// backuper is the modernc.org/sqlite connection's online backup method.
type backuper interface {
	NewBackup(dstURI string) (*sqlite.Backup, error)
}

// Backup writes a consistent copy of the database to path with SQLite's
// online backup API while other connections, in this process or another,
// keep reading and writing. It copies every page in one step inside a single
// read transaction, so the copy is the database as of one moment; in WAL mode
// writers are not blocked meanwhile. The copy is written to a temporary file
// beside path, checked with PRAGMA quick_check, switched to a rollback
// journal so it is a single self-contained file, synced and renamed into
// place. Store.Open turns WAL back on when the copy is restored.
func (db *DB) Backup(ctx context.Context, path string) (err error) {
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("%w: %s", ErrBackupExists, path)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("backup destination: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".wardlock-backup-*")
	if err != nil {
		return fmt.Errorf("create backup file: %w", err)
	}
	tmpName := tmp.Name()
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("create backup file: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmpName)
			_ = os.Remove(tmpName + "-journal")
		}
	}()

	conn, err := db.sql.Conn(ctx)
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	err = conn.Raw(func(dc any) error {
		b, ok := dc.(backuper)
		if !ok {
			return errors.New("driver does not support online backup")
		}
		bk, err := b.NewBackup(fileURI(tmpName))
		if err != nil {
			return err
		}
		// Copying all pages in one step keeps one read transaction for the
		// whole copy; smaller steps restart whenever another connection
		// writes, and might never finish on a busy server.
		if _, err := bk.Step(-1); err != nil {
			_ = bk.Finish()
			return err
		}
		return bk.Finish()
	})
	if cerr := conn.Close(); err == nil && cerr != nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	if err := finishBackupFile(ctx, tmpName); err != nil {
		return err
	}
	if err := syncFile(tmpName); err != nil {
		return err
	}
	if err := os.Link(tmpName, path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%w: %s", ErrBackupExists, path)
		}
		return fmt.Errorf("move backup into place: %w", err)
	}
	_ = os.Remove(tmpName)
	return syncFile(filepath.Dir(path))
}

// finishBackupFile checks the copy and makes it a single file: a WAL-mode
// source leaves the copy's header asking for WAL.
func finishBackupFile(ctx context.Context, path string) error {
	c, err := sql.Open("sqlite", fileURI(path))
	if err != nil {
		return fmt.Errorf("check backup: %w", err)
	}
	defer func() { _ = c.Close() }()
	var result string
	if err := c.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&result); err != nil {
		return fmt.Errorf("check backup: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("check backup: quick_check reported %q", result)
	}
	if _, err := c.ExecContext(ctx, "PRAGMA journal_mode=DELETE"); err != nil {
		return fmt.Errorf("check backup: %w", err)
	}
	if err := c.Close(); err != nil {
		return fmt.Errorf("check backup: %w", err)
	}
	return nil
}

func fileURI(path string) string { return (&url.URL{Scheme: "file", Opaque: path}).String() }

func syncFile(path string) error {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("sync %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	if err := f.Sync(); err != nil && !errors.Is(err, errors.ErrUnsupported) {
		return fmt.Errorf("sync %s: %w", path, err)
	}
	return nil
}

// Check reads the migrations table, proving the database answers queries
// from its file. Readiness calls it.
func (db *DB) Check(ctx context.Context) error {
	if _, err := db.q.ListAppliedMigrations(ctx); err != nil {
		return fmt.Errorf("database check: %w", err)
	}
	return nil
}
