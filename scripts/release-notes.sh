#!/usr/bin/env bash
set -euo pipefail

# Print the body of the `## <version>` section of the changelog, for GoReleaser's
# --release-notes. The section runs from the exact `## <version>` header to the
# next `## ` header, with leading and trailing blank lines removed. It exits
# non-zero with a message on stderr when the section is missing or empty, so a
# release can never publish empty notes.
#
# Usage: release-notes.sh <version-tag>
# Env:   CHANGELOG_FILE=<path>   (default: CHANGELOG.md)

version=${1:-}
if [[ -z "$version" ]]; then
  echo "usage: $0 <version-tag>" >&2
  exit 2
fi

file=${CHANGELOG_FILE:-CHANGELOG.md}
if [[ ! -f "$file" ]]; then
  echo "release-notes: changelog not found: $file" >&2
  exit 1
fi

notes=$(
  awk -v hdr="## ${version}" '
    $0 == hdr { insec = 1; next }
    insec && /^## / { insec = 0 }
    insec { line[n++] = $0 }
    END {
      start = 0
      while (start < n && line[start] ~ /^[[:space:]]*$/) start++
      end = n
      while (end > start && line[end - 1] ~ /^[[:space:]]*$/) end--
      for (i = start; i < end; i++) print line[i]
    }
  ' "$file"
)

if [[ -z "$notes" ]]; then
  echo "release-notes: no CHANGELOG section with a body for '$version' in $file" >&2
  exit 1
fi

printf '%s\n' "$notes"
