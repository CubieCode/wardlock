#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 CubieCode
# SPDX-License-Identifier: MIT
#
# Proves the Go module tag scheme in docs/DEPLOYMENT.md without publishing
# anything. It clones the committed HEAD into a temporary directory, makes the
# release commit there (drop the local replace directives, require the client
# at the release version), tags it with directory-prefixed module tags, and
# runs `go install wardlock.nz/<module>/cmd/...@<version>` through Go's real
# vanity-path lookup:
#
# - a local HTTP proxy (HTTP_PROXY) answers https://wardlock.nz/<module>/...?go-get=1
#   with home-site/public/index.html, the page wardlock.nz serves, except that
#   its repository URL points at the temporary clone served by `git daemon` on
#   127.0.0.1;
# - GOPRIVATE and GOINSECURE cover wardlock.nz only, and the proxy refuses
#   HTTPS to wardlock.nz, so nothing reaches the real domain (other HTTPS,
#   such as the checksum database, is tunnelled unchanged);
# - a throwaway GOMODCACHE keeps the test versions out of your module cache.
#   Third-party modules come from your existing module cache, then the
#   configured GOPROXY.
#
# It also checks the two ways a release can go wrong: an unprefixed vX.Y.Z tag
# is not a module version, and a tagged commit that still has replace
# directives cannot be installed. Every tag exists only in the temporary clone,
# which is deleted on exit; nothing is pushed. Needs git (with git daemon) and
# Go.
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

version=v0.1.0-tagtest.1
tmp=$(mktemp -d)
pids=()
cleanup() {
  for pid in "${pids[@]}"; do kill "$pid" 2>/dev/null || true; done
  wait 2>/dev/null || true
  # The module cache is read-only by design.
  chmod -R u+w "$tmp" 2>/dev/null || true
  rm -rf "$tmp"
}
trap cleanup EXIT
fail() {
  echo "test-module-tags: $*" >&2
  exit 1
}
free_port() { node -e 'const s=require("net").createServer().listen(0,"127.0.0.1",()=>{console.log(s.address().port);s.close()})'; }

real_cache=$(go env GOMODCACHE)
(cd "$repo_root" && go mod download >/dev/null 2>&1 || true)
upstream_proxy=$(go env GOPROXY)

repo=$tmp/srv/wardlock
git clone --quiet --no-local "$repo_root" "$repo"
git -C "$repo" remote remove origin
git -C "$repo" config user.name 'Module tag test'
git -C "$repo" config user.email 'module-tag-test@wardlock.invalid'
git -C "$repo" config commit.gpgsign false
git -C "$repo" config tag.gpgsign false

# Wrong on purpose: a commit that still has the development replace
# directive. On main HEAD has it; on a release commit, which has dropped it,
# add it back in a commit of its own so the check still means something.
if ! grep -q '^replace wardlock.nz/apiclient ' "$repo/apps/cli/go.mod"; then
  (cd "$repo/apps/cli" && go mod edit -replace=wardlock.nz/apiclient=../../packages/apiclient-go)
  git -C "$repo" commit --quiet -am "Development replace directive (module tag test)"
fi
git -C "$repo" tag apps/cli/v0.0.9-tagtest.1

# The release, in the order DEPLOYMENT describes.
git -C "$repo" tag "packages/apiclient-go/$version"
for mod in apps/cli apps/server; do
  (cd "$repo/$mod" && go mod edit -dropreplace=wardlock.nz/apiclient)
done
(cd "$repo/apps/cli" && go mod edit -require="wardlock.nz/apiclient@$version")

git_port=$(free_port)
git daemon --reuseaddr --export-all --listen=127.0.0.1 --port="$git_port" \
  --base-path="$tmp/srv" "$tmp/srv" >"$tmp/git-daemon.log" 2>&1 &
pids+=($!)

mkdir "$tmp/vanity"
cat >"$tmp/vanity/main.go" <<'EOF'
// Answers go-get=1 lookups for wardlock.nz with the real site's page, its
// repository URL replaced by a local repository. Tunnels HTTPS to other hosts (the module
// proxy and checksum database) unchanged; refuses everything else, including
// HTTPS to wardlock.nz, so the real domain is never contacted.
package main

import (
	"io"
	"net"
	"net/http"
	"os"
	"strings"
)

