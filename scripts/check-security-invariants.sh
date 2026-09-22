#!/usr/bin/env bash
set -euo pipefail

# check-security-invariants.sh — verify security invariants across the codebase.
# Fails if any security invariant is violated in production code.

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$repo_root"

failures=0

check_guarded_provider_client() {
  local matches
  matches=$(grep -rn 'Client:[[:space:]]*&http\.Client{}' internal/provider/*.go 2>/dev/null | grep -v '_test\.go' || true)
  if [[ -n "$matches" ]]; then
    echo "provider constructed with an unguarded http.Client — use endpoint.Client(0):" >&2
    echo "$matches" >&2
    return 1
  fi
  return 0
}

# Run all invariant checks
if ! check_guarded_provider_client; then
  failures=$((failures + 1))
fi

if [[ "$failures" -gt 0 ]]; then
  echo "FAIL: $failures security invariant check(s) failed." >&2
  exit 1
fi

echo "OK: all security invariants passed."
exit 0
