-- name: CreateSite :exec
INSERT INTO sites (id, owner_id, name, created_at)
VALUES (sqlc.arg(id), sqlc.arg(owner_id), sqlc.arg(name), sqlc.arg(created_at));

-- Instance-wide: the edge's routing, and owner reads through Actor.Require.
-- name: GetSiteByName :one
SELECT sites.id, sites.owner_id, sites.name, sites.active_deploy_id, sites.created_at, deploys.activated_at, sites.taken_down_at, sites.group_id,
  sites.primary_base, primary_domains.hostname AS primary_domain, primary_domains.status AS primary_domain_status,
  owners.kind AS owner_kind, owners.email AS owner_email, teams.name AS team_name
FROM sites JOIN owners ON owners.id = sites.owner_id
LEFT JOIN teams ON teams.id = sites.owner_id
LEFT JOIN deploys ON deploys.id = sites.active_deploy_id
LEFT JOIN domains AS primary_domains ON primary_domains.id = sites.primary_domain_id
WHERE sites.name = sqlc.arg(name);

-- Instance-wide, as GetSiteByName.
-- name: GetSiteByID :one
SELECT sites.id, sites.owner_id, sites.name, sites.active_deploy_id, sites.created_at, deploys.activated_at, sites.taken_down_at, sites.group_id,
  sites.primary_base, primary_domains.hostname AS primary_domain, primary_domains.status AS primary_domain_status,
  owners.kind AS owner_kind, owners.email AS owner_email, teams.name AS team_name
FROM sites JOIN owners ON owners.id = sites.owner_id
LEFT JOIN teams ON teams.id = sites.owner_id
LEFT JOIN deploys ON deploys.id = sites.active_deploy_id
LEFT JOIN domains AS primary_domains ON primary_domains.id = sites.primary_domain_id
WHERE sites.id = sqlc.arg(id);

-- Instance-wide, as GetSiteByName. Any status: the owner API resolves
-- pending and lapsed hostnames too.
-- name: GetSiteByDomain :one
SELECT sites.id, sites.owner_id, sites.name, sites.active_deploy_id, sites.created_at, deploys.activated_at, sites.taken_down_at, sites.group_id,
  sites.primary_base, primary_domains.hostname AS primary_domain, primary_domains.status AS primary_domain_status,
  owners.kind AS owner_kind, owners.email AS owner_email, teams.name AS team_name
FROM sites
JOIN domains ON domains.site_id = sites.id
JOIN owners ON owners.id = sites.owner_id
LEFT JOIN teams ON teams.id = sites.owner_id
LEFT JOIN deploys ON deploys.id = sites.active_deploy_id
LEFT JOIN domains AS primary_domains ON primary_domains.id = sites.primary_domain_id
WHERE domains.hostname = sqlc.arg(hostname);

-- name: ListSites :many
SELECT sites.id, sites.owner_id, sites.name, sites.active_deploy_id, sites.created_at, deploys.activated_at, sites.taken_down_at, sites.group_id,
  sites.primary_base, primary_domains.hostname AS primary_domain, primary_domains.status AS primary_domain_status,
  owners.kind AS owner_kind, owners.email AS owner_email, teams.name AS team_name
FROM sites JOIN owners ON owners.id = sites.owner_id
LEFT JOIN teams ON teams.id = sites.owner_id
LEFT JOIN deploys ON deploys.id = sites.active_deploy_id
LEFT JOIN domains AS primary_domains ON primary_domains.id = sites.primary_domain_id
WHERE (sites.owner_id = sqlc.arg(account_id)
    OR sites.owner_id IN (SELECT team_members.team_id FROM team_members WHERE team_members.owner_id = sqlc.arg(member_id)))
  AND sites.name > sqlc.arg(after_name)
ORDER BY sites.name
LIMIT sqlc.arg(row_limit);

-- name: SiteOwnedBy :one
SELECT id FROM sites WHERE id = sqlc.arg(id) AND owner_id = sqlc.arg(owner_id);

-- The account a site belongs to, for Actor.Require.
-- name: SiteAccount :one
SELECT owner_id FROM sites WHERE id = sqlc.arg(id);

