-- Ends every live password session bound to a policy, whatever its version.
-- name: RevokePolicyPasswordSessions :execrows
UPDATE site_sessions SET revoked_at = sqlc.arg(revoked_at)
WHERE site_id = sqlc.arg(site_id) AND policy_id = sqlc.arg(policy_id)
  AND method = 'password' AND revoked_at IS NULL;

-- Creates a password session only while its policy is still protected with
-- the password method, the checked password_hash and the same version, in
-- one statement, so a password change that commits first leaves nothing
-- behind (its own session revocation would miss a row inserted after it).
-- name: CreatePasswordSession :execrows
INSERT INTO site_sessions (id, site_id, token_hash, method, share_link_id, policy_id, policy_version, subject, created_at, expires_at, revoked_at)
SELECT sqlc.arg(id), policies.site_id, sqlc.arg(session_token_hash), 'password', NULL, policies.id, policies.version, NULL, sqlc.arg(created_at), sqlc.arg(expires_at), NULL
FROM policies
WHERE policies.id = sqlc.arg(policy_id) AND policies.site_id = sqlc.arg(site_id)
  AND policies.version = sqlc.arg(policy_version) AND policies.visibility = 'protected'
  AND policies.password_enabled = 1 AND policies.password_hash = sqlc.arg(password_hash);

-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
-- (Licence header last: sqlc copies comments above a query into Go docs.)
