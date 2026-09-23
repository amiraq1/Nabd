#!/usr/bin/env bash
set -euo pipefail

[[ ${GITHUB_EVENT_NAME:-} == pull_request ]] || exit 0
: "${GITHUB_BASE_REF:?GITHUB_BASE_REF is required for diff cross-check}"

: "${LIVE_BODY_FILE:?LIVE_BODY_FILE must contain the live GitHub API response}"
[[ -f "$LIVE_BODY_FILE" ]] || { echo "LIVE_BODY_FILE does not exist" >&2; exit 1; }
body=$(<"$LIVE_BODY_FILE")

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

# The THREAT_MODEL.md item is satisfied either by updating the document or by
# confirming that no security claim changed, which is what the PR template asks
# for. Only a diff that touches a security-relevant contract can therefore make
# the document mandatory. The list below is the one enforced by
# scripts/check-threat-model-freshness.sh, so the two gates cannot disagree.
# Entries are directory prefixes, not individual files, so that a new file
# inside a security boundary triggers the THREAT_MODEL requirement automatically
# instead of silently escaping both gates. The exception is a single file that
# is itself the boundary (fence.go, main.go, .goreleaser.yaml).
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

if grep -Fq -- "- [x] I updated \`docs/THREAT_MODEL.md\`" <<<"$body"; then
  security_changed=$(git diff --name-only "$base" HEAD -- "${security_files[@]}")
  if [[ -n "$security_changed" ]] && ! grep -q "docs/THREAT_MODEL.md" <<<"$changed"; then
    echo "S7 gate failed: security-relevant files changed but docs/THREAT_MODEL.md is not in diff:" >&2
    printf '  %s\n' "$security_changed" >&2
    exit 1
  fi
fi

# The regression-test item, like the THREAT_MODEL item, is satisfied either by a
# test or by explaining why no test is applicable — the PR template says so. A
# diff that cannot change behaviour is that explanation: every changed file is
# Markdown, lives under docs/, or is a .go file whose added and removed lines are
# all comments or blank. One executable Go line, one script, workflow, or
# fixture, and a test is required exactly as before.
behaviour_free_diff() {
  local file hunk line content
  while IFS= read -r file; do
    [[ -n "$file" ]] || continue
    case "$file" in
    *.md | docs/*) continue ;;
    *.go)
      hunk=$(git diff -U0 "$base" HEAD -- "$file" | grep -E '^[+-]' | grep -Ev '^[+]{3}|^---' || true)
      while IFS= read -r line; do
        [[ -n "$line" ]] || continue
        content=${line:1}
        content=${content#"${content%%[![:space:]]*}"}
        [[ -z "$content" || $content == //* ]] || return 1
      done <<<"$hunk"
      ;;
    *) return 1 ;;
    esac
  done <<<"$changed"
  return 0
}

# A checked test item may also carry an explicit N/A explanation for a
# documentation, changelog, workflow-pin, or dependency-only change. This is
# deliberately narrow: the checklist still requires the human explanation, and
# executable source changes continue to require a *_test.go file.
no_test_explanation() {
  if grep -Eq '^scripts/check-.*\.sh$' <<<"$changed" || grep -Eq '^\.github/workflows/.*\.ya?ml$' <<<"$changed"; then
    return 1
  fi
  local go_files
  go_files=$(grep -E '\.go$' <<<"$changed" || true)
  if [[ -n "$go_files" ]]; then
    behaviour_free_diff || return 1
  fi
  grep -Eiq -- '^[[:space:]]*N/A:[[:space:]]*(dependency|documentation|docs?|changelog|workflow|pin|release)' <<<"$body"
}

# If the PR body claims a regression test was added/updated, a *_test.go file must be in the diff.
if grep -Fq -- "- [x] I added or updated a regression test" <<<"$body"; then
  if ! grep -Eq "_test\.go$" <<<"$changed"; then
    if behaviour_free_diff; then
      echo "Regression-test cross-check skipped: the diff changes only comments and documentation."
    elif no_test_explanation; then
      echo "Regression-test cross-check skipped: the PR documents why no test applies."
    else
      echo "S7 gate failed: checklist claims 'I added or updated a regression test' but no *_test.go in diff." >&2
      exit 1
    fi
  fi
fi
