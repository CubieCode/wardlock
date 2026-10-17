-- name: CreatePolicy :exec
INSERT INTO policies (id, site_id, path_prefix, visibility, links_enabled, password_enabled, email_enabled, oauth_enabled, password_hash, version, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(site_id), sqlc.arg(path_prefix), sqlc.arg(visibility), sqlc.arg(links_enabled), sqlc.arg(password_enabled), sqlc.arg(email_enabled), sqlc.arg(oauth_enabled), sqlc.narg(password_hash), sqlc.arg(version), sqlc.arg(created_at), sqlc.arg(updated_at));

-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
-- (Licence header last: sqlc copies comments above a query into Go docs.)
