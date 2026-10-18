-- Deploys beyond each site's newest keep_count, oldest first, after the
-- (created_at, id) cursor. Never the site's active deploy, nor a pending
-- deploy created at or after pending_before, whose upload may be in flight.
-- name: ListPrunableDeploys :many
SELECT deploys.id, deploys.site_id, deploys.created_at FROM deploys
WHERE (deploys.created_at > sqlc.arg(after_created_at)
    OR (deploys.created_at = sqlc.arg(after_created_at) AND deploys.id > sqlc.arg(after_id)))
  AND NOT EXISTS (SELECT 1 FROM sites WHERE sites.active_deploy_id = deploys.id)
  AND (deploys.activated_at IS NOT NULL OR deploys.created_at < sqlc.arg(pending_before))
  AND (
    SELECT COUNT(*) FROM deploys AS newer
    WHERE newer.site_id = deploys.site_id
      AND (newer.created_at > deploys.created_at OR (newer.created_at = deploys.created_at AND newer.id > deploys.id))
  ) >= CAST(sqlc.arg(keep_count) AS INTEGER)
ORDER BY deploys.created_at, deploys.id
LIMIT sqlc.arg(row_limit);

-- Deletes one deploy unless it has become its site's active deploy or is a
-- young pending deploy; its manifest goes with it (ON DELETE CASCADE).
-- name: DeletePrunableDeploy :execrows
DELETE FROM deploys
WHERE deploys.id = sqlc.arg(id)
  AND NOT EXISTS (SELECT 1 FROM sites WHERE sites.active_deploy_id = deploys.id)
  AND (deploys.activated_at IS NOT NULL OR deploys.created_at < sqlc.arg(pending_before));

-- Blobs no deploy lists (as a file or a rendition), first stored and last claimed by any owner before
-- created_before, in hash order after the cursor.
-- name: ListUnreferencedBlobs :many
SELECT blobs.sha256, blobs.size, blobs.created_at FROM blobs
WHERE blobs.sha256 > sqlc.arg(after_sha256)
  AND blobs.created_at < sqlc.arg(created_before)
  AND NOT EXISTS (SELECT 1 FROM deploy_files WHERE deploy_files.sha256 = blobs.sha256)
  AND NOT EXISTS (SELECT 1 FROM deploy_renditions WHERE deploy_renditions.sha256 = blobs.sha256)
  AND NOT EXISTS (
    SELECT 1 FROM owner_blobs
    WHERE owner_blobs.sha256 = blobs.sha256 AND owner_blobs.created_at >= sqlc.arg(created_before)
  )
ORDER BY blobs.sha256
LIMIT sqlc.arg(row_limit);

-- The same test as ListUnreferencedBlobs for one blob, inside the deleting
-- transaction.
-- name: UnreferencedBlobSize :one
SELECT blobs.size FROM blobs
WHERE blobs.sha256 = sqlc.arg(sha256)
  AND blobs.created_at < sqlc.arg(created_before)
  AND NOT EXISTS (SELECT 1 FROM deploy_files WHERE deploy_files.sha256 = blobs.sha256)
  AND NOT EXISTS (SELECT 1 FROM deploy_renditions WHERE deploy_renditions.sha256 = blobs.sha256)
  AND NOT EXISTS (
    SELECT 1 FROM owner_blobs
    WHERE owner_blobs.sha256 = blobs.sha256 AND owner_blobs.created_at >= sqlc.arg(created_before)
  );

-- name: BlobRecorded :one
SELECT COUNT(*) FROM blobs WHERE sha256 = sqlc.arg(sha256);

-- name: DeleteOwnerBlobsByHash :exec
DELETE FROM owner_blobs WHERE sha256 = sqlc.arg(sha256);

-- name: DeleteBlob :exec
DELETE FROM blobs WHERE sha256 = sqlc.arg(sha256);

-- Garbage collection (C1.8). Instance-wide: these queries back the GC job
-- only and must never back an owner API handler.

-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
-- (Licence header last: sqlc copies comments above a query into Go docs.)