-- A taken-down site keeps the deploy it had (A2.13).
-- name: SetActiveDeploy :execrows
UPDATE sites SET active_deploy_id = sqlc.arg(deploy_id)
WHERE sites.id = sqlc.arg(site_id) AND sites.owner_id = sqlc.arg(owner_id) AND sites.taken_down_at IS NULL
  AND EXISTS (SELECT 1 FROM deploys WHERE deploys.id = sqlc.arg(deploy_id) AND deploys.site_id = sites.id);

-- Primary hostname (C1.18). The base-domain hostname needs no row; a
-- custom domain must be one of this site's and active as the write lands,
-- so a domain that lapsed or was removed meanwhile changes nothing. A
-- taken-down site's hostnames are frozen with it (A2.13).
-- name: SetPrimaryBase :execrows
UPDATE sites SET primary_base = 1, primary_domain_id = NULL
WHERE id = sqlc.arg(id) AND owner_id = sqlc.arg(owner_id) AND taken_down_at IS NULL;

-- name: SetPrimaryDomain :execrows
UPDATE sites SET primary_base = 0, primary_domain_id = sqlc.arg(domain_id)
WHERE sites.id = sqlc.arg(id) AND sites.owner_id = sqlc.arg(owner_id) AND sites.taken_down_at IS NULL
  AND EXISTS (SELECT 1 FROM domains WHERE domains.id = sqlc.arg(domain_id) AND domains.site_id = sites.id AND domains.status = 'active');

-- name: ClearPrimary :execrows
UPDATE sites SET primary_base = 0, primary_domain_id = NULL
WHERE id = sqlc.arg(id) AND owner_id = sqlc.arg(owner_id) AND taken_down_at IS NULL;

-- Owners cannot delete a taken-down site: its files are evidence (A2.13).
-- name: DeleteSite :execrows
DELETE FROM sites WHERE id = sqlc.arg(id) AND owner_id = sqlc.arg(owner_id) AND taken_down_at IS NULL;

-- Operator controls (A2.13), for `wardlockd admin` only: instance-wide,
-- never an owner API handler. Taking down keeps the first time.
-- name: TakeDownSite :execrows
UPDATE sites SET taken_down_at = COALESCE(taken_down_at, sqlc.arg(taken_down_at)) WHERE id = sqlc.arg(id);

-- name: RestoreSite :execrows
UPDATE sites SET taken_down_at = NULL WHERE id = sqlc.arg(id);

-- Only a taken-down site, so a restore that raced the operator's check
-- leaves the site alone.
-- name: TearDownSite :execrows
DELETE FROM sites WHERE id = sqlc.arg(id) AND taken_down_at IS NOT NULL;

-- name: ListAllSites :many
SELECT sites.id, sites.owner_id, sites.name, sites.active_deploy_id, sites.created_at, deploys.activated_at, sites.taken_down_at, sites.group_id,
  sites.primary_base, primary_domains.hostname AS primary_domain, primary_domains.status AS primary_domain_status,
  owners.kind AS owner_kind, owners.email AS owner_email, teams.name AS team_name,
  owners.suspended_at AS owner_suspended_at
FROM sites
JOIN owners ON owners.id = sites.owner_id
LEFT JOIN teams ON teams.id = sites.owner_id
LEFT JOIN deploys ON deploys.id = sites.active_deploy_id
LEFT JOIN domains AS primary_domains ON primary_domains.id = sites.primary_domain_id
WHERE sites.name > sqlc.arg(after_name)
ORDER BY sites.name
LIMIT sqlc.arg(row_limit);

-- Sites that answer the unavailable page: taken down, or owned by a
-- suspended owner. The server polls it to drop cached sites when an
-- operator command changes either in another process.
-- name: ListUnavailableSiteIDs :many
SELECT sites.id FROM sites JOIN owners ON owners.id = sites.owner_id
WHERE sites.taken_down_at IS NOT NULL OR owners.suspended_at IS NOT NULL
ORDER BY sites.id;

-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
-- (Licence header last: sqlc copies comments above a query into Go docs.)
