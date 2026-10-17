// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
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
	SPA         bool // serve /index.html for missing extensionless paths
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

// DeployCursor positions a deploy listing: List returns deploys strictly
// older than it. The zero value starts at the newest.
type DeployCursor struct {
	CreatedAt time.Time
	ID        string
}

// State errors from Activate and Rollback.
var (
	// ErrDeployNotPending means Activate was given a deploy that has been
	// activated before.
	ErrDeployNotPending = errors.New("deploy is not pending")
	// ErrDeployNotActivated means Rollback was given a deploy that was never
	// activated.
	ErrDeployNotActivated = errors.New("deploy was never activated")
	// ErrNoPreviousDeploy means Rollback found no earlier activated deploy.
	ErrNoPreviousDeploy = errors.New("no previous deploy")
	// ErrBlobsMissing means the owner has not uploaded every blob in the
	// deploy's manifest.
	ErrBlobsMissing = errors.New("deploy blobs missing")
)

// Deploys stores deploys, their manifests and blob records. Methods that
// take ownerID and siteID match only that owner's deploys on that site;
// anything else is ErrNotFound.
type Deploys interface {
	// Create records d and its manifest in one transaction on one of
	// ownerID's sites.
	Create(ctx context.Context, ownerID string, d Deploy, files []DeployFile) error
	Get(ctx context.Context, ownerID, siteID, id string) (Deploy, error)
	// List returns up to limit of the site's deploys, newest first, older
	// than before.
	List(ctx context.Context, ownerID, siteID string, before DeployCursor, limit int) ([]Deploy, error)
	// Files returns a deploy's manifest ordered by path. It is an
	// instance-wide read for the edge and must never back an owner API
	// handler.
	Files(ctx context.Context, deployID string) ([]DeployFile, error)
	// Hashes returns the distinct hashes in the deploy's manifest.
	Hashes(ctx context.Context, ownerID, siteID, deployID string) ([]string, error)
	// MissingBlobs returns the distinct hashes in the deploy's manifest that
	// ownerID has not recorded with RecordUpload.
	MissingBlobs(ctx context.Context, ownerID, siteID, deployID string) ([]string, error)
	// ManifestSize returns the size the deploy's manifest gives sha256;
	// ErrNotFound if the deploy is not found or does not list it.
	ManifestSize(ctx context.Context, ownerID, siteID, deployID, sha256 string) (int64, error)
	// RecordUpload records that ownerID uploaded b with a verified hash:
	// the blob row and the owner's claim to it, in one transaction.
	// Recording either again is a no-op.
	RecordUpload(ctx context.Context, ownerID string, b Blob) error
	// Activate makes a pending deploy the site's active deploy in one
	// transaction, stamping it with at. It fails with ErrDeployNotPending if
	// the deploy was activated before and ErrBlobsMissing unless ownerID has
	// recorded every blob in its manifest.
	Activate(ctx context.Context, ownerID, siteID, deployID string, at time.Time) (Deploy, error)
	// Rollback reactivates an earlier activated deploy in one transaction:
	// deployID, or with "" the most recently activated deploy other than the
	// current one. It fails with ErrDeployNotActivated for a deploy never
	// activated and ErrNoPreviousDeploy when there is none. Rolling back to
	// the active deploy changes nothing.
	Rollback(ctx context.Context, ownerID, siteID, deployID string, at time.Time) (Deploy, error)
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
			Spa:         boolInt(d.SPA),
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

func (r deploysRepo) Get(ctx context.Context, ownerID, siteID, id string) (Deploy, error) {
	return getDeploy(ctx, r.db.q, ownerID, siteID, id)
}

func getDeploy(ctx context.Context, q *sqlcgen.Queries, ownerID, siteID, id string) (Deploy, error) {
	row, err := q.GetDeploy(ctx, sqlcgen.GetDeployParams{ID: id, SiteID: siteID, OwnerID: ownerID})
	if err != nil {
		return Deploy{}, mapErr("get deploy", err)
	}
	return deployFrom(row), nil
}

func deployFrom(row sqlcgen.GetDeployRow) Deploy {
	return Deploy{
		ID:          row.ID,
		SiteID:      row.SiteID,
		Message:     row.Message.String,
		FileCount:   int(row.FileCount),
		TotalSize:   row.TotalSize,
		SPA:         row.Spa == 1,
		CreatedAt:   fromMs(row.CreatedAt),
		ActivatedAt: fromNullMs(row.ActivatedAt),
	}
}

func (r deploysRepo) List(ctx context.Context, ownerID, siteID string, before DeployCursor, limit int) ([]Deploy, error) {
	beforeAt := int64(math.MaxInt64)
	if !before.CreatedAt.IsZero() {
		beforeAt = ms(before.CreatedAt)
	}
	rows, err := r.db.q.ListDeploys(ctx, sqlcgen.ListDeploysParams{
		SiteID:          siteID,
		OwnerID:         ownerID,
		BeforeCreatedAt: beforeAt,
		BeforeID:        before.ID,
		RowLimit:        int64(limit),
	})
	if err != nil {
		return nil, mapErr("list deploys", err)
	}
	deploys := make([]Deploy, 0, len(rows))
	for _, row := range rows {
		deploys = append(deploys, deployFrom(sqlcgen.GetDeployRow(row)))
	}
	return deploys, nil
}

func (r deploysRepo) Files(ctx context.Context, deployID string) ([]DeployFile, error) {
	rows, err := r.db.q.ListDeployFiles(ctx, deployID)
	if err != nil {
		return nil, mapErr("list deploy files", err)
	}
	files := make([]DeployFile, 0, len(rows))
	for _, row := range rows {
		files = append(files, DeployFile{Path: row.Path, SHA256: row.Sha256, Size: row.Size})
	}
	return files, nil
}

func (r deploysRepo) Hashes(ctx context.Context, ownerID, siteID, deployID string) ([]string, error) {
	if _, err := r.Get(ctx, ownerID, siteID, deployID); err != nil {
		return nil, err
	}
	hashes, err := r.db.q.ManifestHashes(ctx, deployID)
	return hashes, mapErr("list manifest hashes", err)
}

func (r deploysRepo) MissingBlobs(ctx context.Context, ownerID, siteID, deployID string) ([]string, error) {
	if _, err := r.Get(ctx, ownerID, siteID, deployID); err != nil {
		return nil, err
	}
	missing, err := r.db.q.MissingOwnerBlobs(ctx, sqlcgen.MissingOwnerBlobsParams{DeployID: deployID, OwnerID: ownerID})
	return missing, mapErr("list missing blobs", err)
}

func (r deploysRepo) ManifestSize(ctx context.Context, ownerID, siteID, deployID, sha256 string) (int64, error) {
	size, err := r.db.q.ManifestBlobSize(ctx, sqlcgen.ManifestBlobSizeParams{
		DeployID: deployID,
		Sha256:   sha256,
		SiteID:   siteID,
		OwnerID:  ownerID,
	})
	return size, mapErr("find manifest blob", err)
}

func (r deploysRepo) RecordUpload(ctx context.Context, ownerID string, b Blob) error {
	return r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		err := q.PutBlob(ctx, sqlcgen.PutBlobParams{Sha256: b.SHA256, Size: b.Size, CreatedAt: ms(b.CreatedAt)})
		if err != nil {
			return mapErr("put blob", err)
		}
		err = q.PutOwnerBlob(ctx, sqlcgen.PutOwnerBlobParams{OwnerID: ownerID, Sha256: b.SHA256, CreatedAt: ms(b.CreatedAt)})
		return mapErr("put owner blob", err)
	})
}

