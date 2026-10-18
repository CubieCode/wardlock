-- name: CreateDomain :exec
INSERT INTO domains (id, site_id, hostname, status, verification_token, created_at, last_checked_at, verified_at, failing_since, last_failure_reason, failure_detail)
VALUES (sqlc.arg(id), sqlc.arg(site_id), sqlc.arg(hostname), sqlc.arg(status), sqlc.arg(verification_token), sqlc.arg(created_at), sqlc.narg(last_checked_at), sqlc.narg(verified_at), sqlc.narg(failing_since), sqlc.narg(last_failure_reason), sqlc.narg(failure_detail));

-- Instance-wide: edge routing only. Pending and lapsed hostnames are not served.
-- name: GetActiveDomainByHostname :one
SELECT id, site_id, hostname, status, verification_token, created_at, last_checked_at, verified_at, failing_since, last_failure_reason, failure_detail
FROM domains
WHERE hostname = sqlc.arg(hostname) AND status = 'active';

-- One of the owner's sites' domains by hostname, whatever its status.
-- name: GetDomain :one
SELECT domains.id, domains.site_id, domains.hostname, domains.status, domains.verification_token, domains.created_at, domains.last_checked_at, domains.verified_at, domains.failing_since, domains.last_failure_reason, domains.failure_detail
FROM domains JOIN sites ON sites.id = domains.site_id
WHERE domains.site_id = sqlc.arg(site_id) AND domains.hostname = sqlc.arg(hostname) AND sites.owner_id = sqlc.arg(owner_id);

-- One of the owner's sites' domains ordered by hostname, after
-- after_hostname ('' for the first page).
-- name: ListDomains :many
SELECT domains.id, domains.site_id, domains.hostname, domains.status, domains.verification_token, domains.created_at, domains.last_checked_at, domains.verified_at, domains.failing_since, domains.last_failure_reason, domains.failure_detail
FROM domains JOIN sites ON sites.id = domains.site_id
WHERE domains.site_id = sqlc.arg(site_id) AND sites.owner_id = sqlc.arg(owner_id)
  AND domains.hostname > sqlc.arg(after_hostname)
ORDER BY domains.hostname
LIMIT sqlc.arg(row_limit);

-- name: DeleteDomain :one
DELETE FROM domains
WHERE site_id = sqlc.arg(site_id) AND hostname = sqlc.arg(hostname)
  AND site_id IN (SELECT sites.id FROM sites WHERE sites.owner_id = sqlc.arg(owner_id))
RETURNING id;

-- Instance-wide: the verification worker records each check. The ID names
-- one claim, so a check that finishes after its domain was removed (and
-- perhaps claimed again under a new ID) changes nothing.
-- name: RecordDomainCheck :one
UPDATE domains SET
  status = sqlc.arg(status),
  last_checked_at = sqlc.arg(last_checked_at),
  verified_at = sqlc.narg(verified_at),
  failing_since = sqlc.narg(failing_since),
  last_failure_reason = sqlc.narg(last_failure_reason),
  failure_detail = sqlc.narg(failure_detail)
WHERE id = sqlc.arg(id)
RETURNING id;

-- Instance-wide: the verification worker's candidates, by ID after
-- after_id. Pending domains still inside their backoff window
-- (created_at >= window_start) and active or lapsed domains not checked
-- since recheck_before (or never); the worker works out from each row
-- whether its next check is due.
-- name: ListDomainsToCheck :many
SELECT id, site_id, hostname, status, verification_token, created_at, last_checked_at, verified_at, failing_since, last_failure_reason, failure_detail
FROM domains
WHERE id > sqlc.arg(after_id)
  AND (
    (status = 'pending' AND created_at >= sqlc.arg(window_start))
    OR (status <> 'pending' AND (last_checked_at IS NULL OR last_checked_at <= sqlc.arg(recheck_before)))
  )
ORDER BY id
LIMIT sqlc.arg(row_limit);

-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
-- (Licence header last: sqlc copies comments above a query into Go docs.)
