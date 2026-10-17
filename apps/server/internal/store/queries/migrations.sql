-- name: ListAppliedMigrations :many
SELECT version FROM schema_migrations ORDER BY version;

-- name: RecordMigration :exec
INSERT INTO schema_migrations (version, name, applied_at)
VALUES (sqlc.arg(version), sqlc.arg(name), sqlc.arg(applied_at));

-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
-- (Licence header last: sqlc copies comments above a query into Go docs.)
