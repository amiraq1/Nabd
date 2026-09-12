#!/usr/bin/env bash
set -euo pipefail

[[ ${GITHUB_EVENT_NAME:-} == pull_request ]] || exit 0
: "${GITHUB_EVENT_PATH:?GITHUB_EVENT_PATH is required}"
body=$(jq -r '.pull_request.body // ""' "$GITHUB_EVENT_PATH")
required=(
  "I classified whether this changes paths"
  "I updated \`docs/THREAT_MODEL.md\`"
  "I added or updated a regression test"
  "I checked that logs, fixtures, and diffs contain no credentials"
  "I verified third-party actions are pinned to full commit SHAs"
)
for item in "${required[@]}"; do
  if ! grep -Fq -- "- [x] $item" <<<"$body"; then
    echo "PR security checklist is incomplete: $item" >&2
    exit 1
  fi
done
