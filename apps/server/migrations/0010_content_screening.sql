-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only

-- Content screening (A2.15). An owner the operator exempts with
-- `wardlockd admin moderation disable` never has content sent for
-- screening; new owners follow the instance default (0). Postgres:
-- INTEGER NOT NULL DEFAULT 0 with the same CHECK.
ALTER TABLE owners ADD COLUMN moderation_exempt INTEGER NOT NULL DEFAULT 0 CHECK (moderation_exempt IN (0, 1));

-- One row per content hash ever queued for screening, so each unique file
-- is checked once however many deploys, sites, paths or owners list it.
-- Every file of a serving deploy is queued: what is sent is decided from
-- its bytes, not its name (an image is screened as an image under any
-- name; a page or text file as text), plus whether any path serves it as
-- text. status is the verdict:
--   pending      not screened yet
--   allowed      nothing to act on, or the operator released it
--   flagged      the operator should review it; still served. reason
--                'unscreenable' marks an image that could not be screened
--                (a format neither the endpoint nor the server can read,
--                or too large even after scaling)
--   quarantined  not served on any site; the blob is held from garbage
--                collection until the operator discards or releases it
--   skipped      nothing in it to screen (not an image, page or text a
--                path serves, such as a script, font or video)
-- kind records what screening covered: 'image', 'text', both
-- ('image,text') or neither (''); a hash later listed under a path that
-- serves it as text is queued again when text was not covered.
-- categories is the comma-separated OpenAI categories the endpoint
-- flagged, '' for none. decoded_sha256 is, for a gzip file, the SHA-256 of
-- what it decompresses to (a precompressed sibling is offered to browsers
-- only when that matches the file it stands for); NULL for anything else
-- or a gzip file that does not decompress cleanly. next_attempt_at makes
-- the row the persistent queue: set while the hash waits to be screened
-- (first time, retry after a failure, or an operator's rescan, which keeps
-- the current verdict unless the endpoint gives a new one), NULL
-- otherwise. Times are UTC Unix milliseconds (Postgres: BIGINT); NULL when
-- not (yet) so.
CREATE TABLE moderation_results (
  sha256 TEXT PRIMARY KEY CHECK (length(sha256) = 64 AND sha256 = lower(sha256)),
  kind TEXT NOT NULL DEFAULT '' CHECK (kind IN ('', 'image', 'text', 'image,text')),
  status TEXT NOT NULL CHECK (status IN ('pending', 'allowed', 'flagged', 'quarantined', 'skipped')),
  categories TEXT NOT NULL DEFAULT '',
  reason TEXT NOT NULL DEFAULT '',
  decoded_sha256 TEXT CHECK (decoded_sha256 IS NULL OR (length(decoded_sha256) = 64 AND decoded_sha256 = lower(decoded_sha256))),
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
