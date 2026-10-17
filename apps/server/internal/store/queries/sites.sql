-- name: CreateSite :exec
INSERT INTO sites (id, owner_id, name, created_at)
VALUES (sqlc.arg(id), sqlc.arg(owner_id), sqlc.arg(name), sqlc.arg(created_at));

-- name: GetSite :one
SELECT sites.id, sites.owner_id, sites.name, sites.active_deploy_id, sites.created_at, deploys.activated_at
FROM sites LEFT JOIN deploys ON deploys.id = sites.active_deploy_id
WHERE sites.id = sqlc.arg(id) AND sites.owner_id = sqlc.arg(owner_id);

-- name: GetSiteByOwnerName :one
SELECT sites.id, sites.owner_id, sites.name, sites.active_deploy_id, sites.created_at, deploys.activated_at
FROM sites LEFT JOIN deploys ON deploys.id = sites.active_deploy_id
WHERE sites.name = sqlc.arg(name) AND sites.owner_id = sqlc.arg(owner_id);

-- Instance-wide: edge routing only, never owner API handlers.
-- name: GetSiteByName :one
SELECT sites.id, sites.owner_id, sites.name, sites.active_deploy_id, sites.created_at, deploys.activated_at
FROM sites LEFT JOIN deploys ON deploys.id = sites.active_deploy_id
WHERE sites.name = sqlc.arg(name);

-- Instance-wide: edge routing only, never owner API handlers.
-- name: GetSiteByID :one
SELECT sites.id, sites.owner_id, sites.name, sites.active_deploy_id, sites.created_at, deploys.activated_at
FROM sites LEFT JOIN deploys ON deploys.id = sites.active_deploy_id
WHERE sites.id = sqlc.arg(id);

-- Any status: the owner API resolves pending and lapsed hostnames too.
-- name: GetSiteByOwnerDomain :one
SELECT sites.id, sites.owner_id, sites.name, sites.active_deploy_id, sites.created_at, deploys.activated_at
FROM sites
JOIN domains ON domains.site_id = sites.id
LEFT JOIN deploys ON deploys.id = sites.active_deploy_id
WHERE domains.hostname = sqlc.arg(hostname) AND sites.owner_id = sqlc.arg(owner_id);

-- name: ListSites :many
SELECT sites.id, sites.owner_id, sites.name, sites.active_deploy_id, sites.created_at, deploys.activated_at
FROM sites LEFT JOIN deploys ON deploys.id = sites.active_deploy_id
WHERE sites.owner_id = sqlc.arg(owner_id) AND sites.name > sqlc.arg(after_name)
ORDER BY sites.name
LIMIT sqlc.arg(row_limit);

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
