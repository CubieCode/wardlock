-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only

-- Content screening (A2.15). An owner the operator exempts with
-- `wardlockd admin moderation disable` never has content sent for
-- screening; new owners follow the instance default (0). Postgres:
-- INTEGER NOT NULL DEFAULT 0 with the same CHECK.
ALTER TABLE owners ADD COLUMN moderation_exempt INTEGER NOT NULL DEFAULT 0 CHECK (moderation_exempt IN (0, 1));

-- One row per content hash ever queued for screening, so each unique image
-- or HTML page is checked once however many deploys, sites or owners list
-- it. status is the verdict:
--   pending      not screened yet
--   allowed      nothing to act on, or the operator released it
--   flagged      the operator should review it; still served
--   quarantined  not served on any site; the blob is held from garbage
--                collection until the operator discards or releases it
--   skipped      not sendable (too large, an animated GIF, a format the
--                endpoint refuses); never retried unless rescanned
-- kind says what is sent: 'image' (the file itself) or 'text' (an HTML
-- page's extracted text). categories is the comma-separated OpenAI
-- categories the endpoint flagged, '' for none. next_attempt_at makes the
-- row the persistent queue: set while the hash waits to be screened (first
-- time, retry after a failure, or an operator's rescan, which keeps the
-- current verdict until the new one), NULL otherwise. Times are UTC Unix
-- milliseconds (Postgres: BIGINT); NULL when not (yet) so.
CREATE TABLE moderation_results (
  sha256 TEXT PRIMARY KEY CHECK (length(sha256) = 64 AND sha256 = lower(sha256)),
  kind TEXT NOT NULL CHECK (kind IN ('image', 'text')),
  status TEXT NOT NULL CHECK (status IN ('pending', 'allowed', 'flagged', 'quarantined', 'skipped')),
  categories TEXT NOT NULL DEFAULT '',
  attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  queued_at INTEGER NOT NULL,
  next_attempt_at INTEGER,
  checked_at INTEGER,
  -- The operator's decisions (`wardlockd admin moderation`): confirmed
  -- keeps a quarantined blob held indefinitely, discarded lets garbage
  -- collection remove it once no deploy lists it, released serves it again.
  confirmed_at INTEGER,
  discarded_at INTEGER,
  released_at INTEGER
);

CREATE INDEX moderation_results_due ON moderation_results (next_attempt_at) WHERE next_attempt_at IS NOT NULL;
CREATE INDEX moderation_results_review ON moderation_results (status) WHERE status IN ('flagged', 'quarantined');
