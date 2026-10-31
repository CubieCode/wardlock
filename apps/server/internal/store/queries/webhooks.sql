-- name: CountWebhooks :one
SELECT COUNT(*) FROM webhooks WHERE owner_id = sqlc.arg(owner_id);

-- name: CreateWebhook :exec
INSERT INTO webhooks (id, owner_id, url, description, all_events, all_sites, secret_salt, created_at)
VALUES (sqlc.arg(id), sqlc.arg(owner_id), sqlc.arg(url), sqlc.arg(description), sqlc.arg(all_events), sqlc.arg(all_sites), sqlc.arg(secret_salt), sqlc.arg(created_at));

-- name: GetWebhook :one
SELECT id, owner_id, url, description, all_events, all_sites, secret_salt, created_at, failing_since, disabled_at, disabled_reason
FROM webhooks WHERE id = sqlc.arg(id) AND owner_id = sqlc.arg(owner_id);

-- name: ListWebhooks :many
SELECT id, owner_id, url, description, all_events, all_sites, secret_salt, created_at, failing_since, disabled_at, disabled_reason
FROM webhooks WHERE owner_id = sqlc.arg(owner_id)
ORDER BY created_at, id;

-- name: ListOwnerWebhookEvents :many
SELECT webhook_events.webhook_id, webhook_events.event
FROM webhook_events
JOIN webhooks ON webhooks.id = webhook_events.webhook_id
WHERE webhooks.owner_id = sqlc.arg(owner_id)
ORDER BY webhook_events.webhook_id, webhook_events.event;

-- A listed site that has gone has no name.
-- name: ListOwnerWebhookSites :many
SELECT webhook_sites.webhook_id, webhook_sites.site_id, sites.name AS site_name
FROM webhook_sites
JOIN webhooks ON webhooks.id = webhook_sites.webhook_id
LEFT JOIN sites ON sites.id = webhook_sites.site_id
WHERE webhooks.owner_id = sqlc.arg(owner_id)
ORDER BY webhook_sites.webhook_id, sites.name;

-- name: UpdateWebhook :execrows
UPDATE webhooks SET url = sqlc.arg(url), description = sqlc.arg(description),
  all_events = sqlc.arg(all_events), all_sites = sqlc.arg(all_sites),
  failing_since = sqlc.narg(failing_since), disabled_at = sqlc.narg(disabled_at), disabled_reason = sqlc.narg(disabled_reason)
WHERE id = sqlc.arg(id) AND owner_id = sqlc.arg(owner_id);

-- name: DeleteWebhookEvents :exec
DELETE FROM webhook_events WHERE webhook_id = sqlc.arg(webhook_id);

-- name: AddWebhookEvent :exec
INSERT INTO webhook_events (webhook_id, event) VALUES (sqlc.arg(webhook_id), sqlc.arg(event));

-- name: DeleteWebhookSites :exec
DELETE FROM webhook_sites WHERE webhook_id = sqlc.arg(webhook_id);

-- name: AddWebhookSite :exec
INSERT INTO webhook_sites (webhook_id, site_id) VALUES (sqlc.arg(webhook_id), sqlc.arg(site_id));

-- name: DeleteWebhook :execrows
DELETE FROM webhooks WHERE id = sqlc.arg(id) AND owner_id = sqlc.arg(owner_id);

-- Instance-wide: a deleted site's rows, once site.deleted has been queued.
-- name: DeleteSiteFromWebhooks :exec
DELETE FROM webhook_sites WHERE site_id = sqlc.arg(site_id);

-- name: InsertWebhookDelivery :exec
INSERT INTO webhook_deliveries (id, webhook_id, event_id, event, body, status, redelivery_of, created_at)
VALUES (sqlc.arg(id), sqlc.arg(webhook_id), sqlc.arg(event_id), sqlc.arg(event), sqlc.arg(body), 'pending', sqlc.narg(redelivery_of), sqlc.arg(created_at));

