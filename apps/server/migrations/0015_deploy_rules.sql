-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only

-- Redirects and response headers (C1.17) are recorded on each deploy, like
-- spa, so rollback restores them and a path deploy can carry over its
-- base's. Each column holds the deploy's rules as one JSON array, in order:
-- redirects as [{"from","to","status"}], headers as [{"path","set"}]. NULL
-- for a deploy without any, which existing deploys become. The server
-- validates them when the deploy is created and the edge reads them whole,
-- so they are never queried by content (Postgres: TEXT as well, or JSONB).
ALTER TABLE deploys ADD COLUMN redirects TEXT;
ALTER TABLE deploys ADD COLUMN headers TEXT;
