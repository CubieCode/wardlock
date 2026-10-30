#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 CubieCode
# SPDX-License-Identifier: MIT
#
# Downloads the wardlock CLI release archive for the runner's OS and
# architecture with the release's checksums.txt, refuses to use it unless
# its SHA-256 matches, and outputs cli, the extracted binary's path.
#
# The version is INPUT_VERSION, else the tag the action is used at
# (ACTION_REF), which must then be vX.Y.Z. WARDLOCK_ACTION_RELEASE_URL
# replaces the release directory for tests (https:// or file://).
set -euo pipefail

fail() {
  echo "::error title=Wardlock deploy::$*"
  exit 1
}

version=${INPUT_VERSION:-}
if [[ -z $version ]]; then
  [[ ${ACTION_REF:-} =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] ||
    fail "cannot tell which wardlock release to download: use the action at a release tag (CubieCode/wardlock/actions/deploy@vX.Y.Z), or set version (or cli-path)"
  version=$ACTION_REF
fi
version=${version#v}
[[ $version =~ ^[0-9A-Za-z.+-]+$ ]] || fail "invalid version \"$version\""

case ${RUNNER_OS:-} in
  Linux) os=linux ext=tar.gz bin=wardlock ;;
  macOS) os=darwin ext=tar.gz bin=wardlock ;;
  Windows) os=windows ext=zip bin=wardlock.exe ;;
  *) fail "unsupported runner OS \"${RUNNER_OS:-}\"" ;;
esac
case ${RUNNER_ARCH:-} in
  X64) arch=amd64 ;;
  ARM64) arch=arm64 ;;
  *) fail "unsupported runner architecture \"${RUNNER_ARCH:-}\"" ;;
esac

base=${WARDLOCK_ACTION_RELEASE_URL:-https://github.com/CubieCode/wardlock/releases/download/v$version}
base=${base%/}
case $base in
  https://* | file://*) ;;
  *) fail "the release URL must be https:// or file://" ;;
esac

if command -v sha256sum >/dev/null 2>&1; then
  sha256() { sha256sum "$1" | cut -d ' ' -f 1; }
else
  sha256() { shasum -a 256 "$1" | cut -d ' ' -f 1; }
fi

dir=$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/wardlock-cli.XXXXXX")
archive=wardlock_${version}_${os}_${arch}.$ext
fetch() { curl -fsSL --retry 3 -o "$2" "$1"; }

fetch "$base/checksums.txt" "$dir/checksums.txt" || fail "could not download $base/checksums.txt"
fetch "$base/$archive" "$dir/$archive" || fail "could not download $base/$archive"
want=$(awk -v f="$archive" '$2 == f || $2 == "*" f { print $1 }' "$dir/checksums.txt")
[[ -n $want ]] || fail "$archive is not listed in checksums.txt; not using it"
got=$(sha256 "$dir/$archive")
[[ $got == "$want" ]] || fail "checksum mismatch for $archive (expected $want, got $got); not using it"
echo "Verified $archive against checksums.txt."

mkdir "$dir/bin"
if [[ $ext == zip ]]; then
  unzip -q "$dir/$archive" "$bin" -d "$dir/bin" || fail "$archive does not contain $bin"
else
  tar -xzf "$dir/$archive" -C "$dir/bin" "$bin" || fail "$archive does not contain $bin"
fi
echo "cli=$dir/bin/$bin" >>"$GITHUB_OUTPUT"
