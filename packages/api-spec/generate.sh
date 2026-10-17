#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 CubieCode
# SPDX-License-Identifier: MIT
#
# Generates every API binding from openapi.yaml:
#   apps/server/internal/api/api.gen.go             server interface + models (oapi-codegen)
#   apps/server/internal/api/unimplemented.gen.go   501 stub for every operation (oapi-codegen, templates/)
#   apps/server/internal/api/operations.gen_test.go operation table for the stub's test (likewise)
#   packages/apiclient-go/client.gen.go             Go client + models (oapi-codegen)
#   packages/apiclient-ts/src/schema.gen.ts         TypeScript types (openapi-typescript)
# Run through `pnpm codegen`, which also regenerates sqlc and checks drift.
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/../../scripts/lib.sh"
ensure_go_tools
spec="$repo_root/packages/api-spec/openapi.yaml"

api="$repo_root/apps/server/internal/api"
for config in oapi-codegen oapi-codegen-unimplemented oapi-codegen-operations; do
  (cd "$repo_root/apps/server" && oapi-codegen -config "internal/api/$config.yaml" "$spec")
done
add_spdx AGPL-3.0-only // "$api/api.gen.go" "$api/unimplemented.gen.go" "$api/operations.gen_test.go"

(cd "$repo_root/packages/apiclient-go" && oapi-codegen -config oapi-codegen.yaml "$spec")
add_spdx MIT // "$repo_root/packages/apiclient-go/client.gen.go"

ts_out="$repo_root/packages/apiclient-ts/src/schema.gen.ts"
(cd "$repo_root/packages/api-spec" && pnpm exec openapi-typescript "$spec" --output "$ts_out" --silent)
add_spdx MIT // "$ts_out"
(cd "$repo_root" && pnpm exec prettier --log-level warn --write "$ts_out")
