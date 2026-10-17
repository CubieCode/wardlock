-- name: CreatePasskey :exec
INSERT INTO owner_passkeys (id, owner_id, credential_id, credential, name, created_at, last_used_at)
VALUES (sqlc.arg(id), sqlc.arg(owner_id), sqlc.arg(credential_id), sqlc.arg(credential), sqlc.arg(name), sqlc.arg(created_at), sqlc.narg(last_used_at));

-- Oldest first, so an owner's list keeps its order as passkeys are added.
-- name: ListOwnerPasskeys :many
SELECT id, owner_id, credential_id, credential, name, created_at, last_used_at
FROM owner_passkeys
WHERE owner_id = sqlc.arg(owner_id)
ORDER BY created_at, id;

-- Instance-wide: discoverable sign-in knows only the credential ID.
-- name: GetPasskeyByCredentialID :one
SELECT id, owner_id, credential_id, credential, name, created_at, last_used_at
FROM owner_passkeys WHERE credential_id = sqlc.arg(credential_id);

-- name: RecordPasskeyUse :execrows
UPDATE owner_passkeys SET credential = sqlc.arg(credential), last_used_at = sqlc.arg(last_used_at)
WHERE id = sqlc.arg(id) AND owner_id = sqlc.arg(owner_id);

-- name: DeleteOwnerPasskey :execrows
DELETE FROM owner_passkeys WHERE id = sqlc.arg(id) AND owner_id = sqlc.arg(owner_id);

-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
-- (Licence header last: sqlc copies comments above a query into Go docs.)
