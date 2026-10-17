#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 CubieCode
# SPDX-License-Identifier: MIT
#
# scripts/go-fmt.sh          list Go files that need gofmt (fails if any)
# scripts/go-fmt.sh --write  format them
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
cd "$repo_root"
mapfile -t dirs < <(go list -m -f '{{.Dir}}')
dirs+=("$repo_root/tools")
if [[ "${1:-}" == --write ]]; then
  gofmt -w "${dirs[@]}"
  exit 0
fi
unformatted="$(gofmt -l "${dirs[@]}")"
if [[ -n "$unformatted" ]]; then
  echo "These Go files need formatting (pnpm format):" >&2
  echo "$unformatted" >&2
  exit 1
fi
