-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only

-- Operator controls (A2.13). A suspended owner cannot sign in or use any
-- credential, and their sites answer the unavailable page; a taken-down
-- site answers it too. Both keep every deploy and file as evidence until
-- the operator reinstates, restores or tears down. NULL while not
-- suspended or taken down; otherwise UTC Unix milliseconds (Postgres:
-- BIGINT).
ALTER TABLE owners ADD COLUMN suspended_at INTEGER;
ALTER TABLE sites ADD COLUMN taken_down_at INTEGER;
