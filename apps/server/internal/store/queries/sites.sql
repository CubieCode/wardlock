-- name: CreateSite :exec
INSERT INTO sites (id, owner_id, name, spa_fallback, created_at)
VALUES (sqlc.arg(id), sqlc.arg(owner_id), sqlc.arg(name), sqlc.arg(spa_fallback), sqlc.arg(created_at));

-- name: GetSite :one
SELECT id, owner_id, name, spa_fallback, active_deploy_id, created_at FROM sites
WHERE id = sqlc.arg(id) AND owner_id = sqlc.arg(owner_id);

-- name: SiteOwnedBy :one
SELECT id FROM sites WHERE id = sqlc.arg(id) AND owner_id = sqlc.arg(owner_id);

-- name: SetActiveDeploy :execrows
UPDATE sites SET active_deploy_id = sqlc.arg(deploy_id)
WHERE sites.id = sqlc.arg(site_id) AND sites.owner_id = sqlc.arg(owner_id)
  AND EXISTS (SELECT 1 FROM deploys WHERE deploys.id = sqlc.arg(deploy_id) AND deploys.site_id = sites.id);

-- name: DeleteSite :execrows
DELETE FROM sites WHERE id = sqlc.arg(id) AND owner_id = sqlc.arg(owner_id);

-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
-- (Licence header last: sqlc copies comments above a query into Go docs.)
