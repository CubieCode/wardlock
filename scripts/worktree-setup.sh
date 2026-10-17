#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 CubieCode
# SPDX-License-Identifier: MIT
#
# Prepares an isolated worktree lane: a lane-specific .env with ports derived
# from the worktree path, locked dependencies, Go modules, then `pnpm check`.
# Starts no services and copies nothing from the root checkout.
set -euo pipefail
root_path="${1:?Usage: worktree-setup.sh ROOT_PATH WORKTREE_PATH}"
worktree_path="${2:?Usage: worktree-setup.sh ROOT_PATH WORKTREE_PATH}"
cd "$worktree_path"
# Orca runs setup in a non-interactive shell, which skips the profile that puts
# a user-installed node/pnpm/go on PATH. Fall back to their standard homes.
if ! command -v node >/dev/null || ! command -v pnpm >/dev/null; then
  pnpm_home="${PNPM_HOME:-$HOME/.local/share/pnpm}"
  export PATH="$pnpm_home/bin:$pnpm_home:$PATH"
fi
if ! command -v go >/dev/null; then
  export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
fi
for tool in node pnpm go; do
  command -v "$tool" >/dev/null || { echo "worktree-setup: $tool not found on PATH." >&2; exit 1; }
done
# ROOT_PATH deliberately supplies no secrets, data or settings to another lane.
: "$root_path"
# Generate once; never overwrite a lane's manual configuration.
if [[ ! -f .env ]]; then
  node --input-type=module <<'NODE'
import { createHash, randomBytes } from 'node:crypto';
import { readFileSync, realpathSync, writeFileSync } from 'node:fs';
const hash = createHash('sha256').update(realpathSync(process.cwd())).digest('hex');
const slot = Number.parseInt(hash.slice(0, 6), 16) % 10000;
const http = 20000 + slot;
const https = 30000 + slot;
const web = 40000 + slot;
const smtp = 50000 + slot;
const overrides = {
  WARDLOCK_HTTP_ADDR: `127.0.0.1:${http}`,
  WARDLOCK_HTTPS_ADDR: `127.0.0.1:${https}`,
  WARDLOCK_WEB_DEV_PORT: String(web),
  WARDLOCK_DEV_SMTP_PORT: String(smtp),
  WARDLOCK_BASE_DOMAIN: 'wardlock.localhost',
  WARDLOCK_DATA_DIR: '.local/data',
  WARDLOCK_SMTP_URL: 'dev://',
  WARDLOCK_SECRET: randomBytes(32).toString('base64url'),
  WARDLOCK_SERVER: `http://wardlock.localhost:${http}`,
};
const contents = readFileSync('.env.example', 'utf8').split('\n').map((line) => {
  const key = line.split('=')[0];
  return Object.hasOwn(overrides, key) ? `${key}=${overrides[key]}` : line;
}).join('\n');
writeFileSync('.env', contents, { flag: 'wx', mode: 0o600 });
console.log(`Worktree ports: HTTP ${http}, HTTPS ${https}, web dev ${web}, SMTP catcher ${smtp}. Edit .env if a port is occupied.`);
NODE
fi
pnpm install --frozen-lockfile
while read -r dir; do go -C "$dir" mod download; done < <(go list -m -f '{{.Dir}}')
GOWORK=off go -C tools mod download
pnpm check
