-- name: InsertAccessLogEntry :exec
INSERT INTO access_log (id, occurred_at, site_id, host, path, status, via, link_public_id, ip, user_agent)
VALUES (sqlc.arg(id), sqlc.arg(occurred_at), sqlc.arg(site_id), sqlc.arg(host), sqlc.arg(path), sqlc.arg(status), sqlc.arg(via), sqlc.narg(link_public_id), sqlc.narg(ip), sqlc.narg(user_agent));

-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
-- (Licence header last: sqlc copies comments above a query into Go docs.)
