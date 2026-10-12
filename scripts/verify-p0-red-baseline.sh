#!/usr/bin/env bash
# Runs the P0 negative tests for Tasks 1/3/4 of the audit spec on a CLEAN base
# tree at the pre-fix commit, and writes the logs.
#
# Intended for Termux (Android, linux/arm64) or any Linux host. Do NOT run this
# on Windows: internal/safefs falls back to a resolve-then-open compatibility
# path there, so the suite would test a different implementation of the very
# mechanism under test.
#
# It never touches the branch you work on: the base tree is a detached worktree
# of the pre-fix commit. Nothing is committed or pushed.
#
#   usage:  bash scripts/verify-p0-red-baseline.sh
#
# Outputs: $HOME/red-base.log and $HOME/full-base.log

set -u

BASE_SHA=9f89de824e8ed3cd9186f8a37781d570c6ca69d2
BASE_REF=origin/ci/threat-model-gate-covers-display
REPO_URL=https://github.com/amiraq1/Nabd.git
SRC_BRANCH=wip/p0-audit-2026-10-12
WORKDIR="$HOME/nabd-verify"
BASEDIR="$HOME/nabd-base"

RUN_FILTER='TestP01CommitRefusesNilRegistry|TestP03ExcludedFileOverLimitRefused|TestP03ExcludedFileChangedBetweenReadAndPublishRefused|TestP04ExcludedWriteJournalsIntent|TestP04ExcludedWriteRefusedWhenIntentJournalingFails|TestP01SymlinkToExcludedTargetIsRefused'

die() { echo "STOP: $*" >&2; exit 1; }

echo "== 0. toolchain =="
command -v go >/dev/null 2>&1 || die "Go not found. In Termux: pkg install golang git"
command -v git >/dev/null 2>&1 || die "git not found. In Termux: pkg install git"
go version
uname -a

echo
echo "== 1. clone the branch that holds the tests =="
rm -rf "$WORKDIR"
# A plain `git clone` checks out the repository's DEFAULT branch, and `git fetch`
# updates refs only — it never touches the working tree. Cloning with -b is what
# actually puts the test files on disk. Fetching alone leaves them absent, and
# step 3 would then die on the first cp.
git clone -b "$SRC_BRANCH" "$REPO_URL" "$WORKDIR" || die "clone of $SRC_BRANCH failed"
cd "$WORKDIR" || die "cd $WORKDIR failed"
[ "$(git branch --show-current)" = "$SRC_BRANCH" ] \
  || die "clone is on $(git branch --show-current), expected $SRC_BRANCH"
# The pre-fix commit lives on its own branch; make sure that branch is present so
# step 2 can fall back to it if the literal SHA is unusable.
git fetch origin ci/threat-model-gate-covers-display || die "fetch of the base branch failed"
# Cross-check BASE_SHA against the branch that actually holds it. A typo here
# would otherwise surface only in step 2 as a confusing "invalid reference" or,
# worse, silently select the wrong tree.
REMOTE_BASE_SHA=$(git rev-parse "$BASE_REF" 2>/dev/null) || die "cannot resolve $BASE_REF"
[ "$REMOTE_BASE_SHA" = "$BASE_SHA" ] \
  || die "BASE_SHA is $BASE_SHA but $BASE_REF is $REMOTE_BASE_SHA — fix BASE_SHA"
for f in p0_2_test.go p0_3_test.go p0_4_test.go p0_enforcement_test.go p0_undo_red_test.go protected.go; do
  [ -f "internal/tools/$f" ] || die "source file internal/tools/$f is missing from $SRC_BRANCH"
done
[ -d internal/approval ] || die "source directory internal/approval is missing from $SRC_BRANCH"
echo "source verified: $(git branch --show-current) @ $(git rev-parse --short HEAD)"

echo
echo "== 2. base worktree at the pre-fix commit =="
rm -rf "$BASEDIR"
git worktree add --detach "$BASEDIR" "$BASE_SHA" \
  || git worktree add --detach "$BASEDIR" "$BASE_REF" \
  || die "could not create the base worktree"
cd "$BASEDIR" || die "cd $BASEDIR failed"
echo "HEAD   = $(git rev-parse HEAD)"
echo "expect = $BASE_SHA"
[ "$(git rev-parse HEAD)" = "$BASE_SHA" ] || die "HEAD does not match the required SHA"
echo "status (must be empty):"
git status --short
[ -z "$(git status --short)" ] || die "base tree is not clean"

echo
echo "== 3. copy the tests and their dependencies =="
SRC="$WORKDIR"
for f in p0_2_test.go p0_3_test.go p0_4_test.go p0_enforcement_test.go p0_undo_red_test.go protected.go; do
  cp "$SRC/internal/tools/$f" internal/tools/ || die "copy $f failed"
done
# nabd/internal/approval does NOT exist at the pre-fix commit: approvedCtx in
# p0_4_test.go needs it, and it was introduced by the fix. Without this copy the
# package does not compile and there is no valid RED, only a build failure.
cp -r "$SRC/internal/approval" internal/ || die "copy internal/approval failed"
echo "note: internal/approval is production code from the fix, so the base is no"
echo "      longer a pure pre-fix tree. This is a deliberate, documented tradeoff."
sha256sum internal/tools/p0_*_test.go internal/tools/protected.go

echo
echo "== 4. scaffold (behaviour-neutral declarations) =="
cat > internal/tools/scaffold_red_test.go <<'SCAFFOLD_EOF'
// scaffold_red_test.go — symbols that only make the tests compile; no behaviour.
package tools

import "errors"

const excludedGuard = "excluded:none"

var (
	ErrExcludedWrite     = errors.New("tools: excluded path write refused")
	ErrUndoExcluded      = errors.New("tools: excluded write cannot be undone")
	maxExcludedReadBytes = 1 << 20
)
SCAFFOLD_EOF

if ! grep -q 'Err  error' internal/tools/undo.go; then
  LN=$(grep -n '^	OK   bool$' internal/tools/undo.go | head -1 | cut -d: -f1)
  [ -n "$LN" ] || die "could not locate the UndoResult.OK field"
  sed -i "${LN}a\\	Err  error // scaffold: used by the RED tests" internal/tools/undo.go
fi
echo "--- UndoResult now: ---"
sed -n '/type UndoResult struct/,/^}/p' internal/tools/undo.go

echo
echo "== 5. compile =="
go vet ./internal/tools/ 2>&1 | tee "$HOME/vet-base.log"
go test -c -o /dev/null ./internal/tools/ 2>&1 | tee "$HOME/testc-base.log"

echo
echo "== 6. run the six P0 tests =="
go test -run "$RUN_FILTER" -count=1 -v ./internal/tools/ 2>&1 | tee "$HOME/red-base.log"

echo
echo "== 7. full package, to catch side breakage =="
go test ./internal/tools/ -count=1 2>&1 | tee "$HOME/full-base.log"

echo
echo "== done =="
echo "logs: $HOME/red-base.log  $HOME/full-base.log"
echo "For each of the six tests record PASS/FAIL/SKIP, the literal failure message,"
echo "and whether it matches the reason written in the test comment."
