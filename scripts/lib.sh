# SPDX-FileCopyrightText: 2026 CubieCode
# SPDX-License-Identifier: MIT
#
# Shared helpers for repository scripts. Source it; do not run it.
# Sets repo_root and puts the pinned Go tools (tools/go.mod) on PATH,
# building them into .local/bin the first time and whenever the pins change.
# WARDLOCK_TOOLS_DIR overrides that directory (CI keeps it in $HOME so it
# survives checkout cleaning the workspace).

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

ensure_go_tools() {
  local bin="${WARDLOCK_TOOLS_DIR:-$repo_root/.local/bin}" stamp want
  stamp="$bin/.tools-stamp"
  want="$(cat "$repo_root/tools/go.mod" "$repo_root/tools/go.sum" | cksum)"
  if [[ ! -f "$stamp" || "$(cat "$stamp")" != "$want" ]]; then
    echo "Building pinned Go tools into $bin (the first build takes a few minutes)..." >&2
    mkdir -p "$bin"
    GOWORK=off GOBIN="$bin" go -C "$repo_root/tools" install tool
    printf '%s' "$want" >"$stamp"
  fi
  export PATH="$bin:$PATH"
}

# add_spdx LICENCE COMMENT FILE... prepends an SPDX header to generated files.
add_spdx() {
  local licence="$1" comment="$2" file tmp
  shift 2
  for file in "$@"; do
    tmp="$(mktemp)"
    {
      printf '%s SPDX-FileCopyrightText: 2026 CubieCode\n' "$comment"
      printf '%s SPDX-License-Identifier: %s\n\n' "$comment" "$licence"
      cat "$file"
    } >"$tmp"
    mv "$tmp" "$file"
  done
}
