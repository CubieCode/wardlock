-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only

-- A site's primary hostname (C1.18): requests on its other hostnames
-- answer 308 to the same path on the primary. At most one of the two is
-- set; the queries that write them keep it so. primary_base chooses
-- <name>.<base domain>, held as a flag so it follows the site's name.
-- primary_domain_id chooses one of the site's custom domains: it is in
-- effect only while that domain is active (a lapse suspends it, recovery
-- resumes it) and removing the domain clears it for good. Every existing
-- site has no primary.
ALTER TABLE sites ADD COLUMN primary_base INTEGER NOT NULL DEFAULT 0 CHECK (primary_base IN (0, 1));
ALTER TABLE sites ADD COLUMN primary_domain_id TEXT REFERENCES domains (id) ON DELETE SET NULL;
