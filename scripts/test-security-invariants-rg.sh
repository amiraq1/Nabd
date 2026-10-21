#!/usr/bin/env bash
# test-security-invariants-rg.sh — regression test for RG_EXEC_COVERAGE_VACUOUS_PASS.
#
# Proves that scripts/check-security-invariants.sh fails closed (non-zero exit)
# when `rg` is absent from PATH, instead of passing vacuously with zero files
# scanned. Also proves the normal path (rg present) still passes.
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
check="$repo_root/scripts/check-security-invariants.sh"

# Build a PATH that contains everything except rg.
tmpbin=$(mktemp -d)
trap 'rm -rf "$tmpbin"' EXIT
for d in /usr/local/bin /usr/bin /bin; do
  [[ -d "$d" ]] || continue
  for f in "$d"/*; do
    base=$(basename "$f")
    [[ "$base" == "rg" ]] && continue
    [[ -e "$tmpbin/$base" ]] || ln -s "$f" "$tmpbin/$base" 2>/dev/null || true
  done
done

# Sanity: rg must really be absent from the restricted PATH.
if PATH="$tmpbin" command -v rg >/dev/null 2>&1; then
  echo "test setup broken: rg found in restricted PATH" >&2
  exit 1
fi

# 1. rg absent -> must FAIL (fail closed), not pass vacuously.
if PATH="$tmpbin" bash "$check" >/dev/null 2>&1; then
  echo "RG_EXEC_COVERAGE_VACUOUS_PASS: script passed with rg absent (vacuous green)" >&2
  exit 1
fi
echo "ok: script fails closed when rg is absent"

# 2. rg present -> must PASS (normal path unaffected).
# On hosts without rg (e.g. Termux, the Windows capture host) this case
# cannot run; print an explicit SKIP instead of a misleading failure.
if ! command -v rg >/dev/null 2>&1; then
  echo "SKIP: rg not installed on this host — normal-path case not applicable"
else
  if ! bash "$check" >/dev/null 2>&1; then
    echo "script failed with rg present (regression in normal path)" >&2
    exit 1
  fi
  echo "ok: script passes when rg is present"
fi

echo "PASS: RG_EXEC_COVERAGE_VACUOUS_PASS regression test"
