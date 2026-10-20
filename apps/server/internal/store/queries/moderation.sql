-- Queues every file the site's active deploy lists that was never queued,
-- when the site is serving and its owner is neither exempt nor suspended.
-- What is sent is decided from the bytes when the hash is screened.
-- name: EnqueueSite :execrows
INSERT INTO moderation_results (sha256, status, attempts, queued_at, next_attempt_at)
SELECT DISTINCT deploy_files.sha256, 'pending', 0, sqlc.arg(at), sqlc.arg(at)
FROM sites
JOIN owners ON owners.id = sites.owner_id
JOIN deploy_files ON deploy_files.deploy_id = sites.active_deploy_id
WHERE sites.id = sqlc.arg(site_id)
  AND owners.moderation_exempt = 0 AND owners.suspended_at IS NULL AND sites.taken_down_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM moderation_results WHERE moderation_results.sha256 = deploy_files.sha256)
ON CONFLICT (sha256) DO NOTHING;

-- Queues again, keeping the verdict, each screened hash the site's active
-- deploy lists under a path served as text (.html, .htm, .xhtml, .svg,
-- .txt, .md, .xml) when its text was never screened. A quarantined hash is
-- withheld already, so it is left alone.
-- name: RequeueSiteText :execrows
UPDATE moderation_results SET next_attempt_at = sqlc.arg(at), attempts = 0
WHERE next_attempt_at IS NULL AND status NOT IN ('pending', 'quarantined') AND kind NOT IN ('text', 'image,text')
  AND sha256 IN (
    SELECT deploy_files.sha256
    FROM sites
    JOIN owners ON owners.id = sites.owner_id
    JOIN deploy_files ON deploy_files.deploy_id = sites.active_deploy_id
    WHERE sites.id = sqlc.arg(site_id)
      AND owners.moderation_exempt = 0 AND owners.suspended_at IS NULL AND sites.taken_down_at IS NULL
      AND (lower(deploy_files.path) LIKE '%.html' OR lower(deploy_files.path) LIKE '%.htm'
    OR lower(deploy_files.path) LIKE '%.xhtml' OR lower(deploy_files.path) LIKE '%.svg'
    OR lower(deploy_files.path) LIKE '%.txt' OR lower(deploy_files.path) LIKE '%.md'
    OR lower(deploy_files.path) LIKE '%.xml')
  );

-- The sites screening sweeps, in ID order after a cursor: each serving site
-- with an active deploy of an owner neither exempt nor suspended.
-- name: ListScreenedSites :many
SELECT sites.id FROM sites
JOIN owners ON owners.id = sites.owner_id
WHERE sites.id > sqlc.arg(after) AND sites.active_deploy_id IS NOT NULL
  AND owners.moderation_exempt = 0 AND owners.suspended_at IS NULL AND sites.taken_down_at IS NULL
ORDER BY sites.id
LIMIT sqlc.arg(row_limit);

-- name: DueModerationResults :many
SELECT sha256, kind, status, attempts FROM moderation_results
WHERE next_attempt_at IS NOT NULL AND next_attempt_at <= sqlc.arg(now)
ORDER BY next_attempt_at, sha256
LIMIT sqlc.arg(row_limit);

-- The size of the hash as a file listed in an active deploy on a serving
-- site of an owner who is not exempt: there is something screening may
-- send.
-- name: ModerationSource :one
SELECT deploy_files.size
FROM deploy_files
JOIN sites ON sites.active_deploy_id = deploy_files.deploy_id
JOIN owners ON owners.id = sites.owner_id
WHERE deploy_files.sha256 = sqlc.arg(sha256)
  AND owners.moderation_exempt = 0 AND owners.suspended_at IS NULL AND sites.taken_down_at IS NULL
LIMIT 1;

-- How many of those files sit under a path served as text.
-- name: CountModerationTextPaths :one
SELECT COUNT(*) FROM deploy_files
JOIN sites ON sites.active_deploy_id = deploy_files.deploy_id
JOIN owners ON owners.id = sites.owner_id
WHERE deploy_files.sha256 = sqlc.arg(sha256)
  AND owners.moderation_exempt = 0 AND owners.suspended_at IS NULL AND sites.taken_down_at IS NULL
  AND (lower(deploy_files.path) LIKE '%.html' OR lower(deploy_files.path) LIKE '%.htm'
    OR lower(deploy_files.path) LIKE '%.xhtml' OR lower(deploy_files.path) LIKE '%.svg'
    OR lower(deploy_files.path) LIKE '%.txt' OR lower(deploy_files.path) LIKE '%.md'
    OR lower(deploy_files.path) LIKE '%.xml');

-- A new verdict replaces the old one; the operator's decision on it stands
-- only while the status stays the same.
-- name: RecordModerationVerdict :execrows
UPDATE moderation_results SET
  confirmed_at = CASE WHEN status = sqlc.arg(status) THEN confirmed_at ELSE NULL END,
  discarded_at = CASE WHEN status = sqlc.arg(status) THEN discarded_at ELSE NULL END,
  released_at = CASE WHEN status = sqlc.arg(status) THEN released_at ELSE NULL END,
  status = sqlc.arg(status), categories = sqlc.arg(categories), reason = sqlc.arg(reason),
  kind = sqlc.arg(kind), decoded_sha256 = sqlc.narg(decoded_sha256), checked_at = sqlc.arg(checked_at),
  attempts = 0, next_attempt_at = NULL
