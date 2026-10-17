-- name: CreateShareLink :exec
INSERT INTO share_links (id, site_id, public_id, token_hash, label, path_prefix, created_at, expires_at, max_uses, use_count, last_accessed_at, rotated_at, revoked_at)
VALUES (sqlc.arg(id), sqlc.arg(site_id), sqlc.arg(public_id), sqlc.arg(token_hash), sqlc.narg(label), sqlc.arg(path_prefix), sqlc.arg(created_at), sqlc.narg(expires_at), sqlc.narg(max_uses), sqlc.arg(use_count), sqlc.narg(last_accessed_at), sqlc.narg(rotated_at), sqlc.narg(revoked_at));

-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
-- (Licence header last: sqlc copies comments above a query into Go docs.)
