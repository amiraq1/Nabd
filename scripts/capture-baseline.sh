#!/usr/bin/env bash
set -euo pipefail

# capture-baseline.sh — reproducible Phase 1 baseline capture for Nabd.
#
# Modes:
#   local          No network, no installs. Host-environment measurements.
#                  Unsupported-host builds are cross-compiled for the
#                  supported platform (linux/amd64) so the baseline always
#                  records a real supported-platform build.
#   ci             Designed to run on a GitHub Actions ubuntu-latest runner.
#                  Executable in principle WITHOUT any workflow modification:
#                  it is a standalone script, never a gate, and Phase 1 does
#                  not touch .github/workflows/**.
#   full-security  local checks plus every security tool that is ALREADY
#                  installed. Nothing is installed; tools that are absent or
#                  that need network are recorded UNAVAILABLE with the
#                  dependency stated.
#
# Exit states, one per check (see docs/BASELINE.md for the definitions):
#   PASS        check executed and passed
#   FAIL        required check executed and failed
#   UNAVAILABLE check could not run: prerequisite or environment missing
#   SKIPPED     intentionally not executed for a documented scope reason
#
# Exit code: 0 when the report was produced and every REQUIRED check for the
# selected mode passed. 1 when a required check failed or a repository
# invariant broke (new working-tree artifacts, unexpected dirty-state change).
# Optional-tool UNAVAILABLE never fails the script by itself; it is recorded
# in the report with evidence.

usage() { echo "usage: $0 {local|ci|full-security}" >&2; exit 2; }

mode="${1:-}"
case "$mode" in
  local|ci|full-security) ;;
  *) usage ;;
esac

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

# local and full-security modes must not touch the network: pin the
# module proxy off so no go command can fetch a dependency. ci mode
# keeps network access because a fresh CI runner downloads the module
# graph on its first build.
case "$mode" in
  local|full-security)
    export GOPROXY=off
    export GOFLAGS=-mod=readonly
    ;;
esac

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

report="$tmp/report.txt"
: >"$report"

# Requirement levels recorded per check:
#   required  failure fails the mode (exit 1)
#   attempted executed when possible; result recorded, never fails the mode
#   optional  tool-dependent; UNAVAILABLE is an acceptable outcome
required_fail=0

now_utc()    { date -u +%Y-%m-%dT%H:%M:%SZ; }
now_epoch_ns() { date +%s%N; }

rec() {
  # rec <name> <status> <requirement> <command/source> <detail>
  local name="$1" status="$2" req="$3" source="$4" detail="$5"
  printf '[check]\nname=%s\nstatus=%s\nrequirement=%s\nsource=%s\ndetail=%s\n\n' \
    "$name" "$status" "$req" "$source" "$detail" >>"$report"
  if [[ "$status" == "FAIL" && "$req" == "required" ]]; then
    required_fail=1
  fi
}

step() { echo "== $* ==" >&2; }

# measure <command...>  — runs a command, records rc/elapsed, keeps output in
# $MEASURE_OUT / $MEASURE_ERR for the caller to classify.
measure() {
  local start end
  start="$(now_epoch_ns)"
  set +e
  "$@" >"$tmp/last.out" 2>"$tmp/last.err"
  MEASURE_RC=$?
  set -e
  end="$(now_epoch_ns)"
  MEASURE_ELAPSED_MS=$(( (end - start) / 1000000 ))
  MEASURE_OUT="$tmp/last.out"
  MEASURE_ERR="$tmp/last.err"
}

count_lines() { awk 'END{print NR}' "$1" 2>/dev/null || echo 0; }
file_size()   { stat -c %s "$1" 2>/dev/null || echo UNKNOWN; }

# ---------------------------------------------------------------------------
# 1. Environment
# ---------------------------------------------------------------------------

head_sha="$(git rev-parse HEAD)"
branch="$(git branch --show-current)"
dirty_before="$(git status --porcelain | sort)"
shallow=no
if [[ -f .git/shallow ]]; then shallow=yes; fi
commit_count="$(git rev-list --count HEAD)"
go_version="$(go version)"
host_goos="$(go env GOOS)"
host_goarch="$(go env GOARCH)"
host_cgo="$(go env CGO_ENABLED)"
gomaxprocs="$(nproc 2>/dev/null || echo UNKNOWN)"
os_name="$(uname -s 2>/dev/null || echo UNKNOWN)"
os_release="$(uname -r 2>/dev/null || echo UNKNOWN)"

