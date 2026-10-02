# Baseline — Phase 1 capture (WINDOWS DIAGNOSTIC — NOT AUTHORITATIVE)

> **STATUS: DRAFT / WINDOWS DIAGNOSTIC CAPTURE ONLY**
> This file records the result of running `scripts/capture-baseline.sh local`
> on a Windows/amd64 host. It is NOT an authoritative Phase 1 reference baseline.
> A reference baseline requires execution on a clean Linux clone at `origin/master`
> with all required tools installed (`rg`, `staticcheck`, `govulncheck`).
> See `docs/PLAN_LOG.md` → NO-GO review entry (2026-10-02T21:43:00Z) for the full
> defect inventory and the blockers required for a GO decision.

Draft baseline for the Nabd repository, captured for issue #189
("Phase 0 — baseline and stabilization tracks"). Produced by
`scripts/capture-baseline.sh` (local mode, Windows host) plus read-only GitHub
API sampling. Nothing in this file is a gate; Phase 1 does not modify CI.

## Reproduction

```sh
./scripts/capture-baseline.sh local          # this host, no network
./scripts/capture-baseline.sh ci             # on a GitHub Actions ubuntu-latest runner
./scripts/capture-baseline.sh full-security  # local + every already-installed security tool
```

`local` and `full-security` pin `GOPROXY=off` and `GOFLAGS=-mod=readonly`
(no network). `ci` is standalone: it is not wired into any workflow in
Phase 1 and acts as no gate.

### Exit states

| State | Meaning |
|---|---|
| PASS | check executed and passed |
| FAIL | required check executed and failed |
| UNAVAILABLE | check could not run: prerequisite or environment missing |
| SKIPPED | intentionally not executed for a documented scope reason |

Exit code: 0 when the report was produced and every required check for the
selected mode passed; 1 when a required check failed or a repository
invariant broke. Optional-tool UNAVAILABLE never fails the script by itself.

## Capture environment

| Fact | Value |
|---|---|
| captured_utc | 2026-10-02T18:32:17Z |
| head_sha | a1d448a3401afacc28e7bc4e11e7df2fabd4e319 |
| branch | master (shallow clone, 2 commits) |
| host | windows/amd64, MINGW64_NT-10.0-26200, Go 1.27.1, CGO_ENABLED=0 |
| cpu / memory | 12 cores / 7.69 GB |

Supported build platforms are `linux` and `android` only (`internal/config`
and `internal/snap` have `//go:build !windows` / `linux || android`
implementations with no windows counterparts), so a windows host cannot
build the project natively. That is a pre-existing platform constraint, not
a regression; the baseline therefore cross-compiles the supported platform.

## Local capture evidence (mode=local)

Summary: `PASS=15 FAIL=1 UNAVAILABLE=7 SKIPPED=1`, `required_failure=0`,
`RESULT: OK`. The single FAIL is `go_build_host` (attempted, not required):
the expected windows platform constraint above.

> **Interpretation note**: `RESULT: OK` means all *required* checks for the
> `local` mode on Windows passed; it does NOT mean "fully green". The correct
> characterisation is: *all checks classified as required in Windows local mode
> passed, but the reference baseline and full-security mode have not been
> demonstrated*. Seven UNAVAILABLE checks — including the unit test suite,
> the race suite, staticcheck, govulncheck, and the `rg` exec-coverage
> sub-check — are required for the authoritative baseline and must be run on
> Linux (see `docs/PLAN_LOG.md` NO-GO entry 2026-10-02T21:43:00Z).

