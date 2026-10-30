#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 CubieCode
# SPDX-License-Identifier: MIT
#
# The action's first step: masks the token, then decides what the run does.
# Outputs mode (deploy, delete or skip) and preview (empty for the live
# site). A run without a token ends with a notice when GitHub withholds
# secrets (pull requests from forks and from Dependabot), and fails otherwise.
set -euo pipefail

if [[ -n ${WARDLOCK_TOKEN:-} ]]; then
  echo "::add-mask::$WARDLOCK_TOKEN"
fi

out() { echo "$1=$2" >>"$GITHUB_OUTPUT"; }

if [[ $EVENT_NAME == pull_request_target ]]; then
  echo "::warning title=Wardlock deploy::pull_request_target runs with secrets; never check out and deploy a fork's code with it (see the action's README)."
fi

if [[ -z ${WARDLOCK_TOKEN:-} ]]; then
  fork=false
  [[ $EVENT_NAME == pull_request && -n ${PR_HEAD_REPO:-} && $PR_HEAD_REPO != "$REPOSITORY" ]] && fork=true
  if [[ $fork == true || ${ACTOR:-} == 'dependabot[bot]' ]]; then
    echo "::notice title=Wardlock deploy skipped::GitHub gives this pull request's workflows no secrets, so there is no Wardlock token and nothing was deployed. A maintainer's pull request from a branch of this repository gets a preview."
    out mode skip
    out preview ''
    exit 0
  fi
  echo "::error title=Wardlock deploy::token is empty: pass a deploy-scoped Wardlock API token from a secret, such as token: \${{ secrets.WARDLOCK_TOKEN }}"
  exit 1
fi

for pair in delete:INPUT_DELETE pr-preview:INPUT_PR_PREVIEW; do
  name=${pair%%:*} var=${pair#*:}
  case ${!var:-} in
    true | false) ;;
    *)
      echo "::error title=Wardlock deploy::$name must be true or false, not \"${!var:-}\""
      exit 1
      ;;
  esac
done

pr=false
[[ $INPUT_PR_PREVIEW == true && $EVENT_NAME == pull_request && -n ${PR_NUMBER:-} ]] && pr=true

preview=${INPUT_PREVIEW:-}
[[ -z $preview && $pr == true ]] && preview=pr-$PR_NUMBER

mode=deploy
if [[ $INPUT_DELETE == true || ($pr == true && ${EVENT_ACTION:-} == closed) ]]; then
  mode=delete
  if [[ -z $preview ]]; then
    echo "::error title=Wardlock deploy::delete needs a preview: set preview, or run on a pull_request event"
    exit 1
  fi
fi

out mode "$mode"
out preview "$preview"
