#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 CubieCode
# SPDX-License-Identifier: MIT
#
# Creates or updates the action's one comment on the pull request for a
# preview: the address, deploy ID and commit after a deploy (MODE=deploy),
# or that the preview was deleted (MODE=delete, which never creates one).
# The comment is found by a hidden marker naming the site and preview, on a
# comment written by a bot, so nobody can make the action edit theirs.
#
# Without GITHUB_TOKEN, or when the token lacks pull-requests write, it ends
# with a notice; any other API failure is a warning. The deploy has already
# happened, so the comment never fails the run.
set -euo pipefail

notice() { echo "::notice title=Wardlock preview comment::$*"; }
warn() { echo "::warning title=Wardlock preview comment::$*"; }

if [[ -z ${GITHUB_TOKEN:-} ]]; then
  notice "skipped: no github-token"
  exit 0
fi

api=${GITHUB_API%/}
work=$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/wardlock-comment.XXXXXX")
marker="<!-- wardlock-preview site=$SITE preview=$PREVIEW -->"

# request METHOD URL [BODY_FILE] writes the response to $work/res and prints
# the HTTP status (000 when the request did not complete).
request() {
  local args=(-sS -o "$work/res" -w '%{http_code}' -X "$1"
    -H "Authorization: Bearer $GITHUB_TOKEN"
    -H 'Accept: application/vnd.github+json'
    -H 'X-GitHub-Api-Version: 2022-11-28')
  [[ $# -gt 2 ]] && args+=(-H 'Content-Type: application/json' --data-binary "@$3")
  curl "${args[@]}" "$2" 2>"$work/curl-err" || true
}

# handle STATUS WHAT: returns 0 on success; otherwise explains and exits 0.
handle() {
  case $1 in
    2??) return 0 ;;
    403 | 404)
      notice "skipped: the token cannot $2 (the job needs permissions: pull-requests: write)"
      ;;
    *)
      warn "could not $2: HTTP $1 $(jq -r '.message? // empty' "$work/res" 2>/dev/null || true)"
      ;;
  esac
  exit 0
}

existing=
page=1
while :; do
  status=$(request GET "$api/repos/$REPOSITORY/issues/$PR_NUMBER/comments?per_page=100&page=$page")
  handle "$status" "read the pull request's comments"
  existing=$(jq -r --arg m "$marker" 'map(select(.user.type == "Bot" and ((.body // "") | contains($m)))) | first | .id // empty' "$work/res")
  [[ -n $existing || $(jq length "$work/res") -lt 100 ]] && break
  page=$((page + 1))
done

if [[ $MODE == delete ]]; then
  [[ -n $existing ]] || exit 0
  body="$marker
The Wardlock preview **$PREVIEW** of **$SITE** was deleted when the pull request closed."
else
  body="$marker
The Wardlock preview **$PREVIEW** of **$SITE** is ready.

| Preview | Deploy | Commit |
| --- | --- | --- |
| $URL | \`$DEPLOY_ID\` | $COMMIT_SHA |"
fi
jq -n --arg body "$body" '{body: $body}' >"$work/body.json"

if [[ -n $existing ]]; then
  status=$(request PATCH "$api/repos/$REPOSITORY/issues/comments/$existing" "$work/body.json")
  handle "$status" "update the pull request comment"
  echo "Updated the pull request comment $existing."
else
  status=$(request POST "$api/repos/$REPOSITORY/issues/$PR_NUMBER/comments" "$work/body.json")
  handle "$status" "comment on the pull request"
  echo "Commented on the pull request ($(jq -r .id "$work/res"))."
fi
