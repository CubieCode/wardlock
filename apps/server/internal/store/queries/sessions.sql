-- name: CreateOwnerSession :exec
INSERT INTO owner_sessions (id, owner_id, token_hash, created_at, last_seen_at, expires_at, revoked_at)
VALUES (sqlc.arg(id), sqlc.arg(owner_id), sqlc.arg(token_hash), sqlc.arg(created_at), sqlc.arg(last_seen_at), sqlc.arg(expires_at), sqlc.narg(revoked_at));

-- name: CreateSiteSession :exec
INSERT INTO site_sessions (id, site_id, token_hash, method, share_link_id, policy_id, policy_version, subject, created_at, expires_at, revoked_at)
VALUES (sqlc.arg(id), sqlc.arg(site_id), sqlc.arg(token_hash), sqlc.arg(method), sqlc.narg(share_link_id), sqlc.arg(policy_id), sqlc.arg(policy_version), sqlc.narg(subject), sqlc.arg(created_at), sqlc.arg(expires_at), sqlc.narg(revoked_at));

-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
-- (Licence header last: sqlc copies comments above a query into Go docs.)
