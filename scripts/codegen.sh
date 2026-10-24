#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 CubieCode
# SPDX-License-Identifier: MIT
#
# Regenerates all committed generated code: OpenAPI bindings and sqlc. The
# check also confirms the world map data (scripts/world-map.mjs) is current.
#   scripts/codegen.sh          regenerate
#   scripts/codegen.sh --check  regenerate, then fail if anything changed
#                               (the CI drift check; commit the result)
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
ensure_go_tools
cd "$repo_root"

generated=(
  apps/server/internal/api/api.gen.go
  apps/server/internal/api/unimplemented.gen.go
  apps/server/internal/api/operations.gen_test.go
  apps/server/internal/store/sqlcgen
  packages/apiclient-go/client.gen.go
  packages/apiclient-ts/src/schema.gen.ts
)
snapshot() { find "${generated[@]}" -type f 2>/dev/null | LC_ALL=C sort | xargs cksum; }

[[ "${1:-}" == --check ]] && before="$(snapshot)"

bash packages/api-spec/generate.sh
rm -rf apps/server/internal/store/sqlcgen
(cd apps/server && sqlc generate)
add_spdx AGPL-3.0-only // apps/server/internal/store/sqlcgen/*.go

if [[ "${1:-}" == --check ]]; then
  if [[ "$(snapshot)" != "$before" ]]; then
    echo "Generated code was stale and has been regenerated. Review and commit:" >&2
    git status --short -- "${generated[@]}" >&2
    exit 1
  fi
  echo "Generated code is up to date."
  # Its sources are downloaded, so it is checked by its stamp, not rebuilt.
  node scripts/world-map.mjs --check
fi
