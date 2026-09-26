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
if bash "$check" "$base" >/dev/null 2>&1; then
  echo "expected security-only change to fail" >&2
  exit 1
fi

printf '\nReviewed.\n' >> docs/THREAT_MODEL.md
git add .
git commit -qm documentation-change
bash "$check" "$base"

# Verify internal/ui/git_header.go is covered by the freshness gate
mkdir -p internal/ui
printf 'package ui\n' > internal/ui/git_header.go
git add internal/ui/git_header.go
git commit -qm base-ui
base_ui=$(git rev-parse HEAD)

printf '// git header change\n' >> internal/ui/git_header.go
git add internal/ui/git_header.go
git commit -qm git-header-change
if bash "$check" "$base_ui" >/dev/null 2>&1; then
  echo "expected internal/ui/git_header.go change without THREAT_MODEL.md to fail" >&2
  exit 1
fi

printf '\nReviewed git header.\n' >> docs/THREAT_MODEL.md
git add docs/THREAT_MODEL.md
git commit -qm doc-change-for-git-header
bash "$check" "$base_ui"
