#!/bin/sh
# SPDX-FileCopyrightText: 2026 CubieCode
# SPDX-License-Identifier: MIT
#
# Installs a Wardlock release binary (the wardlock CLI, wardlockd, or both) on
# Linux or macOS. It downloads the archive and checksums.txt from the GitHub
# release, refuses to install unless the archive's SHA-256 matches, and
# installs only the binary. It never uses sudo; pick a directory you can write
# to, or run it with sudo yourself.
#
#   sh install.sh [--version X.Y.Z] [--component wardlock|wardlockd|all]
#                 [--dir DIR] [--base-url URL]
#
# Environment: WARDLOCK_VERSION, WARDLOCK_COMPONENT, WARDLOCK_INSTALL_DIR and
# WARDLOCK_INSTALL_BASE_URL set the same options. --base-url points at a
# directory holding the release files (for example a snapshot's
# .local/goreleaser, as file:///path); it requires --version.
set -eu

repo=https://github.com/CubieCode/wardlock
version=${WARDLOCK_VERSION:-}
component=${WARDLOCK_COMPONENT:-wardlock}
dir=${WARDLOCK_INSTALL_DIR:-/usr/local/bin}
base_url=${WARDLOCK_INSTALL_BASE_URL:-}

say() { printf 'install.sh: %s\n' "$*" >&2; }
die() {
  say "$*"
  exit 1
}

usage() {
  cat >&2 <<'EOF'
usage: sh install.sh [--version X.Y.Z] [--component wardlock|wardlockd|all]
                     [--dir DIR] [--base-url URL]
EOF
  exit 2
}

while [ $# -gt 0 ]; do
  case $1 in
    --version) [ $# -ge 2 ] || usage; version=$2; shift 2 ;;
    --component) [ $# -ge 2 ] || usage; component=$2; shift 2 ;;
    --dir) [ $# -ge 2 ] || usage; dir=$2; shift 2 ;;
    --base-url) [ $# -ge 2 ] || usage; base_url=$2; shift 2 ;;
    -h | --help) usage ;;
    *) say "unknown option $1"; usage ;;
  esac
done

case $component in
  wardlock | wardlockd) binaries=$component ;;
  all) binaries='wardlock wardlockd' ;;
  *) die "unknown component \"$component\" (wardlock, wardlockd or all)" ;;
esac
version=${version#v}

case $(uname -s) in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) die "unsupported OS $(uname -s); download a release archive by hand" ;;
esac
case $(uname -m) in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) die "unsupported architecture $(uname -m)" ;;
esac

# Release downloads must use HTTPS; an explicit --base-url may be file:// or
# a local mirror.
if command -v curl >/dev/null 2>&1; then
  if [ -n "$base_url" ]; then
    fetch() { curl -fsSL -o "$2" "$1"; }
  else
    fetch() { curl -fsSL --proto '=https' --tlsv1.2 -o "$2" "$1"; }
  fi
elif command -v wget >/dev/null 2>&1; then
  if [ -n "$base_url" ]; then
    fetch() { wget -q -O "$2" "$1"; }
  else
    fetch() { wget -q --https-only -O "$2" "$1"; }
  fi
else
  die "needs curl or wget"
fi

if command -v sha256sum >/dev/null 2>&1; then
  sha256() { sha256sum "$1" | cut -d ' ' -f 1; }
elif command -v shasum >/dev/null 2>&1; then
  sha256() { shasum -a 256 "$1" | cut -d ' ' -f 1; }
else
  die "needs sha256sum or shasum to verify the download"
fi

if [ -z "$version" ]; then
  [ -z "$base_url" ] || die "--base-url needs --version"
  command -v curl >/dev/null 2>&1 || die "pass --version (finding the latest release needs curl)"
  latest=$(curl -fsSLI --proto '=https' --tlsv1.2 -o /dev/null -w '%{url_effective}' "$repo/releases/latest") ||
    die "could not find the latest release; pass --version"
  case $latest in
    */releases/tag/v*) version=${latest##*/releases/tag/v} ;;
    *) die "no published release found; pass --version" ;;
  esac
fi
case $version in
  *[!0-9A-Za-z.+-]* | '') die "invalid version \"$version\"" ;;
esac
[ -n "$base_url" ] || base_url=$repo/releases/download/v$version
base_url=${base_url%/}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

fetch "$base_url/checksums.txt" "$tmp/checksums.txt" ||
  die "could not download $base_url/checksums.txt"

for bin in $binaries; do
  archive=${bin}_${version}_${os}_${arch}.tar.gz
  fetch "$base_url/$archive" "$tmp/$archive" || die "could not download $base_url/$archive"
  want=$(awk -v f="$archive" '$2 == f || $2 == "*" f { print $1 }' "$tmp/checksums.txt")
  [ -n "$want" ] || die "$archive is not listed in checksums.txt; not installing"
  got=$(sha256 "$tmp/$archive")
  [ "$got" = "$want" ] || die "checksum mismatch for $archive (expected $want, got $got); not installing"
  say "verified $archive"

  mkdir "$tmp/$bin"
  tar -xzf "$tmp/$archive" -C "$tmp/$bin" "$bin" || die "$archive does not contain $bin"
  mkdir -p "$dir" || die "cannot create $dir"
  install -m 0755 "$tmp/$bin/$bin" "$dir/$bin" || die "cannot write $dir/$bin; choose --dir or run with sudo"
  say "installed $dir/$bin ($version)"
done
