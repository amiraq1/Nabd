#!/usr/bin/env bash
# S3: measure which env allowlist lets common MCP servers (Node, Python) start.
# Runs each mock server under three candidate allowlists via env -i.
# Output: a table for docs/reports/mcp_phase_s_report.md
set -u

DIR="$(cd "$(dirname "$0")" && pwd)"
MOCK="$DIR/mock"
TMPD="$(mktemp -d)"
trap 'rm -rf "$TMPD"' EXIT

# Candidate allowlists
declare -A LISTS
LISTS[A]="PATH HOME TMPDIR LANG"
LISTS[B]="PATH HOME TMPDIR LANG NODE_PATH PYTHONPATH PYTHONHOME"
LISTS[C]="PATH HOME TMPDIR LANG NODE_PATH PYTHONPATH PYTHONHOME"

run_one() {
  local lang="$1" list="$2" home="$3"
  local bin args
  if [ "$lang" = node ]; then bin="node"; args="$MOCK/node-server.js"
  else bin="python3"; args="$MOCK/python_server.py"; fi
  command -v "$bin" >/dev/null 2>&1 || { echo "SKIP(no $bin)"; return; }

  local envargs=()
  for v in $list; do
    case "$v" in
      HOME) envargs+=("HOME=$home") ;;
      TMPDIR) envargs+=("TMPDIR=$TMPD") ;;
      PATH) envargs+=("PATH=/usr/bin:/bin") ;;
      LANG) envargs+=("LANG=C.UTF-8") ;;
      *) envargs+=("$v=${!v:-}") ;;
    esac
  done

  local start end
  start=$(date +%s%N)
  # Send initialize, expect a JSON-RPC response within 5s.
  local out
  out=$(printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
    | timeout 5 env -i "${envargs[@]}" "$bin" $args 2>/dev/null | head -1)
  end=$(date +%s%N)
  local ms=$(( (end - start) / 1000000 ))

  if echo "$out" | grep -q '"protocolVersion"'; then
    echo "OK(${ms}ms)"
  else
    echo "FAIL(${ms}ms)"
  fi
}

echo "| server | list A | list B | list C |"
echo "|---|---|---|---|"
for lang in node python; do
  ra=$(run_one "$lang" "${LISTS[A]}" "$TMPD")
  rb=$(run_one "$lang" "${LISTS[B]}" "$TMPD")
  rc=$(run_one "$lang" "${LISTS[C]}" "$HOME")
  echo "| $lang | $ra | $rb | $rc |"
done
echo
echo "TMPDIR=$TMPD HOME=$HOME"
