-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
--
-- Core data model v0 (F0.3). Portable SQL: the same statements must run on
-- Postgres, so there is no AUTOINCREMENT, INSERT OR REPLACE or SQLite-only
-- function, and only length(), lower() and LIKE appear in CHECKs.
--
-- Conventions (docs/ARCHITECTURE.md, Data model):
-- - IDs are TEXT (UUIDv7) generated in Go.
-- - *_at columns, sizes and counts are INTEGER. Postgres: BIGINT. Timestamps
--   are UTC Unix milliseconds; NULL means "not yet" or "never".
-- - Booleans are INTEGER NOT NULL CHECK (x IN (0, 1)). Postgres: same, or
--   BOOLEAN with the CHECK dropped.
-- - Token, session and code columns hold HMAC-SHA-256 hashes, never the raw
--   value. The domain verification token is the exception: it is published in
--   DNS, so it is stored as is.

CREATE TABLE owners (
  id TEXT NOT NULL PRIMARY KEY,
  email TEXT NOT NULL UNIQUE CHECK (email = lower(email) AND length(email) BETWEEN 3 AND 254 AND email LIKE '%_@_%'),
  -- The owner who sent the invite; NULL for owners created by setup or
  -- `wardlockd admin create`.
  invited_by TEXT REFERENCES owners (id) ON DELETE SET NULL,
  created_at INTEGER NOT NULL,
  -- First successful sign-in. NULL while an invite (or an admin-created
  -- owner) has not been taken up yet.
  accepted_at INTEGER
);

-- One-time owner credentials (C1.1), hashed:
-- - setup: the first-start setup URL; owner_id is NULL, valid 60 minutes.
-- - signin_url: the one-time sign-in URL from `wardlockd admin create` or an
--   invite.
-- - email_code: a 6-digit dashboard sign-in code, valid 10 minutes, 5 attempts.
-- used_at makes each single use; rows by owner_id and created_at count recent
-- sends for rate limits.
CREATE TABLE owner_login_codes (
  id TEXT NOT NULL PRIMARY KEY,
  owner_id TEXT REFERENCES owners (id) ON DELETE CASCADE,
  purpose TEXT NOT NULL CHECK (purpose IN ('setup', 'signin_url', 'email_code')),
  code_hash TEXT NOT NULL,
  attempts INTEGER NOT NULL CHECK (attempts >= 0),
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  used_at INTEGER,
  CHECK ((purpose = 'setup') = (owner_id IS NULL)),
  CHECK (expires_at > created_at)
);

CREATE INDEX owner_login_codes_code_hash ON owner_login_codes (code_hash);

CREATE INDEX owner_login_codes_owner_id_created_at ON owner_login_codes (owner_id, created_at);

-- Dashboard sessions. Idle expiry is last_seen_at plus the idle timeout;
-- expires_at is the absolute expiry.
CREATE TABLE owner_sessions (
  id TEXT NOT NULL PRIMARY KEY,
  owner_id TEXT NOT NULL REFERENCES owners (id) ON DELETE CASCADE,
  token_hash TEXT NOT NULL UNIQUE,
  created_at INTEGER NOT NULL,
  last_seen_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  revoked_at INTEGER,
  CHECK (expires_at > created_at),
  CHECK (last_seen_at >= created_at)
);

CREATE INDEX owner_sessions_owner_id ON owner_sessions (owner_id);

-- API tokens: wl_<public id>_<secret>; only the HMAC of the whole token is kept.
CREATE TABLE api_tokens (
  id TEXT NOT NULL PRIMARY KEY,
  owner_id TEXT NOT NULL REFERENCES owners (id) ON DELETE CASCADE,
  public_id TEXT NOT NULL UNIQUE CHECK (length(public_id) = 10 AND public_id = lower(public_id)),
  token_hash TEXT NOT NULL,
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
  scope_deploy INTEGER NOT NULL CHECK (scope_deploy IN (0, 1)),
  scope_manage INTEGER NOT NULL CHECK (scope_manage IN (0, 1)),
  created_at INTEGER NOT NULL,
  expires_at INTEGER,
  last_used_at INTEGER,
  revoked_at INTEGER,
  CHECK (scope_deploy = 1 OR scope_manage = 1),
  CHECK (expires_at IS NULL OR expires_at > created_at)
);

CREATE INDEX api_tokens_owner_id ON api_tokens (owner_id);

