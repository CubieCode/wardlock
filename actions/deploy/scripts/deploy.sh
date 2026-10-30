#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 CubieCode
# SPDX-License-Identifier: MIT
#
# Runs `wardlock --json deploy` (MODE=deploy) or `wardlock --json preview
# delete` (MODE=delete) from the working directory, so wardlock.json there
# supplies whatever the inputs leave out. Outputs site, url and deploy-id.
# The token reaches the CLI only as WARDLOCK_TOKEN; nothing here prints it.
set -euo pipefail

fail() {
  echo "::error title=Wardlock deploy::$*"
  exit 1
}
out() { echo "$1=$2" >>"$GITHUB_OUTPUT"; }
# config FIELD prints FIELD from wardlock.json, or nothing.
config() {
  [[ -f wardlock.json ]] || return 0
  jq -r --arg f "$1" '.[$f] // empty | strings' wardlock.json
}

cli=${WARDLOCK_CLI:?no wardlock binary}
global=(--json)
[[ -n ${INPUT_SERVER:-} ]] && global+=(--server "$INPUT_SERVER")
site=${INPUT_SITE:-$(config site)}
work=$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/wardlock-deploy.XXXXXX")

if [[ $MODE == delete ]]; then
  [[ -n $site ]] || fail "which site? Set site, or \"site\" in wardlock.json"
  if "$cli" "${global[@]}" preview delete "$site" "$PREVIEW" --yes >"$work/out.json" 2>"$work/err"; then
    cat "$work/err" >&2
    echo "::notice title=Wardlock preview deleted::Deleted the preview $PREVIEW of $site."
  elif grep -q "has no preview $PREVIEW" "$work/err"; then
    echo "::notice title=Wardlock preview::$site has no preview $PREVIEW; nothing to delete."
  else
    cat "$work/err" >&2
    fail "could not delete the preview $PREVIEW of $site"
  fi
  out site "$site"
  exit 0
fi

message=${INPUT_MESSAGE:-}
if [[ -z $message ]]; then
  subject=$(git log -1 --format=%s "$COMMIT_SHA" 2>/dev/null || true)
  [[ -n $subject ]] || subject=${COMMIT_FALLBACK_SUBJECT%%$'\n'*}
  message="${COMMIT_SHA::7} ${subject::200}"
  message=${message% }
fi

args=(deploy)
if [[ -n ${INPUT_DIRECTORY:-} || -n $site ]]; then
  dir=${INPUT_DIRECTORY:-$(config dir)}
  args+=("${dir:-.}")
  [[ -n $site ]] && args+=("$site")
fi
args+=(--message "$message")
[[ -n ${INPUT_PATH:-} ]] && args+=(--path "$INPUT_PATH")
[[ -n ${PREVIEW:-} ]] && args+=(--preview "$PREVIEW")

"$cli" "${global[@]}" "${args[@]}" >"$work/out.json" || fail "wardlock deploy failed (see above)"

id=$(jq -r '.deploy.id' "$work/out.json")
site=$(jq -r '.site.name' "$work/out.json")
url=$(jq -r '.url // (.site.url + ((.path // "/") | ltrimstr("/")))' "$work/out.json")
out site "$site"
out deploy-id "$id"
out url "$url"
if [[ -n ${PREVIEW:-} ]]; then
  echo "::notice title=Wardlock preview deployed::$url (deploy $id)"
else
  echo "::notice title=Wardlock deployed::$url (deploy $id)"
fi
