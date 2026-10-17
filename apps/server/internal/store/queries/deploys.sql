-- name: CreateDeploy :exec
INSERT INTO deploys (id, site_id, message, file_count, total_size, created_at, activated_at)
VALUES (sqlc.arg(id), sqlc.arg(site_id), sqlc.narg(message), sqlc.arg(file_count), sqlc.arg(total_size), sqlc.arg(created_at), sqlc.narg(activated_at));

-- name: CreateDeployFile :exec
INSERT INTO deploy_files (deploy_id, path, sha256, size)
VALUES (sqlc.arg(deploy_id), sqlc.arg(path), sqlc.arg(sha256), sqlc.arg(size));

-- name: DeleteDeploy :execrows
DELETE FROM deploys
WHERE deploys.id = sqlc.arg(id) AND deploys.site_id IN (SELECT sites.id FROM sites WHERE sites.owner_id = sqlc.arg(owner_id));

-- name: PutBlob :exec
INSERT INTO blobs (sha256, size, created_at)
VALUES (sqlc.arg(sha256), sqlc.arg(size), sqlc.arg(created_at))
ON CONFLICT (sha256) DO NOTHING;

-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
-- (Licence header last: sqlc copies comments above a query into Go docs.)
