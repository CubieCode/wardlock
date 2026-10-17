#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 CubieCode
# SPDX-License-Identifier: MIT
#
# Generates every API binding from openapi.yaml:
#   apps/server/internal/api/api.gen.go      server interface + models (oapi-codegen)
#   packages/apiclient-go/client.gen.go      Go client + models (oapi-codegen)
#   packages/apiclient-ts/src/schema.gen.ts  TypeScript types (openapi-typescript)
# Run through `pnpm codegen`, which also regenerates sqlc and checks drift.
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/../../scripts/lib.sh"
ensure_go_tools
spec="$repo_root/packages/api-spec/openapi.yaml"

(cd "$repo_root/apps/server" && oapi-codegen -config internal/api/oapi-codegen.yaml "$spec")
add_spdx AGPL-3.0-only // "$repo_root/apps/server/internal/api/api.gen.go"

(cd "$repo_root/packages/apiclient-go" && oapi-codegen -config oapi-codegen.yaml "$spec")
add_spdx MIT // "$repo_root/packages/apiclient-go/client.gen.go"

ts_out="$repo_root/packages/apiclient-ts/src/schema.gen.ts"
(cd "$repo_root/packages/api-spec" && pnpm exec openapi-typescript "$spec" --output "$ts_out" --silent)
add_spdx MIT // "$ts_out"
(cd "$repo_root" && pnpm exec prettier --log-level warn --write "$ts_out")
