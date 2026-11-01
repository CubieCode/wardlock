-- Every query here that means a person reads kind = 'owner': a team's row
-- in owners is an account, never a principal (T7.1).
-- name: CreateOwner :exec
INSERT INTO owners (id, email, invited_by, created_at, accepted_at)
VALUES (sqlc.arg(id), sqlc.arg(email), sqlc.narg(invited_by), sqlc.arg(created_at), sqlc.narg(accepted_at));

-- name: GetOwner :one
SELECT id, email, invited_by, created_at, accepted_at, suspended_at FROM owners WHERE id = sqlc.arg(id) AND kind = 'owner';

-- Inserts nothing for a team: only a person holds a sign-in code. owner_id is
-- NULL for a setup code.
-- name: CreateOwnerLoginCode :execrows
INSERT INTO owner_login_codes (id, owner_id, purpose, code_hash, attempts, created_at, expires_at, used_at)
SELECT CAST(sqlc.arg(id) AS TEXT), CAST(sqlc.narg(owner_id) AS TEXT), CAST(sqlc.arg(purpose) AS TEXT), CAST(sqlc.arg(code_hash) AS TEXT),
  CAST(sqlc.arg(attempts) AS INTEGER), CAST(sqlc.arg(created_at) AS INTEGER), CAST(sqlc.arg(expires_at) AS INTEGER), CAST(sqlc.narg(used_at) AS INTEGER)
WHERE NOT EXISTS (SELECT 1 FROM owners WHERE owners.id = sqlc.narg(owner_id) AND owners.kind = 'team');

-- name: GetOwnerByEmail :one
SELECT id, email, invited_by, created_at, accepted_at, suspended_at FROM owners WHERE email = sqlc.arg(email) AND kind = 'owner';

-- name: CountOwners :one
SELECT COUNT(*) FROM owners WHERE kind = 'owner';

-- name: MarkOwnerAccepted :exec
UPDATE owners SET accepted_at = COALESCE(accepted_at, sqlc.arg(accepted_at)) WHERE id = sqlc.arg(id) AND kind = 'owner';

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

-- Suspension (A2.13). Suspending keeps the first time; reinstating clears it.
-- name: SuspendOwner :execrows
UPDATE owners SET suspended_at = COALESCE(suspended_at, sqlc.arg(suspended_at)) WHERE id = sqlc.arg(id) AND kind = 'owner';

-- name: ReinstateOwner :execrows
UPDATE owners SET suspended_at = NULL WHERE id = sqlc.arg(id) AND kind = 'owner';

-- Instance-wide, for `wardlockd admin list-owners` only: every owner by
-- address, after the cursor.
-- name: ListAllOwners :many
SELECT id, email, invited_by, created_at, accepted_at, suspended_at FROM owners
WHERE email > sqlc.arg(after_email) AND kind = 'owner'
ORDER BY email
LIMIT sqlc.arg(row_limit);

-- name: RetireSetupCodes :exec
UPDATE owner_login_codes SET used_at = sqlc.arg(used_at)
WHERE owner_id IS NULL AND purpose = 'setup' AND used_at IS NULL;

-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
-- (Licence header last: sqlc copies comments above a query into Go docs.)
