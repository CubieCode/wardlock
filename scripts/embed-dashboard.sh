#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 CubieCode
# SPDX-License-Identifier: MIT
#
# Copies the dashboard build (apps/web/dist) into the server for go:embed.
# Without it the server embeds and serves ui/fallback.html instead.
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
src="$repo_root/apps/web/dist"
dest="$repo_root/apps/server/internal/dashboard/ui/dist"
if [[ ! -f "$src/index.html" ]]; then
  echo "embed-dashboard: apps/web/dist is missing; run pnpm build first. The server will serve its fallback page." >&2
  exit 0
fi
rm -rf "$dest"
cp -R "$src" "$dest"
