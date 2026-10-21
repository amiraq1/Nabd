#!/usr/bin/env bash
set -euo pipefail

[[ ${GITHUB_EVENT_NAME:-} == pull_request ]] || exit 0
: "${GITHUB_BASE_REF:?GITHUB_BASE_REF is required for diff cross-check}"

# ---------------------------------------------------------------------------
# Dependabot automated path
#
# Dependabot pull requests cannot fill in a human security checklist. To handle
# automated dependency bumps, this path provides a mechanical check with
# explicit, bounded trust properties:
#
#   1. Event-level identity verification:
#      - PR_AUTHOR_LOGIN must be exactly "dependabot[bot]" (the pull request author
#        from github.event.pull_request.user.login).
#      - GITHUB_ACTOR must be exactly "dependabot[bot]" (the actor triggering the
#        current event from github.actor).
#      Contract: An event triggered by a human does not benefit from this automated
#      path, even if the pull request was opened by Dependabot. This check verifies
#      the current event's actor and the PR author login; it does NOT inspect git
#      commit history or prove the absence of prior human commits on the branch.
#      Branch-name prefixes (e.g. dependabot/*) are author-controlled and carry
#      zero identity assurance.
#
#   2. Bounded path scope (file names only, not semantic diff inspection):
#      - Root Go dependency manifests: exactly "go.mod" or "go.sum".
#        Literal exact match only; suffixes or extension variations such as
#        "go.mod.evil" or "go.sum.backup" are rejected.
#      - Submodule Go dependency manifests: "third_party/<name>/go.mod" or
#        "third_party/<name>/go.sum" directly in a single subdirectory of
#        third_party/. No executable Go source files or nested subtrees are permitted.
#      - GitHub Actions workflow definitions: ".github/workflows/<name>.yml" or
#        ".github/workflows/<name>.yaml" directly in .github/workflows/ (no subdirs,
#        scripts, or non-workflow files).
#      Boundary note: This check validates file paths and directory boundaries only;
#      it does NOT inspect or parse diff contents. Specifically, accepting
#      .github/workflows/<name>.yml or .yaml verifies file path scope only and does
#      not prove that changes within the workflow are confined to action version
#      bumps (uses: / commit SHAs).
#
#   Any file changed outside this strictly bounded path scope causes the gate to
#   fall back to the full human checklist.
#
#   3. Build, test, and security CI checks are unaffected and remain required.
# ---------------------------------------------------------------------------

is_dependabot_allowed_file() {
  local file="$1"
  case "$file" in
    go.mod | go.sum)
      return 0
      ;;
    third_party/*)
      local sub="${file#third_party/}"
      if [[ "$sub" =~ ^[^/]+/(go\.mod|go\.sum)$ ]]; then
        return 0
      fi
      return 1
      ;;
    .github/workflows/*)
      local wf="${file#.github/workflows/}"
      if [[ "$wf" =~ ^[^/]+\.(yml|yaml)$ ]]; then
        return 0
      fi
      return 1
      ;;
    *)
      return 1
      ;;
  esac
}

if [[ "${PR_AUTHOR_LOGIN:-}" == "dependabot[bot]" && "${GITHUB_ACTOR:-}" == "dependabot[bot]" ]]; then
  git fetch --no-tags --depth=1 origin "$GITHUB_BASE_REF" 2>/dev/null || true
  dep_base="origin/${GITHUB_BASE_REF}"
  git rev-parse --verify -q "$dep_base" >/dev/null || {
    echo "Dependabot path: cannot resolve $dep_base" >&2; exit 1
  }
  dep_changed=$(git diff --name-only "$dep_base" HEAD)
  out_of_scope=""
  while IFS= read -r dep_file; do
    [[ -n "$dep_file" ]] || continue
    if ! is_dependabot_allowed_file "$dep_file"; then
      out_of_scope="${out_of_scope}  ${dep_file}"$'\n'
    fi
  done <<< "$dep_changed"
  if [[ -z "$out_of_scope" ]]; then
    echo "Dependabot automated path: all changed files are within the allowed dependency scope. Checklist gate passed."
    exit 0
  fi
  echo "Dependabot path: the following files are outside the allowed dependency scope; falling back to the full human checklist:" >&2
  printf '%s' "$out_of_scope" >&2
  # Fall through to the full checklist check below.
fi

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
  internal/mcp/
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
