-- name: CreateAPIToken :exec
INSERT INTO api_tokens (id, owner_id, public_id, token_hash, name, scope_deploy, scope_manage, created_at, expires_at, last_used_at, revoked_at)
VALUES (sqlc.arg(id), sqlc.arg(owner_id), sqlc.arg(public_id), sqlc.arg(token_hash), sqlc.arg(name), sqlc.arg(scope_deploy), sqlc.arg(scope_manage), sqlc.arg(created_at), sqlc.narg(expires_at), sqlc.narg(last_used_at), sqlc.narg(revoked_at));

-- Instance-wide: Bearer authentication looks a token up by its public ID
-- before comparing the HMAC.
-- name: GetAPITokenByPublicID :one
SELECT id, owner_id, public_id, token_hash, name, scope_deploy, scope_manage, created_at, expires_at, last_used_at, revoked_at
FROM api_tokens WHERE public_id = sqlc.arg(public_id);

-- name: TouchAPIToken :exec
UPDATE api_tokens SET last_used_at = sqlc.arg(last_used_at)
WHERE id = sqlc.arg(id) AND (last_used_at IS NULL OR last_used_at < sqlc.arg(last_used_at));

-- name: RevokeAPIToken :execrows
UPDATE api_tokens SET revoked_at = COALESCE(revoked_at, sqlc.arg(revoked_at))
WHERE id = sqlc.arg(id) AND owner_id = sqlc.arg(owner_id);

-- Usable tokens only: not revoked and not expired at now. Newest first; the
-- cursor is the (created_at, id) of the previous page's last row; the first
-- page passes the largest before_created.
-- name: ListAPITokens :many
SELECT id, owner_id, public_id, token_hash, name, scope_deploy, scope_manage, created_at, expires_at, last_used_at, revoked_at
FROM api_tokens
WHERE owner_id = sqlc.arg(owner_id)
  AND revoked_at IS NULL
  AND (expires_at IS NULL OR expires_at > sqlc.arg(now))
  AND (
    created_at < sqlc.arg(before_created)
    OR (created_at = sqlc.arg(before_created) AND id < sqlc.arg(before_id))
  )
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(row_limit);

-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
-- (Licence header last: sqlc copies comments above a query into Go docs.)
