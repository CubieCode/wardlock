-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only

-- Preview deploys (C1.19): a deploy can go to a named preview of its site,
-- served at <site>--<name>.<base domain>, instead of the live site.
--
-- previews holds each site's previews by name, with the deploy each serves.
-- lifetime_days is how long it lasts after each deploy (1 to 90, or 0 for
-- never) and expires_at when the current one runs out (NULL for never);
-- once past, the preview answers as an unknown host until garbage
-- collection deletes the row. Names follow the same rule as the deploys
-- package checks: 1-20 of a-z, 0-9 and '-', no leading, trailing or double
-- '-', so <site>--<name> splits at its last '--'.
CREATE TABLE previews (
  site_id TEXT NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
  name TEXT NOT NULL CHECK (
    length(name) BETWEEN 1 AND 20
    AND name = lower(name)
    AND name NOT LIKE '-%'
    AND name NOT LIKE '%-'
    AND name NOT LIKE '%--%'
  ),
  deploy_id TEXT NOT NULL REFERENCES deploys (id) ON DELETE CASCADE,
  lifetime_days INTEGER NOT NULL CHECK (lifetime_days BETWEEN 0 AND 90),
  created_at INTEGER NOT NULL,
  deployed_at INTEGER NOT NULL,
  expires_at INTEGER,
  PRIMARY KEY (site_id, name)
);

CREATE INDEX previews_deploy_id ON previews (deploy_id);
CREATE INDEX previews_expires_at ON previews (expires_at);

-- A deploy created for a preview names it in preview, with the lifetime
-- asked for (NULL: the preview keeps its own, or 14 days for a new one).
-- preview_activated_at is when it went live as that preview; activated_at
-- stays the live site's activations only, so a preview deploy enters the
-- site's history and rollback list only when it is promoted. Existing
-- deploys are not previews.
ALTER TABLE deploys ADD COLUMN preview TEXT CHECK (preview IS NULL OR length(preview) BETWEEN 1 AND 20);
ALTER TABLE deploys ADD COLUMN preview_lifetime_days INTEGER CHECK (preview_lifetime_days IS NULL OR preview_lifetime_days BETWEEN 0 AND 90);
ALTER TABLE deploys ADD COLUMN preview_activated_at INTEGER;

-- The preview a request reached, NULL for the live site. Analytics leaves
-- such entries out of its rollup.
ALTER TABLE access_log ADD COLUMN preview TEXT;
