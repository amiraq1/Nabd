#!/usr/bin/env bash
# S3: measure which env allowlist lets common MCP servers (Node, Python) start.
# Runs each mock server under three candidate allowlists via env -i.
# Output: a table for docs/reports/mcp_phase_s_report.md
#
# D3/D4 fixes (2026-10-10):
# - Execute by ABSOLUTE path (command -v): bare-name exec via env -i breaks
#   Python's self-lookup on Android (sys.executable="" -> stdout swallowed).
# - A/B/C are genuinely different allowlists now.
# - Empty values are never passed (empty PYTHONHOME is a live bomb).
set -u

DIR="$(cd "$(dirname "$0")" && pwd)"
MOCK="$DIR/mock"
TMPD="$(mktemp -d)"
trap 'rm -rf "$TMPD"' EXIT

# Candidate allowlists — genuinely different.
# A: minimal (PATH with $PREFIX/bin, HOME, TMPDIR, LANG)
# B: A + language-specific (NODE_PATH / PYTHONPATH / PYTHONHOME)
# C: B + real HOME (tests whether temp HOME breaks common servers)
PREFIX_BIN="${PREFIX:-/data/data/com.termux/files/usr}/bin"

run_one() {
  local lang="$1" list="$2" home="$3"
  local bin args
  if [ "$lang" = node ]; then bin="node"; args="$MOCK/node-server.js"
  else bin="python3"; args="$MOCK/python_server.py"; fi

  # D3: absolute path — never rely on bare-name lookup under env -i.
  local binpath
  binpath="$(command -v "$bin" 2>/dev/null)" || { echo "SKIP(no $bin)"; return; }

  local envargs=()
  for v in $list; do
    local val=""
    case "$v" in
      HOME) val="$home" ;;
      TMPDIR) val="$TMPD" ;;
      PATH) val="$PREFIX_BIN:/system/bin" ;;
      LANG) val="C.UTF-8" ;;
      NODE_PATH) val="${NODE_PATH:-}" ;;
      PYTHONPATH) val="${PYTHONPATH:-}" ;;
      PYTHONHOME) val="${PYTHONHOME:-}" ;;
      TERM) val="${TERM:-xterm-256color}" ;;
    esac
    # D4: never pass empty values.
    [ -n "$val" ] || continue
    envargs+=("$v=$val")
  done

  local start end
  start=$(date +%s%N)
  local out
  out=$(printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
    | timeout 5 env -i "${envargs[@]}" "$binpath" $args 2>/dev/null | head -1)
  end=$(date +%s%N)
  local ms=$(( (end - start) / 1000000 ))

  if echo "$out" | grep -q '"protocolVersion"'; then
    echo "OK(${ms}ms)"
  else
    echo "FAIL(${ms}ms)"
  fi
}

echo "| server | list A (minimal) | list B (+lang) | list C (+real HOME) |"
echo "|---|---|---|---|"
for lang in node python; do
  ra=$(run_one "$lang" "PATH HOME TMPDIR LANG TERM" "$TMPD")
  rb=$(run_one "$lang" "PATH HOME TMPDIR LANG TERM NODE_PATH PYTHONPATH PYTHONHOME" "$TMPD")
  rc=$(run_one "$lang" "PATH HOME TMPDIR LANG TERM NODE_PATH PYTHONPATH PYTHONHOME" "$HOME")
  echo "| $lang | $ra | $rb | $rc |"
done
echo
echo "TMPDIR=$TMPD HOME=$HOME PREFIX_BIN=$PREFIX_BIN"
echo
echo "Note: ADR §5.3 already requires an absolute server path; this harness"
echo "enforces the same (S3 pass criterion: absolute + minimal PATH)."

# --- S3 negative test: each variable absence must fail and be named ---
# Usage: bash scripts/mcp/s3-env-allowlist.sh --negative
if [ "${1:-}" = "--negative" ]; then
  echo
  echo "### S3 negative: removing each variable in turn"
  echo "| removed | node | python |"
  echo "|---|---|---|"
  for v in PATH HOME TMPDIR LANG TERM; do
    rn="FAIL"; rp="FAIL"
    for lang in node python; do
      bin="node"; args="$MOCK/node-server.js"
      [ "$lang" = python ] && { bin="python3"; args="$MOCK/python_server.py"; }
      binpath="$(command -v "$bin" 2>/dev/null)" || continue
      envargs=()
      for w in PATH HOME TMPDIR LANG TERM; do
        [ "$w" = "$v" ] && continue  # remove this one
        case "$w" in
          HOME) envargs+=("HOME=$TMPD") ;;
          TMPDIR) envargs+=("TMPDIR=$TMPD") ;;
          PATH) envargs+=("PATH=$PREFIX_BIN:/system/bin") ;;
          LANG) envargs+=("LANG=C.UTF-8") ;;
          TERM) envargs+=("TERM=xterm-256color") ;;
        esac
      done
      out=$(printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
        | timeout 5 env -i "${envargs[@]}" "$binpath" $args 2>/dev/null | head -1)
      if echo "$out" | grep -q protocolVersion; then r="OK"; else r="FAIL"; fi
      [ "$lang" = node ] && rn="$r ($v removed)" || rp="$r ($v removed)"
    done
    echo "| $v | $rn | $rp |"
  done
  exit 0
fi
