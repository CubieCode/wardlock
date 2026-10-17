-- name: CreatePolicy :exec
INSERT INTO policies (id, site_id, path_prefix, visibility, links_enabled, password_enabled, email_enabled, oauth_enabled, password_hash, version, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(site_id), sqlc.arg(path_prefix), sqlc.arg(visibility), sqlc.arg(links_enabled), sqlc.arg(password_enabled), sqlc.arg(email_enabled), sqlc.arg(oauth_enabled), sqlc.narg(password_hash), sqlc.arg(version), sqlc.arg(created_at), sqlc.arg(updated_at));

-- Instance-wide: edge routing only, never owner API handlers.
-- name: ListSitePolicies :many
SELECT id, site_id, path_prefix, visibility, links_enabled, password_enabled, email_enabled, oauth_enabled, password_hash, version, created_at, updated_at
FROM policies WHERE site_id = sqlc.arg(site_id)
ORDER BY path_prefix;

-- Instance-wide: inserts the site default unless one exists.
-- name: EnsureDefaultPolicy :exec
INSERT INTO policies (id, site_id, path_prefix, visibility, links_enabled, password_enabled, email_enabled, oauth_enabled, password_hash, version, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(site_id), '/', sqlc.arg(visibility), sqlc.arg(links_enabled), sqlc.arg(password_enabled), sqlc.arg(email_enabled), sqlc.arg(oauth_enabled), sqlc.narg(password_hash), 1, sqlc.arg(created_at), sqlc.arg(created_at))
ON CONFLICT (site_id, path_prefix) DO NOTHING;

-- Ordered by path prefix; the cursor is the previous page's last prefix
-- ('' for the first page).
-- name: ListOwnerPolicies :many
SELECT policies.id, policies.site_id, policies.path_prefix, policies.visibility, policies.links_enabled, policies.password_enabled, policies.email_enabled, policies.oauth_enabled, policies.password_hash, policies.version, policies.created_at, policies.updated_at
FROM policies JOIN sites ON sites.id = policies.site_id
WHERE sites.owner_id = sqlc.arg(owner_id) AND policies.site_id = sqlc.arg(site_id)
  AND policies.path_prefix > sqlc.arg(after_prefix)
ORDER BY policies.path_prefix
LIMIT sqlc.arg(row_limit);

-- name: GetPolicyByPrefix :one
SELECT policies.id, policies.site_id, policies.path_prefix, policies.visibility, policies.links_enabled, policies.password_enabled, policies.email_enabled, policies.oauth_enabled, policies.password_hash, policies.version, policies.created_at, policies.updated_at
FROM policies JOIN sites ON sites.id = policies.site_id
WHERE sites.owner_id = sqlc.arg(owner_id) AND policies.site_id = sqlc.arg(site_id)
  AND policies.path_prefix = sqlc.arg(path_prefix);

-- name: UpdatePolicy :exec
UPDATE policies
SET visibility = sqlc.arg(visibility), links_enabled = sqlc.arg(links_enabled), password_enabled = sqlc.arg(password_enabled),
  email_enabled = sqlc.arg(email_enabled), oauth_enabled = sqlc.arg(oauth_enabled), password_hash = sqlc.narg(password_hash),
  version = sqlc.arg(version), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND site_id = sqlc.arg(site_id);

-- name: DeletePolicy :execrows
DELETE FROM policies
WHERE policies.id = sqlc.arg(id) AND policies.site_id = sqlc.arg(site_id)
  AND policies.site_id IN (SELECT sites.id FROM sites WHERE sites.owner_id = sqlc.arg(owner_id));

-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
-- (Licence header last: sqlc copies comments above a query into Go docs.)
