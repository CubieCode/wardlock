#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 CubieCode
# SPDX-License-Identifier: MIT
#
# `pnpm demo`: builds Wardlock, wipes .local/demo, starts wardlockd there on
# localhost and fills it with synthetic sites and 90 days of traffic
# (apps/server/cmd/wardlock-demo), then keeps it running until Ctrl+C.
# Never touches .local/data or the lane's .env settings: only the HTTP port
# is derived from .env, one above the lane's (WARDLOCK_DEMO_PORT overrides
# it). Sites live under wardlock-demo.localhost, so the demo's session
# cookie never replaces a pnpm dev one.
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
cd "$repo_root"

dev_port=8080
if [[ -f .env ]]; then
  addr="$(grep -E '^WARDLOCK_HTTP_ADDR=' .env | tail -n 1 | cut -d= -f2-)"
  [[ -n "$addr" ]] && dev_port="${addr##*:}"
fi
port="${WARDLOCK_DEMO_PORT:-$((dev_port + 1))}"
base=wardlock-demo.localhost
origin="http://$base:$port"
data="$repo_root/.local/demo"
email=owner@example.test

if (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null; then
  echo "demo: port $port is in use; stop what is on it or set WARDLOCK_DEMO_PORT." >&2
  exit 1
fi

pnpm build
go build -o .local/bin/wardlock-demo ./apps/server/cmd/wardlock-demo

# Only the settings below reach the server: none from the shell or .env.
while IFS= read -r name; do unset "$name"; done < <(compgen -e | grep '^WARDLOCK_' || true)
export WARDLOCK_HTTP_ADDR="127.0.0.1:$port"
export WARDLOCK_BASE_DOMAIN="$base"
export WARDLOCK_DATA_DIR="$data"
export WARDLOCK_SMTP_URL=dev://
export WARDLOCK_LOG_FORMAT=text
# A fresh server key per run; the data is wiped each run anyway.
WARDLOCK_SECRET="$(head -c 32 /dev/urandom | base64 | tr '+/' '-_' | tr -d '=\n')"
export WARDLOCK_SECRET
# The synthetic country database the browser tests use, so the dashboard
# shows countries; the seeded entries name their countries directly.
export WARDLOCK_COUNTRY_DB="$repo_root/apps/server/internal/country/countrytest/testdata/acceptance.mmdb"
export WARDLOCK_COUNTRY_DB_ATTRIBUTION='Synthetic country data for the demo (https://example.test/countries)'
export WARDLOCK_SERVER_COUNTRY=NZ

rm -rf "$data"
mkdir -p "$data"
# dev:// mail lands in <working directory>/.local/mail, so run from $data.
cd "$data"
sign_in="$("$repo_root/.local/bin/wardlockd" admin create --email "$email" 2>>wardlockd.log | grep -Eo 'https?://[^[:space:]]+' | head -n 1)"

"$repo_root/.local/bin/wardlockd" serve >>wardlockd.log 2>&1 &
server=$!
trap 'kill "$server" 2>/dev/null || true; wait "$server" 2>/dev/null || true' EXIT
trap 'exit 0' INT TERM
for i in $(seq 1 100); do
  curl -sf "http://127.0.0.1:$port/healthz" >/dev/null && break
  kill -0 "$server" 2>/dev/null || { cat wardlockd.log >&2; exit 1; }
  ((i == 100)) && { echo "demo: wardlockd did not start; see $data/wardlockd.log" >&2; exit 1; }
  sleep 0.1
done

"$repo_root/.local/bin/wardlock-demo" -server "$origin" -email "$email" \
  -mail-dir "$data/.local/mail" -data-dir "$data" -base-domain "$base" \
  -cli "$repo_root/.local/bin/wardlock" -fixtures "$repo_root/fixtures/sites"

cat <<EOF

Wardlock demo running at $origin (data in .local/demo, wiped next run).
Sign in once within 60 minutes at:

  $sign_in

After that, sign in as $email with an emailed code: messages are written
to .local/demo/.local/mail/ (newest last). Password-protected sites use
demo-password-1234. Server log: .local/demo/wardlockd.log. Ctrl+C stops it.
EOF
wait "$server"
