#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 CubieCode
# SPDX-License-Identifier: MIT
#
# Builds wardlockd (with the embedded dashboard) and wardlock into .local/bin.
# Release builds use GoReleaser (.goreleaser.yaml) instead.
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
cd "$repo_root"
bash scripts/embed-dashboard.sh
version="${WARDLOCK_VERSION:-dev}"
export CGO_ENABLED=0
go build -trimpath -ldflags "-s -w -X wardlock.nz/server/internal/platform.Version=$version" \
  -o .local/bin/wardlockd ./apps/server/cmd/wardlockd
go build -trimpath -ldflags "-s -w -X main.version=$version" \
  -o .local/bin/wardlock ./apps/cli/cmd/wardlock
echo "Built .local/bin/wardlockd and .local/bin/wardlock ($version)."
