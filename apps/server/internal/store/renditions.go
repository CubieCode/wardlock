// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"time"

	"wardlock.nz/server/internal/store/sqlcgen"
)

// Rendition is a Markdown ("md") or plain-text ("txt") version of an HTML
// file in a deploy's manifest (A2.6), stored as a blob.
type Rendition struct {
	Path   string // the HTML file's manifest path
	Format string // "md" or "txt"
	SHA256 string // lowercase hex
	Size   int64
}

// Renditions stores the renditions of deploys' HTML files. Methods that
// take ownerID and siteID match only that owner's deploys on that site;
// anything else is ErrNotFound.
type Renditions interface {
	// Sources returns the HTML files (.html or .htm in any letter case) in
	// the deploy's manifest, ordered by path.
	Sources(ctx context.Context, ownerID, siteID, deployID string) ([]DeployFile, error)
	// Put records rs for the deploy in one transaction, replacing any
	// rendition already recorded for the same path and format, together
	// with a blob record (created at) for each hash not yet recorded. A
	// path not in the manifest is ErrConstraint.
	Put(ctx context.Context, ownerID, siteID, deployID string, rs []Rendition, at time.Time) error
	// List returns the deploy's renditions ordered by path and format. It is
	// an instance-wide read for the edge and must never back an owner API
	// handler.
	List(ctx context.Context, deployID string) ([]Rendition, error)
}

// Renditions returns the SQLite Renditions repository.
func (db *DB) Renditions() Renditions { return renditionsRepo{db} }

type renditionsRepo struct{ db *DB }

func (r renditionsRepo) Sources(ctx context.Context, ownerID, siteID, deployID string) ([]DeployFile, error) {
	if _, err := getDeploy(ctx, r.db.q, ownerID, siteID, deployID); err != nil {
		return nil, err
	}
	rows, err := r.db.q.ListRenditionSources(ctx, sqlcgen.ListRenditionSourcesParams{
		DeployID: deployID, SiteID: siteID, OwnerID: ownerID,
	})
	if err != nil {
		return nil, mapErr("list rendition sources", err)
	}
	out := make([]DeployFile, len(rows))
	for i, row := range rows {
		out[i] = DeployFile{Path: row.Path, SHA256: row.Sha256, Size: row.Size}
	}
	return out, nil
}

func (r renditionsRepo) Put(ctx context.Context, ownerID, siteID, deployID string, rs []Rendition, at time.Time) error {
	return r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		if _, err := getDeploy(ctx, q, ownerID, siteID, deployID); err != nil {
			return err
		}
		for _, rd := range rs {
			err := q.PutBlob(ctx, sqlcgen.PutBlobParams{Sha256: rd.SHA256, Size: rd.Size, CreatedAt: ms(at)})
			if err != nil {
				return mapErr("record rendition blob", err)
			}
			err = q.PutRendition(ctx, sqlcgen.PutRenditionParams{
				DeployID: deployID, Path: rd.Path, Format: rd.Format, Sha256: rd.SHA256, Size: rd.Size,
			})
			if err != nil {
				return mapErr("record rendition", err)
			}
		}
		return nil
	})
}

func (r renditionsRepo) List(ctx context.Context, deployID string) ([]Rendition, error) {
	rows, err := r.db.q.ListDeployRenditions(ctx, deployID)
	if err != nil {
		return nil, mapErr("list renditions", err)
	}
	out := make([]Rendition, len(rows))
	for i, row := range rows {
		out[i] = Rendition{Path: row.Path, Format: row.Format, SHA256: row.Sha256, Size: row.Size}
	}
	return out, nil
}
