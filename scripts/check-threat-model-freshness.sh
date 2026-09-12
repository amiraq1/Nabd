#!/usr/bin/env bash
set -euo pipefail

base=${1:-${BASE_SHA:-}}
if [[ -z "$base" ]]; then
  echo "usage: $0 <base-sha>" >&2
  exit 2
fi

git cat-file -e "$base^{commit}"

security_files=(
  internal/tools/path.go
  internal/tools/bash.go
  internal/perm/policy.go
  internal/config/config.go
  internal/snap/shadow.go
  internal/safefs
  internal/agent/fence.go
  cmd/ag/main.go
)

mapfile -t changed < <(git diff --name-only "$base" HEAD -- "${security_files[@]}")
doc_changed=$(git diff --name-only "$base" HEAD -- docs/THREAT_MODEL.md)

if (( ${#changed[@]} > 0 )) && [[ -z "$doc_changed" ]]; then
  echo "security-relevant files changed without docs/THREAT_MODEL.md update:" >&2
  printf '  %s\n' "${changed[@]}" >&2
  exit 1
fi
