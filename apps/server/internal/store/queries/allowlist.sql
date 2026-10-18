-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
-- (Licence header last: sqlc copies comments above a query into Go docs.)
-- Instance-wide: every allowlist entry of a site's policies, for the edge.
-- name: ListSiteAllowlist :many
SELECT policy_id, site_id, kind, value
FROM policy_allowlist WHERE site_id = sqlc.arg(site_id)
ORDER BY policy_id, kind, value;

-- name: ListOwnerSiteAllowlist :many
SELECT policy_allowlist.policy_id, policy_allowlist.site_id, policy_allowlist.kind, policy_allowlist.value
FROM policy_allowlist JOIN sites ON sites.id = policy_allowlist.site_id
WHERE sites.owner_id = sqlc.arg(owner_id) AND policy_allowlist.site_id = sqlc.arg(site_id)
ORDER BY policy_allowlist.policy_id, policy_allowlist.kind, policy_allowlist.value;

-- name: ListPolicyAllowlist :many
SELECT policy_id, site_id, kind, value
FROM policy_allowlist WHERE site_id = sqlc.arg(site_id) AND policy_id = sqlc.arg(policy_id)
ORDER BY kind, value;

-- name: AddAllowlistEntry :exec
INSERT INTO policy_allowlist (policy_id, site_id, kind, value)
VALUES (sqlc.arg(policy_id), sqlc.arg(site_id), sqlc.arg(kind), sqlc.arg(value));

-- name: ClearPolicyAllowlist :exec
DELETE FROM policy_allowlist WHERE site_id = sqlc.arg(site_id) AND policy_id = sqlc.arg(policy_id);

-- Ends every live session of a policy that an allowlisted identity earned
-- (email code, and OAuth from A2.5), whatever its version; Policies.Set
-- runs it when the allowlist changes.
-- name: RevokePolicyAllowlistSessions :exec
UPDATE site_sessions SET revoked_at = sqlc.arg(revoked_at)
WHERE site_id = sqlc.arg(site_id) AND policy_id = sqlc.arg(policy_id)
  AND method IN ('email', 'oauth') AND revoked_at IS NULL;
