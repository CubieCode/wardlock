-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only

-- SPA fallback is recorded on each deploy, so rollback restores it (F0.2
-- decision 4); the site-wide flag goes.
ALTER TABLE deploys ADD COLUMN spa INTEGER NOT NULL DEFAULT 0 CHECK (spa IN (0, 1));
ALTER TABLE sites DROP COLUMN spa_fallback;

-- Optional fields the API contract (F0.2) exposes.
ALTER TABLE owner_sessions ADD COLUMN user_agent TEXT;
ALTER TABLE domains ADD COLUMN verified_at INTEGER;
ALTER TABLE domains ADD COLUMN failure_detail TEXT;
ALTER TABLE cli_login_requests ADD COLUMN client_name TEXT;

-- Blobs each owner has proved they hold by uploading them with a verified
-- hash. A deploy's missing hashes and the activation check are computed per
-- owner from this table, so one owner can never learn whether another
-- owner's content is on the server. Garbage collection (C1.8) deletes these
-- rows together with the blob. trim(s, chars) strips every hex digit, so the
-- CHECK accepts exactly 64 lowercase hex characters (same form on Postgres).
CREATE TABLE owner_blobs (
  owner_id TEXT NOT NULL REFERENCES owners (id) ON DELETE CASCADE,
  sha256 TEXT NOT NULL CHECK (length(sha256) = 64 AND trim(sha256, '0123456789abcdef') = ''),
  created_at INTEGER NOT NULL,
  PRIMARY KEY (owner_id, sha256)
);

CREATE INDEX owner_blobs_sha256 ON owner_blobs (sha256);
