-- name: CreateDomain :exec
INSERT INTO domains (id, site_id, hostname, status, verification_token, created_at, last_checked_at, verified_at, failing_since, last_failure_reason, failure_detail)
VALUES (sqlc.arg(id), sqlc.arg(site_id), sqlc.arg(hostname), sqlc.arg(status), sqlc.arg(verification_token), sqlc.arg(created_at), sqlc.narg(last_checked_at), sqlc.narg(verified_at), sqlc.narg(failing_since), sqlc.narg(last_failure_reason), sqlc.narg(failure_detail));

-- Instance-wide: edge routing only. Pending and lapsed hostnames are not served.
-- name: GetActiveDomainByHostname :one
SELECT id, site_id, hostname, status, verification_token, created_at, last_checked_at, verified_at, failing_since, last_failure_reason, failure_detail
FROM domains
WHERE hostname = sqlc.arg(hostname) AND status = 'active';

-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
-- (Licence header last: sqlc copies comments above a query into Go docs.)
