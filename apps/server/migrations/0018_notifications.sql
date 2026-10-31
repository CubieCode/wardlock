-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only

-- Owner notifications (I6.2).
--
-- notification_opt_outs holds the events an owner turned off: every event
-- is on until a row says otherwise, so a new event starts on for everyone.
CREATE TABLE notification_opt_outs (
  owner_id TEXT NOT NULL REFERENCES owners (id) ON DELETE CASCADE,
  event TEXT NOT NULL CHECK (length(event) BETWEEN 1 AND 64),
  PRIMARY KEY (owner_id, event)
);

-- notification_outbox is the durable queue of things to deliver: one row
-- per (channel, event_key), so noticing the same event twice, or after a
-- restart, delivers it once. channel says how the row leaves ('email';
-- I6.3 adds webhooks), event what it is about and payload, JSON, what the
-- channel needs to send it, written when the event is noticed. No address
-- is stored: email goes to the owner's address at the time of sending.
--
-- A row is 'pending' until next_attempt_at, then 'sending' while one
-- attempt runs, and it ends 'sent', 'failed' (attempts ran out, or a
-- 'sending' row a crash left behind), 'dropped' (turned off or the owner
-- suspended before it went) or 'summarised' (rolled into a later summary).
-- 'held' means the owner's hourly cap stopped it, waiting for that summary.
-- finished_at is when a row reached 'held' or an end state.
CREATE TABLE notification_outbox (
  id TEXT NOT NULL PRIMARY KEY,
  channel TEXT NOT NULL CHECK (length(channel) BETWEEN 1 AND 32),
  event_key TEXT NOT NULL CHECK (length(event_key) BETWEEN 1 AND 512),
  owner_id TEXT NOT NULL REFERENCES owners (id) ON DELETE CASCADE,
  event TEXT NOT NULL CHECK (length(event) BETWEEN 1 AND 64),
  payload TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('pending', 'sending', 'held', 'sent', 'failed', 'dropped', 'summarised')),
  attempts INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  next_attempt_at INTEGER,
  finished_at INTEGER,
  UNIQUE (channel, event_key)
);

CREATE INDEX notification_outbox_due ON notification_outbox (channel, status, next_attempt_at);
CREATE INDEX notification_outbox_owner ON notification_outbox (owner_id, channel, status, finished_at);
CREATE INDEX notification_outbox_finished ON notification_outbox (finished_at);

-- When a use-limited link's last use was spent, set in the statement that
-- spends it. Links used up before this migration stay NULL, so upgrading
-- sends nothing about them.
ALTER TABLE share_links ADD COLUMN used_up_at INTEGER;

CREATE INDEX share_links_used_up_at ON share_links (used_up_at);
CREATE INDEX share_links_expires_at ON share_links (expires_at);
CREATE INDEX api_tokens_expires_at ON api_tokens (expires_at);
