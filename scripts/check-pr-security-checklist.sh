#!/usr/bin/env bash
set -euo pipefail

[[ ${GITHUB_EVENT_NAME:-} == pull_request ]] || exit 0
: "${GITHUB_BASE_REF:?GITHUB_BASE_REF is required for diff cross-check}"

: "${LIVE_BODY_FILE:?LIVE_BODY_FILE must contain the live GitHub API response}"
[[ -f "$LIVE_BODY_FILE" ]] || { echo "LIVE_BODY_FILE does not exist" >&2; exit 1; }
body=$(<"$LIVE_BODY_FILE")

# Phase 1: Every checklist item must be resolved.
#
# An item is satisfied either by ticking it in full (`- [x] <item>`) or, when it
# genuinely does not apply, by marking the item's own line `- [x] N/A: <reason>`
# with a reason of at least min_na_reason characters. The items whose
# applicability depends on the diff (the THREAT_MODEL update and the regression
# test) may be marked N/A; the items that state an invariant applying to every
# pull request (classification, credential hygiene, action pinning) may not. An
# unchecked `- [ ] ` item always fails.
required=(
  "I classified whether this changes paths"
  "I updated \`docs/THREAT_MODEL.md\`"
  "I added or updated a regression test"
  "I checked that logs, fixtures, and diffs contain no credentials"
  "I verified third-party actions are pinned to full commit SHAs"
)
na_forbidden=(0 3 4)
min_na_reason=10

mapfile -t checklist_lines < <(grep -E '^- \[[ x]\] ' <<<"$body" || true)
if [[ ${#checklist_lines[@]} -ne ${#required[@]} ]]; then
  echo "PR security checklist is incomplete: expected ${#required[@]} items, found ${#checklist_lines[@]}" >&2
  exit 1
fi
for i in "${!required[@]}"; do
  item=${required[$i]}
  line=${checklist_lines[$i]}
  if [[ "$line" != "- [x] "* ]]; then
    echo "PR security checklist is incomplete: $item" >&2
    exit 1
  fi
  rest=${line#"- [x] "}
  if [[ "$rest" == "N/A:"* ]]; then
    for j in "${na_forbidden[@]}"; do
      if [[ "$i" -eq "$j" ]]; then
        echo "PR security checklist: this item always applies and cannot be marked N/A: $item" >&2
        exit 1
      fi
    done
    reason=${rest#"N/A:"}
    reason=${reason#"${reason%%[![:space:]]*}"}
    if (( ${#reason} < min_na_reason )); then
      echo "PR security checklist: N/A reason is shorter than ${min_na_reason} characters: $item" >&2
      exit 1
    fi
    continue
  fi
  if [[ "$rest" != *"$item"* ]]; then
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
  internal/ui/copy.go
  internal/ui/git_header.go
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
      hunk=$(git diff -U0 "$base" HEAD -- "$file" | grep -E '^[+-]' | grep -Ev '^--- (a/|/dev/null)|^\+\+\+ (b/|/dev/null)' || true)
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
  grep -Eiq -- '(^[[:space:]]*N/A:|^- \[x\] N/A:)[[:space:]]*(dependency|documentation|docs?|changelog|workflow|pin|release)' <<<"$body"
}

# The regression-test item is resolved either by ticking it or by marking it N/A,
# so this cross-check must not key on the ticked wording: doing so let a pull
# request disable the requirement entirely by writing `- [x] N/A: <reason>`.
# Phase 1 has already established that the item is resolved, so the check always
# runs. "claims" below therefore covers both forms.
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
