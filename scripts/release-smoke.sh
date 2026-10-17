#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 CubieCode
# SPDX-License-Identifier: MIT
#
# Checks a GoReleaser snapshot in .local/goreleaser (or $1): every archive and
# SBOM exists and matches checksums.txt, deploy/install.sh installs the host's
# binaries from it after verifying checksums, and the host-architecture image
# runs as non-root and answers /healthz. Other-architecture images are checked
# for their platform and user only. Needs Docker unless WARDLOCK_SMOKE_DOCKER=0.
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

dist=$(cd "${1:-$repo_root/.local/goreleaser}" && pwd)
image=ghcr.io/cubiecode/wardlock
version=$(node -p "require('$dist/metadata.json').version")
fail() {
  echo "release-smoke: $*" >&2
  exit 1
}
echo "release-smoke: checking $version in $dist"

expected=()
for bin in wardlockd wardlock; do
  for os in linux darwin windows; do
    for arch in amd64 arm64; do
      ext=tar.gz
      [[ $os == windows ]] && ext=zip
      expected+=("${bin}_${version}_${os}_${arch}.$ext")
    done
  done
done
for name in "${expected[@]}"; do
  for file in "$name" "$name.sbom.json"; do
    [[ -s $dist/$file ]] || fail "missing $file"
    grep -qE "^[0-9a-f]{64}  $file\$" "$dist/checksums.txt" || fail "$file is not in checksums.txt"
  done
  grep -q '"spdxVersion"' "$dist/$name.sbom.json" || fail "$name.sbom.json is not an SPDX document"
done
(cd "$dist" && sha256sum --quiet --strict -c checksums.txt) || fail "checksums.txt does not match"
echo "release-smoke: ${#expected[@]} archives, their SBOMs and checksums OK"

case $(uname -m) in
  x86_64 | amd64) host_arch=amd64 ;;
  aarch64 | arm64) host_arch=arm64 ;;
  *) fail "unsupported host architecture $(uname -m)" ;;
esac
tmp=$(mktemp -d)
container=
cleanup() {
  [[ -z $container ]] || docker rm -f "$container" >/dev/null 2>&1 || true
  rm -rf "$tmp"
}
trap cleanup EXIT

sh "$repo_root/deploy/install.sh" --version "$version" --component all \
  --dir "$tmp/bin" --base-url "file://$dist"
[[ $("$tmp/bin/wardlock" version) == "wardlock $version" ]] || fail "installed wardlock reports the wrong version"
[[ $("$tmp/bin/wardlockd" version) == "wardlockd $version" ]] || fail "installed wardlockd reports the wrong version"
cp "$dist/checksums.txt" "$tmp/checksums.txt"
for f in "${expected[@]}"; do [[ $f == *_linux_${host_arch}.tar.gz ]] && ln -s "$dist/$f" "$tmp/$f"; done
sed -i.bak "s/^[0-9a-f]\{64\}  \(wardlock_${version}_linux_${host_arch}.tar.gz\)\$/$(printf '0%.0s' {1..64})  \1/" "$tmp/checksums.txt"
if sh "$repo_root/deploy/install.sh" --version "$version" --dir "$tmp/bad" --base-url "file://$tmp" 2>/dev/null; then
  fail "install.sh accepted an archive with the wrong checksum"
fi
[[ ! -e $tmp/bad/wardlock ]] || fail "install.sh installed an unverified binary"
echo "release-smoke: install.sh installs verified binaries and refuses a bad checksum"

if [[ ${WARDLOCK_SMOKE_DOCKER:-1} == 0 ]]; then
  echo "release-smoke: skipping image checks (WARDLOCK_SMOKE_DOCKER=0)"
  exit 0
fi
for arch in amd64 arm64; do
  ref=$image:$version-$arch
  got=$(docker image inspect "$ref" --format '{{.Os}}/{{.Architecture}} {{.Config.User}}') || fail "missing image $ref"
  [[ $got == "linux/$arch nonroot:nonroot" ]] || fail "$ref is $got, expected linux/$arch nonroot:nonroot"
done

ref=$image:$version-$host_arch
# wardlockd serve needs a server key; use a throwaway one for this run.
secret=$(node -e "process.stdout.write(require('node:crypto').randomBytes(32).toString('base64url'))")
container=$(docker run -d --rm -p 127.0.0.1::8080 -e WARDLOCK_SECRET="$secret" "$ref")
port=$(docker port "$container" 8080/tcp | head -n 1 | sed 's/.*://')
for _ in $(seq 1 50); do
  body=$(curl -fsS "http://127.0.0.1:$port/healthz" 2>/dev/null) && break
  sleep 0.2
done
[[ ${body:-} == *'"status":"ok"'* ]] || {
  docker logs "$container" >&2 || true
  fail "/healthz did not answer from $ref"
}
uid=$(docker top "$container" -o pid,uid,comm | awk '$3 == "wardlockd" { print $2 }')
[[ $uid == 65532 ]] || fail "wardlockd runs as uid ${uid:-?}, expected 65532 (nonroot)"
[[ $(docker exec "$container" /usr/local/bin/wardlockd version) == "wardlockd $version" ]] ||
  fail "image reports the wrong version"
echo "release-smoke: $ref runs as uid 65532 and /healthz answers: $body"
