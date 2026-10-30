-- name: CreateDeploy :exec
INSERT INTO deploys (id, site_id, message, file_count, total_size, spa, immutable_assets, created_at, activated_at, path_prefix, base_deploy_id, redirects, headers, preview, preview_lifetime_days)
VALUES (sqlc.arg(id), sqlc.arg(site_id), sqlc.narg(message), sqlc.arg(file_count), sqlc.arg(total_size), sqlc.arg(spa), sqlc.arg(immutable_assets), sqlc.arg(created_at), sqlc.narg(activated_at), sqlc.narg(path_prefix), sqlc.narg(base_deploy_id), sqlc.narg(redirects), sqlc.narg(headers), sqlc.narg(preview), sqlc.narg(preview_lifetime_days));

-- name: CreateDeployFile :exec
INSERT INTO deploy_files (deploy_id, path, sha256, size)
VALUES (sqlc.arg(deploy_id), sqlc.arg(path), sqlc.arg(sha256), sqlc.arg(size));

-- name: GetDeploy :one
SELECT deploys.id, deploys.site_id, deploys.message, deploys.file_count, deploys.total_size, deploys.spa, deploys.immutable_assets, deploys.created_at, deploys.activated_at, deploys.path_prefix, deploys.base_deploy_id, deploys.redirects, deploys.headers, deploys.preview, deploys.preview_lifetime_days, deploys.preview_activated_at
FROM deploys JOIN sites ON sites.id = deploys.site_id
WHERE deploys.id = sqlc.arg(id) AND deploys.site_id = sqlc.arg(site_id) AND sites.owner_id = sqlc.arg(owner_id);

-- Newest first, strictly before the cursor (created_at, id); the first page
-- passes the largest created_at.
-- name: ListDeploys :many
SELECT deploys.id, deploys.site_id, deploys.message, deploys.file_count, deploys.total_size, deploys.spa, deploys.immutable_assets, deploys.created_at, deploys.activated_at, deploys.path_prefix, deploys.base_deploy_id, deploys.redirects, deploys.headers, deploys.preview, deploys.preview_lifetime_days, deploys.preview_activated_at
FROM deploys JOIN sites ON sites.id = deploys.site_id
WHERE deploys.site_id = sqlc.arg(site_id) AND sites.owner_id = sqlc.arg(owner_id)
  AND (deploys.created_at < sqlc.arg(before_created_at)
    OR (deploys.created_at = sqlc.arg(before_created_at) AND deploys.id < sqlc.arg(before_id)))
ORDER BY deploys.created_at DESC, deploys.id DESC
LIMIT sqlc.arg(row_limit);

-- The activated deploy other than the current one that was activated most
-- recently: the rollback target when none is named.
-- name: PreviousDeploy :one
SELECT deploys.id FROM deploys
WHERE deploys.site_id = sqlc.arg(site_id) AND deploys.activated_at IS NOT NULL AND deploys.id <> sqlc.arg(current_id)
ORDER BY deploys.activated_at DESC, deploys.id DESC
LIMIT 1;

-- Instance-wide: edge serving only.
-- name: ListDeployFiles :many
SELECT path, sha256, size FROM deploy_files WHERE deploy_id = sqlc.arg(deploy_id) ORDER BY path;

-- One of the owner's deploys' manifest, ordered by path.
-- name: ListOwnerDeployFiles :many
SELECT deploy_files.path, deploy_files.sha256, deploy_files.size FROM deploy_files
JOIN deploys ON deploys.id = deploy_files.deploy_id
JOIN sites ON sites.id = deploys.site_id
WHERE deploy_files.deploy_id = sqlc.arg(deploy_id) AND deploys.site_id = sqlc.arg(site_id) AND sites.owner_id = sqlc.arg(owner_id)
ORDER BY deploy_files.path;

-- One page of an owner's deploy's files, ordered by path bytewise (SQLite's
-- BINARY collation; Postgres needs COLLATE "C"), after the cursor path (''
-- for the first page).
-- name: ListOwnerDeployFilesPage :many
SELECT deploy_files.path, deploy_files.sha256, deploy_files.size FROM deploy_files
JOIN deploys ON deploys.id = deploy_files.deploy_id
JOIN sites ON sites.id = deploys.site_id
WHERE deploy_files.deploy_id = sqlc.arg(deploy_id) AND deploys.site_id = sqlc.arg(site_id) AND sites.owner_id = sqlc.arg(owner_id)
  AND deploy_files.path > sqlc.arg(after_path)
ORDER BY deploy_files.path
LIMIT sqlc.arg(row_limit);

