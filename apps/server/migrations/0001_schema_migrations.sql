-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
--
-- Migration bookkeeping. The runner executes this file before reading which
-- versions are applied, so it must stay idempotent (IF NOT EXISTS).
CREATE TABLE IF NOT EXISTS schema_migrations (
  version INTEGER NOT NULL PRIMARY KEY,
  name TEXT NOT NULL,
  -- UTC Unix milliseconds. Postgres: BIGINT.
  applied_at INTEGER NOT NULL
);
