-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only

-- Long-term caching for hashed assets (C1.10) is an opt-in recorded on each
-- deploy, like spa, so rollback restores matching cache headers. Existing
-- deploys keep revalidating. Boolean as INTEGER 0/1 (Postgres: the same
-- INTEGER column, or BOOLEAN NOT NULL DEFAULT false without the CHECK).
ALTER TABLE deploys ADD COLUMN immutable_assets INTEGER NOT NULL DEFAULT 0 CHECK (immutable_assets IN (0, 1));
