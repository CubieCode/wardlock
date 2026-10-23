-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only

-- Groups, tags and pins (V5.3): how an owner organises their sites in the
-- dashboard and CLI. None of it is ever shown to visitors. Names are unique
-- per owner, compared exactly. internal/sites checks every rule before
-- anything is stored; the checks here (lengths in characters) are a
-- backstop.

-- At most one group per site. Deleting a group keeps its sites: they become
-- ungrouped.
CREATE TABLE site_groups (
  id TEXT NOT NULL PRIMARY KEY,
  owner_id TEXT NOT NULL REFERENCES owners (id) ON DELETE CASCADE,
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 40),
  -- NULL for no note.
  note TEXT CHECK (note IS NULL OR length(note) BETWEEN 1 AND 200),
  created_at INTEGER NOT NULL,
  UNIQUE (owner_id, name)
);

-- colour is a slot in the dashboard's tag palette: 0 is neutral, 1 to 8
-- the categorical colours.
CREATE TABLE tags (
  id TEXT NOT NULL PRIMARY KEY,
  owner_id TEXT NOT NULL REFERENCES owners (id) ON DELETE CASCADE,
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 30 AND name = lower(name)),
  colour INTEGER NOT NULL CHECK (colour BETWEEN 0 AND 8),
  created_at INTEGER NOT NULL,
  UNIQUE (owner_id, name)
);

-- A site's tags. Deleting the tag or the site (an owner's delete or the
-- operator's teardown) deletes its rows. Queries add a row only when the
-- site and the tag have the same owner.
CREATE TABLE site_tags (
  site_id TEXT NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
  tag_id TEXT NOT NULL REFERENCES tags (id) ON DELETE CASCADE,
  PRIMARY KEY (site_id, tag_id)
);

CREATE INDEX site_tags_tag_id ON site_tags (tag_id);

-- NULL for an ungrouped site. Queries set it only to a group of the site's
-- own owner.
ALTER TABLE sites ADD COLUMN group_id TEXT REFERENCES site_groups (id) ON DELETE SET NULL;

-- When the owner pinned the site; NULL while it is not pinned (Postgres:
-- BIGINT).
ALTER TABLE sites ADD COLUMN pinned_at INTEGER;

CREATE INDEX sites_group_id ON sites (group_id);
