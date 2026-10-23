-- Site summaries (V5.5): one owner's sites with what the dashboard's lists,
-- navigator, map and overview show of each. Sites.Summaries runs these
-- four queries, whatever the number of sites, and internal/sites filters,
-- counts, sorts and pages the result.

-- Each of the owner's sites with its live deploy, its default policy (all
-- NULL until the site stores one) and its working and soon-expiring links.
-- A working link is unrevoked, unexpired at now and not used up; an
-- expiring one is a working link that expires by expiring_by and lives
-- longer than min_lifetime (milliseconds from creation to expiry). active
-- is 1 when the rollup has any request from active_from on.
-- name: ListSiteSummaries :many
SELECT sites.id, sites.owner_id, sites.name, sites.active_deploy_id, sites.created_at, deploys.activated_at, sites.taken_down_at, sites.group_id, sites.pinned_at,
  deploys.message AS deploy_message,
  policies.visibility, policies.links_enabled, policies.password_enabled, policies.email_enabled, policies.oauth_enabled,
  CAST((
    SELECT COUNT(*) FROM share_links
    WHERE share_links.site_id = sites.id AND share_links.revoked_at IS NULL
      AND (share_links.expires_at IS NULL OR share_links.expires_at > CAST(sqlc.arg(now) AS BIGINT))
      AND (share_links.max_uses IS NULL OR share_links.use_count < share_links.max_uses)
  ) AS BIGINT) AS working_links,
  CAST((
    SELECT COUNT(*) FROM share_links
    WHERE share_links.site_id = sites.id AND share_links.revoked_at IS NULL
      AND share_links.expires_at > CAST(sqlc.arg(now) AS BIGINT) AND share_links.expires_at <= CAST(sqlc.arg(expiring_by) AS BIGINT)
      AND share_links.expires_at - share_links.created_at > CAST(sqlc.arg(min_lifetime) AS BIGINT)
      AND (share_links.max_uses IS NULL OR share_links.use_count < share_links.max_uses)
  ) AS BIGINT) AS expiring_links,
  CAST(CASE WHEN EXISTS (
    SELECT 1 FROM traffic_hours WHERE traffic_hours.site_id = sites.id AND traffic_hours.hour >= sqlc.arg(active_from)
  ) THEN 1 ELSE 0 END AS BIGINT) AS active
FROM sites
LEFT JOIN deploys ON deploys.id = sites.active_deploy_id
LEFT JOIN policies ON policies.site_id = sites.id AND policies.path_prefix = '/'
WHERE sites.owner_id = sqlc.arg(owner_id)
ORDER BY sites.name;

-- The tags of every one of the owner's sites, each site's in tag-name order.
-- name: ListOwnerSiteTags :many
SELECT site_tags.site_id, site_tags.tag_id
FROM site_tags
JOIN sites ON sites.id = site_tags.site_id
JOIN tags ON tags.id = site_tags.tag_id
WHERE sites.owner_id = sqlc.arg(owner_id) AND tags.owner_id = sqlc.arg(owner_id)
ORDER BY site_tags.site_id, tags.name;

-- The custom domains of every one of the owner's sites, whatever their
-- status.
-- name: ListOwnerSiteDomains :many
SELECT domains.site_id, domains.hostname
FROM domains JOIN sites ON sites.id = domains.site_id
WHERE sites.owner_id = sqlc.arg(owner_id)
ORDER BY domains.site_id, domains.hostname;

-- Requests (allowed and refused) of each of the owner's sites on each UTC
-- day from from_day (a UTC midnight) on: day 0 is from_day's. Days with
-- none are left out. GROUP BY names the day by its output alias, which
-- Postgres accepts too.
-- name: OwnerRequestsByDay :many
SELECT traffic_hours.site_id,
  CAST((traffic_hours.hour - sqlc.arg(from_day)) / 86400000 AS BIGINT) AS day,
  CAST(SUM(traffic_hours.allowed + traffic_hours.refused) AS BIGINT) AS requests
FROM traffic_hours JOIN sites ON sites.id = traffic_hours.site_id
WHERE sites.owner_id = sqlc.arg(owner_id) AND traffic_hours.hour >= sqlc.arg(from_day)
GROUP BY traffic_hours.site_id, day
ORDER BY traffic_hours.site_id, day;

-- The owner's expiring links, soonest first: the links ListSiteSummaries
-- counts as expiring_links, with the same conditions, so the list and the
-- counts agree. Keep the two WHERE clauses identical.
-- name: ListExpiringLinks :many
SELECT share_links.id, sites.name AS site_name, share_links.label, share_links.expires_at, share_links.max_uses, share_links.use_count
FROM share_links JOIN sites ON sites.id = share_links.site_id
WHERE sites.owner_id = sqlc.arg(owner_id) AND share_links.revoked_at IS NULL
  AND share_links.expires_at > CAST(sqlc.arg(now) AS BIGINT) AND share_links.expires_at <= CAST(sqlc.arg(expiring_by) AS BIGINT)
  AND share_links.expires_at - share_links.created_at > CAST(sqlc.arg(min_lifetime) AS BIGINT)
  AND (share_links.max_uses IS NULL OR share_links.use_count < share_links.max_uses)
ORDER BY share_links.expires_at, share_links.id
LIMIT sqlc.arg(row_limit);

-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
-- (Licence header last: sqlc copies comments above a query into Go docs.)
