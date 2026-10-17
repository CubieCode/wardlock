#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 CubieCode
# SPDX-License-Identifier: MIT
#
# The Go half of `pnpm check`, run for every module in go.work: gofmt, go vet,
# golangci-lint, go test -race and govulncheck. Go modules have no
# package.json; Go's own build and test caches make reruns fast, so Turbo
# does not wrap them. govulncheck needs network access to vuln.go.dev.
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
ensure_go_tools
cd "$repo_root"
bash scripts/embed-dashboard.sh
bash scripts/go-fmt.sh

mapfile -t modules < <(go list -m -f '{{.Dir}}')
for dir in "${modules[@]}"; do
  rel="${dir#"$repo_root"/}"
  echo "== $rel"
  (
    cd "$dir"
    go vet ./...
    golangci-lint run ./...
    go test -race ./...
    govulncheck ./...
  )
done
