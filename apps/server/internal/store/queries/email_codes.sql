-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
-- (Licence header last: sqlc copies comments above a query into Go docs.)
-- Deletes every code that expired at or before now, on any site.
-- name: DeleteExpiredVisitorEmailCodes :exec
DELETE FROM visitor_email_codes WHERE expires_at <= sqlc.arg(now);

-- Stores a code, replacing any earlier code for the same address and
-- policy, only while the policy is still protected with the email method at
-- the same version, in one statement.
-- name: UpsertVisitorEmailCode :execrows
INSERT INTO visitor_email_codes (id, site_id, policy_id, policy_version, address_hash, code_hash, attempts, created_at, expires_at)
SELECT sqlc.arg(id), policies.site_id, policies.id, policies.version, sqlc.arg(address_hash), sqlc.arg(code_hash), 0, sqlc.arg(created_at), sqlc.arg(expires_at)
FROM policies
WHERE policies.id = sqlc.arg(policy_id) AND policies.site_id = sqlc.arg(site_id)
  AND policies.version = sqlc.arg(policy_version) AND policies.visibility = 'protected'
  AND policies.email_enabled = 1
ON CONFLICT (site_id, policy_id, address_hash) DO UPDATE
SET id = excluded.id, policy_version = excluded.policy_version, code_hash = excluded.code_hash,
  attempts = 0, created_at = excluded.created_at, expires_at = excluded.expires_at;

-- name: GetVisitorEmailCode :one
SELECT id, site_id, policy_id, policy_version, address_hash, code_hash, attempts, created_at, expires_at
FROM visitor_email_codes
WHERE site_id = sqlc.arg(site_id) AND policy_id = sqlc.arg(policy_id) AND address_hash = sqlc.arg(address_hash);

-- Counts one attempt on a live code that has attempts left.
-- name: AddVisitorEmailCodeAttempt :one
UPDATE visitor_email_codes SET attempts = attempts + 1
WHERE id = sqlc.arg(id) AND attempts < sqlc.arg(max_attempts) AND expires_at > sqlc.arg(now)
RETURNING attempts;

-- Spends a code: it is deleted, so it can be used once.
-- name: UseVisitorEmailCode :execrows
DELETE FROM visitor_email_codes
WHERE id = sqlc.arg(id) AND expires_at > sqlc.arg(now);

-- Creates an email-code session only while its policy is still protected
-- with the email method at the same version, in one statement, so an
-- allowlist change that commits first leaves nothing behind.
-- name: CreateEmailSession :execrows
INSERT INTO site_sessions (id, site_id, token_hash, method, share_link_id, policy_id, policy_version, subject, created_at, expires_at, revoked_at)
SELECT sqlc.arg(id), policies.site_id, sqlc.arg(session_token_hash), 'email', NULL, policies.id, policies.version, sqlc.arg(subject), sqlc.arg(created_at), sqlc.arg(expires_at), NULL
FROM policies
WHERE policies.id = sqlc.arg(policy_id) AND policies.site_id = sqlc.arg(site_id)
  AND policies.version = sqlc.arg(policy_version) AND policies.visibility = 'protected'
  AND policies.email_enabled = 1;