func main() {
	modules := map[string]bool{"cli": true, "server": true, "apiclient": true}
	page, err := os.ReadFile(os.Args[3])
	if err != nil {
		panic(err)
	}
	body := strings.ReplaceAll(string(page), "https://github.com/CubieCode/wardlock", os.Args[2])
	l, err := net.Listen("tcp", "127.0.0.1:"+os.Args[1])
	if err != nil {
		panic(err)
	}
	panic(http.Serve(l, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect {
			host, _, _ := net.SplitHostPort(r.Host)
			if host == "wardlock.nz" || strings.HasSuffix(host, ".wardlock.nz") {
				http.Error(w, "refused", http.StatusForbidden)
				return
			}
			tunnel(w, r.Host)
			return
		}
		mod, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
		if r.URL.Hostname() != "wardlock.nz" || r.URL.Query().Get("go-get") != "1" || !modules[mod] {
			http.Error(w, "refused", http.StatusForbidden)
			return
		}
		io.WriteString(w, body)
	})))
}

func tunnel(w http.ResponseWriter, addr string) {
	up, err := net.Dial("tcp", addr)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusOK)
	down, _, err := http.NewResponseController(w).Hijack()
	if err != nil {
		up.Close()
		return
	}
	go func() { io.Copy(up, down); up.Close() }()
	io.Copy(down, up)
	down.Close()
}
EOF
proxy_port=$(free_port)
(cd "$tmp/vanity" && GOWORK=off GOFLAGS= go mod init vanity >/dev/null 2>&1 && GOWORK=off GOFLAGS= go build -o vanity .)
"$tmp/vanity/vanity" "$proxy_port" "git://127.0.0.1:$git_port/wardlock" "$repo/home-site/public/index.html" >"$tmp/vanity.log" 2>&1 &
pids+=($!)
for _ in $(seq 1 50); do
  (exec 3<>"/dev/tcp/127.0.0.1/$proxy_port") 2>/dev/null && (exec 3<>"/dev/tcp/127.0.0.1/$git_port") 2>/dev/null && break
  sleep 0.1
done

export GOWORK=off GOFLAGS= GOTOOLCHAIN=local GOENV=off
export GOMODCACHE=$tmp/modcache GOBIN=$tmp/bin GOCACHE=$tmp/gocache
export GOPRIVATE=wardlock.nz GOINSECURE=wardlock.nz GONOSUMDB=wardlock.nz
export GOPROXY="file://$real_cache/cache/download,$upstream_proxy"
export HTTP_PROXY=http://127.0.0.1:$proxy_port HTTPS_PROXY=http://127.0.0.1:$proxy_port
export NO_PROXY=127.0.0.1 GIT_TERMINAL_PROMPT=0

# go.sum for the release commit, resolved through the vanity path.
(cd "$repo/apps/cli" && go mod tidy) || fail "go mod tidy cannot resolve wardlock.nz/apiclient@$version"
git -C "$repo" commit --quiet -am "Release $version"
git -C "$repo" tag "apps/cli/$version"
git -C "$repo" tag "apps/server/$version"
git -C "$repo" tag "${version}" # The product tag GoReleaser uses.

for mod in cli server apiclient; do
  got=$(go list -m -versions "wardlock.nz/$mod") || fail "go list -m wardlock.nz/$mod failed"
  [[ " $got " == *" $version "* ]] || fail "wardlock.nz/$mod does not list $version (got: $got)"
done
echo "test-module-tags: prefixed tags resolve as module versions: $version"

go install "wardlock.nz/cli/cmd/wardlock@$version"
go install "wardlock.nz/server/cmd/wardlockd@$version"
"$GOBIN/wardlock" version | grep -q '^wardlock ' || fail "installed wardlock does not run"
"$GOBIN/wardlockd" version | grep -q '^wardlockd ' || fail "installed wardlockd does not run"
go version -m "$GOBIN/wardlock" | grep -qE "dep[[:space:]]+wardlock.nz/apiclient[[:space:]]+$version" ||
  fail "wardlock was not built against wardlock.nz/apiclient@$version"
echo "test-module-tags: go install wardlock.nz/cli/cmd/wardlock@$version and wardlock.nz/server/cmd/wardlockd@$version work"

if go install "wardlock.nz/cli/cmd/wardlock@v0.0.9-tagtest.1" 2>"$tmp/replace.log"; then
  fail "a tag on a commit with replace directives should not install"
fi
grep -q 'replace directives' "$tmp/replace.log" || {
  cat "$tmp/replace.log" >&2
  fail "unexpected error for a tag with replace directives"
}
echo "test-module-tags: a tagged commit that keeps replace directives is rejected, as expected"

git -C "$repo" tag v0.1.0-unprefixed.1
if go install "wardlock.nz/cli/cmd/wardlock@v0.1.0-unprefixed.1" 2>/dev/null; then
  fail "an unprefixed tag should not be a module version"
fi
echo "test-module-tags: an unprefixed vX.Y.Z tag is not a module version, as expected"
echo "test-module-tags: OK; the temporary clone and its tags are deleted"
