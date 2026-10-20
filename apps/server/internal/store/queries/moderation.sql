-- Queues every image and HTML page that an active deploy lists, on a site
-- that is serving, of an owner who is not exempt, and that has never been
-- queued. A hash listed both as a page and as an image is screened as text.
-- name: EnqueueUnscreened :execrows
INSERT INTO moderation_results (sha256, kind, status, attempts, queued_at, next_attempt_at)
SELECT deploy_files.sha256,
  MAX(CASE WHEN lower(deploy_files.path) LIKE '%.html' OR lower(deploy_files.path) LIKE '%.htm' THEN 'text' ELSE 'image' END),
  'pending', 0, sqlc.arg(at), sqlc.arg(at)
FROM deploy_files
JOIN sites ON sites.active_deploy_id = deploy_files.deploy_id
JOIN owners ON owners.id = sites.owner_id
WHERE owners.moderation_exempt = 0 AND owners.suspended_at IS NULL AND sites.taken_down_at IS NULL
  AND (lower(deploy_files.path) LIKE '%.html' OR lower(deploy_files.path) LIKE '%.htm'
    OR lower(deploy_files.path) LIKE '%.png' OR lower(deploy_files.path) LIKE '%.jpg'
    OR lower(deploy_files.path) LIKE '%.jpeg' OR lower(deploy_files.path) LIKE '%.webp'
    OR lower(deploy_files.path) LIKE '%.gif')
  AND NOT EXISTS (SELECT 1 FROM moderation_results WHERE moderation_results.sha256 = deploy_files.sha256)
GROUP BY deploy_files.sha256
ON CONFLICT (sha256) DO NOTHING;

-- name: DueModerationResults :many
SELECT sha256, kind, attempts FROM moderation_results
WHERE next_attempt_at IS NOT NULL AND next_attempt_at <= sqlc.arg(now)
ORDER BY next_attempt_at, sha256
LIMIT sqlc.arg(row_limit);

-- One file listing the hash in an active deploy on a serving site of an
-- owner who is not exempt, with the page's plain-text rendition when it
-- has one: what screening may send.
-- name: ModerationSource :one
SELECT deploy_files.path, deploy_files.size,
  COALESCE(deploy_renditions.sha256, '') AS text_sha256, COALESCE(deploy_renditions.size, 0) AS text_size
FROM deploy_files
JOIN sites ON sites.active_deploy_id = deploy_files.deploy_id
JOIN owners ON owners.id = sites.owner_id
LEFT JOIN deploy_renditions ON deploy_renditions.deploy_id = deploy_files.deploy_id
  AND deploy_renditions.path = deploy_files.path AND deploy_renditions.format = 'txt'
WHERE deploy_files.sha256 = sqlc.arg(sha256)
  AND owners.moderation_exempt = 0 AND owners.suspended_at IS NULL AND sites.taken_down_at IS NULL
ORDER BY CASE WHEN deploy_renditions.sha256 IS NULL THEN 1 ELSE 0 END, deploy_files.deploy_id, deploy_files.path
LIMIT 1;

-- A new verdict replaces the old one and any operator decision on it.
-- name: RecordModerationVerdict :execrows
UPDATE moderation_results SET status = sqlc.arg(status), categories = sqlc.arg(categories), checked_at = sqlc.arg(checked_at),
  attempts = 0, next_attempt_at = NULL, confirmed_at = NULL, discarded_at = NULL, released_at = NULL
WHERE sha256 = sqlc.arg(sha256) AND next_attempt_at IS NOT NULL;

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
SELECT sha256, kind, status, categories, attempts, queued_at, next_attempt_at, checked_at, confirmed_at, discarded_at, released_at
FROM moderation_results WHERE sha256 = sqlc.arg(sha256);

-- name: ListQuarantinedHashes :many
SELECT sha256 FROM moderation_results WHERE status = 'quarantined' ORDER BY sha256;

-- name: ListModerationReview :many
SELECT sha256, kind, status, categories, attempts, queued_at, next_attempt_at, checked_at, confirmed_at, discarded_at, released_at
FROM moderation_results WHERE status IN ('flagged', 'quarantined')
ORDER BY checked_at, sha256;

-- Every site whose active deploy lists the hash, with the path and owner,
-- exempt or not: who is affected when the hash is withheld.
-- name: ListModerationUses :many
SELECT sites.id AS site_id, sites.name AS site_name, owners.id AS owner_id, owners.email AS owner_email, deploy_files.path
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
