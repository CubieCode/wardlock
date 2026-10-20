-- name: CreateCLILoginRequest :exec
INSERT INTO cli_login_requests (id, device_code_hash, user_code, status, owner_id, scope_deploy, scope_manage, api_token_id, created_at, expires_at, decided_at, client_name)
VALUES (sqlc.arg(id), sqlc.arg(device_code_hash), sqlc.arg(user_code), sqlc.arg(status), sqlc.narg(owner_id), sqlc.arg(scope_deploy), sqlc.arg(scope_manage), sqlc.narg(api_token_id), sqlc.arg(created_at), sqlc.arg(expires_at), sqlc.narg(decided_at), sqlc.narg(client_name));

-- name: GetCLILoginByDeviceCodeHash :one
SELECT id, device_code_hash, user_code, status, owner_id, scope_deploy, scope_manage, api_token_id, created_at, expires_at, decided_at, client_name
FROM cli_login_requests WHERE device_code_hash = sqlc.arg(device_code_hash);

-- Approves or denies a pending, unexpired request by its user code. On
-- approval client_name becomes the name of the token the CLI will receive
-- (the owner's choice, else what the CLI sent).
-- name: DecideCLILogin :execrows
UPDATE cli_login_requests
SET status = sqlc.arg(status),
    owner_id = sqlc.arg(owner_id),
    scope_deploy = sqlc.arg(scope_deploy),
    scope_manage = sqlc.arg(scope_manage),
    client_name = COALESCE(sqlc.narg(client_name), client_name),
    decided_at = sqlc.arg(decided_at)
WHERE user_code = sqlc.arg(user_code)
  AND status = 'pending'
  AND expires_at > sqlc.arg(decided_at);

-- name: RedeemCLILogin :execrows
UPDATE cli_login_requests SET status = 'redeemed', api_token_id = sqlc.arg(api_token_id)
WHERE cli_login_requests.id = sqlc.arg(id) AND status = 'approved' AND expires_at > sqlc.arg(now)
  AND NOT EXISTS (
    SELECT 1 FROM owners WHERE owners.id = cli_login_requests.owner_id AND owners.suspended_at IS NOT NULL
  );

-- Suspension denies the owner's approved logins the CLI has not yet
-- collected (A2.13).
-- name: DenyApprovedCLILogins :exec
UPDATE cli_login_requests SET status = 'denied'
WHERE owner_id = sqlc.arg(owner_id) AND status = 'approved';

-- name: DeleteCLILoginsExpiredBefore :exec
DELETE FROM cli_login_requests WHERE expires_at < sqlc.arg(before);

-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
-- (Licence header last: sqlc copies comments above a query into Go docs.)