func (r deploysRepo) Activate(ctx context.Context, ownerID, siteID, deployID string, at time.Time) (Deploy, error) {
	var d Deploy
	err := r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		var err error
		if d, err = getDeploy(ctx, q, ownerID, siteID, deployID); err != nil {
			return err
		}
		if !d.ActivatedAt.IsZero() {
			return fmt.Errorf("activate deploy: %w", ErrDeployNotPending)
		}
		missing, err := q.MissingOwnerBlobs(ctx, sqlcgen.MissingOwnerBlobsParams{DeployID: deployID, OwnerID: ownerID})
		if err != nil {
			return mapErr("list missing blobs", err)
		}
		if len(missing) > 0 {
			return fmt.Errorf("activate deploy: %d missing: %w", len(missing), ErrBlobsMissing)
		}
		return setActive(ctx, q, ownerID, &d, at)
	})
	return d, err
}

func (r deploysRepo) Rollback(ctx context.Context, ownerID, siteID, deployID string, at time.Time) (Deploy, error) {
	var d Deploy
	err := r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		site, err := q.GetSite(ctx, sqlcgen.GetSiteParams{ID: siteID, OwnerID: ownerID})
		if err != nil {
			return mapErr("get site", err)
		}
		if deployID == "" {
			deployID, err = q.PreviousDeploy(ctx, sqlcgen.PreviousDeployParams{SiteID: siteID, CurrentID: site.ActiveDeployID.String})
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("rollback: %w", ErrNoPreviousDeploy)
			}
			if err != nil {
				return mapErr("find previous deploy", err)
			}
		}
		if d, err = getDeploy(ctx, q, ownerID, siteID, deployID); err != nil {
			return err
		}
		if d.ActivatedAt.IsZero() {
			return fmt.Errorf("rollback: %w", ErrDeployNotActivated)
		}
		if d.ID == site.ActiveDeployID.String {
			return nil
		}
		return setActive(ctx, q, ownerID, &d, at)
	})
	return d, err
}

// setActive stamps d as activated at and points its site at it.
func setActive(ctx context.Context, q *sqlcgen.Queries, ownerID string, d *Deploy, at time.Time) error {
	err := q.MarkDeployActivated(ctx, sqlcgen.MarkDeployActivatedParams{ActivatedAt: nullMs(at), ID: d.ID})
	if err != nil {
		return mapErr("mark deploy activated", err)
	}
	n, err := q.SetActiveDeploy(ctx, sqlcgen.SetActiveDeployParams{DeployID: nullString(d.ID), SiteID: d.SiteID, OwnerID: ownerID})
	if err := rowsAffected("set active deploy", n, err); err != nil {
		return err
	}
	d.ActivatedAt = fromMs(ms(at))
	return nil
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
