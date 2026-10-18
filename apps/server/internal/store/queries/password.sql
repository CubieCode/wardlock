-- Ends every live password session bound to a policy, whatever its version.
-- name: RevokePolicyPasswordSessions :execrows
UPDATE site_sessions SET revoked_at = sqlc.arg(revoked_at)
WHERE site_id = sqlc.arg(site_id) AND policy_id = sqlc.arg(policy_id)
  AND method = 'password' AND revoked_at IS NULL;

-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
-- (Licence header last: sqlc copies comments above a query into Go docs.)