WHERE sha256 = sqlc.arg(sha256) AND next_attempt_at IS NOT NULL;

-- Screening ended without a new verdict (nothing the endpoint could judge):
-- the current one stands, and what was covered is recorded.
-- name: KeepModerationVerdict :execrows
UPDATE moderation_results SET kind = sqlc.arg(kind), decoded_sha256 = sqlc.narg(decoded_sha256),
  attempts = 0, next_attempt_at = NULL
WHERE sha256 = sqlc.arg(sha256) AND next_attempt_at IS NOT NULL AND status <> 'pending';

-- name: RetryModeration :execrows
UPDATE moderation_results SET attempts = sqlc.arg(attempts), next_attempt_at = sqlc.arg(next_attempt_at)
WHERE sha256 = sqlc.arg(sha256) AND next_attempt_at IS NOT NULL;

-- Nothing may be sent for the hash any more (every owner listing it is now
-- exempt, or it left every serving deploy): a hash never screened is
-- forgotten, so it is queued again if it reappears; a rescan is dropped.
-- name: DequeueModeration :exec
DELETE FROM moderation_results WHERE sha256 = sqlc.arg(sha256) AND status = 'pending';

-- name: CancelModerationRescan :exec
UPDATE moderation_results SET next_attempt_at = NULL, attempts = 0 WHERE sha256 = sqlc.arg(sha256) AND status <> 'pending';

-- name: GetModerationResult :one
SELECT sha256, kind, status, categories, reason, decoded_sha256, attempts, queued_at, next_attempt_at, checked_at, confirmed_at, discarded_at, released_at
FROM moderation_results WHERE sha256 = sqlc.arg(sha256);

-- name: ListQuarantinedHashes :many
SELECT sha256 FROM moderation_results WHERE status = 'quarantined' ORDER BY sha256;

-- name: ListModerationReview :many
SELECT sha256, kind, status, categories, reason, decoded_sha256, attempts, queued_at, next_attempt_at, checked_at, confirmed_at, discarded_at, released_at
FROM moderation_results WHERE status IN ('flagged', 'quarantined')
ORDER BY checked_at, sha256;

-- Every site whose active deploy lists the hash, with the path and owner,
-- exempt or not, taken down or not: who is affected when the hash is
-- withheld.
-- name: ListModerationUses :many
SELECT sites.id AS site_id, sites.name AS site_name, owners.id AS owner_id, owners.email AS owner_email, deploy_files.path,
  sites.taken_down_at, owners.suspended_at
FROM deploy_files
JOIN sites ON sites.active_deploy_id = deploy_files.deploy_id
JOIN owners ON owners.id = sites.owner_id
WHERE deploy_files.sha256 = sqlc.arg(sha256)
ORDER BY sites.name, deploy_files.path;

-- name: ReleaseModeration :execrows
UPDATE moderation_results SET status = 'allowed', released_at = sqlc.arg(at), confirmed_at = NULL, discarded_at = NULL
WHERE sha256 = sqlc.arg(sha256) AND status IN ('flagged', 'quarantined');

-- name: ConfirmModeration :execrows
UPDATE moderation_results SET confirmed_at = sqlc.arg(at), discarded_at = NULL
WHERE sha256 = sqlc.arg(sha256) AND status = 'quarantined';

-- name: DiscardModeration :execrows
UPDATE moderation_results SET discarded_at = sqlc.arg(at), confirmed_at = NULL
WHERE sha256 = sqlc.arg(sha256) AND status = 'quarantined';

-- The current verdict holds (a quarantined hash stays withheld) until the
-- new one is recorded.
-- name: RescanModeration :execrows
UPDATE moderation_results SET attempts = 0, next_attempt_at = sqlc.arg(at), queued_at = sqlc.arg(at)
WHERE sha256 = sqlc.arg(sha256);

-- Each file of a deploy with its screening status ('pending' when never
-- queued) and, for a gzip file, what it decompresses to: what the edge
-- needs to serve it.
-- name: ListDeployScreening :many
SELECT DISTINCT deploy_files.sha256, COALESCE(moderation_results.status, 'pending') AS status,
  COALESCE(moderation_results.decoded_sha256, '') AS decoded_sha256
FROM deploy_files
LEFT JOIN moderation_results ON moderation_results.sha256 = deploy_files.sha256
WHERE deploy_files.deploy_id = sqlc.arg(deploy_id);

-- name: CountQueuedModeration :one
SELECT COUNT(*) FROM moderation_results WHERE next_attempt_at IS NOT NULL;

-- name: SetOwnerModerationExempt :execrows
UPDATE owners SET moderation_exempt = sqlc.arg(moderation_exempt) WHERE id = sqlc.arg(id);

-- name: GetOwnerModerationExempt :one
SELECT moderation_exempt FROM owners WHERE id = sqlc.arg(id);

-- Content screening (A2.15). Instance-wide: these queries back the
-- screening worker, the edge's quarantine list and `wardlockd admin` only,
-- and must never back an owner API handler.

-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
-- (Licence header last: sqlc copies comments above a query into Go docs.)