-- Device-code style CLI login (C1.7). The CLI polls with a secret device code
-- (stored hashed); the owner confirms the short user code in the dashboard.
-- The API token is minted when the CLI redeems an approved request, so no raw
-- token is ever stored here.
CREATE TABLE cli_login_requests (
  id TEXT NOT NULL PRIMARY KEY,
  device_code_hash TEXT NOT NULL UNIQUE,
  user_code TEXT NOT NULL UNIQUE CHECK (length(user_code) BETWEEN 6 AND 16),
  status TEXT NOT NULL CHECK (status IN ('pending', 'approved', 'denied', 'redeemed')),
  owner_id TEXT REFERENCES owners (id) ON DELETE CASCADE,
  scope_deploy INTEGER NOT NULL CHECK (scope_deploy IN (0, 1)),
  scope_manage INTEGER NOT NULL CHECK (scope_manage IN (0, 1)),
  api_token_id TEXT REFERENCES api_tokens (id) ON DELETE SET NULL,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  decided_at INTEGER,
  CHECK (expires_at > created_at),
  CHECK ((status = 'pending') = (owner_id IS NULL)),
  CHECK (status <> 'redeemed' OR api_token_id IS NOT NULL)
);

-- Site names are DNS labels; the character set and reserved names are
-- checked in Go (internal/sites), the shape here.
CREATE TABLE sites (
  id TEXT NOT NULL PRIMARY KEY,
  owner_id TEXT NOT NULL REFERENCES owners (id),
  name TEXT NOT NULL UNIQUE CHECK (
    length(name) BETWEEN 1 AND 63
    AND name = lower(name)
    AND name NOT LIKE '-%'
    AND name NOT LIKE '%-'
  ),
  spa_fallback INTEGER NOT NULL CHECK (spa_fallback IN (0, 1)),
  created_at INTEGER NOT NULL
);

CREATE INDEX sites_owner_id ON sites (owner_id);

-- Custom domains (C1.9). Hostnames are lowercase A-labels without a trailing
-- dot or port, unique per instance.
CREATE TABLE domains (
  id TEXT NOT NULL PRIMARY KEY,
  site_id TEXT NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
  hostname TEXT NOT NULL UNIQUE CHECK (
    length(hostname) BETWEEN 1 AND 253
    AND hostname = lower(hostname)
    AND hostname NOT LIKE '%.'
    AND hostname NOT LIKE '%:%'
  ),
  status TEXT NOT NULL CHECK (status IN ('pending', 'active', 'lapsed')),
  verification_token TEXT NOT NULL UNIQUE CHECK (length(verification_token) >= 16),
  created_at INTEGER NOT NULL,
  last_checked_at INTEGER,
  -- First failure in the current run of consecutive failed checks; NULL once a
  -- check passes. An active domain lapses 72 hours after this.
  failing_since INTEGER,
  last_failure_reason TEXT CHECK (last_failure_reason IN ('no_record', 'cname_elsewhere', 'txt_mismatch', 'dns_error')),
  CHECK (failing_since IS NULL OR last_failure_reason IS NOT NULL)
);

CREATE INDEX domains_site_id ON domains (site_id);

CREATE TABLE deploys (
  id TEXT NOT NULL PRIMARY KEY,
  site_id TEXT NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
  message TEXT CHECK (length(message) <= 500),
  file_count INTEGER NOT NULL CHECK (file_count >= 0),
  total_size INTEGER NOT NULL CHECK (total_size >= 0),
  created_at INTEGER NOT NULL,
  -- Most recent activation; NULL until first activated.
  activated_at INTEGER
);

CREATE INDEX deploys_site_id_created_at ON deploys (site_id, created_at);

-- Added after deploys exists because the two tables reference each other.
-- NO ACTION (the default) means the active deploy cannot be deleted on its own,
-- while deleting the site still cascades through its deploys.
ALTER TABLE sites ADD COLUMN active_deploy_id TEXT REFERENCES deploys (id);

-- Content-addressed file bodies; the bytes live in the BlobStore.
CREATE TABLE blobs (
  sha256 TEXT NOT NULL PRIMARY KEY CHECK (length(sha256) = 64 AND sha256 = lower(sha256)),
  size INTEGER NOT NULL CHECK (size >= 0),
  created_at INTEGER NOT NULL
);

-- A deploy's manifest. sha256 deliberately has no foreign key to blobs: the
-- manifest is recorded before its missing blobs are uploaded, and activation
-- checks they all exist.
CREATE TABLE deploy_files (
  deploy_id TEXT NOT NULL REFERENCES deploys (id) ON DELETE CASCADE,
  path TEXT NOT NULL CHECK (length(path) BETWEEN 1 AND 1024 AND path NOT LIKE '/%'),
  sha256 TEXT NOT NULL CHECK (length(sha256) = 64 AND sha256 = lower(sha256)),
  size INTEGER NOT NULL CHECK (size >= 0),
  PRIMARY KEY (deploy_id, path)
);