| Check | Status | Requirement | Detail |
|---|---|---|---|
| project_size | PASS | required | 200 production / 352 test Go files; 37,616 / 69,788 LOC; test:production file ratio 1.760; 27 packages; third_party/bubbles 14 files / 6,704 LOC; Unicode corpus 4 files / 1,752,487 B; fixtures 56 files / 1,773,466 B; no vendor dir |
| go_build_host | FAIL | attempted | `go build ./...` exit=1 in 4.3 s: undefined `openConfigFile`, `checkOwnerPlatform`, `renameNoReplace`, `syncDir`, `probeNoReplaceSupport` (pre-existing windows constraint) |
| go_build_supported_platform | PASS | required | `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./...` exit=0 in 3.2 s (14.7 s on the first, colder run) |
| go_test_unit | UNAVAILABLE | required | host is windows: cannot execute GOOS=linux test binaries; compile proof below |
| go_test_compile_supported | PASS | required | `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c -o <tmpdir> ./...` exit=0 in 46.1 s — the whole test suite builds for linux/amd64 |
| race_detector | UNAVAILABLE | attempted | `-race` requires CGO_ENABLED=1 and a C toolchain; host is CGO_ENABLED=0 (matches TECH_DEBT.md §G1 BLOCKED_BY_ENVIRONMENT) |
| rtl_unicode_conformance | PASS | required | `go run ./cmd/rtlconformance --unicode internal/rtl/testdata/unicode/17.0.0` exit=0 in 6.3 s: BidiCharacterTest 91707/91707, BidiTest 770241/770241, BidiBrackets 1152/1152, Mirroring 428/428 |
| nested_module_tests | UNAVAILABLE | attempted | third_party/bubbles deps for this platform are not in the local module cache and local mode must not download them; runs in ci mode on ubuntu-latest |
| benchmark_inventory | PASS | required | 20 benchmarks discovered (inventory below) |
| go_vet | PASS | required | `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go vet ./...` exit=0 in 2.0 s (22.8 s cold) |
| gofmt_main_module | PASS | required | `gofmt -l .` excluding third_party: clean |
| gofmt_third_party | UNAVAILABLE | attempted | 14 third_party files flagged on this host; `git ls-files --eol` shows 14 of 14 are `i/lf w/crlf` — a working-tree line-ending artifact of the windows checkout, not a formatting defect; CI runs gofmt on linux where these files are clean |
| check_exec_env | PASS | required | `scripts/check-exec-env.sh` exit=0 in 18.3 s |
| check_security_invariants | PASS | required | `scripts/check-security-invariants.sh` exit=0 in 0.5 s |
| check_threat_model_tests | PASS | required | `scripts/check-threat-model-tests.sh` exit=0 in 64.2 s |
| test_threat_model_freshness | PASS | required | `scripts/test-threat-model-freshness.sh` exit=0 in 4.3 s |
| check_security_invariants_rg_subcheck | UNAVAILABLE | attempted | ripgrep not installed on this host: the exec-coverage sub-check of check-security-invariants.sh scanned zero files and passed vacuously — recorded so a vacuous pass is never mistaken for coverage |
| check_threat_model_freshness | PASS | required | `scripts/check-threat-model-freshness.sh HEAD~1` exit=0 in 0.4 s (proves the gate executes; full PR-base usage is in ci mode) |
| staticcheck | UNAVAILABLE | optional | not installed on this host; ci installs it pinned (staticcheck v0.8.1) |
| govulncheck | UNAVAILABLE | optional | not installed on this host; ci installs it pinned (govulncheck v1.8.0) |
| binary_artifact | PASS | attempted | `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o <tmp>/nabd_linux_amd64 ./cmd/ag`: 14,502,152 bytes, sha256 `e63f3bbb26acfc88292205725e365e7ed478e610a50b8fb38b797fb735529222` (identical across two captures — deterministic build) |
| sbom | SKIPPED | attempted | SBOM generation exists in the release-dryrun CI job via anchore/sbom-action/download-syft; not reproduced here to avoid adding tooling outside the Hard Scope Gate |
| working_tree_artifacts | PASS | required | `git status --porcelain` before/after diff: none — the capture leaves nothing behind |
| git_diff_check | PASS | required | `git diff --check` exit=0 |

### Script portability fixes made during W-01

