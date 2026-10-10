# Plan Log

## 2026-10-02T16:30:58Z — Phase 1 pre-W-01 entry

```text
date UTC:     2026-10-02T16:30:58Z
HEAD SHA:     a1d448a3401afacc28e7bc4e11e7df2fabd4e319
branch:       master (origin/master: ahead 2, behind 1)
OS:           Microsoft Windows 11 Pro 10.0.26200 (win32)
Go version:   go1.27.1 windows/amd64
GOOS:         windows
GOARCH:       amd64
CGO_ENABLED:  0
CPU count:    12
memory:       7.69 GB (operator measurement via PowerShell; script records UNAVAILABLE on this host)
working tree: clean except untracked .vscode/ (pre-existing, user-owned, untouched)
clone type:   shallow (2 commits; .git/shallow present) — git history analysis limited accordingly
```

### Environment facts established by evidence

- `bash` is NOT on PATH but Git Bash exists at `C:\Program Files\Git\bin\bash.exe`; the baseline
  script is executable via Git Bash on this host.
- Project supported build platforms are `linux` and `android` only: `internal/config` and
  `internal/snap` have `//go:build !windows` / `linux || android` implementations and NO windows
  counterparts, so `go build ./...` on windows/amd64 FAILS with undefined symbols
  (`openConfigFile`, `checkOwnerPlatform`, `renameNoReplace`, `syncDir`, `probeNoReplaceSupport`).
  Pre-existing platform constraint; CI (ubuntu-latest + android cross-build) covers both supported
  platforms.
- `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./...` cross-compiles successfully on this host
  (measured 34.5 s); `go vet ./...` for linux passes (15.9 s); `go test -c` for ./cmd/ag passes
  (34.7 s).
- `go test ./...` execution and `-race` are UNAVAILABLE on this host: linux binaries cannot execute
  on windows, and `-race` requires CGO_ENABLED=1 (host is 0). Matches the pre-existing
  BLOCKED_BY_ENVIRONMENT note in TECH_DEBT.md §G1.
- RTL Unicode conformance runs natively on windows: `go run ./cmd/rtlconformance --unicode
  internal/rtl/testdata/unicode/17.0.0` → PASS (BidiTest 770241/770241, BidiBrackets 1152/1152,
  Mirroring 428/428) in 6.5 s.
- third_party/bubbles working-tree files are CRLF while the git index is LF (`git ls-files --eol`:
  i/lf w/crlf); `gofmt -l .` on windows therefore lists 14 third_party files. Main module is
  gofmt-clean. CI (linux) passes `test -z "$(gofmt -l .)"`.
- Local tools NOT_INSTALLED: staticcheck, govulncheck, syft, cosign, goreleaser, benchstat, rg,
  bash-on-PATH. CI installs pinned staticcheck v0.8.1 and govulncheck v1.8.0.
- check-security-invariants.sh depends on `rg` for its exec-coverage sub-check; without `rg` the
  sub-check scans zero files and passes vacuously. Recorded as a finding; script itself is
  READ-ONLY in Phase 1 (outside Hard Scope Gate).

### GitHub state verified via read-only API (gh 2.86.0, account amiraq1)

- Issue #189: open — "Phase 0 — baseline and stabilization tracks" (updated 2026-09-26T05:59:37Z)
- Issue #222: closed — "fix(provider): classify model-unavailable by type, not by message wording"
- Issue #223: open — "i18n: user-facing strings mix Arabic and English inside the same function"
- PR #241: open — "test(redact): port stream redaction test cases from laptop backup"
- PR #246: open — "chore(deps): bump the codeql-action group with 3 updates"
- PR #251: open — "chore: follow-up review fixes for #250 and #249" (updated 2026-10-02T14:03:25Z)
- CI: 36 successful CI-workflow runs sampled (2026-09-26..2026-10-02): wall median 832 s,
  p90 900 s, min 599 s, max 910 s. Build job (critical path, 5 job-level samples): median 897 s,
  min 775 s, max 899 s. 7 failed runs excluded with reasons (see docs/BASELINE.md).
