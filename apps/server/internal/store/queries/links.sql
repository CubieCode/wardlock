-- name: CreateShareLink :exec
INSERT INTO share_links (id, site_id, public_id, token_hash, label, path_prefix, created_at, expires_at, max_uses, use_count, last_accessed_at, rotated_at, revoked_at, created_by, created_by_token)
VALUES (sqlc.arg(id), sqlc.arg(site_id), sqlc.arg(public_id), sqlc.arg(token_hash), sqlc.narg(label), sqlc.arg(path_prefix), sqlc.arg(created_at), sqlc.narg(expires_at), sqlc.narg(max_uses), sqlc.arg(use_count), sqlc.narg(last_accessed_at), sqlc.narg(rotated_at), sqlc.narg(revoked_at), sqlc.narg(created_by), sqlc.narg(created_by_token));

-- Instance-wide: the edge looks a token up by its public ID before
-- comparing the HMAC.
-- name: GetShareLinkByPublicID :one
SELECT id, site_id, public_id, token_hash, label, path_prefix, created_at, expires_at, max_uses, use_count, last_accessed_at, rotated_at, revoked_at, used_up_at, created_by, created_by_token
FROM share_links WHERE public_id = sqlc.arg(public_id);

-- Instance-wide: the edge checks the link behind a link session.
-- name: GetShareLink :one
SELECT id, site_id, public_id, token_hash, label, path_prefix, created_at, expires_at, max_uses, use_count, last_accessed_at, rotated_at, revoked_at, used_up_at, created_by, created_by_token
FROM share_links WHERE id = sqlc.arg(id) AND site_id = sqlc.arg(site_id);

-- Unrevoked links of one of the owner's sites, newest first; the cursor is
-- the (created_at, id) of the previous page's last row; the first page
-- passes the largest before_created.
-- name: ListShareLinks :many
SELECT share_links.id, share_links.site_id, share_links.public_id, share_links.token_hash, share_links.label, share_links.path_prefix, share_links.created_at, share_links.expires_at, share_links.max_uses, share_links.use_count, share_links.last_accessed_at, share_links.rotated_at, share_links.revoked_at, share_links.used_up_at, share_links.created_by, share_links.created_by_token
FROM share_links JOIN sites ON sites.id = share_links.site_id
WHERE share_links.site_id = sqlc.arg(site_id) AND sites.owner_id = sqlc.arg(owner_id)
  AND share_links.revoked_at IS NULL
  AND (
    share_links.created_at < sqlc.arg(before_created)
    OR (share_links.created_at = sqlc.arg(before_created) AND share_links.id < sqlc.arg(before_id))
  )
ORDER BY share_links.created_at DESC, share_links.id DESC
LIMIT sqlc.arg(row_limit);

-- name: RevokeShareLink :one
UPDATE share_links SET revoked_at = sqlc.arg(revoked_at)
WHERE site_id = sqlc.arg(site_id) AND public_id = sqlc.arg(public_id) AND revoked_at IS NULL
  AND site_id IN (SELECT sites.id FROM sites WHERE sites.owner_id = sqlc.arg(owner_id))
RETURNING id;

-- Rotation keeps the public ID, label, scope, expiry and use count; only the
-- secret (and so the hash) changes.
-- name: RotateShareLink :one
UPDATE share_links SET token_hash = sqlc.arg(token_hash), rotated_at = sqlc.arg(rotated_at)
WHERE site_id = sqlc.arg(site_id) AND public_id = sqlc.arg(public_id) AND revoked_at IS NULL
  AND site_id IN (SELECT sites.id FROM sites WHERE sites.owner_id = sqlc.arg(owner_id))
RETURNING id, site_id, public_id, token_hash, label, path_prefix, created_at, expires_at, max_uses, use_count, last_accessed_at, rotated_at, revoked_at, used_up_at, created_by, created_by_token;

-- Ends every session a link created, so revocation and rotation reach
-- browsers that hold its cookie.
-- name: RevokeShareLinkSessions :exec
UPDATE site_sessions SET revoked_at = COALESCE(revoked_at, sqlc.arg(revoked_at))
WHERE site_id = sqlc.arg(site_id) AND share_link_id = sqlc.arg(share_link_id);

-- Spends one use of a usable link whose hash is still token_hash (so a
-- rotation in between fails it), noting when the last use goes.
-- name: UseShareLink :execrows
UPDATE share_links SET
  use_count = use_count + 1,
  last_accessed_at = sqlc.arg(at),
  used_up_at = CASE WHEN max_uses IS NOT NULL AND use_count + 1 >= max_uses THEN sqlc.arg(at) ELSE used_up_at END
WHERE id = sqlc.arg(id) AND token_hash = sqlc.arg(token_hash) AND revoked_at IS NULL
  AND (expires_at IS NULL OR expires_at > sqlc.arg(at))
  AND (max_uses IS NULL OR use_count < max_uses);

-- Creates a link session only while the link still has token_hash and is
-- unrevoked and unexpired at created_at, in one statement, so a rotation or
-- revocation that commits first leaves nothing behind (its own session
-- revocation would miss a row inserted after it).
-- name: CreateLinkSession :execrows
INSERT INTO site_sessions (id, site_id, token_hash, method, share_link_id, policy_id, policy_version, subject, created_at, expires_at, revoked_at)
SELECT sqlc.arg(id), share_links.site_id, sqlc.arg(session_token_hash), 'link', share_links.id, sqlc.arg(policy_id), sqlc.arg(policy_version), NULL, sqlc.arg(created_at), sqlc.arg(expires_at), NULL
FROM share_links
WHERE share_links.id = sqlc.arg(share_link_id) AND share_links.site_id = sqlc.arg(site_id)
  AND share_links.token_hash = sqlc.arg(link_token_hash) AND share_links.revoked_at IS NULL
  AND (share_links.expires_at IS NULL OR share_links.expires_at > sqlc.arg(created_at));

-- name: TouchShareLink :exec
UPDATE share_links SET last_accessed_at = sqlc.arg(last_accessed_at)
WHERE id = sqlc.arg(id) AND (last_accessed_at IS NULL OR last_accessed_at < sqlc.arg(last_accessed_at));

-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
-- (Licence header last: sqlc copies comments above a query into Go docs.)