-- Garbage collection (C1.8) looks up references by hash.
CREATE INDEX deploy_files_sha256 ON deploy_files (sha256);

-- One default policy per site (path_prefix '/') plus path policies. version
-- increases on every change so site sessions can be re-checked.
CREATE TABLE policies (
  id TEXT NOT NULL PRIMARY KEY,
  site_id TEXT NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
  path_prefix TEXT NOT NULL CHECK (path_prefix LIKE '/%' AND length(path_prefix) <= 1024),
  visibility TEXT NOT NULL CHECK (visibility IN ('public', 'protected')),
  links_enabled INTEGER NOT NULL CHECK (links_enabled IN (0, 1)),
  password_enabled INTEGER NOT NULL CHECK (password_enabled IN (0, 1)),
  email_enabled INTEGER NOT NULL CHECK (email_enabled IN (0, 1)),
  oauth_enabled INTEGER NOT NULL CHECK (oauth_enabled IN (0, 1)),
  -- Argon2id (A2.3).
  password_hash TEXT,
  version INTEGER NOT NULL CHECK (version >= 1),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  UNIQUE (site_id, path_prefix),
  CHECK (visibility = 'protected' OR (links_enabled = 0 AND password_enabled = 0 AND email_enabled = 0 AND oauth_enabled = 0)),
  CHECK (password_enabled = 0 OR password_hash IS NOT NULL)
);

-- Share links (A2.2): wl_<public id>_<secret>; only the HMAC is kept.
CREATE TABLE share_links (
  id TEXT NOT NULL PRIMARY KEY,
  site_id TEXT NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
  public_id TEXT NOT NULL UNIQUE CHECK (length(public_id) = 10 AND public_id = lower(public_id)),
  token_hash TEXT NOT NULL,
  label TEXT CHECK (length(label) <= 200),
  path_prefix TEXT NOT NULL CHECK (path_prefix LIKE '/%' AND length(path_prefix) <= 1024),
  created_at INTEGER NOT NULL,
  expires_at INTEGER,
  max_uses INTEGER CHECK (max_uses >= 1),
  use_count INTEGER NOT NULL CHECK (use_count >= 0),
  last_accessed_at INTEGER,
  rotated_at INTEGER,
  revoked_at INTEGER,
  CHECK (max_uses IS NULL OR use_count <= max_uses),
  CHECK (expires_at IS NULL OR expires_at > created_at)
);

CREATE INDEX share_links_site_id ON share_links (site_id);

-- Visitor sessions on a site, created by any successful method. The cookie
-- carries a random token; only its HMAC is kept.
CREATE TABLE site_sessions (
  id TEXT NOT NULL PRIMARY KEY,
  site_id TEXT NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
  token_hash TEXT NOT NULL UNIQUE,
  method TEXT NOT NULL CHECK (method IN ('link', 'password', 'email', 'oauth')),
  share_link_id TEXT REFERENCES share_links (id) ON DELETE CASCADE,
  policy_id TEXT NOT NULL REFERENCES policies (id) ON DELETE CASCADE,
  policy_version INTEGER NOT NULL CHECK (policy_version >= 1),
  -- Verified email for email-code and OAuth sessions.
  subject TEXT,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  revoked_at INTEGER,
  CHECK ((method = 'link') = (share_link_id IS NOT NULL)),
  CHECK (expires_at > created_at)
);

CREATE INDEX site_sessions_site_id ON site_sessions (site_id);

CREATE INDEX site_sessions_share_link_id ON site_sessions (share_link_id);

-- One row per access. No foreign keys, so it can move to a separate store and
-- outlive the sites it mentions. Never holds raw tokens.
CREATE TABLE access_log (
  id TEXT NOT NULL PRIMARY KEY,
  occurred_at INTEGER NOT NULL,
  site_id TEXT NOT NULL,
  host TEXT NOT NULL,
  path TEXT NOT NULL,
  status INTEGER NOT NULL CHECK (status BETWEEN 100 AND 599),
  via TEXT NOT NULL CHECK (via IN ('public', 'link', 'password', 'email', 'oauth', 'none')),
  link_public_id TEXT,
  -- Truncated per configuration before it is written.
  ip TEXT,
  user_agent TEXT,
  CHECK ((via = 'link') = (link_public_id IS NOT NULL))
);

CREATE INDEX access_log_site_id_occurred_at ON access_log (site_id, occurred_at);

CREATE INDEX access_log_link_public_id_occurred_at ON access_log (link_public_id, occurred_at);