-- The size the deploy lists sha256 with, only when the owner has also
-- uploaded it: a deploy can list any hash before its upload.
-- name: HeldManifestBlobSize :one
SELECT deploy_files.size FROM deploy_files
JOIN deploys ON deploys.id = deploy_files.deploy_id
JOIN sites ON sites.id = deploys.site_id
JOIN owner_blobs ON owner_blobs.owner_id = sites.owner_id AND owner_blobs.sha256 = deploy_files.sha256
WHERE deploy_files.deploy_id = sqlc.arg(deploy_id) AND deploy_files.sha256 = sqlc.arg(sha256)
  AND deploys.site_id = sqlc.arg(site_id) AND sites.owner_id = sqlc.arg(owner_id)
LIMIT 1;

-- The stored size of a blob the owner has uploaded; no row for a hash only
-- other owners hold, so the answer says nothing about their blobs.
-- name: OwnerHeldBlobSize :one
SELECT blobs.size FROM owner_blobs
JOIN blobs ON blobs.sha256 = owner_blobs.sha256
WHERE owner_blobs.owner_id = sqlc.arg(owner_id) AND owner_blobs.sha256 = sqlc.arg(sha256);

-- The first file, by path, of one of the owner's deploys that lists a hash
-- the owner holds at a size other than the stored blob's.
-- name: HeldSizeMismatch :one
SELECT deploy_files.path, deploy_files.size AS listed_size, blobs.size AS stored_size
FROM deploy_files
JOIN deploys ON deploys.id = deploy_files.deploy_id
JOIN sites ON sites.id = deploys.site_id
JOIN owner_blobs ON owner_blobs.owner_id = sites.owner_id AND owner_blobs.sha256 = deploy_files.sha256
JOIN blobs ON blobs.sha256 = deploy_files.sha256
WHERE deploy_files.deploy_id = sqlc.arg(deploy_id) AND deploys.site_id = sqlc.arg(site_id)
  AND sites.owner_id = sqlc.arg(owner_id) AND deploy_files.size <> blobs.size
ORDER BY deploy_files.path
LIMIT 1;

-- name: ManifestBlobSize :one
SELECT deploy_files.size FROM deploy_files
JOIN deploys ON deploys.id = deploy_files.deploy_id
JOIN sites ON sites.id = deploys.site_id
WHERE deploy_files.deploy_id = sqlc.arg(deploy_id) AND deploy_files.sha256 = sqlc.arg(sha256)
  AND deploys.site_id = sqlc.arg(site_id) AND sites.owner_id = sqlc.arg(owner_id)
LIMIT 1;

-- name: ManifestHashes :many
SELECT DISTINCT sha256 FROM deploy_files WHERE deploy_id = sqlc.arg(deploy_id) ORDER BY sha256;

-- The deploy's distinct hashes the owner has not uploaded.
-- name: MissingOwnerBlobs :many
SELECT DISTINCT deploy_files.sha256 FROM deploy_files
WHERE deploy_files.deploy_id = sqlc.arg(deploy_id)
  AND NOT EXISTS (
    SELECT 1 FROM owner_blobs
    WHERE owner_blobs.owner_id = sqlc.arg(owner_id) AND owner_blobs.sha256 = deploy_files.sha256
  )
ORDER BY deploy_files.sha256;

-- name: MarkDeployActivated :exec
UPDATE deploys SET activated_at = sqlc.arg(activated_at) WHERE id = sqlc.arg(id);

-- A preview deploy went live as its preview (C1.19).
-- name: MarkDeployPreviewActivated :exec
UPDATE deploys SET preview_activated_at = sqlc.arg(preview_activated_at) WHERE id = sqlc.arg(id);

-- name: DeleteDeploy :execrows
DELETE FROM deploys
WHERE deploys.id = sqlc.arg(id) AND deploys.site_id IN (SELECT sites.id FROM sites WHERE sites.owner_id = sqlc.arg(owner_id));

-- name: PutBlob :exec
INSERT INTO blobs (sha256, size, created_at)
VALUES (sqlc.arg(sha256), sqlc.arg(size), sqlc.arg(created_at))
ON CONFLICT (sha256) DO NOTHING;

-- name: PutOwnerBlob :exec
INSERT INTO owner_blobs (owner_id, sha256, created_at)
VALUES (sqlc.arg(owner_id), sqlc.arg(sha256), sqlc.arg(created_at))
ON CONFLICT (owner_id, sha256) DO NOTHING;

-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
-- (Licence header last: sqlc copies comments above a query into Go docs.)
