-- name: CreateOwnerSession :exec
INSERT INTO owner_sessions (id, owner_id, token_hash, created_at, last_seen_at, expires_at, revoked_at, user_agent)
VALUES (sqlc.arg(id), sqlc.arg(owner_id), sqlc.arg(token_hash), sqlc.arg(created_at), sqlc.arg(last_seen_at), sqlc.arg(expires_at), sqlc.narg(revoked_at), sqlc.narg(user_agent));

-- name: CreateSiteSession :exec
INSERT INTO site_sessions (id, site_id, token_hash, method, share_link_id, policy_id, policy_version, subject, created_at, expires_at, revoked_at)
VALUES (sqlc.arg(id), sqlc.arg(site_id), sqlc.arg(token_hash), sqlc.arg(method), sqlc.narg(share_link_id), sqlc.arg(policy_id), sqlc.arg(policy_version), sqlc.narg(subject), sqlc.arg(created_at), sqlc.arg(expires_at), sqlc.narg(revoked_at));

-- name: GetOwnerSessionByTokenHash :one
SELECT id, owner_id, token_hash, created_at, last_seen_at, expires_at, revoked_at, user_agent
FROM owner_sessions WHERE token_hash = sqlc.arg(token_hash);

-- name: TouchOwnerSession :exec
UPDATE owner_sessions SET last_seen_at = sqlc.arg(last_seen_at)
WHERE id = sqlc.arg(id) AND last_seen_at < sqlc.arg(last_seen_at);

-- name: RevokeOwnerSession :execrows
UPDATE owner_sessions SET revoked_at = COALESCE(revoked_at, sqlc.arg(revoked_at))
WHERE id = sqlc.arg(id) AND owner_id = sqlc.arg(owner_id);

-- Active sessions only: not revoked, before the absolute expiry and seen
-- after idle_since. Ordered most recently used first; the cursor is the
-- (last_seen_at, id) of the previous page's last row; the first page passes
-- the largest after_seen.
-- name: ListOwnerSessions :many
SELECT id, owner_id, token_hash, created_at, last_seen_at, expires_at, revoked_at, user_agent
FROM owner_sessions
WHERE owner_id = sqlc.arg(owner_id)
  AND revoked_at IS NULL
  AND expires_at > sqlc.arg(now)
  AND last_seen_at > sqlc.arg(idle_since)
  AND (
    last_seen_at < sqlc.arg(after_seen)
    OR (last_seen_at = sqlc.arg(after_seen) AND id < sqlc.arg(after_id))
  )
ORDER BY last_seen_at DESC, id DESC
LIMIT sqlc.arg(row_limit);

-- Instance-wide: the edge verifies visitor cookies with it.
-- name: GetSiteSessionByTokenHash :one
SELECT id, site_id, token_hash, method, share_link_id, policy_id, policy_version, subject, created_at, expires_at, revoked_at
FROM site_sessions WHERE token_hash = sqlc.arg(token_hash);

-- name: RevokeSiteSession :execrows
UPDATE site_sessions SET revoked_at = COALESCE(revoked_at, sqlc.arg(revoked_at))
WHERE id = sqlc.arg(id) AND site_id = sqlc.arg(site_id);

-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
-- (Licence header last: sqlc copies comments above a query into Go docs.)
