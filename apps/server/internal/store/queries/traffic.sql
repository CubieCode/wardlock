-- Rollup writes (V5.4), inside the access-log batch's transaction. Each
-- adds to one counter row, and only for a site that still exists, so a
-- site deleted while its entries waited never fails the batch.

-- name: TrafficBackfilled :one
SELECT COUNT(*) FROM traffic_backfill;

-- Claims the backfill: no row affected means it has already run.
-- name: MarkTrafficBackfilled :execrows
INSERT INTO traffic_backfill (id, done_at) VALUES ('access_log', sqlc.arg(done_at))
ON CONFLICT (id) DO NOTHING;

-- name: AddTrafficHour :exec
INSERT INTO traffic_hours (site_id, hour, via, allowed, refused)
SELECT sites.id, sqlc.arg(hour), sqlc.arg(via), sqlc.arg(allowed), sqlc.arg(refused)
FROM sites WHERE sites.id = sqlc.arg(site_id)
ON CONFLICT (site_id, hour, via) DO UPDATE
SET allowed = traffic_hours.allowed + excluded.allowed, refused = traffic_hours.refused + excluded.refused;

-- name: AddTrafficLink :exec
INSERT INTO traffic_links (site_id, hour, link_public_id, allowed, refused)
SELECT sites.id, sqlc.arg(hour), sqlc.arg(link_public_id), sqlc.arg(allowed), sqlc.arg(refused)
FROM sites WHERE sites.id = sqlc.arg(site_id)
ON CONFLICT (site_id, hour, link_public_id) DO UPDATE
SET allowed = traffic_links.allowed + excluded.allowed, refused = traffic_links.refused + excluded.refused;

-- name: AddTrafficBrowser :exec
INSERT INTO traffic_browsers (site_id, hour, browser, allowed, refused)
SELECT sites.id, sqlc.arg(hour), sqlc.arg(browser), sqlc.arg(allowed), sqlc.arg(refused)
FROM sites WHERE sites.id = sqlc.arg(site_id)
ON CONFLICT (site_id, hour, browser) DO UPDATE
SET allowed = traffic_browsers.allowed + excluded.allowed, refused = traffic_browsers.refused + excluded.refused;

-- Adds to a path's row while the site has fewer than max_paths distinct
-- paths in the hour, or the path already has a row; otherwise it adds
-- nothing (no row affected) and the caller counts it under the empty path,
-- which always takes it.
-- name: AddTrafficPath :execrows
INSERT INTO traffic_paths (site_id, hour, path, allowed, refused)
SELECT sites.id, sqlc.arg(hour), sqlc.arg(path), sqlc.arg(allowed), sqlc.arg(refused)
FROM sites WHERE sites.id = sqlc.arg(site_id)
  AND (
    sqlc.arg(path) = ''
    OR EXISTS (
      SELECT 1 FROM traffic_paths AS seen
      WHERE seen.site_id = sqlc.arg(site_id) AND seen.hour = sqlc.arg(hour) AND seen.path = sqlc.arg(path)
    )
    OR (
      SELECT COUNT(*) FROM traffic_paths AS kept
      WHERE kept.site_id = sqlc.arg(site_id) AND kept.hour = sqlc.arg(hour) AND kept.path <> ''
    ) < CAST(sqlc.arg(max_paths) AS INTEGER)
  )
ON CONFLICT (site_id, hour, path) DO UPDATE
SET allowed = traffic_paths.allowed + excluded.allowed, refused = traffic_paths.refused + excluded.refused;

-- The backfill reads the log oldest first, after the cursor (occurred_at,
-- id), for sites that exist: a deleted site's entries have nowhere to go.
-- name: ListAccessLogForRollup :many
SELECT access_log.id, access_log.occurred_at, access_log.site_id, access_log.path, access_log.status, access_log.via, access_log.link_public_id, access_log.user_agent
FROM access_log JOIN sites ON sites.id = access_log.site_id
WHERE access_log.occurred_at > sqlc.arg(after_occurred_at)
  OR (access_log.occurred_at = sqlc.arg(after_occurred_at) AND access_log.id > sqlc.arg(after_id))
ORDER BY access_log.occurred_at, access_log.id
LIMIT sqlc.arg(row_limit);

