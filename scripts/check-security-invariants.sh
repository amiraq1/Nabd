#!/usr/bin/env bash
set -euo pipefail

# check-security-invariants.sh — verify security invariants across the codebase.
# Fails if any security invariant is violated in production code.

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$repo_root"

failures=0

check_guarded_provider_client() {
  local matches
  matches=$(grep -rn 'Client:[[:space:]]*&http\.Client{}' internal/provider/*.go 2>/dev/null | grep -v '_test\.go' || true)
  if [[ -n "$matches" ]]; then
    echo "provider constructed with an unguarded http.Client — use endpoint.Client(0):" >&2
    echo "$matches" >&2
    return 1
  fi
  return 0
}

check_freshness_covers_process_execution() {
  local exec_files=() missing=()
  mapfile -t exec_files < <(rg -l 'exec\.Command|os\.StartProcess|syscall\.(Exec|ForkExec)|pty\.Start' \
    internal cmd --glob '!*_test.go' --glob '!third_party/**' 2>/dev/null || true)

  local sec_patterns=() line
  while IFS= read -r line; do
    line="${line#"${line%%[![:space:]]*}"}"
    line="${line%"${line##*[![:space:]]}"}"
    [[ -n "$line" && "$line" != "#"* && "$line" != "security_files=("* && "$line" != ")" ]] || continue
    sec_patterns+=("$line")
  done < <(sed -n '/^security_files=(/,/^)/p' scripts/check-threat-model-freshness.sh)

  for file in "${exec_files[@]}"; do
    [[ -n "$file" ]] || continue
    local covered=0
    for pat in "${sec_patterns[@]}"; do
      if [[ "$pat" == */ ]]; then
        if [[ "$file" == "$pat"* ]]; then
          covered=1
          break
        fi
      elif [[ "$file" == "$pat" ]]; then
        covered=1
        break
      fi
    done
    if [[ "$covered" -eq 0 ]]; then
      missing+=("$file")
    fi
  done

  if (( ${#missing[@]} > 0 )); then
    echo "non-test file(s) execute processes but are not covered by security_files in scripts/check-threat-model-freshness.sh:" >&2
    printf '  %s\n' "${missing[@]}" >&2
    return 1
  fi
  return 0
}

# Run all invariant checks
if ! check_guarded_provider_client; then
  failures=$((failures + 1))
fi
if ! check_freshness_covers_process_execution; then
  failures=$((failures + 1))
fi

if [[ "$failures" -gt 0 ]]; then
  echo "FAIL: $failures security invariant check(s) failed." >&2
  exit 1
fi

echo "OK: all security invariants passed."
exit 0
