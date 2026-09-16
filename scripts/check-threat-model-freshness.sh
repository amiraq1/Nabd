#!/usr/bin/env bash
set -euo pipefail

base=${1:-${BASE_SHA:-}}
if [[ -z "$base" ]]; then
  echo "usage: $0 <base-sha>" >&2
  exit 2
fi

git cat-file -e "$base^{commit}"

# Entries are directory prefixes, not individual files, so that a new file
# inside a security boundary triggers the THREAT_MODEL requirement automatically
# instead of silently escaping both gates.
security_files=(
  internal/agent/fence.go
  internal/config/
  internal/perm/
  internal/pathindex/
  internal/provider/
  internal/redact/
  internal/safefs/
  internal/snap/
  internal/store/
  internal/tools/
  cmd/ag/main.go
  .goreleaser.yaml
  scripts/
)

mapfile -t changed < <(git diff --name-only "$base" HEAD -- "${security_files[@]}")
doc_changed=$(git diff --name-only "$base" HEAD -- docs/THREAT_MODEL.md)

if (( ${#changed[@]} > 0 )) && [[ -z "$doc_changed" ]]; then
  echo "security-relevant files changed without docs/THREAT_MODEL.md update:" >&2
  printf '  %s\n' "${changed[@]}" >&2
  exit 1
fi
