-- name: CreateOwner :exec
INSERT INTO owners (id, email, invited_by, created_at, accepted_at)
VALUES (sqlc.arg(id), sqlc.arg(email), sqlc.narg(invited_by), sqlc.arg(created_at), sqlc.narg(accepted_at));

-- name: GetOwner :one
SELECT id, email, invited_by, created_at, accepted_at FROM owners WHERE id = sqlc.arg(id);

-- name: CreateOwnerLoginCode :exec
INSERT INTO owner_login_codes (id, owner_id, purpose, code_hash, attempts, created_at, expires_at, used_at)
VALUES (sqlc.arg(id), sqlc.narg(owner_id), sqlc.arg(purpose), sqlc.arg(code_hash), sqlc.arg(attempts), sqlc.arg(created_at), sqlc.arg(expires_at), sqlc.narg(used_at));

-- name: GetOwnerByEmail :one
SELECT id, email, invited_by, created_at, accepted_at FROM owners WHERE email = sqlc.arg(email);

-- name: CountOwners :one
SELECT COUNT(*) FROM owners;

-- name: MarkOwnerAccepted :exec
UPDATE owners SET accepted_at = COALESCE(accepted_at, sqlc.arg(accepted_at)) WHERE id = sqlc.arg(id);

-- name: GetOwnerLoginCodeByHash :one
SELECT id, owner_id, purpose, code_hash, attempts, created_at, expires_at, used_at
FROM owner_login_codes
WHERE purpose = sqlc.arg(purpose) AND code_hash = sqlc.arg(code_hash);

-- name: LatestOwnerLoginCode :one
SELECT id, owner_id, purpose, code_hash, attempts, created_at, expires_at, used_at
FROM owner_login_codes
WHERE owner_id = sqlc.arg(owner_id) AND purpose = sqlc.arg(purpose)
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: AddOwnerLoginCodeAttempt :one
UPDATE owner_login_codes SET attempts = attempts + 1
WHERE id = sqlc.arg(id) AND used_at IS NULL AND attempts < sqlc.arg(max_attempts)
RETURNING attempts;

-- name: UseOwnerLoginCode :execrows
UPDATE owner_login_codes SET used_at = sqlc.arg(used_at)
WHERE id = sqlc.arg(id) AND used_at IS NULL AND expires_at > sqlc.arg(used_at);

-- name: RetireOwnerLoginCodes :exec
UPDATE owner_login_codes SET used_at = sqlc.arg(used_at)
WHERE owner_id = sqlc.arg(owner_id) AND purpose = sqlc.arg(purpose) AND used_at IS NULL;

-- name: RetireSetupCodes :exec
UPDATE owner_login_codes SET used_at = sqlc.arg(used_at)
WHERE owner_id IS NULL AND purpose = 'setup' AND used_at IS NULL;

-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
-- (Licence header last: sqlc copies comments above a query into Go docs.)
