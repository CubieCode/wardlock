-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only

-- Requests by country (V5.13): the country the access-log writer looked
-- up from an entry's stored (already truncated) IP in the operator's local
-- IP-to-country database, and a country dimension in the traffic rollup.
--
-- country is an ISO 3166-1 alpha-2 code in upper case, or 'unknown' when
-- the database has no match. It is NULL when the lookup is off, the
-- database could not be read, or no IP was stored. Never a city or region.
-- The check spells out two letters from A to Z without GLOB or regular
-- expressions, so Postgres accepts it as written (Postgres: compare with
-- COLLATE "C", so lower-case letters fall outside the range).
ALTER TABLE access_log ADD COLUMN country TEXT CHECK (
  country IS NULL
  OR country = 'unknown'
  OR (
    length(country) = 2
    AND substr(country, 1, 1) BETWEEN 'A' AND 'Z'
    AND substr(country, 2, 1) BETWEEN 'A' AND 'Z'
  )
);

-- Requests by country, for entries with a country only: summed over
-- country these can be less than the site's traffic_hours totals, since
-- an entry logged with the lookup off has no country. Rows follow
-- traffic_hours: deleted with the site and pruned by hour (Postgres:
-- hour, allowed and refused BIGINT).
CREATE TABLE traffic_countries (
  site_id TEXT NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
  hour INTEGER NOT NULL,
  country TEXT NOT NULL CHECK (
    country = 'unknown'
    OR (
      length(country) = 2
      AND substr(country, 1, 1) BETWEEN 'A' AND 'Z'
      AND substr(country, 2, 1) BETWEEN 'A' AND 'Z'
    )
  ),
  allowed INTEGER NOT NULL CHECK (allowed >= 0),
  refused INTEGER NOT NULL CHECK (refused >= 0),
  PRIMARY KEY (site_id, hour, country)
);

CREATE INDEX traffic_countries_hour ON traffic_countries (hour);
