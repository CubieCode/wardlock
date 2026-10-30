-- One of the owner's previews that has not expired at now.
-- name: GetPreview :one
SELECT previews.site_id, previews.name, previews.deploy_id, previews.lifetime_days, previews.created_at, previews.deployed_at, previews.expires_at
FROM previews JOIN sites ON sites.id = previews.site_id
WHERE previews.site_id = sqlc.arg(site_id) AND previews.name = sqlc.arg(name) AND sites.owner_id = sqlc.arg(owner_id)
  AND (previews.expires_at IS NULL OR previews.expires_at > CAST(sqlc.arg(now) AS INTEGER));

-- The owner's site's previews that have not expired at now, by name after
-- the cursor ('' for the first page).
-- name: ListPreviews :many
SELECT previews.site_id, previews.name, previews.deploy_id, previews.lifetime_days, previews.created_at, previews.deployed_at, previews.expires_at
FROM previews JOIN sites ON sites.id = previews.site_id
WHERE previews.site_id = sqlc.arg(site_id) AND sites.owner_id = sqlc.arg(owner_id)
  AND (previews.expires_at IS NULL OR previews.expires_at > CAST(sqlc.arg(now) AS INTEGER))
  AND previews.name > sqlc.arg(after_name)
ORDER BY previews.name
LIMIT sqlc.arg(row_limit);

-- Instance-wide: edge routing only. A preview that has expired at now is
-- not served.
-- name: GetServedPreview :one
SELECT site_id, name, deploy_id, lifetime_days, created_at, deployed_at, expires_at FROM previews
WHERE site_id = sqlc.arg(site_id) AND name = sqlc.arg(name)
  AND (expires_at IS NULL OR expires_at > CAST(sqlc.arg(now) AS INTEGER));

-- The lifetime of a preview that has not expired at now, which a deploy
-- that gives none keeps.
-- name: PreviewLifetime :one
SELECT lifetime_days FROM previews
WHERE site_id = sqlc.arg(site_id) AND name = sqlc.arg(name)
  AND (expires_at IS NULL OR expires_at > CAST(sqlc.arg(now) AS INTEGER));

-- Points a preview at a deploy that has just gone live as it. A preview
-- that had expired starts afresh, as if new.
-- name: UpsertPreview :exec
INSERT INTO previews (site_id, name, deploy_id, lifetime_days, created_at, deployed_at, expires_at)
VALUES (sqlc.arg(site_id), sqlc.arg(name), sqlc.arg(deploy_id), sqlc.arg(lifetime_days), sqlc.arg(deployed_at), sqlc.arg(deployed_at), sqlc.narg(expires_at))
ON CONFLICT (site_id, name) DO UPDATE SET
  created_at = CASE WHEN previews.expires_at IS NOT NULL AND previews.expires_at <= excluded.deployed_at THEN excluded.created_at ELSE previews.created_at END,
  deploy_id = excluded.deploy_id,
  lifetime_days = excluded.lifetime_days,
  deployed_at = excluded.deployed_at,
  expires_at = excluded.expires_at;

-- Deletes one of the owner's previews unless it has expired at now or its
-- site is taken down.
-- name: DeletePreview :execrows
DELETE FROM previews
WHERE previews.site_id = sqlc.arg(site_id) AND previews.name = sqlc.arg(name)
  AND (previews.expires_at IS NULL OR previews.expires_at > CAST(sqlc.arg(now) AS INTEGER))
  AND previews.site_id IN (
    SELECT sites.id FROM sites WHERE sites.owner_id = sqlc.arg(owner_id) AND sites.taken_down_at IS NULL
  );

-- Whether any site, of any owner, holds the name: a preview whose hostname
-- label would be a site's is refused. Site names are public through DNS.
-- name: SiteNameExists :one
SELECT COUNT(*) FROM sites WHERE name = sqlc.arg(name);

-- Garbage collection: deletes previews that expired at or before now,
-- except on a taken-down site or a suspended owner's site, kept as
-- evidence (A2.13). Their deploys are pruned afterwards.
-- name: DeleteExpiredPreviews :execrows
DELETE FROM previews
WHERE previews.expires_at IS NOT NULL AND previews.expires_at <= CAST(sqlc.arg(now) AS INTEGER)
  AND NOT EXISTS (
    SELECT 1 FROM sites JOIN owners ON owners.id = sites.owner_id
    WHERE sites.id = previews.site_id AND (sites.taken_down_at IS NOT NULL OR owners.suspended_at IS NOT NULL)
  );

-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
-- (Licence header last: sqlc copies comments above a query into Go docs.)