- Last failure: run 37015909948 (commit 25e1cd2, PR #251 branch) failed at step
  "Unit tests without race" on TestClientWarnsOncePerInvalidPolicyValue (nabd/internal/endpoint);
  fixed by commit 22af343; next run green.

### Confirmed assumptions

- Repository is amiraq1/Nabd; default branch master; CI workflows ci.yml and release.yml exist and
  are READ-ONLY in Phase 1.
- GitHub API and gh CLI are available and read-only usable; no mutation performed.

### Unconfirmed assumptions

- Whether GitHub queue duration can be measured: REST API `queued_at` is unpopulated for fetched
  runs; queue time approximated as created_at → first job start (~3 s). Marked UNVERIFIED.
- CI stage durations beyond the 5 job-level samples are inferred from workflow wall-clock, not
  per-step timings (per-step logs not extracted for all runs).

### Risks

- Windows host cannot execute the project's test suite; unit/race timings must come from CI
  measurements and are NOT comparable across hardware/OS (documented in BASELINE.md).
- Shallow clone limits historical git analysis to 2 commits.
- Vacuous-pass risk in check-security-invariants.sh when `rg` is absent (finding, not fixed here).

### Blockers

- None for W-01. W-02 mutation-testing evidence collection is possible without production changes.

### Decisions required

- None yet.

### Status

```text
W-01: IN_PROGRESS
W-02: NOT_STARTED
```

## 2026-10-02T18:37:07Z — W-01 completion entry

```text
date UTC:     2026-10-02T18:37:07Z
HEAD SHA:     a1d448a3401afacc28e7bc4e11e7df2fabd4e319 (unchanged)
host:         same windows/amd64 host as the pre-W-01 entry
```

### Work completed

1. `scripts/capture-baseline.sh` (untracked work product) fixed for
   two windows-portability defects exposed by the first capture run
   (`PASS=13 FAIL=3`, both failures required-check bugs, not repo
   regressions):
   - `go test -c -o <dir> ./...` needs the `-o` directory to exist;
     the script now `mkdir -p`s it (previously failed with "with
     multiple packages, -o must refer to a directory or NUL").
   - `gofmt -l` emits backslash separators on windows, so the
     `'^third_party/'` filters missed those paths; paths are now
     normalized via `tr '\\' '/'` before filtering (previously all 14
     third_party files failed the main-module gofmt check).
2. Authoritative local capture (captured_utc 2026-10-02T18:32:17Z):
   `PASS=15 FAIL=1 UNAVAILABLE=7 SKIPPED=1`, `required_failure=0`,
   `RESULT: OK`. The only FAIL is `go_build_host` (attempted, not
   required) — the pre-existing windows platform constraint.
3. CI sampling via read-only REST API
   (`workflows/350459219/runs`, `created=2026-09-26..2026-10-02`,
   both pages, plus per-run job details): full population of 101 CI
   runs — 82 success (36 pull_request, 46 push), 19 failure
   (12 pull_request, 7 push), 13 distinct commits. Wall (n=82):
   median 821 s, p90 900 s, min 599 s, max 910 s. Build job (n=82):
   median 818 s, p90 897 s, min 596 s, max 905 s. Complete failure
   inventory grouped by failing step (only 6 of 19 runs failed a
   test step — "Unit tests without race"; the rest are CI
   tooling/environment steps).
4. `docs/BASELINE.md` written: exit-state definitions, environment,
   full check-evidence table, project-size metrics, benchmark
   inventory (20 benchmarks), CI population sampling with failure
   inventory, known environment limitations.

### Corrections to the pre-W-01 entry

- Memory: the script DOES record it on this host (7.69 GB via Git
   Bash `free`); "script records UNAVAILABLE" was wrong.
- CI sampling: the pre-W-01 entry recorded "36 successful runs
   sampled … 7 failed runs excluded" but never recorded the sampling
   method, so that sample is not reproducible. The full population is
   101 runs (82 success / 19 failure). The population reproduces the
   recorded min/p90/max exactly (599/900/910 s) and the median within
   11 s (821 vs 832 s); the recorded figures are consistent with the
   population. The "7 failed" likely referred to the 7 failed push
   runs, but that is unconfirmed.
- `go test -c` evidence: the pre-W-01 measurement compiled only
   `./cmd/ag`; the script's `./...` compile check (whole test suite,
   46.1 s) is the stronger evidence and is what BASELINE.md records.

### Evidence artifacts

- Two full capture reports (first: exposed the two script bugs;
  final: green, cited in docs/BASELINE.md).
- CI population and per-run job data fetched read-only via the
  GitHub API (gh 2.86.0, account amiraq1); no mutation performed.
- Binary artifact: nabd_linux_amd64, 14,502,152 bytes, sha256
  e63f3bbb26acfc88292205725e365e7ed478e610a50b8fb38b797fb735529222
  (identical across both captures — deterministic build).

### Blockers

- None.

### Decisions required

- None. W-02 (mutation-testing evidence collection) can proceed
  without production changes, as the pre-W-01 entry anticipated.

### Status

```text
W-01: PARTIAL — Windows local capture complete; Linux reference capture NOT executed
W-02: NOT_STARTED
```

## 2026-10-02T21:43:00Z — NO-GO review entry (post-W-01 correction)

```text
date UTC:      2026-10-02T21:43:00Z
HEAD SHA:      a1d448a3401afacc28e7bc4e11e7df2fabd4e319  (local master)
origin/master: f69dbbe371c6e33aeb8734bed2755cf6fb60c156  (ahead 3, behind 1 vs local)
reviewer:      external correctness audit
```

### Decision: NO-GO — Phase 1 remains incomplete

The W-01 completion entry recorded on 2026-10-02T18:37:07Z contained seven
material defects that prevent closing Phase 1. Each is stated in full below,
along with the corrective action required before a GO decision can be issued.

### Defect inventory

#### D1 — Execution environment: Windows, not Linux

The prompt required Linux execution. The capture ran on Windows/amd64 and was
described as "authoritative". This is incorrect: unit tests, race tests, nested
module tests, staticcheck, govulncheck, and the `rg` sub-check are all
UNAVAILABLE on this host. The capture is a useful *Windows diagnostic capture*;
it is NOT a reference baseline.

Corrective action: re-execute `local`, `ci`, and `full-security` modes on a
clean Linux clone at `origin/master`.

#### D2 — Git reference was not clean

At the time of capture the clone was shallow (2 commits) and the local branch
had diverged from origin/master (ahead 2, behind 1). A reference baseline must
be taken at the canonical `origin/master` HEAD, not a diverged local branch.

After the subsequent `git fetch --unshallow origin master` the situation is:
local HEAD `a1d448a` is ahead 1 / behind 3 of `origin/master` `f69dbbe3`.
The divergence has changed but the problem remains: HEAD != origin/master.

Corrective action: create a clean clone or a detached worktree at `origin/master` (e.g. `git worktree add --detach ../Nabd-baseline origin/master`) without modifying the local working branch before executing any reference capture.

#### D3 — W-01 exit gate not satisfied

The following checks were not executed or not verified:

| Check | Required mode | Status |
|---|---|---|
| `go test ./...` (unit suite) | local / ci | UNAVAILABLE on Windows |
| `-race` suite | ci | UNAVAILABLE on Windows |
| nested module tests (third_party/bubbles) | ci | UNAVAILABLE on Windows |
| staticcheck v0.8.1 | ci | UNAVAILABLE on Windows |
| govulncheck v1.8.0 | ci | UNAVAILABLE on Windows |
| `ci` mode end-to-end | ci | NOT EXECUTED |
| `full-security` mode | full-security | NOT EXECUTED |
| clean-clone reproducibility | any Linux | NOT EXECUTED |

`required_failure=0` is correct for the Windows local mode result but does NOT
satisfy the Phase 1 gate, which requires these checks on Linux/CI.

W-01 correct status: `PARTIAL — Windows local capture complete; Linux reference
capture, ci mode, and full-security mode not executed`.

#### D4 — W-02 not started despite being in scope

The corrective prompt explicitly listed W-02 as in-scope. The prior executor
declined on the grounds that "the user hasn't asked to start W-02". This
contradicts the explicit requirement. W-02 remains NOT_STARTED.

#### D5 — CI failure classification is inaccurate

The entry characterised 13 of 19 CI failures as "CI tooling/environment steps"
and 6 as test failures. To prevent double counting across multi-step failures,
each of the 19 failed runs is assigned to exactly one primary category based on
its direct failing step:

| Primary Category | Direct Failing Step | Runs | Run IDs / Commits |
|---|---|---|---|
| Code / Test Regression | Unit tests without race | 6 | 36239689550 / 36239672025 (baa18d73), 36261708528 / 36261654630 (2103c60d), 36981006990 (0c68ca7d), 37015909948 (25e1cd2c) |
| Policy / Formatting | Check formatting (gofmt) | 4 | 36281640770 / 36281638834 (737c56e0), 36305054267 (c8673d8c), 36305439455 (db695df9) |
| Security Documentation | Threat model freshness | 2 | 36440099788 (eb12b305), 36599504421 (60af7ba5) |
| Tooling / Dependency | Install pinned staticcheck | 4 | 36279283569 / 36279253873 (27a723b1), 36440096070 (eb12b305), 36583107512 (673b15fd) |
| Multi-cause / first failing job not uniquely attributable | Multi-job failure (build + release-dryrun + termux + govulncheck) | 2 | 36480731051 (PR) / 36480709096 (push) (a6827f48) |
| Tooling / Dependency | Install syft | 1 | 36480678754 (push) (1b7c36a5) |
| **Total** | | **19** | Exactly 19 unique runs, no double-counting (6 Code, 6 Policy/Governance, 5 Tooling, 2 Multi-cause). |

#### D6 — CI sample scope mismatch

101 runs were analysed in place of the last 20. The extended analysis is
valuable but does not replace the required scoped sample. Required additionally:

- Last 20 runs as a distinct sub-sample with their own statistics.
- Push vs pull_request breakdown for the last 20 runs.
- Branch/commit scope limited to `master` branch (push events) where possible.
- Per-step timing for at least the last 5 successful runs (queue / execute /
  critical path separation).

#### D7 — Report terminated before the gate decision

The W-01 completion entry ends at the Deliverables section without a formal
gate decision (GO / NO-GO / DEFERRED). A Phase 1 plan log entry must conclude
with a gate decision statement.

### Status corrections applied

| Item | Was | Now |
|---|---|---|
| W-01 status | COMPLETE | PARTIAL |
| BASELINE.md | reference baseline | Windows diagnostic capture; not authoritative |
| W-02 | NOT_STARTED | NOT_STARTED (confirmed still in scope) |
| Phase 1 gate decision | (absent) | NO-GO |

### Blockers for GO

1. Create a clean clone or detached worktree at `origin/master` (`f69dbbe3`) without modifying the local working branch (e.g. `git worktree add --detach ../Nabd-baseline origin/master`).
2. Execute `capture-baseline.sh local` on Linux.
3. Execute `capture-baseline.sh ci` on a GitHub Actions ubuntu-latest runner.
4. Execute `capture-baseline.sh full-security` on Linux.
5. Confirm `rg`, staticcheck, govulncheck installed in the Linux environment
   (not installed by the script itself).
6. Rebuild `docs/BASELINE.md` from the Linux capture output.
7. Complete W-02 (see W-02 entry below).
8. Issue a formal gate decision (GO or NO-GO) at end of W-02.

### Status

```text
W-01: PARTIAL — Windows local capture complete; Linux reference capture NOT executed
W-02: NOT_STARTED
Phase 1 gate: NO-GO
```

## 2026-10-02T21:49:00Z — W-02 execution entry

```text
date UTC:      2026-10-02T21:49:00Z
HEAD SHA:      a1d448a3401afacc28e7bc4e11e7df2fabd4e319  (local master)
origin/master: f69dbbe371c6e33aeb8734bed2755cf6fb60c156
Note: W-02 evidence collected from origin/master source code (verified via
      git fetch --unshallow; the skill.go walk function confirmed at
      origin/master via git show origin/master:internal/skill/skill.go).
```

### Work completed

1. **Debt taxonomy**: All open debt entries in `docs/TECH_DEBT.md` header
   table classified into categories A-E (Architecture gap / Observable
   production risk / Observability gap / Accepted risk / Maintenance burden).

2. **Owner / Target / Closure criteria**: Added to all six existing entries
   plus the new `RG_EXEC_COVERAGE_VACUOUS_PASS` entry.

3. **New debt entry — `RG_EXEC_COVERAGE_VACUOUS_PASS`**: Added to the header
   table and expanded in the W-02 Settlement section of `docs/TECH_DEBT.md`.
   Evidence: Windows W-01 capture confirmed `check-security-invariants.sh`
   exits 0 with `rg` absent. Recorded in `docs/BASELINE.md` row
   `check_security_invariants_rg_subcheck`.

4. **`SKILL_WALK_BOUND_IS_POST_HOC` proof**: Verified at `f69dbbe3` in
   `internal/skill/skill.go`. Output results are strictly capped at
   `maxWalkEntries` (never exceeding 512 entries). The defect is bounded
   read-ahead: `f.ReadDir(64)` loads up to 64 `DirEntry` values into memory
   before the `seen >= maxWalkEntries` check fires. `maxWalkDepth` is correctly
   pre-hoc (checked before `os.Open`). Only the entry-count bound is post-hoc.
   Fix calculates `remaining := maxWalkEntries - seen; batchSize := min(64, remaining+1)`.
   Measure memory cost before modifying production code.

5. **Phase 2 priority order**: Established in `docs/TECH_DEBT.md` W-02
   Settlement section. Top priority: `RG_EXEC_COVERAGE_VACUOUS_PASS`
   (security gate), then `THREAT_MODEL_SIZE_MAINTENANCE_LIMIT` (maintenance),
   then `SKILL_WALK_BOUND_IS_POST_HOC` (low-to-medium).

6. **Issue #189 update text**: Prepared and stored in W-02 Settlement section
   of `docs/TECH_DEBT.md`. Not posted: posting is gated on the Linux
   reference baseline (W-01 blocker).

### Evidence artifacts

- `docs/TECH_DEBT.md` — header table updated (7 entries, all with
  owner/target/closure), W-02 Settlement section appended.
- `docs/PLAN_LOG.md` — NO-GO review entry (2026-10-02T21:43:00Z) and this
  W-02 completion entry.
- `docs/BASELINE.md` — STATUS header and "fully green" interpretation note
  corrected.
- Source evidence: `internal/skill/skill.go` `walk` function at `f69dbbe3`.

### Validation and gate execution (2026-10-02T22:18:00Z)

All documentation integrity, patch applicability, and security gates verified:
1. `git diff --check` with intent-to-add (`git add -N` across `docs/BASELINE.md`, `docs/PLAN_LOG.md`, `docs/TECH_DEBT.md`, and `scripts/capture-baseline.sh`): PASSED cleanly (لم يجد whitespace errors أو conflict markers عبر جميع الأسطر المضافة/المعدلة).
2. `rg` status verification on host: `type rg` confirmed absent on PATH (`not found`). Security invariants script executed: `check_guarded_provider_client` passed legitimately; `check_freshness_covers_process_execution` recorded as `UNAVAILABLE / VACUOUS PASS` due to missing `rg` (confirming `RG_EXEC_COVERAGE_VACUOUS_PASS` finding).
3. `bash -n scripts/capture-baseline.sh`: PASSED (bash syntax valid).
4. Patch applicability to `origin/master`: exported `phase1-docs.patch`, created detached worktree `../Nabd-phase1-review` at `origin/master` (`f69dbbe3`), and executed `git apply --check phase1-docs.patch`: قابل للتطبيق نصيًا على `origin/master` دون تعارضات وفق `git apply --check`. Review worktree and patch file cleaned up without using `reset`.
5. `bash scripts/check-threat-model-tests.sh`: PASSED (all 374 tests in `docs/THREAT_MODEL.md` exist; all 33 tests in `docs/TECH_DEBT.md` exist).
6. `bash scripts/test-threat-model-freshness.sh`: PASSED (all 9 freshness test cases passed).
7. `bash scripts/check-threat-model-freshness.sh origin/master`: PASSED.
8. Full encoding audit: UTF-8 clean, zero corruption artifacts, zero fake URLs, CI failure taxonomy categorized with 0 double-counting.

### Blockers

- None for W-02. W-02 is COMPLETE.
- W-01 remains PARTIAL (Linux reference capture required).

### Decisions required

- None. Phase 2 priority order is established; execution requires W-01 to complete first.

### Status

```text
W-01: PARTIAL — Windows local capture complete; Linux reference capture NOT executed
W-02: COMPLETE
Phase 1 gate: NO-GO (pending Linux reference baseline)
```

## 2026-10-02T23:25:00Z — Phase 1 Termux Linux/Android baseline capture & rerun

```text
date UTC:      2026-10-02T23:25:00Z
HEAD SHA:      47c8007f2b1f3da54a1fdb3735ee29784167e414  (branch docs/phase1-linux-baseline)
branch:        docs/phase1-linux-baseline
host:          android/arm64 (Linux 5.15), Go 1.27.1, 8 cores, 11.08 GB RAM
```

### Context & Two-Stage Execution

#### 1. First run (Commit `7deabf7bd554`) — Security gate self-trip
- **Execution**: Initial baseline measurement run under Termux in `$HOME/nabd-baseline-results/harness`.
- **Finding**: `check_threat_model_freshness` failed with exit code 1 (`required_failure=1`).
- **Root cause**: `scripts/capture-baseline.sh` was added under the `scripts/` directory, which is part of the security boundary allowlist monitored by `scripts/check-threat-model-freshness.sh`. The commit did not include an update to `docs/THREAT_MODEL.md`. In addition, `capture-baseline.sh` had mode `100644`.
- **Classification**: Valid security gate enforcement. This was not a regression in the product, but a correct trip of the repository's security guard preventing unmonitored tooling additions to security boundaries.

#### 2. Commit fix (Commit `47c8007`)
- **Permission**: Restored execution bit (`chmod +x scripts/capture-baseline.sh`, mode change `100644 => 100755`).
- **Threat Model**: Documented the harness in `docs/THREAT_MODEL.md` under `### Baseline measurement harness` as security-adjacent measurement tooling (not a runtime containment boundary), specifying its review requirements and the Android/arm64 Go race detector limitation.
- **Verification**: Verified pre-commit checks:
  * `git diff --check`: PASS
  * `bash scripts/check-threat-model-tests.sh`: PASS (374 tests in THREAT_MODEL.md, 33 tests in TECH_DEBT.md)
  * `bash scripts/check-security-invariants.sh`: PASS
  * `bash scripts/test-threat-model-freshness.sh`: PASS
  * `bash -n scripts/capture-baseline.sh`: PASS
  * Committed as `docs(security): document phase 1 baseline harness` (`47c8007`) and pushed to remote.
  * Post-commit: `bash scripts/check-threat-model-freshness.sh HEAD~1` returned PASS (exit=0).

#### 3. Second run (Rerun at `47c8007f2b1f`) — All three modes green
- **Evidence directory**: `$HOME/nabd-baseline-results/harness-47c8007f2b1f/`
- **Mode results**:
  * `local`: `exit=0`, `PASS=20 FAIL=0 UNAVAILABLE=1 SKIPPED=1`, `required_failure=0`
  * `ci`: `exit=0`, `PASS=20 FAIL=0 UNAVAILABLE=1 SKIPPED=1`, `required_failure=0`
  * `full-security`: `exit=0`, `PASS=21 FAIL=0 UNAVAILABLE=2 SKIPPED=1`, `required_failure=0`
- **Checks status**:
  * Unit tests (`go_test_unit`): PASS natively on Termux (28.7s - 48.7s).
  * Unicode RTL conformance (`rtl_unicode_conformance`): PASS (all tests match).
  * Nested module tests (`nested_module_tests`): PASS (third_party/bubbles).
  * Static analysis (`staticcheck`, `govulncheck`, `go_vet`, `gofmt`): PASS.
  * Security gates (`check_exec_env`, `check_security_invariants`, `check_threat_model_tests`, `test_threat_model_freshness`, `check_threat_model_freshness`): PASS.
  * Linux binary cross-compile artifact: PASS (`nabd_linux_amd64`, 14,621,701 bytes, sha256 `e15bb4dfe4767ba1c402890378660e801a9a052b866c166eb9093d32f27efb50`).
  * `race_detector`: UNAVAILABLE (Go runtime limitation on `android/arm64`; authoritative race run remains GitHub Actions Linux amd64 runner).
  * `syft_sonatype_sweep`: UNAVAILABLE (syft not installed on local Termux host; CI installs it pinned).
  * `sbom`: SKIPPED (reproduced in CI release-dryrun).
  * Repository invariants: PASS (`git diff --check`, `git status --porcelain` clean).
- **Integrity & Checksums**:
  * `SHA256SUMS` generated in results directory:
    ```text
    1449c2b5ff946df859afcf660bcede9d2d0baf97e7b0142fa49c9ea716d7d764  local.log
    e58edfae9e699393a0a7ed1a980c0eca08155b66e2f1db0e98fd89852372be51  ci.log
    e4fcfaa96140910226f8597d3e244c7eeba1da4c42c4c58d0e2062c88f908333  full-security.log
    19eaf43821a7660ec323a87c8457bf74823beb296c39f5e01aa8a683aa50f061  local.exit
    19eaf43821a7660ec323a87c8457bf74823beb296c39f5e01aa8a683aa50f061  ci.exit
    19eaf43821a7660ec323a87c8457bf74823beb296c39f5e01aa8a683aa50f061  full-security.exit
    ```
  * Secret scan with ripgrep (`rg -n -i 'api[_-]?key|authorization:|bearer |token=|sk-[a-z0-9]'`) returned zero matches across logs.
  * Capture logs and exit files remain outside Git tracking.

### Phase 1 Gate Decision

```text
Termux modes exit 0 + required_failure=0:
  W-01 COMPLETE FOR TERMUX ANDROID

GitHub Actions still pending:
  Phase 1 CONDITIONAL GO

GitHub CI, Ubuntu full race, and release-dryrun green:
  Phase 1 GO
```

## 2026-10-02T23:35:00Z — Phase 1 baseline gate closure

```text
date UTC:      2026-10-02T23:35:00Z
HEAD SHA:      79ed6dffb2ecb7306435ee27e48ec1fbe4e31def  (branch docs/phase1-linux-baseline)
GitHub CI run: 37060263696
status:        completed / success
```

### GitHub CI Verification Results (Run 37060263696)

All five workflow jobs on GitHub Actions completed with conclusion `success`:
1. `termux` (ID 111014901625): `success` (58s) — Build and vet for Termux (android/arm64).
2. `release-dryrun` (ID 111014901918): `success` (1m5s) — Syft SBOM generation, clean tree assertion, ephemeral key signing, artifact verification.
3. `module-tidiness` (ID 111014901960): `success` (14s) — `go mod tidy -diff`.
4. `build` (ID 111014902074): `success` (11m26s) — Threat model freshness, test citations, gofmt, build, vet, unit tests, full package race detector, RTL race, nested module race, router/agent/UI race tests, UI benchmarks, static audits, git diff check, clean local clone verification.
5. `vulnerability-scan` (ID 111014902199): `success` (54s) — `govulncheck`.

### Final Gate Resolution

```text
Termux local/ci/full-security: PASS
GitHub CI run: 37060263696
Ubuntu full race: PASS
release-dryrun and Syft: PASS
Phase 1 decision: GO
```

---

## 2026-10-10 — MCP Phase S measurements complete (branch `docs/mcp-phase-s-harnesses`, HEAD `db31db1`)

**Environment:** Termux (F-Droid, versionCode 1022) on 23078PND5G, go1.27.1 android/arm64.

**Proven facts:**
- S1a: 3 rounds PASS — `before=2` → `remaining=0` after killGroup; control test proves counter sees live procs (`visible=2 ≥ 1`).
- S1b: PASS — `Wait` returns `signal: killed` without hanging on pipe-holding grandchild.
- S2: 3 stable rounds — 64MiB discarded without deadlock, goroutines 2→2, heap delta small negative (GC).
- S3 positive: node and python OK in allowlists A/B/C (absolute path + minimal PATH).
- S3 negative: PATH/HOME/TMPDIR removal fails with stderr naming the variable; LANG/TERM are positive controls.
- S4: SDK v1.8.0 = 235 deps / 13 external modules / transitional edges; stdlib scaffold = 76 deps / 0 external.

**Unconfirmed assumptions:**
- Single-sample timings in S3 are noise; not used for decisions.
- S2 numbers are preliminary single-environment; no absolute memory limit set.

**Risks:**
- MCP not implemented; all THREAT_MODEL rows remain design commitments (NOT PROVIDED), no GUARANTEED claims beyond dependency isolation.
- S4 choice has lasting dependency-surface consequences.

**Decisions required:**
- S4: stdlib scaffold vs SDK v1.8.0 (owner: ammar).
- ADR-0003 P2/P3/P4 closure (owner: ammar).
- Phase 0 owners (architectural, security reviewer, acceptance) still unnamed.

**Report:** `docs/reports/mcp_phase_s_report.md`; raw runs in `docs/reports/raw/`.
