-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only

-- AI-readable renditions (A2.6): the Markdown and plain-text versions of
-- each HTML file in a deploy, generated before activation and stored as
-- blobs. A rendition belongs to one manifest entry and goes with it, so
-- deleting a deploy (teardown, garbage collection) deletes its renditions.
-- Garbage collection keeps every blob a rendition lists, as it does for
-- deploy_files. trim(s, chars) accepts exactly 64 lowercase hex characters
-- (same form on Postgres), as in owner_blobs.
CREATE TABLE deploy_renditions (
  deploy_id TEXT NOT NULL,
  path TEXT NOT NULL,
  format TEXT NOT NULL CHECK (format IN ('md', 'txt')),
  sha256 TEXT NOT NULL CHECK (length(sha256) = 64 AND trim(sha256, '0123456789abcdef') = ''),
  size INTEGER NOT NULL CHECK (size >= 0),
  PRIMARY KEY (deploy_id, path, format),
  FOREIGN KEY (deploy_id, path) REFERENCES deploy_files (deploy_id, path) ON DELETE CASCADE
);

-- Garbage collection looks up references by hash.
CREATE INDEX deploy_renditions_sha256 ON deploy_renditions (sha256);
