// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"time"

	"wardlock.nz/server/internal/store/sqlcgen"
)

// Deploy is an immutable snapshot of a site's files.
type Deploy struct {
	ID          string
	SiteID      string
	Message     string
	FileCount   int
	TotalSize   int64
	CreatedAt   time.Time
	ActivatedAt time.Time // most recent activation; zero if never
}

// DeployFile is one manifest entry. Its blob need not exist yet: activation
// checks that every referenced blob has been uploaded.
type DeployFile struct {
	Path   string // relative, forward slashes, no leading slash
	SHA256 string // lowercase hex
	Size   int64
}

// Blob records a content-addressed file body held by the BlobStore.
type Blob struct {
	SHA256    string
	Size      int64
	CreatedAt time.Time
}

// Deploys stores deploys, their manifests and blob records.
type Deploys interface {
	// Create records d and its manifest in one transaction on one of
	// ownerID's sites.
	Create(ctx context.Context, ownerID string, d Deploy, files []DeployFile) error
	// Delete removes an inactive deploy; the active one is ErrConstraint.
	Delete(ctx context.Context, ownerID, id string) error
	// PutBlob records a blob; recording one that exists is a no-op.
	PutBlob(ctx context.Context, b Blob) error
}

// Deploys returns the SQLite Deploys repository.
func (db *DB) Deploys() Deploys { return deploysRepo{db} }

type deploysRepo struct{ db *DB }

func (r deploysRepo) Create(ctx context.Context, ownerID string, d Deploy, files []DeployFile) error {
	return r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		if err := requireSite(ctx, q, ownerID, d.SiteID); err != nil {
			return err
		}
		err := q.CreateDeploy(ctx, sqlcgen.CreateDeployParams{
			ID:          d.ID,
			SiteID:      d.SiteID,
			Message:     nullString(d.Message),
			FileCount:   int64(d.FileCount),
			TotalSize:   d.TotalSize,
			CreatedAt:   ms(d.CreatedAt),
			ActivatedAt: nullMs(d.ActivatedAt),
		})
		if err != nil {
			return mapErr("create deploy", err)
		}
		for _, f := range files {
			err := q.CreateDeployFile(ctx, sqlcgen.CreateDeployFileParams{
				DeployID: d.ID,
				Path:     f.Path,
				Sha256:   f.SHA256,
				Size:     f.Size,
			})
			if err != nil {
				return mapErr("create deploy file", err)
			}
		}
		return nil
	})
}

func (r deploysRepo) Delete(ctx context.Context, ownerID, id string) error {
	n, err := r.db.q.DeleteDeploy(ctx, sqlcgen.DeleteDeployParams{ID: id, OwnerID: ownerID})
	return rowsAffected("delete deploy", n, err)
}

func (r deploysRepo) PutBlob(ctx context.Context, b Blob) error {
	return mapErr("put blob", r.db.q.PutBlob(ctx, sqlcgen.PutBlobParams{
		Sha256:    b.SHA256,
		Size:      b.Size,
		CreatedAt: ms(b.CreatedAt),
	}))
}
