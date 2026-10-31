-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only

-- Webhooks (I6.3).
--
-- webhooks holds what an owner registered. The signing secret is not stored:
-- it is derived from the server key, id and secret_salt, so the database
-- alone cannot sign. all_events and all_sites say the scope tables below do
-- not apply, which keeps a webhook whose listed sites have all gone from
-- turning into one for every site. failing_since is when its current run of
-- failures began. disabled_at is set while it is off, with disabled_reason
-- 'failing' (three days of failures) or 'owner'.
CREATE TABLE webhooks (
  id TEXT NOT NULL PRIMARY KEY,
  owner_id TEXT NOT NULL REFERENCES owners (id) ON DELETE CASCADE,
  url TEXT NOT NULL CHECK (length(url) BETWEEN 1 AND 2048),
  description TEXT NOT NULL DEFAULT '' CHECK (length(description) <= 200),
  all_events INTEGER NOT NULL CHECK (all_events IN (0, 1)),
  all_sites INTEGER NOT NULL CHECK (all_sites IN (0, 1)),
  secret_salt TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  failing_since INTEGER,
  disabled_at INTEGER,
  disabled_reason TEXT CHECK (disabled_reason IN ('failing', 'owner'))
);

CREATE INDEX webhooks_owner ON webhooks (owner_id, created_at);

CREATE TABLE webhook_events (
  webhook_id TEXT NOT NULL REFERENCES webhooks (id) ON DELETE CASCADE,
  event TEXT NOT NULL CHECK (length(event) BETWEEN 1 AND 64),
  PRIMARY KEY (webhook_id, event)
);

-- site_id has no foreign key: site.deleted is matched against these rows
-- after the site has gone, and removes them itself.
CREATE TABLE webhook_sites (
  webhook_id TEXT NOT NULL REFERENCES webhooks (id) ON DELETE CASCADE,
  site_id TEXT NOT NULL,
  PRIMARY KEY (webhook_id, site_id)
);

CREATE INDEX webhook_sites_site ON webhook_sites (site_id);

-- webhook_deliveries is one event going to one webhook, and shares its id
-- with that event's row in notification_outbox, which schedules the
-- attempts. The newest are kept for each webhook. body is the JSON that is
-- signed and sent: a redelivery repeats it long after the outbox row has
-- gone. Nothing of the receiver's response is stored but its status code.
-- error is a short class, never a message, which could carry the URL.
--
-- A row is 'pending' while attempts remain, and ends 'succeeded', 'failed'
-- (attempts ran out, or the webhook was disabled by this failure) or
-- 'dropped' (the webhook was disabled or its owner suspended before it went).
CREATE TABLE webhook_deliveries (
  id TEXT NOT NULL PRIMARY KEY,
  webhook_id TEXT NOT NULL REFERENCES webhooks (id) ON DELETE CASCADE,
  event_id TEXT NOT NULL,
  event TEXT NOT NULL CHECK (length(event) BETWEEN 1 AND 64),
  body TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('pending', 'succeeded', 'failed', 'dropped')),
  attempts INTEGER NOT NULL DEFAULT 0,
  response_code INTEGER,
  duration_ms INTEGER,
  error TEXT,
  redelivery_of TEXT,
  created_at INTEGER NOT NULL,
  last_attempt_at INTEGER,
  finished_at INTEGER
);

CREATE INDEX webhook_deliveries_recent ON webhook_deliveries (webhook_id, created_at, id);
