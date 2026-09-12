#!/usr/bin/env bash
set -euo pipefail

doc=${1:-docs/THREAT_MODEL.md}
[[ -f "$doc" ]] || { echo "missing threat model: $doc" >&2; exit 1; }

mapfile -t tests < <(
  LC_ALL=C grep -oE '`Test[A-Za-z0-9_]+' "$doc" |
    sed 's/^`//' |
    sort -u
)

found=${#tests[@]}
if [[ "$found" -eq 0 ]]; then
  echo "no backtick-quoted Test... citations found in $doc" >&2
  exit 1
fi

missing=0
for test_name in "${tests[@]}"; do
  if ! grep -R --include='*_test.go' -Eq \
    "func[[:space:]]+${test_name}[[:space:]]*\\(" .; then
    echo "missing cited test: ${test_name}" >&2
    missing=1
  fi
done

[[ "$missing" -eq 0 ]] || exit 1
echo "OK: all ${found} tests cited in ${doc} exist."
