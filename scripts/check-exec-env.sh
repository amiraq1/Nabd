#!/usr/bin/env bash
# check-exec-env.sh — static audit: every os/exec command construction in
# non-test Go files must assign cmd.Env explicitly before Start/Run.
#
# Why this exists (Phase 3, item B): in Go, exec.Command returns an
# *exec.Cmd whose Env field defaults to nil, and a nil Env makes the child
# inherit the FULL parent environment automatically. The bash tool's child
# allowlist only protects the child if Env is set explicitly — a single
# unset call site defeats the entire design. This check fails the build (and
# CI) if any call site on any non-test code path forgets the assignment.
#
# Usage: scripts/check-exec-env.sh   (run from the repository root)
# Exit 0: every exec.Command/CommandContext site assigns Env before Start.
# Exit 1: at least one site is missing an explicit Env assignment.

set -u

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$repo_root" || exit 1

found=0
while IFS= read -r f; do
  awk -v file="$f" '
    /^func / { in_func = 1; e_cmd = -1; e_env = -1; e_start = -1 }
    in_func {
      if ($0 ~ /exec\.Command\(|exec\.CommandContext\(/) e_cmd = NR
      if ($0 ~ /\.Env[[:space:]]*=/ || $0 ~ /Env:[[:space:]]*\[?\]?string/) e_env = NR
      if ($0 ~ /\.Start\(|\.Run\(|\.Output\(|\.CombinedOutput\(/) { if (e_start < 0) e_start = NR }
      if (/^}/) {
        if (e_cmd >= 0) {
          if (e_env < 0) {
            print file ":" e_cmd ": exec.Command call site without explicit Env assignment"
            found_bad = 1
          } else if (e_start >= 0 && e_env > e_start) {
            print file ":" e_cmd ": Env assigned AFTER Start/Run (child inherited full parent env first)"
            found_bad = 1
          }
        }
        in_func = 0
      }
    }
    END { if (found_bad) exit 1; exit 0 }
  ' "$f" || found=1
done < <(find . -type f -name '*.go' ! -name '*_test.go' ! -path './.git/*')

if [ "$found" -ne 0 ]; then
  echo "FAIL: unset-Env exec.Command call site(s) found (see above)."
  echo "Set cmd.Env explicitly — even to []string{} — before Start on every child process."
  exit 1
fi

echo "OK: every exec.Command construction in non-test Go files assigns Env explicitly."