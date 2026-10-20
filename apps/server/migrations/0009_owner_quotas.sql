-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only

-- Owner quotas (A2.14). The instance sets defaults in the environment; an
-- operator can override each one per owner with `wardlockd admin quota`.
-- NULL follows the instance default; 0 is unlimited; otherwise bytes, sites
-- or deploys in any 24 hours (Postgres: BIGINT).
ALTER TABLE owners ADD COLUMN max_storage_bytes INTEGER CHECK (max_storage_bytes >= 0);
ALTER TABLE owners ADD COLUMN max_sites INTEGER CHECK (max_sites >= 0);
ALTER TABLE owners ADD COLUMN max_deploys_per_day INTEGER CHECK (max_deploys_per_day >= 0);

-- Running total of the owner's stored bytes: the sum of owner_blob_refs.size
-- for the owner, kept in step by every statement that adds or removes a
-- deploy, so the quota check never adds up the owner's files (Postgres:
-- BIGINT).
ALTER TABLE owners ADD COLUMN stored_bytes INTEGER NOT NULL DEFAULT 0 CHECK (stored_bytes >= 0);

-- Each distinct blob the owner's deploys list, once per owner however many
-- deploys and paths list it, with the number of deploys that do. A row
-- whose count reaches 0 is released: its size leaves stored_bytes and the
-- row is deleted in the same transaction. Unlike owner_blobs (what the owner
-- has uploaded), a pending deploy counts from the moment it is created, so
-- concurrent deploys cannot both claim the same headroom.
CREATE TABLE owner_blob_refs (
  owner_id TEXT NOT NULL REFERENCES owners (id) ON DELETE CASCADE,
  sha256 TEXT NOT NULL CHECK (length(sha256) = 64 AND sha256 = lower(sha256)),
  size INTEGER NOT NULL CHECK (size >= 0),
  deploys INTEGER NOT NULL CHECK (deploys >= 0),
  PRIMARY KEY (owner_id, sha256)
);

CREATE INDEX owner_blob_refs_released ON owner_blob_refs (owner_id) WHERE deploys = 0;

-- One row per deploy the owner created, for the daily deploy quota. Rows
-- outlive the deploy (garbage collection and deleting a site do not hand
-- deploys back) and are pruned once they are more than 24 hours old.
CREATE TABLE owner_deploy_log (
  owner_id TEXT NOT NULL REFERENCES owners (id) ON DELETE CASCADE,
  deploy_id TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (owner_id, deploy_id)
);

CREATE INDEX owner_deploy_log_owner_id_created_at ON owner_deploy_log (owner_id, created_at);

-- Existing deploys count from the start. Two manifests can give one hash
-- different sizes, but only the true size can ever be uploaded; MAX picks
-- one so the row is well defined.
INSERT INTO owner_blob_refs (owner_id, sha256, size, deploys)
SELECT sites.owner_id, deploy_files.sha256, MAX(deploy_files.size), COUNT(DISTINCT deploy_files.deploy_id)
FROM deploy_files
JOIN deploys ON deploys.id = deploy_files.deploy_id
JOIN sites ON sites.id = deploys.site_id
GROUP BY sites.owner_id, deploy_files.sha256;

UPDATE owners SET stored_bytes = (
  SELECT COALESCE(SUM(owner_blob_refs.size), 0) FROM owner_blob_refs WHERE owner_blob_refs.owner_id = owners.id
);

INSERT INTO owner_deploy_log (owner_id, deploy_id, created_at)
SELECT sites.owner_id, deploys.id, deploys.created_at
FROM deploys JOIN sites ON sites.id = deploys.site_id;
