-- name: CreateShareLink :exec
INSERT INTO share_links (id, site_id, public_id, token_hash, label, path_prefix, created_at, expires_at, max_uses, use_count, last_accessed_at, rotated_at, revoked_at)
VALUES (sqlc.arg(id), sqlc.arg(site_id), sqlc.arg(public_id), sqlc.arg(token_hash), sqlc.narg(label), sqlc.arg(path_prefix), sqlc.arg(created_at), sqlc.narg(expires_at), sqlc.narg(max_uses), sqlc.arg(use_count), sqlc.narg(last_accessed_at), sqlc.narg(rotated_at), sqlc.narg(revoked_at));

-- Instance-wide: the edge looks a token up by its public ID before
-- comparing the HMAC.
-- name: GetShareLinkByPublicID :one
SELECT id, site_id, public_id, token_hash, label, path_prefix, created_at, expires_at, max_uses, use_count, last_accessed_at, rotated_at, revoked_at
FROM share_links WHERE public_id = sqlc.arg(public_id);

-- Instance-wide: the edge checks the link behind a link session.
-- name: GetShareLink :one
SELECT id, site_id, public_id, token_hash, label, path_prefix, created_at, expires_at, max_uses, use_count, last_accessed_at, rotated_at, revoked_at
FROM share_links WHERE id = sqlc.arg(id) AND site_id = sqlc.arg(site_id);

-- Unrevoked links of one of the owner's sites, newest first; the cursor is
-- the (created_at, id) of the previous page's last row; the first page
-- passes the largest before_created.
-- name: ListShareLinks :many
SELECT share_links.id, share_links.site_id, share_links.public_id, share_links.token_hash, share_links.label, share_links.path_prefix, share_links.created_at, share_links.expires_at, share_links.max_uses, share_links.use_count, share_links.last_accessed_at, share_links.rotated_at, share_links.revoked_at
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
RETURNING id, site_id, public_id, token_hash, label, path_prefix, created_at, expires_at, max_uses, use_count, last_accessed_at, rotated_at, revoked_at;

-- Ends every session a link created, so revocation and rotation reach
-- browsers that hold its cookie.
-- name: RevokeShareLinkSessions :exec
UPDATE site_sessions SET revoked_at = COALESCE(revoked_at, sqlc.arg(revoked_at))
WHERE site_id = sqlc.arg(site_id) AND share_link_id = sqlc.arg(share_link_id);

-- Spends one use of a usable link whose hash is still token_hash (so a
-- rotation in between fails it).
-- name: UseShareLink :execrows
UPDATE share_links SET use_count = use_count + 1, last_accessed_at = sqlc.arg(at)
WHERE id = sqlc.arg(id) AND token_hash = sqlc.arg(token_hash) AND revoked_at IS NULL
  AND (expires_at IS NULL OR expires_at > sqlc.arg(at))
  AND (max_uses IS NULL OR use_count < max_uses);

-- name: TouchShareLink :exec
UPDATE share_links SET last_accessed_at = sqlc.arg(last_accessed_at)
WHERE id = sqlc.arg(id) AND (last_accessed_at IS NULL OR last_accessed_at < sqlc.arg(last_accessed_at));

-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
-- (Licence header last: sqlc copies comments above a query into Go docs.)
