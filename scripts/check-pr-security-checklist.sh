#!/usr/bin/env bash
set -euo pipefail

[[ ${GITHUB_EVENT_NAME:-} == pull_request ]] || exit 0
: "${GITHUB_EVENT_PATH:?GITHUB_EVENT_PATH is required}"
: "${GITHUB_BASE_REF:?GITHUB_BASE_REF is required for diff cross-check}"

body=$(jq -r '.pull_request.body // ""' "$GITHUB_EVENT_PATH")

# Phase 1: All checklist items must be checked.
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

# Phase 2: Cross-check — checked claims must match actual diff.
# Fetch base ref and resolve it to avoid silent failures when the base is missing.
git fetch --no-tags --depth=1 origin "$GITHUB_BASE_REF" 2>/dev/null || true
base="origin/${GITHUB_BASE_REF}"
git rev-parse --verify -q "$base" >/dev/null || { echo "S7 gate failed: cannot resolve $base" >&2; exit 1; }
changed=$(git diff --name-only "$base" HEAD)

# If the PR body claims THREAT_MODEL.md was updated, the file must appear in the diff.
if grep -Fq -- "- [x] I updated \`docs/THREAT_MODEL.md\`" <<<"$body"; then
  if ! grep -q "docs/THREAT_MODEL.md" <<<"$changed"; then
    echo "S7 gate failed: checklist claims 'I updated docs/THREAT_MODEL.md' but file is not in diff." >&2
    exit 1
  fi
fi

# If the PR body claims a regression test was added/updated, a *_test.go file must be in the diff.
if grep -Fq -- "- [x] I added or updated a regression test" <<<"$body"; then
  if ! grep -Eq "_test\.go$" <<<"$changed"; then
    echo "S7 gate failed: checklist claims 'I added or updated a regression test' but no *_test.go in diff." >&2
    exit 1
  fi
fi
