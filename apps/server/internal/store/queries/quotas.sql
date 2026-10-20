-- Owner quotas (A2.14).

-- Takes the owner row's write lock for the rest of the transaction and
-- reads the owner's overrides and stored bytes. SQLite already serialises
-- writers (every transaction begins IMMEDIATE); on Postgres the row lock
-- orders concurrent deploys and site creations by the same owner, so each
-- one's counts include the others'.
-- name: LockOwnerQuotas :one
UPDATE owners SET stored_bytes = stored_bytes WHERE id = sqlc.arg(id)
RETURNING stored_bytes, max_storage_bytes, max_sites, max_deploys_per_day;

-- name: GetOwnerQuotas :one
SELECT stored_bytes, max_storage_bytes, max_sites, max_deploys_per_day FROM owners WHERE id = sqlc.arg(id);

-- name: SetOwnerQuotas :execrows
UPDATE owners SET max_storage_bytes = sqlc.narg(max_storage_bytes), max_sites = sqlc.narg(max_sites), max_deploys_per_day = sqlc.narg(max_deploys_per_day)
WHERE id = sqlc.arg(id);

-- name: SetOwnerStoredBytes :exec
UPDATE owners SET stored_bytes = sqlc.arg(stored_bytes) WHERE id = sqlc.arg(id);

-- name: CountOwnerSites :one
SELECT COUNT(*) FROM sites WHERE owner_id = sqlc.arg(owner_id);

-- Counts one more deploy listing the hash and returns how many now do: 1
-- means the hash is new to the owner and its size joins stored_bytes.
-- name: AddOwnerBlobRef :one
INSERT INTO owner_blob_refs (owner_id, sha256, size, deploys)
VALUES (sqlc.arg(owner_id), sqlc.arg(sha256), sqlc.arg(size), 1)
ON CONFLICT (owner_id, sha256) DO UPDATE SET deploys = owner_blob_refs.deploys + 1
RETURNING deploys;

-- Counts one deploy fewer for each distinct hash the deploy lists. Run it
-- before the deploy is deleted, then SubtractReleasedBytes and
-- DeleteReleasedBlobRefs.
-- name: ReleaseDeployBlobRefs :exec
UPDATE owner_blob_refs SET deploys = deploys - 1
WHERE owner_id = sqlc.arg(owner_id)
  AND sha256 IN (SELECT deploy_files.sha256 FROM deploy_files WHERE deploy_files.deploy_id = sqlc.arg(deploy_id));

-- name: SubtractReleasedBytes :exec
UPDATE owners SET stored_bytes = stored_bytes - (
  SELECT COALESCE(SUM(owner_blob_refs.size), 0) FROM owner_blob_refs
  WHERE owner_blob_refs.owner_id = sqlc.arg(owner_id) AND owner_blob_refs.deploys = 0
)
WHERE id = sqlc.arg(owner_id);

-- name: DeleteReleasedBlobRefs :exec
DELETE FROM owner_blob_refs WHERE owner_id = sqlc.arg(owner_id) AND deploys = 0;

-- name: SiteDeployIDs :many
SELECT id FROM deploys WHERE site_id = sqlc.arg(site_id) ORDER BY id;

-- name: DeployOwner :one
SELECT sites.owner_id FROM deploys JOIN sites ON sites.id = deploys.site_id WHERE deploys.id = sqlc.arg(id);

-- name: PruneOwnerDeployLog :exec
DELETE FROM owner_deploy_log WHERE owner_id = sqlc.arg(owner_id) AND created_at <= sqlc.arg(before);

-- name: CountOwnerDeploysSince :one
SELECT COUNT(*) FROM owner_deploy_log WHERE owner_id = sqlc.arg(owner_id) AND created_at > sqlc.arg(after);

-- name: LogOwnerDeploy :exec
INSERT INTO owner_deploy_log (owner_id, deploy_id, created_at)
VALUES (sqlc.arg(owner_id), sqlc.arg(deploy_id), sqlc.arg(created_at))
ON CONFLICT (owner_id, deploy_id) DO NOTHING;

-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
-- (Licence header last: sqlc copies comments above a query into Go docs.)