-- Retention: whole hours before the cutoff, on any site, except those of a
-- taken-down site or a suspended owner's sites, kept as the access log
-- keeps theirs. Callers delete one hour of one table at a time, oldest
-- first, so no statement holds the write lock for long.
-- name: OldestPrunableTrafficHour :one
SELECT CAST(COALESCE(MIN(traffic_hours.hour), -1) AS BIGINT) FROM traffic_hours
WHERE traffic_hours.hour < sqlc.arg(before)
  AND NOT EXISTS (
    SELECT 1 FROM sites JOIN owners ON owners.id = sites.owner_id
    WHERE sites.id = traffic_hours.site_id AND (sites.taken_down_at IS NOT NULL OR owners.suspended_at IS NOT NULL)
  );

-- name: PruneTrafficHours :execrows
DELETE FROM traffic_hours WHERE hour = sqlc.arg(hour)
  AND site_id NOT IN (SELECT sites.id FROM sites JOIN owners ON owners.id = sites.owner_id
    WHERE sites.taken_down_at IS NOT NULL OR owners.suspended_at IS NOT NULL);

-- name: PruneTrafficLinks :execrows
DELETE FROM traffic_links WHERE hour = sqlc.arg(hour)
  AND site_id NOT IN (SELECT sites.id FROM sites JOIN owners ON owners.id = sites.owner_id
    WHERE sites.taken_down_at IS NOT NULL OR owners.suspended_at IS NOT NULL);

-- name: PruneTrafficPaths :execrows
DELETE FROM traffic_paths WHERE hour = sqlc.arg(hour)
  AND site_id NOT IN (SELECT sites.id FROM sites JOIN owners ON owners.id = sites.owner_id
    WHERE sites.taken_down_at IS NOT NULL OR owners.suspended_at IS NOT NULL);

-- name: PruneTrafficBrowsers :execrows
DELETE FROM traffic_browsers WHERE hour = sqlc.arg(hour)
  AND site_id NOT IN (SELECT sites.id FROM sites JOIN owners ON owners.id = sites.owner_id
    WHERE sites.taken_down_at IS NOT NULL OR owners.suspended_at IS NOT NULL);

-- Analytics reads, owner-scoped. Sums are cast to BIGINT so Postgres, too,
-- answers 64-bit counts. Each narrows the owner's sites to at most
-- one of a site, a group or a tag (all NULL for every site) and reads the
-- hours from from_hour up to but not including to_hour.

-- name: TrafficByHour :many
SELECT traffic_hours.hour, CAST(SUM(traffic_hours.allowed) AS BIGINT) AS allowed, CAST(SUM(traffic_hours.refused) AS BIGINT) AS refused
FROM traffic_hours JOIN sites ON sites.id = traffic_hours.site_id
WHERE sites.owner_id = sqlc.arg(owner_id)
  AND (CAST(sqlc.narg(site_id) AS TEXT) IS NULL OR sites.id = sqlc.narg(site_id))
  AND (CAST(sqlc.narg(group_id) AS TEXT) IS NULL OR sites.group_id = sqlc.narg(group_id))
  AND (CAST(sqlc.narg(tag_id) AS TEXT) IS NULL OR EXISTS (SELECT 1 FROM site_tags WHERE site_tags.site_id = sites.id AND site_tags.tag_id = sqlc.narg(tag_id)))
  AND traffic_hours.hour >= sqlc.arg(from_hour) AND traffic_hours.hour < sqlc.arg(to_hour)
GROUP BY traffic_hours.hour
ORDER BY traffic_hours.hour;

-- name: TrafficByVia :many
SELECT traffic_hours.via, CAST(SUM(traffic_hours.allowed) AS BIGINT) AS allowed, CAST(SUM(traffic_hours.refused) AS BIGINT) AS refused
FROM traffic_hours JOIN sites ON sites.id = traffic_hours.site_id
WHERE sites.owner_id = sqlc.arg(owner_id)
  AND (CAST(sqlc.narg(site_id) AS TEXT) IS NULL OR sites.id = sqlc.narg(site_id))
  AND (CAST(sqlc.narg(group_id) AS TEXT) IS NULL OR sites.group_id = sqlc.narg(group_id))
  AND (CAST(sqlc.narg(tag_id) AS TEXT) IS NULL OR EXISTS (SELECT 1 FROM site_tags WHERE site_tags.site_id = sites.id AND site_tags.tag_id = sqlc.narg(tag_id)))
  AND traffic_hours.hour >= sqlc.arg(from_hour) AND traffic_hours.hour < sqlc.arg(to_hour)
