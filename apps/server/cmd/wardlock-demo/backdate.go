// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"time"

	_ "modernc.org/sqlite" // The driver internal/store uses.
)

// backdater moves creation times into the past, which no API or store
// method does: the demo alone needs it, so its statements live here rather
// than in internal/store. backdate_test.go runs them against a database
// migrated by internal/store, so a schema change that breaks them fails
// the tests.
type backdater struct{ db *sql.DB }

func openBackdater(ctx context.Context, path string) (*backdater, error) {
	dsn := (&url.URL{Scheme: "file", Opaque: path, RawQuery: url.Values{
		"_pragma": {"busy_timeout(5000)"},
	}.Encode()}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open database %s: %w", path, err)
	}
	return &backdater{db}, nil
}

func (b *backdater) Close() error { return b.db.Close() }

func (b *backdater) exec(ctx context.Context, what, query string, args ...any) error {
	res, err := b.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("backdate %s: %w", what, err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return fmt.Errorf("backdate %s: no row %v", what, args[len(args)-1])
	}
	return nil
}

// owner backdates the owner who owns siteID.
func (b *backdater) owner(ctx context.Context, siteID string, at time.Time) error {
	return b.exec(ctx, "owner", `UPDATE owners SET created_at = ? WHERE id = (SELECT owner_id FROM sites WHERE id = ?)`, at.UnixMilli(), siteID)
}

// site backdates a site and its policies.
func (b *backdater) site(ctx context.Context, id string, at time.Time) error {
	if err := b.exec(ctx, "site", `UPDATE sites SET created_at = ? WHERE id = ?`, at.UnixMilli(), id); err != nil {
		return err
	}
	_, err := b.db.ExecContext(ctx, `UPDATE policies SET created_at = ?, updated_at = ? WHERE site_id = ?`, at.UnixMilli(), at.UnixMilli(), id)
	if err != nil {
		return fmt.Errorf("backdate policies: %w", err)
	}
	return nil
}

// deploy backdates a deploy's creation to at and its activation, if any,
// to a few seconds later.
func (b *backdater) deploy(ctx context.Context, id string, at time.Time) error {
	activated := at.Add(4 * time.Second).UnixMilli()
	return b.exec(ctx, "deploy", `UPDATE deploys SET created_at = ?, activated_at = CASE WHEN activated_at IS NULL THEN NULL ELSE ? END WHERE id = ?`,
		at.UnixMilli(), activated, id)
}

// link backdates a share link's creation.
func (b *backdater) link(ctx context.Context, id string, at time.Time) error {
	return b.exec(ctx, "share link", `UPDATE share_links SET created_at = ? WHERE id = ?`, at.UnixMilli(), id)
}
