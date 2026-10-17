-- name: CreateDomain :exec
INSERT INTO domains (id, site_id, hostname, status, verification_token, created_at, last_checked_at, failing_since, last_failure_reason)
VALUES (sqlc.arg(id), sqlc.arg(site_id), sqlc.arg(hostname), sqlc.arg(status), sqlc.arg(verification_token), sqlc.arg(created_at), sqlc.narg(last_checked_at), sqlc.narg(failing_since), sqlc.narg(last_failure_reason));

-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
-- (Licence header last: sqlc copies comments above a query into Go docs.)