GROUP BY traffic_hours.via
ORDER BY traffic_hours.via;

-- name: TrafficBySite :many
SELECT sites.name, CAST(SUM(traffic_hours.allowed) AS BIGINT) AS allowed, CAST(SUM(traffic_hours.refused) AS BIGINT) AS refused
FROM traffic_hours JOIN sites ON sites.id = traffic_hours.site_id
WHERE sites.owner_id = sqlc.arg(owner_id)
  AND (CAST(sqlc.narg(site_id) AS TEXT) IS NULL OR sites.id = sqlc.narg(site_id))
  AND (CAST(sqlc.narg(group_id) AS TEXT) IS NULL OR sites.group_id = sqlc.narg(group_id))
  AND (CAST(sqlc.narg(tag_id) AS TEXT) IS NULL OR EXISTS (SELECT 1 FROM site_tags WHERE site_tags.site_id = sites.id AND site_tags.tag_id = sqlc.narg(tag_id)))
  AND traffic_hours.hour >= sqlc.arg(from_hour) AND traffic_hours.hour < sqlc.arg(to_hour)
GROUP BY sites.id, sites.name
ORDER BY sites.name;

-- A NULL group_id is the ungrouped sites.
-- name: TrafficByGroup :many
SELECT sites.group_id, CAST(SUM(traffic_hours.allowed) AS BIGINT) AS allowed, CAST(SUM(traffic_hours.refused) AS BIGINT) AS refused
FROM traffic_hours JOIN sites ON sites.id = traffic_hours.site_id
WHERE sites.owner_id = sqlc.arg(owner_id)
  AND (CAST(sqlc.narg(site_id) AS TEXT) IS NULL OR sites.id = sqlc.narg(site_id))
  AND (CAST(sqlc.narg(group_id) AS TEXT) IS NULL OR sites.group_id = sqlc.narg(group_id))
  AND (CAST(sqlc.narg(tag_id) AS TEXT) IS NULL OR EXISTS (SELECT 1 FROM site_tags WHERE site_tags.site_id = sites.id AND site_tags.tag_id = sqlc.narg(tag_id)))
  AND traffic_hours.hour >= sqlc.arg(from_hour) AND traffic_hours.hour < sqlc.arg(to_hour)
GROUP BY sites.group_id
ORDER BY sites.group_id;

-- A site with several tags counts under each of them.
-- name: TrafficByTag :many
SELECT site_tags.tag_id, CAST(SUM(traffic_hours.allowed) AS BIGINT) AS allowed, CAST(SUM(traffic_hours.refused) AS BIGINT) AS refused
FROM traffic_hours
JOIN sites ON sites.id = traffic_hours.site_id
JOIN site_tags ON site_tags.site_id = sites.id
WHERE sites.owner_id = sqlc.arg(owner_id)
  AND (CAST(sqlc.narg(site_id) AS TEXT) IS NULL OR sites.id = sqlc.narg(site_id))
  AND (CAST(sqlc.narg(group_id) AS TEXT) IS NULL OR sites.group_id = sqlc.narg(group_id))
  AND (CAST(sqlc.narg(tag_id) AS TEXT) IS NULL OR EXISTS (SELECT 1 FROM site_tags AS scoped WHERE scoped.site_id = sites.id AND scoped.tag_id = sqlc.narg(tag_id)))
  AND traffic_hours.hour >= sqlc.arg(from_hour) AND traffic_hours.hour < sqlc.arg(to_hour)
GROUP BY site_tags.tag_id
ORDER BY site_tags.tag_id;

-- The busiest links, with the label of a link that still exists on the
-- site (revoked ones too).
-- name: TopTrafficLinks :many
SELECT sites.name, traffic_links.link_public_id, CAST(COALESCE(MAX(share_links.label), '') AS TEXT) AS label,
  CAST(SUM(traffic_links.allowed) AS BIGINT) AS allowed, CAST(SUM(traffic_links.refused) AS BIGINT) AS refused
FROM traffic_links
JOIN sites ON sites.id = traffic_links.site_id
LEFT JOIN share_links ON share_links.public_id = traffic_links.link_public_id AND share_links.site_id = traffic_links.site_id
WHERE sites.owner_id = sqlc.arg(owner_id)
  AND (CAST(sqlc.narg(site_id) AS TEXT) IS NULL OR sites.id = sqlc.narg(site_id))
  AND (CAST(sqlc.narg(group_id) AS TEXT) IS NULL OR sites.group_id = sqlc.narg(group_id))
  AND (CAST(sqlc.narg(tag_id) AS TEXT) IS NULL OR EXISTS (SELECT 1 FROM site_tags WHERE site_tags.site_id = sites.id AND site_tags.tag_id = sqlc.narg(tag_id)))
  AND traffic_links.hour >= sqlc.arg(from_hour) AND traffic_links.hour < sqlc.arg(to_hour)
