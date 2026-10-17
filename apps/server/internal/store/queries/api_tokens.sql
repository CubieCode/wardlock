-- name: CreateAPIToken :exec
INSERT INTO api_tokens (id, owner_id, public_id, token_hash, name, scope_deploy, scope_manage, created_at, expires_at, last_used_at, revoked_at)
VALUES (sqlc.arg(id), sqlc.arg(owner_id), sqlc.arg(public_id), sqlc.arg(token_hash), sqlc.arg(name), sqlc.arg(scope_deploy), sqlc.arg(scope_manage), sqlc.arg(created_at), sqlc.narg(expires_at), sqlc.narg(last_used_at), sqlc.narg(revoked_at));

-- name: CreateCLILoginRequest :exec
INSERT INTO cli_login_requests (id, device_code_hash, user_code, status, owner_id, scope_deploy, scope_manage, api_token_id, created_at, expires_at, decided_at)
VALUES (sqlc.arg(id), sqlc.arg(device_code_hash), sqlc.arg(user_code), sqlc.arg(status), sqlc.narg(owner_id), sqlc.arg(scope_deploy), sqlc.arg(scope_manage), sqlc.narg(api_token_id), sqlc.arg(created_at), sqlc.arg(expires_at), sqlc.narg(decided_at));

-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
-- (Licence header last: sqlc copies comments above a query into Go docs.)
