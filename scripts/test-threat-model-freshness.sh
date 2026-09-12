#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
check="$repo_root/scripts/check-threat-model-freshness.sh"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

cd "$tmp"
git init -q
git config user.name test
git config user.email test@example.invalid
mkdir -p internal/perm docs
printf 'package perm\n' > internal/perm/policy.go
printf '# Threat model\n' > docs/THREAT_MODEL.md
git add .
git commit -qm base
base=$(git rev-parse HEAD)

printf '// security change\n' >> internal/perm/policy.go
git add .
git commit -qm security-change
if "$check" "$base" >/dev/null 2>&1; then
  echo "expected security-only change to fail" >&2
  exit 1
fi

printf '\nReviewed.\n' >> docs/THREAT_MODEL.md
git add .
git commit -qm documentation-change
"$check" "$base"
