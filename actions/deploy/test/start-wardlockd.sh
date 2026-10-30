#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 CubieCode
# SPDX-License-Identifier: MIT
#
# For the action's CI job (.github/workflows/deploy-action.yml): starts a
# throwaway wardlockd on loopback, as `pnpm demo` does, with its data in a
# new directory under RUNNER_TEMP, and signs in to create a deploy-scoped
# API token. The token is masked before it is written to GITHUB_OUTPUT and
# never printed. Never point this at a real server or data directory.
#
#   start-wardlockd.sh WARDLOCKD
#
# Outputs: server (the dashboard origin), port, base-domain, token,
# token-public-id (to look for in the logs afterwards) and pid.
set -euo pipefail

wardlockd=$(realpath "$1")
base=wardlock-ci.localhost
data=$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/wardlockd.XXXXXX")
email=owner@example.test

port=
for _ in $(seq 1 50); do
  candidate=$((20000 + RANDOM % 20000))
  if ! (exec 3<>"/dev/tcp/127.0.0.1/$candidate") 2>/dev/null; then
    port=$candidate
    break
  fi
done
[[ -n $port ]] || { echo "no free port" >&2; exit 1; }
origin="http://$base:$port"

# Only these settings reach the server.
while IFS= read -r name; do unset "$name"; done < <(compgen -e | grep '^WARDLOCK_' || true)
export WARDLOCK_HTTP_ADDR="127.0.0.1:$port"
export WARDLOCK_BASE_DOMAIN="$base"
export WARDLOCK_DATA_DIR="$data"
export WARDLOCK_SMTP_URL=dev://
export WARDLOCK_LOG_FORMAT=text
WARDLOCK_SECRET="$(head -c 32 /dev/urandom | base64 | tr '+/' '-_' | tr -d '=\n')"
export WARDLOCK_SECRET

cd "$data"
link=$("$wardlockd" admin create --email "$email" 2>>wardlockd.log | grep -Eo 'https?://[^[:space:]]+' | head -n 1)
code=${link##*code=}
code=${code%%&*}
echo "::add-mask::$code"

"$wardlockd" serve >>wardlockd.log 2>&1 &
pid=$!
for i in $(seq 1 100); do
  curl -sf "http://127.0.0.1:$port/healthz" >/dev/null && break
  kill -0 "$pid" 2>/dev/null || { cat wardlockd.log >&2; exit 1; }
  ((i == 100)) && { echo "wardlockd did not start; see $data/wardlockd.log" >&2; exit 1; }
  sleep 0.1
done

api() {
  curl -sSf --resolve "$base:$port:127.0.0.1" -H "Origin: $origin" \
    -H 'Content-Type: application/json' "$@"
}
# The session cookie is Secure, which curl never sends over plain http, so
# it goes back by hand.
session=$(api -o /dev/null -D - -X POST "$origin/api/v0/auth/sign-in-link" \
  --data "$(jq -n --arg code "$code" '{code: $code}')" |
  sed -n 's/^[Ss]et-[Cc]ookie: *\([^;]*\).*/\1/p' | head -n 1)
[[ -n $session ]] || { echo "signing in set no session cookie" >&2; exit 1; }
echo "::add-mask::${session#*=}"
created=$(api -H "Cookie: $session" -X POST "$origin/api/v0/tokens" \
  --data '{"name":"Action CI","scopes":["deploy"]}')
token=$(jq -r .token <<<"$created")
echo "::add-mask::$token"
public_id=$(jq -r .apiToken.publicId <<<"$created")
[[ $token == wl_* && -n $public_id ]] || { echo "no API token in the response" >&2; exit 1; }

{
  echo "server=$origin"
  echo "port=$port"
  echo "base-domain=$base"
  echo "token=$token"
  echo "token-public-id=$public_id"
  echo "pid=$pid"
  echo "data=$data"
} >>"$GITHUB_OUTPUT"
echo "wardlockd is running at $origin (data in $data)."
