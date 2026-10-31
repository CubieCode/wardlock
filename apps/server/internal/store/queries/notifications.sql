-- name: ListNotificationOptOuts :many
SELECT event FROM notification_opt_outs WHERE owner_id = sqlc.arg(owner_id) ORDER BY event;

-- name: DeleteNotificationOptOut :exec
DELETE FROM notification_opt_outs WHERE owner_id = sqlc.arg(owner_id) AND event = sqlc.arg(event);

-- name: AddNotificationOptOut :exec
INSERT INTO notification_opt_outs (owner_id, event) VALUES (sqlc.arg(owner_id), sqlc.arg(event))
ON CONFLICT (owner_id, event) DO NOTHING;

-- Whether a notification may reach the owner: their address and whether
-- they are suspended. Instance-wide: the worker names the owner of a row.
-- name: GetNotificationRecipient :one
SELECT email, suspended_at FROM owners WHERE id = sqlc.arg(id);

-- Adds a row unless its (channel, event_key) has one already.
-- name: EnqueueNotification :execrows
INSERT INTO notification_outbox (id, channel, event_key, owner_id, event, payload, status, attempts, created_at, next_attempt_at)
VALUES (sqlc.arg(id), sqlc.arg(channel), sqlc.arg(event_key), sqlc.arg(owner_id), sqlc.arg(event), sqlc.arg(payload), 'pending', 0, sqlc.arg(created_at), sqlc.arg(created_at))
ON CONFLICT (channel, event_key) DO NOTHING;

-- Instance-wide: rows waiting to be tried, oldest due first.
-- name: ListDueNotifications :many
SELECT id, channel, event_key, owner_id, event, payload, status, attempts, created_at, next_attempt_at, finished_at
FROM notification_outbox
WHERE channel = sqlc.arg(channel) AND status = 'pending' AND next_attempt_at <= sqlc.arg(now)
ORDER BY next_attempt_at, id
LIMIT sqlc.arg(row_limit);

-- Starts one attempt, only from 'pending', so a row is never in two
-- attempts at once.
-- name: ClaimNotification :execrows
UPDATE notification_outbox SET status = 'sending', attempts = attempts + 1, next_attempt_at = NULL
WHERE id = sqlc.arg(id) AND status = 'pending';

-- name: RescheduleNotification :execrows
UPDATE notification_outbox SET status = 'pending', next_attempt_at = sqlc.arg(next_attempt_at)
WHERE id = sqlc.arg(id) AND status = 'sending';

-- Moves a row that has not reached an end state to status.
-- name: SettleNotification :execrows
UPDATE notification_outbox SET status = sqlc.arg(status), next_attempt_at = NULL, finished_at = sqlc.arg(at)
WHERE id = sqlc.arg(id) AND status IN ('pending', 'sending', 'held');

-- Ends every row of a channel left 'sending' by a process that stopped
-- mid-attempt: whether it went out cannot be known.
-- name: FailInterruptedNotifications :execrows
UPDATE notification_outbox SET status = 'failed', finished_at = sqlc.arg(at)
WHERE channel = sqlc.arg(channel) AND status = 'sending';

-- Makes every row of a channel left 'sending' due again at at, for a channel
-- whose receivers cope with a repeat.
-- name: RequeueInterruptedNotifications :execrows
UPDATE notification_outbox SET status = 'pending', next_attempt_at = sqlc.arg(at)
WHERE channel = sqlc.arg(channel) AND status = 'sending';

-- How many of the owner's rows went out on the channel at or after since.
-- name: CountSentNotifications :one
SELECT COUNT(*) FROM notification_outbox
WHERE owner_id = sqlc.arg(owner_id) AND channel = sqlc.arg(channel) AND status = 'sent' AND finished_at >= sqlc.arg(since);

-- Instance-wide: owners with rows held back by the cap.
-- name: ListHeldNotificationOwners :many
SELECT DISTINCT owner_id FROM notification_outbox
WHERE channel = sqlc.arg(channel) AND status = 'held'
ORDER BY owner_id;

-- name: ListHeldNotifications :many
SELECT id, channel, event_key, owner_id, event, payload, status, attempts, created_at, next_attempt_at, finished_at
FROM notification_outbox
WHERE owner_id = sqlc.arg(owner_id) AND channel = sqlc.arg(channel) AND status = 'held'
ORDER BY created_at, id;

-- name: MarkNotificationSummarised :exec
UPDATE notification_outbox SET status = 'summarised', finished_at = sqlc.arg(at)
WHERE id = sqlc.arg(id) AND status = 'held';

-- Deletes rows that reached an end state at or before finished_before.
-- name: DeleteFinishedNotifications :execrows
DELETE FROM notification_outbox
WHERE finished_at <= sqlc.arg(finished_before) AND status IN ('sent', 'failed', 'dropped', 'summarised');

-- The scans below are instance-wide: the worker reads what happened and
-- enqueues it for each row's owner. All of them skip suspended owners,
-- taken-down sites and owners who turned the event off.

-- Domains that became active at or after since.
-- name: ListVerifiedDomainNotices :many
SELECT sites.owner_id, sites.id AS site_id, sites.name AS site_name, domains.id, domains.hostname, domains.verified_at
FROM domains
JOIN sites ON sites.id = domains.site_id
JOIN owners ON owners.id = sites.owner_id
WHERE domains.status = 'active' AND domains.verified_at >= sqlc.arg(since)
  AND owners.suspended_at IS NULL AND sites.taken_down_at IS NULL
  AND NOT EXISTS (
    SELECT 1 FROM notification_opt_outs
    WHERE notification_opt_outs.owner_id = owners.id AND notification_opt_outs.event = sqlc.arg(event)
  )
ORDER BY domains.id;

