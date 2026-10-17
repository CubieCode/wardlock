-- name: CreateOwner :exec
INSERT INTO owners (id, email, invited_by, created_at, accepted_at)
VALUES (sqlc.arg(id), sqlc.arg(email), sqlc.narg(invited_by), sqlc.arg(created_at), sqlc.narg(accepted_at));

-- name: GetOwner :one
SELECT id, email, invited_by, created_at, accepted_at FROM owners WHERE id = sqlc.arg(id);

-- name: CreateOwnerLoginCode :exec
INSERT INTO owner_login_codes (id, owner_id, purpose, code_hash, attempts, created_at, expires_at, used_at)
VALUES (sqlc.arg(id), sqlc.narg(owner_id), sqlc.arg(purpose), sqlc.arg(code_hash), sqlc.arg(attempts), sqlc.arg(created_at), sqlc.arg(expires_at), sqlc.narg(used_at));

-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
-- (Licence header last: sqlc copies comments above a query into Go docs.)
