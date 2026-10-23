-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only

-- Traffic rollup (V5.4): hourly counts of the access log for analytics,
-- kept apart from the log's own retention. The access-log writer adds to
-- them in the same transaction as each batch of entries, so they always
-- equal a recount of the log over the same entries. They hold no IP
-- address, User-Agent or token: only counts by site and UTC hour.
--
-- hour is the start of the UTC hour, in Unix milliseconds (Postgres:
-- BIGINT). allowed counts requests answered below 400, refused the rest
-- (Postgres: BIGINT too).
-- Deleting a site (its owner's delete or the operator's teardown) deletes
-- its rows; queries add rows only for a site that exists.

-- Requests by way in. Summed over via, these are the site's hourly totals.
CREATE TABLE traffic_hours (
  site_id TEXT NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
  hour INTEGER NOT NULL,
  via TEXT NOT NULL CHECK (via IN ('public', 'link', 'password', 'email', 'oauth', 'none')),
  allowed INTEGER NOT NULL CHECK (allowed >= 0),
  refused INTEGER NOT NULL CHECK (refused >= 0),
  PRIMARY KEY (site_id, hour, via)
);

CREATE INDEX traffic_hours_hour ON traffic_hours (hour);

-- Requests through each share link, by public ID. A deleted link's rows
-- stay until retention: they name it, never its token.
CREATE TABLE traffic_links (
  site_id TEXT NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
  hour INTEGER NOT NULL,
  link_public_id TEXT NOT NULL CHECK (length(link_public_id) BETWEEN 1 AND 64),
  allowed INTEGER NOT NULL CHECK (allowed >= 0),
  refused INTEGER NOT NULL CHECK (refused >= 0),
  PRIMARY KEY (site_id, hour, link_public_id)
);

CREATE INDEX traffic_links_hour ON traffic_links (hour);

-- Requests by path, top paths only: a site keeps at most 50 distinct paths
-- per hour (store.MaxTrafficPaths), the first 50 the writer sees, and
-- counts the rest under the empty path, which stands for every other path.
CREATE TABLE traffic_paths (
  site_id TEXT NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
  hour INTEGER NOT NULL,
  path TEXT NOT NULL CHECK (length(path) <= 256),
  allowed INTEGER NOT NULL CHECK (allowed >= 0),
  refused INTEGER NOT NULL CHECK (refused >= 0),
  PRIMARY KEY (site_id, hour, path)
);

CREATE INDEX traffic_paths_hour ON traffic_paths (hour);

-- Requests by browser family, classified from the User-Agent when the
-- entry is written; the User-Agent itself is not kept here.
CREATE TABLE traffic_browsers (
  site_id TEXT NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
  hour INTEGER NOT NULL,
  browser TEXT NOT NULL CHECK (browser IN ('chrome', 'safari', 'firefox', 'edge', 'tool', 'other')),
  allowed INTEGER NOT NULL CHECK (allowed >= 0),
  refused INTEGER NOT NULL CHECK (refused >= 0),
  PRIMARY KEY (site_id, hour, browser)
);

CREATE INDEX traffic_browsers_hour ON traffic_browsers (hour);

-- One row once the one-time backfill has counted every entry the access
-- log held when it ran (Postgres: done_at BIGINT). Until then batches add
-- entries to the log only, so each entry is counted exactly once.
CREATE TABLE traffic_backfill (
  id TEXT NOT NULL PRIMARY KEY CHECK (id = 'access_log'),
  done_at INTEGER NOT NULL
);
