-- Inserts nothing for an unknown or suspended owner, in the same statement
-- that reads the suspension, so a session can never be created by a
-- sign-in that raced a suspension (A2.13).
-- name: CreateOwnerSession :execrows
INSERT INTO owner_sessions (id, owner_id, token_hash, created_at, last_seen_at, expires_at, revoked_at, user_agent)
SELECT CAST(sqlc.arg(id) AS TEXT), owners.id, CAST(sqlc.arg(token_hash) AS TEXT), CAST(sqlc.arg(created_at) AS INTEGER),
  CAST(sqlc.arg(last_seen_at) AS INTEGER), CAST(sqlc.arg(expires_at) AS INTEGER), CAST(sqlc.narg(revoked_at) AS INTEGER),
  CAST(sqlc.narg(user_agent) AS TEXT)
FROM owners WHERE owners.id = sqlc.arg(owner_id) AND owners.suspended_at IS NULL;

-- name: CreateSiteSession :exec
INSERT INTO site_sessions (id, site_id, token_hash, method, share_link_id, policy_id, policy_version, subject, created_at, expires_at, revoked_at)
VALUES (sqlc.arg(id), sqlc.arg(site_id), sqlc.arg(token_hash), sqlc.arg(method), sqlc.narg(share_link_id), sqlc.arg(policy_id), sqlc.arg(policy_version), sqlc.narg(subject), sqlc.arg(created_at), sqlc.arg(expires_at), sqlc.narg(revoked_at));

-- A suspended owner's sessions are never found (A2.13).
-- name: GetOwnerSessionByTokenHash :one
SELECT owner_sessions.id, owner_sessions.owner_id, owner_sessions.token_hash, owner_sessions.created_at,
  owner_sessions.last_seen_at, owner_sessions.expires_at, owner_sessions.revoked_at, owner_sessions.user_agent
FROM owner_sessions JOIN owners ON owners.id = owner_sessions.owner_id
WHERE owner_sessions.token_hash = sqlc.arg(token_hash) AND owners.suspended_at IS NULL;

-- name: TouchOwnerSession :exec
UPDATE owner_sessions SET last_seen_at = sqlc.arg(last_seen_at)
WHERE id = sqlc.arg(id) AND last_seen_at < sqlc.arg(last_seen_at);

-- name: RevokeOwnerSession :execrows
UPDATE owner_sessions SET revoked_at = COALESCE(revoked_at, sqlc.arg(revoked_at))
WHERE id = sqlc.arg(id) AND owner_id = sqlc.arg(owner_id);

-- Suspension revokes every session the owner has (A2.13).
-- name: RevokeAllOwnerSessions :exec
UPDATE owner_sessions SET revoked_at = COALESCE(revoked_at, sqlc.arg(revoked_at))
WHERE owner_id = sqlc.arg(owner_id);

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