-- Instance-wide: the worker names a row by its outbox ID.
-- name: GetWebhookDeliveryToSend :one
SELECT webhook_deliveries.id, webhook_deliveries.webhook_id, webhook_deliveries.body,
  webhooks.url, webhooks.secret_salt, webhooks.failing_since, webhooks.disabled_at
FROM webhook_deliveries
JOIN webhooks ON webhooks.id = webhook_deliveries.webhook_id
WHERE webhook_deliveries.id = sqlc.arg(id);

-- name: GetWebhookDelivery :one
SELECT webhook_deliveries.id, webhook_deliveries.webhook_id, webhook_deliveries.event_id, webhook_deliveries.event,
  webhook_deliveries.body, webhook_deliveries.status, webhook_deliveries.attempts, webhook_deliveries.response_code,
  webhook_deliveries.duration_ms, webhook_deliveries.error, webhook_deliveries.redelivery_of, webhook_deliveries.created_at,
  webhook_deliveries.last_attempt_at, webhook_deliveries.finished_at
FROM webhook_deliveries
JOIN webhooks ON webhooks.id = webhook_deliveries.webhook_id
WHERE webhook_deliveries.id = sqlc.arg(id) AND webhook_deliveries.webhook_id = sqlc.arg(webhook_id)
  AND webhooks.owner_id = sqlc.arg(owner_id);

-- name: ListWebhookDeliveries :many
SELECT webhook_deliveries.id, webhook_deliveries.webhook_id, webhook_deliveries.event_id, webhook_deliveries.event,
  webhook_deliveries.body, webhook_deliveries.status, webhook_deliveries.attempts, webhook_deliveries.response_code,
  webhook_deliveries.duration_ms, webhook_deliveries.error, webhook_deliveries.redelivery_of, webhook_deliveries.created_at,
  webhook_deliveries.last_attempt_at, webhook_deliveries.finished_at
FROM webhook_deliveries
JOIN webhooks ON webhooks.id = webhook_deliveries.webhook_id
WHERE webhook_deliveries.webhook_id = sqlc.arg(webhook_id) AND webhooks.owner_id = sqlc.arg(owner_id)
ORDER BY webhook_deliveries.created_at DESC, webhook_deliveries.id DESC
LIMIT sqlc.arg(row_limit);

-- name: RecordWebhookAttempt :exec
UPDATE webhook_deliveries SET status = sqlc.arg(status), attempts = attempts + 1,
  response_code = sqlc.narg(response_code), duration_ms = sqlc.arg(duration_ms), error = sqlc.narg(error),
  last_attempt_at = sqlc.arg(at), finished_at = sqlc.narg(finished_at)
WHERE id = sqlc.arg(id);

-- name: ClearWebhookFailing :exec
UPDATE webhooks SET failing_since = NULL WHERE id = sqlc.arg(id);

-- The run of failures begins with the first; later ones leave it.
-- name: MarkWebhookFailing :exec
UPDATE webhooks SET failing_since = COALESCE(failing_since, sqlc.arg(at)) WHERE id = sqlc.arg(id);

-- Only the first of several failing attempts running at once disables it.
-- name: DisableWebhook :execrows
UPDATE webhooks SET disabled_at = sqlc.arg(at), disabled_reason = 'failing'
WHERE id = sqlc.arg(id) AND disabled_at IS NULL;

-- name: DropWebhookDelivery :exec
UPDATE webhook_deliveries SET status = 'dropped', finished_at = sqlc.arg(at)
WHERE id = sqlc.arg(id) AND status = 'pending';

-- Keeps a webhook's newest rows, and any still pending.
-- name: TrimWebhookDeliveries :exec
DELETE FROM webhook_deliveries
WHERE webhook_deliveries.webhook_id = sqlc.arg(webhook_id) AND webhook_deliveries.status <> 'pending' AND webhook_deliveries.id NOT IN (
  SELECT recent.id FROM webhook_deliveries AS recent
  WHERE recent.webhook_id = sqlc.arg(webhook_id)
  ORDER BY recent.created_at DESC, recent.id DESC
  LIMIT sqlc.arg(keep)
);

-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
-- (Licence header last: sqlc copies comments above a query into Go docs.)