mem_total=""
if [[ -r /proc/meminfo ]]; then
  mem_total="$(awk '/^MemTotal:/ {printf "%.2f GB", $2/1024/1024}' /proc/meminfo)"
elif command -v free >/dev/null 2>&1; then
  mem_total="$(free -b | awk '/^Mem:/ {printf "%.2f GB", $2/1024/1024}')"
fi

printf '[env]\nmode=%s\ncaptured_utc=%s\nhead_sha=%s\nbranch=%s\nshallow_clone=%s\ncommit_count=%s\n' \
  "$mode" "$(now_utc)" "$head_sha" "$branch" "$shallow" "$commit_count" >>"$report"
printf 'go_version=%s\nhost_goos=%s\nhost_goarch=%s\nhost_cgo_enabled=%s\n' \
  "$go_version" "$host_goos" "$host_goarch" "$host_cgo" >>"$report"
printf 'os=%s\nos_release=%s\ncpu_count=%s\nmemory=%s\nworking_tree_dirty_entries=%s\n\n' \
  "$os_name" "$os_release" "$gomaxprocs" "${mem_total:-UNAVAILABLE}" \
  "$(printf '%s' "$dirty_before" | grep -c . || true)" >>"$report"

# Supported build platforms: linux and android. internal/config and internal/snap
# implement their platform primitives behind //go:build !windows and
# //go:build linux || android with no windows counterpart, so a windows host
# cannot build the project. That is a pre-existing platform constraint, not a
# regression; the baseline therefore cross-compiles the supported platform.
host_supported=no
case "$host_goos" in linux|android) host_supported=yes ;; esac
target_goos=linux
target_goarch=amd64

# ---------------------------------------------------------------------------
# 2. Project-size metrics
# ---------------------------------------------------------------------------
step "project-size metrics"
# Definitions:
#   production : git-tracked *.go excluding *_test.go, main module only
#   test       : git-tracked *_test.go, main module only
#   third_party: git-tracked *.go under third_party/ (separate nested module)
#   generated  : none present (no go:generate output committed)
#   vendor     : none present (no vendor/ directory)

mapfile -t prod_files < <(git ls-files '*.go' | grep -v '^third_party/' | grep -v '_test\.go$')
mapfile -t test_files < <(git ls-files '*.go' | grep -v '^third_party/' | grep '_test\.go$')
mapfile -t third_party_files < <(git ls-files '*.go' | grep '^third_party/')