Two bugs in `scripts/capture-baseline.sh` surfaced on this host and were
fixed before the authoritative capture (the first run recorded
`PASS=13 FAIL=3`, with both defects below failing as required checks):

1. `go test -c -o <dir> ./...` requires the `-o` target to be an existing
   directory when multiple packages are compiled; the script now creates the
   temp directory first (previously failed with "with multiple packages, -o
   must refer to a directory or NUL").
2. On windows, `gofmt -l` emits backslash path separators, so the
   `grep '^third_party/'` exclusion missed those paths and leaked them into
   the main-module check; paths are now normalized with `tr '\\' '/'`
   before filtering (previously all 14 third_party files failed the
   main-module check).

## Benchmark inventory (20 benchmarks, main module)

| Package | Benchmarks |
|---|---|
| internal/redact | BenchmarkRedact, BenchmarkStreamWrite |
| internal/rtl | BenchmarkLayoutMixed, BenchmarkLayoutASCII, BenchmarkLayoutLogical |
| internal/tools | BenchmarkUnifiedDiff |
| internal/ui | BenchmarkRefreshStreaming, BenchmarkRefreshLiveStreaming, BenchmarkFormatInlineLong, BenchmarkFormatMarkdown, BenchmarkFormatMarkdownLong, BenchmarkRenderItems500, BenchmarkViewFullScreen, BenchmarkViewFullScreenWithLiveThroughput, BenchmarkThroughputBatchedDeltas, BenchmarkRefreshWithRunningTool, BenchmarkComposerBackspaceOversized, BenchmarkResizeAndStreamingRefresh, BenchmarkAcceptanceReplay1000, BenchmarkAcceptanceReplay10000 |

CI benchmark command (non-gating, `bench-ui` artifact, continue-on-error):

```sh
go test ./internal/ui/ -run "^$" -bench "^(BenchmarkRefreshStreaming|BenchmarkRefreshLiveStreaming|BenchmarkFormatInlineLong|BenchmarkFormatMarkdown|BenchmarkViewFullScreen)$" -benchmem -count=1
```

CI also runs a non-gating layout-benchmark baseline (benchtime=10x count=5);
third_party/bubbles has its own tests. No new benchmarks are created by the
baseline; discovery is inventory-only.

## CI sampling (GitHub API, read-only)

Method: `GET /repos/amiraq1/Nabd/actions/workflows/350459219/runs` (the
workflow named CI, `.github/workflows/ci.yml`) with
`created=2026-09-26..2026-10-02`, both pages, plus
`GET /repos/amiraq1/Nabd/actions/runs/{id}/jobs` for every failed run.
No runs were excluded; the full population is reported.

### Population

101 CI runs, 2026-09-26T05:37:14Z..2026-10-02T14:03:21Z:

- 82 success (36 `pull_request`, 46 `push`)
- 19 failure (12 `pull_request`, 7 `push`), 13 distinct commits

### Wall-clock duration (run_started_at → updated_at)

| Sample | n | median | p90 | min | max |
|---|---|---|---|---|---|
| All successful runs | 82 | 821 s | 900 s | 599 s | 910 s |
| Successful runs, latest per commit | 54 | 822 s | 900 s | 615 s | 908 s |
| Successful pull_request runs only | 36 | 817 s | 901 s | 654 s | 908 s |
| Failed runs (mostly fast-fail) | 19 | 72 s | — | 41 s | 895 s |

Build job (critical path, n=82 successful runs): median 818 s, p90 897 s,
min 596 s, max 905 s.

The pre-W-01 plan-log entry recorded a 36-run operator sample (median 832 s,
p90 900 s, min 599 s, max 910 s; build job "5 job-level samples": median
897 s, min 775 s, max 899 s; "7 failed runs excluded"). That entry's
sampling method was never recorded, so the sample itself is not reproducible;
this baseline supersedes it with the full-population measurement. The
population reproduces the recorded min/p90/max exactly (599/900/910 s) and
the median within 11 s (821 vs 832 s), so the recorded figures are
consistent with the population; the build-job figures differ because the
recorded sample was 5 job-level draws from the same distribution.

### Failure inventory (19 runs, 13 commits, grouped by failing step)

| Failing step | Runs (event, run id, commit, created) |
|---|---|
| Unit tests without race (build job) — the only test failures | 36239689550 (PR) / 36239672025 (push), baa18d73, 2026-09-26T11:43Z; 36261708528 (PR) / 36261654630 (push), 2103c60d, 2026-09-26T18:12Z; 36981006990 (PR), 0c68ca7d, 2026-10-02T07:53Z; 37015909948 (PR), 25e1cd2c, 2026-10-02T13:52Z |
| Install pinned staticcheck (build job) | 36279283569 (PR) / 36279253873 (push), 27a723b1, 2026-09-26T23:23Z; 36440096070 (push), eb12b305, 2026-09-28T14:59Z; 36583107512 (PR), 673b15fd, 2026-09-29T14:29Z |
| Check formatting (gofmt) (build job) | 36281640770 (PR) / 36281638834 (push), 737c56e0, 2026-09-27T00:09Z; 36305054267 (PR), c8673d8c, 2026-09-27T08:04Z; 36305439455 (PR), db695df9, 2026-09-27T08:11Z |
| Threat model freshness (build job) | 36440099788 (PR), eb12b305, 2026-09-28T14:59Z; 36599504421 (PR), 60af7ba5, 2026-09-29T16:41Z |
| Multi-job: build (Build), release-dryrun (Snapshot release, no publish, no sign), termux (Build and vet for Termux android/arm64), vulnerability-scan (Run govulncheck) | 36480731051 (PR) / 36480709096 (push), a6827f48, 2026-09-28T20:39Z |
| Install syft (release-dryrun job) | 36480678754 (push), 1b7c36a5, 2026-09-28T20:38Z |

Most failures are CI-environment/tooling steps (staticcheck install, gofmt,
threat-model freshness, syft install), not test failures. The last failure
in the window, run 37015909948 (commit 25e1cd2, PR #251 branch), failed
at "Unit tests without race" on `TestClientWarnsOncePerInvalidPolicyValue`;
both commits live on the open PR #251 branch, not on master, so the test
is not present at this baseline's HEAD. It was fixed by commit 22af343
(`internal/endpoint/policy_warn_internal_test.go`: count warning lines,
not substring occurrences) and the next run was green. The current HEAD
(a1d448a) is green on CI (all 6 of its in-window runs succeeded).

### Queue time

`queued_at` is unpopulated for fetched runs (UNVERIFIED). At run granularity
`run_started_at == created_at`; the plan log's operator approximation of
created_at → first job start (~3 s) stands as the only queue-time estimate.

## Known environment limitations of this capture

- The capture host is windows/amd64 with CGO_ENABLED=0: `go test ./...`
  execution and `-race` are unavailable here (linux binaries cannot execute;
  race needs CGO). Unit/race evidence must come from CI and is not
  comparable across hardware/OS. Matches TECH_DEBT.md §G1.
- staticcheck, govulncheck, syft, cosign, goreleaser, benchstat, rg, and
  bash-on-PATH are not installed locally; CI installs pinned staticcheck
  v0.8.1 and govulncheck v1.8.0.
- The exec-coverage sub-check of `scripts/check-security-invariants.sh`
  passes vacuously without `rg` (recorded above; the script itself is
  read-only in Phase 1, outside the Hard Scope Gate).
- The clone is shallow (2 commits), so git-history analysis is limited.

## Cross-references

- Issue #189 acceptance criterion "Baseline metrics captured from master" —
  this file plus `scripts/capture-baseline.sh` is the evidence.
- `docs/TECH_DEBT.md` §G1 — race-detector environment constraint and the
  measured diff ceilings.
- `docs/PLAN_LOG.md` — session plan log; the W-01 completion entry records
  the capture run and the two script fixes.