GROUP BY sites.id, sites.name, traffic_links.link_public_id
ORDER BY SUM(traffic_links.allowed) + SUM(traffic_links.refused) DESC, sites.name, traffic_links.link_public_id
LIMIT sqlc.arg(row_limit);

-- The busiest kept paths; the empty path (every other path) is read by
-- TrafficOtherPaths instead.
-- name: TopTrafficPaths :many
SELECT sites.name, traffic_paths.path, CAST(SUM(traffic_paths.allowed) AS BIGINT) AS allowed, CAST(SUM(traffic_paths.refused) AS BIGINT) AS refused
FROM traffic_paths JOIN sites ON sites.id = traffic_paths.site_id
WHERE sites.owner_id = sqlc.arg(owner_id)
  AND (CAST(sqlc.narg(site_id) AS TEXT) IS NULL OR sites.id = sqlc.narg(site_id))
  AND (CAST(sqlc.narg(group_id) AS TEXT) IS NULL OR sites.group_id = sqlc.narg(group_id))
  AND (CAST(sqlc.narg(tag_id) AS TEXT) IS NULL OR EXISTS (SELECT 1 FROM site_tags WHERE site_tags.site_id = sites.id AND site_tags.tag_id = sqlc.narg(tag_id)))
  AND traffic_paths.hour >= sqlc.arg(from_hour) AND traffic_paths.hour < sqlc.arg(to_hour)
  AND traffic_paths.path <> ''
GROUP BY sites.id, sites.name, traffic_paths.path
ORDER BY SUM(traffic_paths.allowed) + SUM(traffic_paths.refused) DESC, sites.name, traffic_paths.path
LIMIT sqlc.arg(row_limit);

-- name: TrafficOtherPaths :one
SELECT CAST(COALESCE(SUM(traffic_paths.allowed), 0) AS BIGINT) AS allowed, CAST(COALESCE(SUM(traffic_paths.refused), 0) AS BIGINT) AS refused
FROM traffic_paths JOIN sites ON sites.id = traffic_paths.site_id
WHERE sites.owner_id = sqlc.arg(owner_id)
  AND (CAST(sqlc.narg(site_id) AS TEXT) IS NULL OR sites.id = sqlc.narg(site_id))
  AND (CAST(sqlc.narg(group_id) AS TEXT) IS NULL OR sites.group_id = sqlc.narg(group_id))
  AND (CAST(sqlc.narg(tag_id) AS TEXT) IS NULL OR EXISTS (SELECT 1 FROM site_tags WHERE site_tags.site_id = sites.id AND site_tags.tag_id = sqlc.narg(tag_id)))
  AND traffic_paths.hour >= sqlc.arg(from_hour) AND traffic_paths.hour < sqlc.arg(to_hour)
  AND traffic_paths.path = '';

-- name: TrafficByBrowser :many
SELECT traffic_browsers.browser, CAST(SUM(traffic_browsers.allowed) AS BIGINT) AS allowed, CAST(SUM(traffic_browsers.refused) AS BIGINT) AS refused
FROM traffic_browsers JOIN sites ON sites.id = traffic_browsers.site_id
WHERE sites.owner_id = sqlc.arg(owner_id)
  AND (CAST(sqlc.narg(site_id) AS TEXT) IS NULL OR sites.id = sqlc.narg(site_id))
  AND (CAST(sqlc.narg(group_id) AS TEXT) IS NULL OR sites.group_id = sqlc.narg(group_id))
  AND (CAST(sqlc.narg(tag_id) AS TEXT) IS NULL OR EXISTS (SELECT 1 FROM site_tags WHERE site_tags.site_id = sites.id AND site_tags.tag_id = sqlc.narg(tag_id)))
  AND traffic_browsers.hour >= sqlc.arg(from_hour) AND traffic_browsers.hour < sqlc.arg(to_hour)
GROUP BY traffic_browsers.browser
ORDER BY traffic_browsers.browser;

-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
-- (Licence header last: sqlc copies comments above a query into Go docs.)
