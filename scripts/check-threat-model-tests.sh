#!/usr/bin/env bash
set -euo pipefail

# Checks that every backtick-quoted Test[A-Za-z0-9_]+ citation in each
# guarded document resolves to a real func Test* in some *_test.go file.
#
# Guarded documents:
#   docs/THREAT_MODEL.md  (original scope; output contract unchanged)
#   docs/TECH_DEBT.md     (extended in ci/extend-test-citation-guard)
#
# NOTES.md is intentionally excluded. It contains historical citations
# (e.g. TestDedupReads on line 253, which explicitly states the test was
# deleted, and TestFlushJoinSameForChatAndReplay retained for text search)
# that are past-tense records, not present-tense contract claims. Extending
# the guard to NOTES.md requires an agreed convention for marking historical
# references (e.g. a "(historical)" suffix or a dedicated section acting as
# an allowlist). Until that convention is established and the two known
# historical citations are annotated, applying a strict check would produce
# false positives. See TECH_DEBT.md §NOTES_CITATION_GUARD_DEFERRED.

# Pattern: backtick-prefixed Test followed by one or more word characters.
# Rationale for the pattern:
#   - Starts with capital T so "test_gate" and "test_" (lowercase) are not
#     matched; no explicit exclusion list is needed.
#   - Underscore is included because NBD-numbered test names use it (e.g.
#     TestReadCapPinsMeasuredTurnCost_NBD401).
#   - The closing backtick is not part of the match; grep -oE stops at the
#     first non-word character after the name, which is the closing backtick.
#   - Fenced code block citations are treated the same as inline citations:
#     if a fenced block cites TestFoo, TestFoo must exist in *_test.go.
#     Fenced blocks are documentation of a test that should exist; they are
#     not examples of an imaginary name.
#   - Functions that begin with "Test" but are not Go test functions (e.g. a
#     helper TestMakeFixture) are also found by this check. That is correct:
#     any name cited in security documentation must resolve; if the helper
#     were removed, the citation would break. The check is conservative.

PATTERN='`Test[A-Za-z0-9_]+'

# Guarded documents in order. The first entry preserves the original output
# contract for THREAT_MODEL.md (see §Existing output contract below).
docs=(
  "docs/THREAT_MODEL.md"
  "docs/TECH_DEBT.md"
)

overall_missing=0

for doc in "${docs[@]}"; do
  [[ -f "$doc" ]] || { echo "missing document: $doc" >&2; exit 1; }

  mapfile -t tests < <(
    LC_ALL=C grep -oE "$PATTERN" "$doc" |
      sed 's/^`//' |
      sort -u
  )

  found=${#tests[@]}

  if [[ "$doc" == "docs/THREAT_MODEL.md" ]]; then
    # §Existing output contract — the original THREAT_MODEL.md behaviour is
    # preserved exactly. The "no citations" guard and "OK:" line use the
    # original wording so that scripts/release_pipeline_test.go and any CI
    # log parsers that key on this string continue to work unchanged.
    if [[ "$found" -eq 0 ]]; then
      echo "no backtick-quoted Test... citations found in $doc" >&2
      exit 1
    fi
    doc_missing=0
    for test_name in "${tests[@]}"; do
      if ! grep -R --include='*_test.go' -Eq \
          "func[[:space:]]+${test_name}[[:space:]]*\(" .; then
        echo "missing cited test: ${test_name}" >&2
        doc_missing=1
      fi
    done
    [[ "$doc_missing" -eq 0 ]] || overall_missing=1
    echo "OK: all ${found} tests cited in ${doc} exist."
  else
    # §New document output — one summary line per document; failures include
    # the document name and line number so engineers can locate the citation
    # without searching manually.
    if [[ "$found" -eq 0 ]]; then
      echo "TECH_DEBT: no backtick-quoted Test... citations found in $doc" >&2
      overall_missing=1
      continue
    fi
    doc_missing=0
    for test_name in "${tests[@]}"; do
      if ! grep -R --include='*_test.go' -Eq \
          "func[[:space:]]+${test_name}[[:space:]]*\(" .; then
        # Print document name and line number alongside the missing name so the
        # engineer can find the citation without a second grep.
        lineno=$(LC_ALL=C grep -n "$PATTERN" "$doc" |
          grep -m1 "\`${test_name}" |
          cut -d: -f1)
        echo "missing cited test: ${test_name} (${doc}:${lineno})" >&2
        doc_missing=1
      fi
    done
    if [[ "$doc_missing" -eq 0 ]]; then
      echo "TECH_DEBT: all ${found} tests cited in ${doc} exist."
    else
      overall_missing=1
    fi
  fi
done

[[ "$overall_missing" -eq 0 ]] || exit 1
