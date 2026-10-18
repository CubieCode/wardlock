-- The HTML files (.html or .htm in any letter case) in one of the owner's
-- deploys: the pages that get renditions.
-- name: ListRenditionSources :many
SELECT deploy_files.path, deploy_files.sha256, deploy_files.size FROM deploy_files
JOIN deploys ON deploys.id = deploy_files.deploy_id
JOIN sites ON sites.id = deploys.site_id
WHERE deploy_files.deploy_id = sqlc.arg(deploy_id) AND deploys.site_id = sqlc.arg(site_id) AND sites.owner_id = sqlc.arg(owner_id)
  AND (lower(deploy_files.path) LIKE '%.html' OR lower(deploy_files.path) LIKE '%.htm')
ORDER BY deploy_files.path;

-- name: PutRendition :exec
INSERT INTO deploy_renditions (deploy_id, path, format, sha256, size)
VALUES (sqlc.arg(deploy_id), sqlc.arg(path), sqlc.arg(format), sqlc.arg(sha256), sqlc.arg(size))
ON CONFLICT (deploy_id, path, format) DO UPDATE SET sha256 = excluded.sha256, size = excluded.size;

-- Instance-wide: edge serving only.
-- name: ListDeployRenditions :many
SELECT path, format, sha256, size FROM deploy_renditions WHERE deploy_id = sqlc.arg(deploy_id) ORDER BY path, format;

-- AI-readable renditions (A2.6).

-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
-- (Licence header last: sqlc copies comments above a query into Go docs.)