-- Active domains whose checks have failed for at least failing_for: the
-- last failed check, not the clock, sets the age, so a domain whose next
-- check may yet pass is not reported.
-- name: ListFailingDomainNotices :many
SELECT sites.owner_id, sites.name AS site_name, domains.id, domains.hostname, domains.failing_since
FROM domains
JOIN sites ON sites.id = domains.site_id
JOIN owners ON owners.id = sites.owner_id
WHERE domains.status = 'active' AND domains.failing_since IS NOT NULL
  AND domains.last_checked_at - domains.failing_since >= CAST(sqlc.arg(failing_for) AS BIGINT)
  AND owners.suspended_at IS NULL AND sites.taken_down_at IS NULL
  AND NOT EXISTS (
    SELECT 1 FROM notification_opt_outs
    WHERE notification_opt_outs.owner_id = owners.id AND notification_opt_outs.event = sqlc.arg(event)
  )
ORDER BY domains.id;

-- Lapsed domains whose failures began at or after since.
-- name: ListLapsedDomainNotices :many
SELECT sites.owner_id, sites.id AS site_id, sites.name AS site_name, domains.id, domains.hostname, domains.failing_since
FROM domains
JOIN sites ON sites.id = domains.site_id
JOIN owners ON owners.id = sites.owner_id
WHERE domains.status = 'lapsed' AND domains.failing_since >= sqlc.arg(since)
  AND owners.suspended_at IS NULL AND sites.taken_down_at IS NULL
  AND NOT EXISTS (
    SELECT 1 FROM notification_opt_outs
    WHERE notification_opt_outs.owner_id = owners.id AND notification_opt_outs.event = sqlc.arg(event)
  )
ORDER BY domains.id;

-- Unrevoked links used up at or after since.
-- name: ListUsedUpLinkNotices :many
SELECT sites.owner_id, sites.id AS site_id, sites.name AS site_name, share_links.id, share_links.public_id, share_links.label, share_links.used_up_at
FROM share_links
JOIN sites ON sites.id = share_links.site_id
JOIN owners ON owners.id = sites.owner_id
WHERE share_links.used_up_at >= sqlc.arg(since) AND share_links.revoked_at IS NULL
  AND owners.suspended_at IS NULL AND sites.taken_down_at IS NULL
  AND NOT EXISTS (
    SELECT 1 FROM notification_opt_outs
    WHERE notification_opt_outs.owner_id = owners.id AND notification_opt_outs.event = sqlc.arg(event)
  )
ORDER BY share_links.id;

-- The links the Overview calls expiring (ListExpiringLinks), by owner.
-- name: ListExpiringLinkNotices :many
SELECT sites.owner_id, sites.name AS site_name, share_links.id, share_links.public_id, share_links.label, share_links.expires_at
FROM share_links
JOIN sites ON sites.id = share_links.site_id
JOIN owners ON owners.id = sites.owner_id
WHERE share_links.revoked_at IS NULL
  AND share_links.expires_at > CAST(sqlc.arg(now) AS BIGINT) AND share_links.expires_at <= CAST(sqlc.arg(expiring_by) AS BIGINT)
  AND share_links.expires_at - share_links.created_at > CAST(sqlc.arg(min_lifetime) AS BIGINT)
  AND (share_links.max_uses IS NULL OR share_links.use_count < share_links.max_uses)
  AND owners.suspended_at IS NULL AND sites.taken_down_at IS NULL
  AND NOT EXISTS (
    SELECT 1 FROM notification_opt_outs
    WHERE notification_opt_outs.owner_id = owners.id AND notification_opt_outs.event = sqlc.arg(event)
  )
ORDER BY sites.owner_id, share_links.expires_at, share_links.id;

-- Usable tokens expiring by expiring_by that live longer than min_lifetime.
-- name: ListExpiringTokenNotices :many
SELECT api_tokens.owner_id, api_tokens.id, api_tokens.name, api_tokens.expires_at
FROM api_tokens
JOIN owners ON owners.id = api_tokens.owner_id
WHERE api_tokens.revoked_at IS NULL
  AND api_tokens.expires_at > CAST(sqlc.arg(now) AS BIGINT) AND api_tokens.expires_at <= CAST(sqlc.arg(expiring_by) AS BIGINT)
  AND api_tokens.expires_at - api_tokens.created_at > CAST(sqlc.arg(min_lifetime) AS BIGINT)
  AND owners.suspended_at IS NULL
  AND NOT EXISTS (
    SELECT 1 FROM notification_opt_outs
    WHERE notification_opt_outs.owner_id = owners.id AND notification_opt_outs.event = sqlc.arg(event)
  )
ORDER BY api_tokens.id;

-- Previews expiring by expiring_by whose current deploy lives longer than
-- min_lifetime.
-- name: ListExpiringPreviewNotices :many
SELECT sites.owner_id, sites.name AS site_name, previews.site_id, previews.name, previews.expires_at
FROM previews
JOIN sites ON sites.id = previews.site_id
JOIN owners ON owners.id = sites.owner_id
WHERE previews.expires_at > CAST(sqlc.arg(now) AS BIGINT) AND previews.expires_at <= CAST(sqlc.arg(expiring_by) AS BIGINT)
  AND previews.expires_at - previews.deployed_at > CAST(sqlc.arg(min_lifetime) AS BIGINT)
  AND owners.suspended_at IS NULL AND sites.taken_down_at IS NULL
  AND NOT EXISTS (
    SELECT 1 FROM notification_opt_outs
    WHERE notification_opt_outs.owner_id = owners.id AND notification_opt_outs.event = sqlc.arg(event)
  )
ORDER BY previews.site_id, previews.name;

-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
-- (Licence header last: sqlc copies comments above a query into Go docs.)