# One awk per category: a per-file process spawn is needlessly slow
# and the aggregate is the number the baseline records.
prod_loc=0
if [[ ${#prod_files[@]} -gt 0 ]]; then prod_loc="$(cat "${prod_files[@]}" | awk 'END{print NR}')"; fi
test_loc=0
if [[ ${#test_files[@]} -gt 0 ]]; then test_loc="$(cat "${test_files[@]}" | awk 'END{print NR}')"; fi
tp_loc=0
if [[ ${#third_party_files[@]} -gt 0 ]]; then tp_loc="$(cat "${third_party_files[@]}" | awk 'END{print NR}')"; fi

if [[ ${#prod_files[@]} -gt 0 ]]; then
  ratio="$(awk -v t="${#test_files[@]}" -v p="${#prod_files[@]}" 'BEGIN{printf "%.3f", t/p}')"
else
  ratio="N/A"
fi

mapfile -t packages < <(printf '%s\n' "${prod_files[@]}" | xargs -n1 dirname | sort -u)

mapfile -t corpus_files < <(find internal/rtl/testdata/unicode -type f 2>/dev/null)
corpus_bytes=0; for f in "${corpus_files[@]}"; do corpus_bytes=$((corpus_bytes + $(file_size "$f"))); done

mapfile -t fixture_files < <(find . -path ./third_party -prune -o -path ./.git -prune -o -type d -name testdata -print 2>/dev/null | while read -r d; do find "$d" -type f; done)
fixture_bytes=0; for f in "${fixture_files[@]}"; do fixture_bytes=$((fixture_bytes + $(file_size "$f"))); done

rec project_size PASS required "git ls-files '*.go' + awk/wc" \
  "production_go_files=${#prod_files[@]} test_go_files=${#test_files[@]} production_loc=$prod_loc test_loc=$test_loc test_production_file_ratio=$ratio third_party_go_files=${#third_party_files[@]} third_party_loc=$tp_loc packages=${#packages[@]} unicode_corpus_files=${#corpus_files[@]} unicode_corpus_bytes=$corpus_bytes fixture_files=${#fixture_files[@]} fixture_bytes=$fixture_bytes vendor_dir=absent"

# ---------------------------------------------------------------------------
# 3. Build / test / race measurements
# ---------------------------------------------------------------------------
step "build/test/race (this is the slow section)"

# 3a. Host build (attempted everywhere; on an unsupported host the failure is a
# pre-existing platform constraint and is recorded as evidence, not a gate).
measure go build ./...
if [[ $MEASURE_RC -eq 0 ]]; then
  rec go_build_host PASS attempted "go build ./..." "exit=0 elapsed_ms=$MEASURE_ELAPSED_MS host=$host_goos/$host_goarch"
else
  err="$(head -c 600 "$MEASURE_ERR" | tr '\n' ' ')"
  rec go_build_host FAIL attempted "go build ./..." "exit=$MEASURE_RC elapsed_ms=$MEASURE_ELAPSED_MS host=$host_goos/$host_goarch error=$err"
fi

# 3b. Supported-platform build. Required in every mode: this is the build the
# project actually ships (linux/amd64 on CI; android/arm64 in the termux job).
if [[ "$host_supported" == "yes" && "$host_goarch" == "$target_goarch" ]]; then
  build_desc="go build ./... (native supported host)"
  measure go build ./...
else
  build_desc="env GOOS=$target_goos GOARCH=$target_goarch CGO_ENABLED=0 go build ./... (cross-compile from $host_goos/$host_goarch)"
  measure env GOOS="$target_goos" GOARCH="$target_goarch" CGO_ENABLED=0 go build ./...
fi
if [[ $MEASURE_RC -eq 0 ]]; then
  rec go_build_supported_platform PASS required "$build_desc" "exit=0 elapsed_ms=$MEASURE_ELAPSED_MS target=$target_goos/$target_goarch"
else
  err="$(head -c 600 "$MEASURE_ERR" | tr '\n' ' ')"
  rec go_build_supported_platform FAIL required "$build_desc" "exit=$MEASURE_RC elapsed_ms=$MEASURE_ELAPSED_MS target=$target_goos/$target_goarch error=$err"
fi

# 3c. Unit tests. Executable only on a supported host.
if [[ "$host_supported" == "yes" ]]; then
  measure go test ./... -count=1
  if [[ $MEASURE_RC -eq 0 ]]; then
    rec go_test_unit PASS required "go test ./... -count=1" "exit=0 elapsed_ms=$MEASURE_ELAPSED_MS host=$host_goos/$host_goarch"
  else
    err="$(tail -c 600 "$MEASURE_ERR" | tr '\n' ' ')"
    rec go_test_unit FAIL required "go test ./... -count=1" "exit=$MEASURE_RC elapsed_ms=$MEASURE_ELAPSED_MS error=$err"
  fi
else
  rec go_test_unit UNAVAILABLE required "go test ./... -count=1" \
    "host=$host_goos cannot execute GOOS=$target_goos test binaries; run on a $target_goos host or in ci mode. Test compilation is measured separately below."
fi

# 3d. Test compilation for the supported platform (compile-only proof that the
# whole test suite builds for linux/amd64). Works from any host.
# With multiple packages, `go test -c -o` requires the target to be an
# existing directory; it is created here and lives in the temp tree.
mkdir -p "$tmp/testbins"
measure env GOOS="$target_goos" GOARCH="$target_goarch" CGO_ENABLED=0 go test -c -o "$tmp/testbins" ./...
if [[ $MEASURE_RC -eq 0 ]]; then
  rec go_test_compile_supported PASS required \
    "env GOOS=$target_goos GOARCH=$target_goarch CGO_ENABLED=0 go test -c -o <tmpdir> ./..." \
    "exit=0 elapsed_ms=$MEASURE_ELAPSED_MS"
else
  err="$(head -c 600 "$MEASURE_ERR" | tr '\n' ' ')"
  rec go_test_compile_supported FAIL required \
    "env GOOS=$target_goos GOARCH=$target_goarch CGO_ENABLED=0 go test -c -o <tmpdir> ./..." \
    "exit=$MEASURE_RC elapsed_ms=$MEASURE_ELAPSED_MS error=$err"
fi

# 3e. Race detector availability probe (deterministic, tiny, offline).
race_probe_dir="$tmp/raceprobe"
mkdir -p "$race_probe_dir"
printf 'module raceprobe\n\ngo 1.27\n' >"$race_probe_dir/go.mod"
printf 'package raceprobe\n\nimport "testing"\n\nfunc TestRaceProbe(t *testing.T) {}\n' >"$race_probe_dir/x_test.go"
race_probe_rc=0
( cd "$race_probe_dir" && go test -race -count=1 ./... ) >"$tmp/raceprobe.out" 2>"$tmp/raceprobe.err" || race_probe_rc=$?
race_available=no
if [[ $race_probe_rc -eq 0 ]]; then
  race_available=yes
elif grep -q 'requires cgo' "$tmp/raceprobe.err"; then
  rec race_detector UNAVAILABLE attempted "go test -race (probe)" \
    "host=$host_goos CGO_ENABLED=$host_cgo: -race requires CGO_ENABLED=1 and a C toolchain; probe error: $(head -c 200 "$tmp/raceprobe.err" | tr '\n' ' ')"
else
  rec race_detector UNAVAILABLE attempted "go test -race (probe)" \
    "probe failed unexpectedly (rc=$race_probe_rc): $(head -c 200 "$tmp/raceprobe.err" | tr '\n' ' ')"
fi

if [[ "$race_available" == "yes" ]]; then
  race_required=attempted
  [[ "$mode" != "local" ]] && race_required=required
  measure go test ./... -race -count=1
  if [[ $MEASURE_RC -eq 0 ]]; then
    rec go_test_race PASS "$race_required" "go test ./... -race -count=1" "exit=0 elapsed_ms=$MEASURE_ELAPSED_MS host=$host_goos/$host_goarch"
  else
    err="$(tail -c 600 "$MEASURE_ERR" | tr '\n' ' ')"
    rec go_test_race FAIL "$race_required" "go test ./... -race -count=1" "exit=$MEASURE_RC elapsed_ms=$MEASURE_ELAPSED_MS error=$err"
  fi
fi

# 3f. RTL / Unicode conformance gate (runs natively on any host: the harness is
# a pure-Go program with no platform-specific dependencies).
measure go run ./cmd/rtlconformance --unicode internal/rtl/testdata/unicode/17.0.0
if [[ $MEASURE_RC -eq 0 ]]; then
  conf_detail="$(tail -c 300 "$MEASURE_OUT" | tr '\n' ' ')"
  rec rtl_unicode_conformance PASS required "go run ./cmd/rtlconformance --unicode internal/rtl/testdata/unicode/17.0.0" \
    "exit=0 elapsed_ms=$MEASURE_ELAPSED_MS result=$conf_detail"
else
  err="$(tail -c 600 "$MEASURE_ERR" | tr '\n' ' ')"
  rec rtl_unicode_conformance FAIL required "go run ./cmd/rtlconformance --unicode internal/rtl/testdata/unicode/17.0.0" \
    "exit=$MEASURE_RC elapsed_ms=$MEASURE_ELAPSED_MS error=$err"
fi

# 3g. Nested module (third_party/bubbles) tests. Requires its own module graph;
# on a windows host the windows-platform dependency variants are not in the
# local module cache, so offline execution is impossible — recorded, not
# downloaded (local/full-security modes never touch the network).
if [[ "$host_supported" == "yes" ]]; then
  bub_rc=0
  ( cd third_party/bubbles && go test ./... -count=1 ) >"$tmp/bubbles.out" 2>"$tmp/bubbles.err" || bub_rc=$?
  if [[ $bub_rc -eq 0 ]]; then
    rec nested_module_tests PASS required "third_party/bubbles: go test ./... -count=1" "exit=0"
  else
    rec nested_module_tests FAIL required "third_party/bubbles: go test ./... -count=1" \
      "exit=$bub_rc error=$(tail -c 400 "$tmp/bubbles.err" | tr '\n' ' ')"
  fi
else
  rec nested_module_tests UNAVAILABLE attempted "third_party/bubbles: go test ./... -count=1" \
    "host=$host_goos: nested-module dependencies for this platform are not in the local module cache and local mode must not download them; runs in ci mode on ubuntu-latest"
fi

# ---------------------------------------------------------------------------
# 4. Benchmark inventory (discovery only — no new benchmarks are created)
# ---------------------------------------------------------------------------
step "benchmark inventory"

bench_list="$tmp/benchmarks.txt"
: >"$bench_list"
# grep exits 1 on a test file that declares no benchmark; the
# || true inside the process substitution keeps pipefail from turning
# that ordinary empty result into a script-killing failure.
while IFS= read -r f; do
  pkg="$(dirname "$f" | sed 's|^\./||')"
  while IFS= read -r b; do
    printf '%s :: %s\n' "$pkg" "$b" >>"$bench_list"
  done < <(grep -E '^func Benchmark[A-Za-z0-9_]*' "$f" 2>/dev/null | sed -E 's/^func (Benchmark[A-Za-z0-9_]*).*/\1/' || true)
done < <(git ls-files '*_test.go' | grep -v '^third_party/')
bench_count="$(grep -c . "$bench_list" || true)"

ci_bench_cmd='go test ./internal/ui/ -run "^$" -bench "^(BenchmarkRefreshStreaming|BenchmarkRefreshLiveStreaming|BenchmarkFormatInlineLong|BenchmarkFormatMarkdown|BenchmarkViewFullScreen)$" -benchmem -count=1'
ci_bench_artifact='actions/upload-artifact name=bench-ui-${{ github.sha }} (file bench-ui.txt, non-gating, continue-on-error)'
rec benchmark_inventory PASS required "git ls-files + grep '^func Benchmark'" \
  "discovered_benchmarks=$bench_count gomaxprocs=$gomaxprocs ci_command=$ci_bench_cmd benchtime=default ci_artifact=$ci_bench_artifact note=CI also runs a non-gating layout-benchmark baseline (benchtime=10x count=5) and third_party/bubbles has its own tests; see docs/BASELINE.md for the full per-package inventory"
cat "$bench_list" >>"$report"
printf '\n' >>"$report"

# ---------------------------------------------------------------------------
# 5. Security checks
# ---------------------------------------------------------------------------
step "security checks"

# 5a. go vet for the supported platform (required).
if [[ "$host_supported" == "yes" ]]; then
  measure go vet ./...
  vet_desc="go vet ./..."
else
  measure env GOOS="$target_goos" GOARCH="$target_goarch" CGO_ENABLED=0 go vet ./...
  vet_desc="env GOOS=$target_goos GOARCH=$target_goarch CGO_ENABLED=0 go vet ./..."
fi
if [[ $MEASURE_RC -eq 0 ]]; then
  rec go_vet PASS required "$vet_desc" "exit=0 elapsed_ms=$MEASURE_ELAPSED_MS"
else
  err="$(head -c 600 "$MEASURE_ERR" | tr '\n' ' ')"
  rec go_vet FAIL required "$vet_desc" "exit=$MEASURE_RC elapsed_ms=$MEASURE_ELAPSED_MS error=$err"
fi

# 5b. gofmt (main module). third_party is reported separately because its
# working-tree line endings are host-dependent (see docs/BASELINE.md).
# On windows, gofmt -l emits backslash separators; normalizing to '/'
# before filtering keeps the third_party exclusion working on every host.
gofmt_main="$(gofmt -l . 2>/dev/null | tr '\\' '/' | grep -v '^third_party/' || true)"
if [[ -z "$gofmt_main" ]]; then
  rec gofmt_main_module PASS required "gofmt -l . (excluding third_party/)" "clean"
else
  rec gofmt_main_module FAIL required "gofmt -l . (excluding third_party/)" "unformatted: $gofmt_main"
fi
gofmt_tp="$(gofmt -l . 2>/dev/null | tr '\\' '/' | grep '^third_party/' || true)"
if [[ -n "$gofmt_tp" ]]; then
  idx_lf_w_crlf="$(git ls-files --eol $gofmt_tp 2>/dev/null | grep -c 'i/lf.*w/crlf' || true)"
  rec gofmt_third_party UNAVAILABLE attempted "gofmt -l third_party/" \
    "$gofmt_tp flagged on this host; git index is LF ($idx_lf_w_crlf of $(printf '%s' "$gofmt_tp" | grep -c .) show i/lf w/crlf working-tree artifact); CI runs gofmt on linux where these files are clean"
fi

# 5c. Repository security scripts (required in every mode; they are static
# audits and run offline).
run_security_script() {
  # run_security_script <name> <path> [args...]
  local name="$1" path="$2"; shift 2
  measure bash "$path" "$@"
  if [[ $MEASURE_RC -eq 0 ]]; then
    rec "$name" PASS required "bash $path $*" "exit=0 elapsed_ms=$MEASURE_ELAPSED_MS"
  else
    err="$(tail -c 600 "$MEASURE_ERR" | tr '\n' ' ')"
    rec "$name" FAIL required "bash $path $*" "exit=$MEASURE_RC elapsed_ms=$MEASURE_ELAPSED_MS error=$err"
  fi
}

run_security_script check_exec_env scripts/check-exec-env.sh
run_security_script check_security_invariants scripts/check-security-invariants.sh
run_security_script check_threat_model_tests scripts/check-threat-model-tests.sh
run_security_script test_threat_model_freshness scripts/test-threat-model-freshness.sh

# Sub-check honesty for check-security-invariants.sh: its exec-coverage scan
# depends on ripgrep. Without rg the scan is empty and passes vacuously —
# recorded explicitly so a vacuous pass is never mistaken for coverage.
if ! command -v rg >/dev/null 2>&1; then
  rec check_security_invariants_rg_subcheck UNAVAILABLE attempted "rg -l 'exec.Command|...' internal cmd" \
    "ripgrep (rg) not installed on this host; the exec-coverage sub-check of scripts/check-security-invariants.sh scanned zero files and passed vacuously. Install rg or run on CI for a non-vacuous result."
fi

# 5d. Threat-model freshness gate: requires a base SHA. In ci mode the workflow
# would supply the PR base SHA; here it is optional and defaults to HEAD~1 when
# the shallow clone provides it, which proves the gate executes.
if [[ "$mode" != "local" && -n "${BASE_SHA:-}" ]]; then
  run_security_script check_threat_model_freshness scripts/check-threat-model-freshness.sh "$BASE_SHA"
else
  if git rev-parse --verify HEAD~1 >/dev/null 2>&1; then
    run_security_script check_threat_model_freshness scripts/check-threat-model-freshness.sh HEAD~1
  else
    rec check_threat_model_freshness SKIPPED attempted "bash scripts/check-threat-model-freshness.sh <base-sha>" \
      "no base SHA available (single-commit history); the gate needs a PR base SHA and runs in ci mode on pull_request events"
  fi
fi

# 5e. Optional security tooling: never installed, never downloaded.
check_optional_tool() {
  # check_optional_tool <name> <command...>
  local name="$1"; shift
  local cmd_str="$*"
  if ! command -v "$1" >/dev/null 2>&1; then
    rec "$name" UNAVAILABLE optional "$cmd_str" "tool not installed on this host; ci mode installs it pinned (see .github/workflows/ci.yml)"
    return
  fi
  measure "$@"
  if [[ $MEASURE_RC -eq 0 ]]; then
    rec "$name" PASS optional "$cmd_str" "exit=0 elapsed_ms=$MEASURE_ELAPSED_MS"
  else
    err="$(tail -c 400 "$MEASURE_ERR" | tr '\n' ' ')"
    rec "$name" FAIL optional "$cmd_str" "exit=$MEASURE_RC elapsed_ms=$MEASURE_ELAPSED_MS error=$err"
  fi
}

check_optional_tool staticcheck staticcheck ./...
check_optional_tool govulncheck govulncheck ./...

if [[ "$mode" == "full-security" ]]; then
  check_optional_tool syft_sonatype_sweep syft sbom .
  check_optional_tool cosign_experiment cosign version
fi

# ---------------------------------------------------------------------------
# 6. Binary artifact (built into the temp directory; never left in the repo)
# ---------------------------------------------------------------------------
step "binary artifact"

bin_name="nabd_${target_goos}_${target_goarch}"
measure env GOOS="$target_goos" GOARCH="$target_goarch" CGO_ENABLED=0 go build -o "$tmp/$bin_name" ./cmd/ag
if [[ $MEASURE_RC -eq 0 ]]; then
  bin_size="$(file_size "$tmp/$bin_name")"
  bin_sha="$(sha256sum "$tmp/$bin_name" | awk '{print $1}')"
  rec binary_artifact PASS attempted "env GOOS=$target_goos GOARCH=$target_goarch CGO_ENABLED=0 go build -o <tmp>/$bin_name ./cmd/ag" \
    "path=<tempdir>/$bin_name size_bytes=$bin_size sha256=$bin_sha goos=$target_goos goarch=$target_goarch cgo_enabled=0"
else
  rec binary_artifact UNAVAILABLE attempted "go build -o <tmp>/$bin_name ./cmd/ag" "exit=$MEASURE_RC"
fi

# SBOM: exists only in the release workflow tooling (syft in ci.yml
# release-dryrun); Phase 1 does not add SBOM generation, so it is recorded as
# a reference, not produced here.
rec sbom SKIPPED attempted "syft sbom (ci.yml release-dryrun job)" \
  "SBOM generation exists in the release-dryrun CI job via anchore/sbom-action/download-syft; not reproduced here to avoid adding tooling outside the Hard Scope Gate"

# ---------------------------------------------------------------------------
# 7. CI mode: runner metadata (no workflow modification implied)
# ---------------------------------------------------------------------------

if [[ "$mode" == "ci" ]]; then
  printf '[ci_runner]\n' >>"$report"
  printf 'runner_os=%s\n' "${RUNNER_OS:-UNKNOWN}" >>"$report"
  printf 'github_run_id=%s\n' "${GITHUB_RUN_ID:-UNKNOWN}" >>"$report"
  printf 'github_sha=%s\n' "${GITHUB_SHA:-UNKNOWN}" >>"$report"
  printf 'github_event_name=%s\n' "${GITHUB_EVENT_NAME:-UNKNOWN}" >>"$report"
  printf 'github_ref=%s\n' "${GITHUB_REF:-UNKNOWN}" >>"$report"
  printf 'note=this script is standalone: it is not wired into any workflow in Phase 1 and acts as no gate\n\n' >>"$report"
fi

# ---------------------------------------------------------------------------
# 8. Repository invariant: no artifacts left behind
# ---------------------------------------------------------------------------
step "repository invariant checks"

dirty_after="$(git status --porcelain | sort)"
new_artifacts="$(comm -13 <(printf '%s\n' "$dirty_before") <(printf '%s\n' "$dirty_after") | grep -v '^$' || true)"
if [[ -z "$new_artifacts" ]]; then
  rec working_tree_artifacts PASS required "git status --porcelain before/after diff" "none"
else
  rec working_tree_artifacts FAIL required "git status --porcelain before/after diff" "new entries: $new_artifacts"
fi
measure git diff --check
if [[ $MEASURE_RC -eq 0 ]]; then
  rec git_diff_check PASS required "git diff --check" "exit=0"
else
  rec git_diff_check FAIL required "git diff --check" "exit=$MEASURE_RC (whitespace errors)"
fi

# ---------------------------------------------------------------------------
# 9. Report
# ---------------------------------------------------------------------------

pass_c="$(grep -c '^status=PASS$' "$report" || true)"
fail_c="$(grep -c '^status=FAIL$' "$report" || true)"
unavail_c="$(grep -c '^status=UNAVAILABLE$' "$report" || true)"
skip_c="$(grep -c '^status=SKIPPED$' "$report" || true)"

cat <<EOF
================================================================================
nabd baseline capture — mode=$mode — captured $(now_utc)
================================================================================
PASS=$pass_c FAIL=$fail_c UNAVAILABLE=$unavail_c SKIPPED=$skip_c
required_failure=$required_fail
--------------------------------------------------------------------------------
EOF
cat "$report"

if [[ "$required_fail" -ne 0 ]]; then
  echo "RESULT: FAIL (one or more required checks failed)" >&2
  exit 1
fi
echo "RESULT: OK (report produced; no required check failed)" >&2
exit 0
