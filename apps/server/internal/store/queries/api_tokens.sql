-- Inserts a token for the account in owner_id only when its kind is
-- account_kind: an owner's personal token, or a team token with created_by
-- the admin who made it. Nothing else can give a team a personal token.
-- name: CreateAPIToken :execrows
INSERT INTO api_tokens (id, owner_id, public_id, token_hash, name, scope_deploy, scope_manage, created_at, expires_at, last_used_at, revoked_at, created_by)
SELECT CAST(sqlc.arg(id) AS TEXT), owners.id, CAST(sqlc.arg(public_id) AS TEXT), CAST(sqlc.arg(token_hash) AS TEXT), CAST(sqlc.arg(name) AS TEXT),
  CAST(sqlc.arg(scope_deploy) AS INTEGER), CAST(sqlc.arg(scope_manage) AS INTEGER), CAST(sqlc.arg(created_at) AS INTEGER),
  CAST(sqlc.narg(expires_at) AS INTEGER), CAST(sqlc.narg(last_used_at) AS INTEGER), CAST(sqlc.narg(revoked_at) AS INTEGER), CAST(sqlc.narg(created_by) AS TEXT)
FROM owners WHERE owners.id = sqlc.arg(owner_id) AND owners.kind = sqlc.arg(account_kind);

-- Instance-wide: Bearer authentication looks a token up by its public ID
-- before comparing the HMAC. A suspended owner's tokens are never found
-- (A2.13); they work again once the owner is reinstated.
-- name: GetAPITokenByPublicID :one
SELECT api_tokens.id, api_tokens.owner_id, api_tokens.public_id, api_tokens.token_hash, api_tokens.name,
  api_tokens.scope_deploy, api_tokens.scope_manage, api_tokens.created_at, api_tokens.expires_at,
  api_tokens.last_used_at, api_tokens.revoked_at, api_tokens.created_by, owners.kind AS account_kind
FROM api_tokens JOIN owners ON owners.id = api_tokens.owner_id
WHERE api_tokens.public_id = sqlc.arg(public_id) AND owners.suspended_at IS NULL;

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
SELECT id, owner_id, public_id, token_hash, name, scope_deploy, scope_manage, created_at, expires_at, last_used_at, revoked_at, created_by
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

-- The name createdBy shows for a token, revoked or not.
-- name: GetAPITokenName :one
SELECT name FROM api_tokens WHERE id = sqlc.arg(id);

-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
-- (Licence header last: sqlc copies comments above a query into Go docs.)
