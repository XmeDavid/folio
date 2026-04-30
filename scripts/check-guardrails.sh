#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

fail=0

check() {
  local name="$1"
  shift
  if "$@"; then
    printf 'ok: %s\n' "$name"
  else
    printf 'fail: %s\n' "$name" >&2
    fail=1
  fi
}

check "OpenAPI does not use legacy workspace header" \
  bash -c "! rg -n 'workspaceHeader|X-Workspace-ID' openapi/openapi.yaml"

check "OpenAPI workspace routes are path-scoped" \
  bash -c "! rg -n '^  /api/v1/(accounts|transactions|categories|merchants|tags|categorization-rules)(:|/)' openapi/openapi.yaml"

check "Tailwind v4 token utilities are not malformed" \
  bash -c "! rg -n '\\b(bg|text|border|ring|divide|outline|fill|placeholder|from|to|via)-\\[--' web"

go_version="$(awk '/^go / { print $2; exit }' backend/go.mod)"
check "README Go version matches backend/go.mod" \
  bash -c "rg -q \"Go ${go_version}\" README.md"

next_major="$(node -e 'const p=require("./web/package.json"); console.log(String(p.dependencies.next).match(/[0-9]+/)[0])')"
check "architecture docs mention current Next.js major" \
  bash -c "rg -q \"Next\\.js ${next_major}\" docs/architecture.md README.md"

exit "$fail"
