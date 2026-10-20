-- name: InsertAccessLogEntry :exec
INSERT INTO access_log (id, occurred_at, site_id, host, path, status, via, link_public_id, ip, user_agent)
VALUES (sqlc.arg(id), sqlc.arg(occurred_at), sqlc.arg(site_id), sqlc.arg(host), sqlc.arg(path), sqlc.arg(status), sqlc.arg(via), sqlc.narg(link_public_id), sqlc.narg(ip), sqlc.narg(user_agent));

-- A site's entries, newest first, strictly before the cursor (occurred_at,
-- id), for the owner of a site that still exists. The first page passes the
-- largest occurred_at.
-- name: ListAccessLogBySite :many
SELECT access_log.id, access_log.occurred_at, access_log.site_id, access_log.host, access_log.path, access_log.status, access_log.via, access_log.link_public_id, access_log.ip, access_log.user_agent
FROM access_log JOIN sites ON sites.id = access_log.site_id
WHERE access_log.site_id = sqlc.arg(site_id) AND sites.owner_id = sqlc.arg(owner_id)
  AND (access_log.occurred_at < sqlc.arg(before_occurred_at)
    OR (access_log.occurred_at = sqlc.arg(before_occurred_at) AND access_log.id < sqlc.arg(before_id)))
ORDER BY access_log.occurred_at DESC, access_log.id DESC
LIMIT sqlc.arg(row_limit);

-- One share link's entries on a site, paged like ListAccessLogBySite.
-- name: ListAccessLogByLink :many
SELECT access_log.id, access_log.occurred_at, access_log.site_id, access_log.host, access_log.path, access_log.status, access_log.via, access_log.link_public_id, access_log.ip, access_log.user_agent
FROM access_log JOIN sites ON sites.id = access_log.site_id
WHERE access_log.link_public_id = sqlc.arg(link_public_id) AND access_log.site_id = sqlc.arg(site_id)
  AND sites.owner_id = sqlc.arg(owner_id)
  AND (access_log.occurred_at < sqlc.arg(before_occurred_at)
    OR (access_log.occurred_at = sqlc.arg(before_occurred_at) AND access_log.id < sqlc.arg(before_id)))
ORDER BY access_log.occurred_at DESC, access_log.id DESC
LIMIT sqlc.arg(row_limit);

-- Retention: up to row_limit entries of any site, including deleted ones,
-- older than the cutoff. Callers loop, so no one statement holds the write
-- lock for long. Entries of a taken-down site or a suspended owner's site
-- are evidence and kept until the operator restores, reinstates or tears
-- down (A2.13).
-- name: PruneAccessLog :execrows
DELETE FROM access_log WHERE id IN (
  SELECT old.id FROM access_log AS old
  WHERE old.occurred_at < sqlc.arg(before)
    AND NOT EXISTS (
      SELECT 1 FROM sites JOIN owners ON owners.id = sites.owner_id
      WHERE sites.id = old.site_id AND (sites.taken_down_at IS NOT NULL OR owners.suspended_at IS NOT NULL)
    )
  LIMIT sqlc.arg(row_limit)
);

-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
-- (Licence header last: sqlc copies comments above a query into Go docs.)
