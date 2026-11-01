-- Inserts only while the owner has fewer than max_passkeys, in the same
-- statement as the count; no row inserted means the owner is at the limit.
-- name: CreatePasskey :execrows
INSERT INTO owner_passkeys (id, owner_id, credential_id, credential, sign_count, name, created_at, last_used_at)
SELECT sqlc.arg(id), sqlc.arg(owner_id), sqlc.arg(credential_id), sqlc.arg(credential), sqlc.arg(sign_count), sqlc.arg(name), sqlc.arg(created_at), sqlc.narg(last_used_at)
WHERE (SELECT count(*) FROM owner_passkeys AS existing WHERE existing.owner_id = sqlc.arg(owner_id)) < CAST(sqlc.arg(max_passkeys) AS INTEGER)
  AND NOT EXISTS (SELECT 1 FROM owners WHERE owners.id = sqlc.arg(owner_id) AND owners.kind = 'team');

-- Oldest first, so an owner's list keeps its order as passkeys are added.
-- name: ListOwnerPasskeys :many
SELECT id, owner_id, credential_id, credential, sign_count, name, created_at, last_used_at
FROM owner_passkeys
WHERE owner_id = sqlc.arg(owner_id)
ORDER BY created_at, id;

-- Instance-wide: discoverable sign-in knows only the credential ID.
-- name: GetPasskeyByCredentialID :one
SELECT id, owner_id, credential_id, credential, sign_count, name, created_at, last_used_at
FROM owner_passkeys WHERE credential_id = sqlc.arg(credential_id);

-- Compare-and-set on the sign count read before the sign-in, so of two
-- concurrent sign-ins with one counter value only the first is recorded.
-- name: RecordPasskeyUse :execrows
UPDATE owner_passkeys
SET credential = sqlc.arg(credential), sign_count = sqlc.arg(sign_count), last_used_at = sqlc.arg(last_used_at)
WHERE id = sqlc.arg(id) AND owner_id = sqlc.arg(owner_id) AND sign_count = sqlc.arg(previous_sign_count);

-- name: DeleteOwnerPasskey :execrows
DELETE FROM owner_passkeys WHERE id = sqlc.arg(id) AND owner_id = sqlc.arg(owner_id);

-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
-- (Licence header last: sqlc copies comments above a query into Go docs.)
