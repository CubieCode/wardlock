-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only

-- Teams (T7.1). A team is an account: a row in owners with kind 'team' and
-- a row in teams with the same id. sites.owner_id, groups, tags, tokens,
-- blob holdings, quotas and stored bytes all name an account, so a site
-- belongs to exactly one owner or one team by construction, and every
-- existing row keeps its current owner. A team row cannot sign in: its
-- address is reserved (RFC 2606 .invalid) and internal/store refuses it
-- wherever a person is meant, so no owner lookup, session, passkey, sign-in
-- code or CLI login ever reaches one.
ALTER TABLE owners ADD COLUMN kind TEXT NOT NULL DEFAULT 'owner' CHECK (kind IN ('owner', 'team'));

-- How many teams the owner may create, overriding WARDLOCK_QUOTA_TEAMS. NULL
-- follows the instance; 0 is unlimited (Postgres: BIGINT).
ALTER TABLE owners ADD COLUMN max_teams INTEGER CHECK (max_teams >= 0);

-- created_by has no ON DELETE CASCADE: a team outlives whoever started it.
CREATE TABLE teams (
  id TEXT NOT NULL PRIMARY KEY REFERENCES owners (id) ON DELETE CASCADE,
  -- Not unique: a name is shown to the team's members only, so a unique one
  -- would let anyone learn which names are taken.
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
  created_by TEXT REFERENCES owners (id) ON DELETE SET NULL,
  created_at INTEGER NOT NULL
);

CREATE INDEX teams_created_by ON teams (created_by);

-- A row is a person's membership. Removing it ends their access on their
-- next request. internal/store keeps at least one admin per team.
CREATE TABLE team_members (
  team_id TEXT NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
  owner_id TEXT NOT NULL REFERENCES owners (id) ON DELETE CASCADE,
  role TEXT NOT NULL CHECK (role IN ('admin', 'editor', 'viewer')),
  created_at INTEGER NOT NULL,
  PRIMARY KEY (team_id, owner_id)
);

CREATE INDEX team_members_owner_id ON team_members (owner_id);

-- An invitation is bound to an address, not to a secret: only an owner
-- signed in with that verified address can see or accept it. One per
-- address per team; inviting again replaces it. accepted_at keeps an accepted
-- invitation so accepting it twice is harmless and, once its member has
-- gone, it grants nothing.
CREATE TABLE team_invitations (
  id TEXT NOT NULL PRIMARY KEY,
  team_id TEXT NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
  email TEXT NOT NULL CHECK (email = lower(email) AND length(email) BETWEEN 3 AND 254),
  role TEXT NOT NULL CHECK (role IN ('admin', 'editor', 'viewer')),
  invited_by TEXT REFERENCES owners (id) ON DELETE SET NULL,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  accepted_at INTEGER,
  UNIQUE (team_id, email),
  CHECK (expires_at > created_at)
);

CREATE INDEX team_invitations_email ON team_invitations (email);

-- Pins are personal, so they leave the site row: each person who can see a
-- team's site pins it for themselves.
CREATE TABLE site_pins (
  owner_id TEXT NOT NULL REFERENCES owners (id) ON DELETE CASCADE,
  site_id TEXT NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
  pinned_at INTEGER NOT NULL,
  PRIMARY KEY (owner_id, site_id)
);

CREATE INDEX site_pins_site_id ON site_pins (site_id);

INSERT INTO site_pins (owner_id, site_id, pinned_at)
SELECT owner_id, id, pinned_at FROM sites WHERE pinned_at IS NOT NULL;

ALTER TABLE sites DROP COLUMN pinned_at;

-- Who made each thing, kept when they leave: created_by is the owner, and
-- for a team token's work the team, with created_by_token naming the token.
-- Neither has a foreign key: the record outlives what it names. A policy's
-- created_by moves with each change. Everything made before now was made by
-- the site's only owner.
ALTER TABLE deploys ADD COLUMN created_by TEXT;
ALTER TABLE deploys ADD COLUMN created_by_token TEXT;
ALTER TABLE policies ADD COLUMN created_by TEXT;
ALTER TABLE policies ADD COLUMN created_by_token TEXT;
ALTER TABLE share_links ADD COLUMN created_by TEXT;
ALTER TABLE share_links ADD COLUMN created_by_token TEXT;
ALTER TABLE domains ADD COLUMN created_by TEXT;
ALTER TABLE domains ADD COLUMN created_by_token TEXT;

UPDATE deploys SET created_by = (SELECT sites.owner_id FROM sites WHERE sites.id = deploys.site_id);
UPDATE policies SET created_by = (SELECT sites.owner_id FROM sites WHERE sites.id = policies.site_id);
UPDATE share_links SET created_by = (SELECT sites.owner_id FROM sites WHERE sites.id = share_links.site_id);
UPDATE domains SET created_by = (SELECT sites.owner_id FROM sites WHERE sites.id = domains.site_id);

-- The admin who created a team token (NULL on personal tokens).
ALTER TABLE api_tokens ADD COLUMN created_by TEXT;
