// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"wardlock.nz/server/internal/store/sqlcgen"
)

// PrunableDeploy is a deploy beyond its site's history limit.
type PrunableDeploy struct {
	ID        string
	SiteID    string
	CreatedAt time.Time
}

// GC is the instance-wide repository behind garbage collection (C1.8). It
// is not owner-scoped and must never back an owner API handler.
//
// DeleteBlob and DeleteUnrecorded rely on their transactions being ordered
// with Deploys.Create and Deploys.RecordUpload for the same hash. SQLite
// gives that by beginning every transaction IMMEDIATE; another backend
// must order them too, for example with a per-hash advisory lock that
// all four take.
type GC interface {
	// PrunableDeploys returns up to limit deploys that have at least keep
	// newer deploys on their site, oldest first, strictly after the cursor
	// (zero starts at the oldest). It never returns a site's active deploy
	// or a pending deploy created at or after pendingBefore.
	PrunableDeploys(ctx context.Context, keep int, pendingBefore time.Time, after DeployCursor, limit int) ([]PrunableDeploy, error)
	// DeletePrunableDeploy deletes the deploy and its manifest unless, when
	// the delete runs, it is its site's active deploy or a pending deploy
	// created at or after pendingBefore. deleted reports which. Storage
	// only it held goes back to its owner's quota (A2.14).
	DeletePrunableDeploy(ctx context.Context, id string, pendingBefore time.Time) (deleted bool, err error)
	// UnreferencedBlobs returns up to limit blobs that no deploy lists, that
	// were first recorded before createdBefore and that no owner has
	// claimed since, in hash order strictly after afterSHA256.
	UnreferencedBlobs(ctx context.Context, createdBefore time.Time, afterSHA256 string, limit int) ([]Blob, error)
	// DeleteBlob deletes the blob's record and every owner's claim to it in
	// one transaction, provided it still passes the UnreferencedBlobs test
	// inside that transaction. remove runs inside the transaction too, after
	// the rows are deleted: it deletes the stored bytes, and an error from
	// it rolls the rows back. Holding the transaction across remove orders
	// the file's removal before any deploy created, or upload recorded, for
	// the same hash. size is the recorded size; deleted is false (with no
	// call to remove) when the blob is missing or no longer garbage.
	DeleteBlob(ctx context.Context, sha256 string, createdBefore time.Time, remove func(context.Context) error) (size int64, deleted bool, err error)
	// DeleteUnrecorded calls remove inside a transaction unless a blob
	// record exists for sha256, for stored bytes no record describes.
	// deleted reports whether remove ran and succeeded.
	DeleteUnrecorded(ctx context.Context, sha256 string, remove func(context.Context) error) (deleted bool, err error)
}

// GC returns the SQLite GC repository.
func (db *DB) GC() GC { return gcRepo{db} }

type gcRepo struct{ db *DB }

func (r gcRepo) PrunableDeploys(ctx context.Context, keep int, pendingBefore time.Time, after DeployCursor, limit int) ([]PrunableDeploy, error) {
	afterAt := int64(-1)
	if !after.CreatedAt.IsZero() {
		afterAt = ms(after.CreatedAt)
	}
	rows, err := r.db.q.ListPrunableDeploys(ctx, sqlcgen.ListPrunableDeploysParams{
		AfterCreatedAt: afterAt,
		AfterID:        after.ID,
		PendingBefore:  ms(pendingBefore),
		KeepCount:      int64(keep),
		RowLimit:       int64(limit),
	})
	if err != nil {
		return nil, mapErr("list prunable deploys", err)
	}
	out := make([]PrunableDeploy, 0, len(rows))
	for _, row := range rows {
		out = append(out, PrunableDeploy{ID: row.ID, SiteID: row.SiteID, CreatedAt: fromMs(row.CreatedAt)})
	}
	return out, nil
}

func (r gcRepo) DeletePrunableDeploy(ctx context.Context, id string, pendingBefore time.Time) (bool, error) {
	err := r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		owner, err := q.DeployOwner(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return errNothingDeleted
		}
		if err != nil {
			return mapErr("find deploy owner", err)
		}
		if err := releaseDeploys(ctx, q, owner, id); err != nil {
			return err
		}
		n, err := q.DeletePrunableDeploy(ctx, sqlcgen.DeletePrunableDeployParams{ID: id, PendingBefore: ms(pendingBefore)})
		if err != nil {
			return mapErr("delete prunable deploy", err)
		}
		if n == 0 {
			return errNothingDeleted // undo the release
		}
		return nil
	})
	if errors.Is(err, errNothingDeleted) {
		return false, nil
	}
	return err == nil, err
}

func (r gcRepo) UnreferencedBlobs(ctx context.Context, createdBefore time.Time, afterSHA256 string, limit int) ([]Blob, error) {
	rows, err := r.db.q.ListUnreferencedBlobs(ctx, sqlcgen.ListUnreferencedBlobsParams{
		AfterSha256:   afterSHA256,
		CreatedBefore: ms(createdBefore),
		RowLimit:      int64(limit),
	})
	if err != nil {
		return nil, mapErr("list unreferenced blobs", err)
	}
	out := make([]Blob, 0, len(rows))
	for _, row := range rows {
		out = append(out, Blob{SHA256: row.Sha256, Size: row.Size, CreatedAt: fromMs(row.CreatedAt)})
	}
	return out, nil
}

func (r gcRepo) DeleteBlob(ctx context.Context, sha256 string, createdBefore time.Time, remove func(context.Context) error) (int64, bool, error) {
	var size int64
	deleted := false
	err := r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		var err error
		size, err = q.UnreferencedBlobSize(ctx, sqlcgen.UnreferencedBlobSizeParams{Sha256: sha256, CreatedBefore: ms(createdBefore)})
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return mapErr("check unreferenced blob", err)
		}
		if err := q.DeleteOwnerBlobsByHash(ctx, sha256); err != nil {
			return mapErr("delete owner blobs", err)
		}
		if err := q.DeleteBlob(ctx, sha256); err != nil {
			return mapErr("delete blob", err)
		}
		if err := remove(ctx); err != nil {
			return err
		}
		deleted = true
		return nil
	})
	if err != nil {
		return 0, false, err
	}
	return size, deleted, nil
}

func (r gcRepo) DeleteUnrecorded(ctx context.Context, sha256 string, remove func(context.Context) error) (bool, error) {
	deleted := false
	err := r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		n, err := q.BlobRecorded(ctx, sha256)
		if err != nil {
			return mapErr("check blob record", err)
		}
		if n > 0 {
			return nil
		}
		if err := remove(ctx); err != nil {
			return err
		}
		deleted = true
		return nil
	})
	return deleted, err
}
