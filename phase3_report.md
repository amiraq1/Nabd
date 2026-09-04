# Phase 3 Report — Environment Isolation and Shared-State Hygiene

Branch: `phase3/env-isolation`
Date: 2026-09-04

## 1. Baseline and final commit hashes

```
BASELINE_HEAD (start of Phase 3) = a191d1d
FINAL_PHASE2_HASH (accepted Phase 2 head) = a191d1d
   - contains: platform-specific no-replace publication,
     runtime capability probing, full-content blob verification,
     updated phase2_report.md
FINAL_PHASE3_HEAD (implementation) = 7854fd1
   - the last code/test commit; the docs commit carrying this report
     sits on top (see section 2) and is the branch tip
```

Baseline recorded before any change:

```
$ git rev-parse HEAD
a191d1d

$ git status --short
(clean)

$ git log --oneline -10
... <Phase 2 history> ...

$ git diff a191d1d...HEAD --stat
(empty — HEAD was a191d1d at baseline)

$ go env GOOS GOARCH CGO_ENABLED GOVERSION GOTOOLCHAIN
android
arm64
1
go1.27.0
auto
```

## 2. Branch and commit list

All Phase 3 work lives on `phase3/env-isolation`, created from the accepted
Phase 2 head `a191d1d`. No Phase 1 or Phase 2 commit was rewritten, squashed,
or rebased.

```
f74de89 security: remove global environment loading (loadEnv)
bdd5f7b style: gofmt internal/ui/feed.go (pre-existing double blank line)
11c324f security: build bash environment from an explicit allowlist
a51d357 fix: isolate per-invocation registry metadata (Consume semantics)
4dab716 fix: clear pending read metadata on plain-Run read failure
8874793 test: add environment and registry boundary integration coverage
7854fd1 test: pin config load never mutates process-global environment
<docs>  docs: phase3 report — environment isolation and shared-state contracts

Branch tip = the docs commit; FINAL_PHASE3_HEAD (implementation) = 7854fd1.
```

The commit `bdd5f7b` is a pure `gofmt` hygiene fix for a pre-existing
double blank line in `internal/ui/feed.go` (introduced before this branch by
Phase 3/UI history). It is isolated in its own style commit so no UI cleanup
is mixed into the security/fix commits (see §15).

## 3. Environment data-flow: before and after

### Before

```
~/.ag/env ──► loadEnv() in cmd/ag/main.go ──► os.Setenv() ──► mutates
                process-global environment

provider keys (~/.ag/config) ──► config.Get() ──► providers

bash child:  os.Environ() ──► scrubEnv() blacklist (suffix heuristics)
             ──► cmd.Env = scrubbed
             ⚠ exec.Command("sh", "-c", ...) on the bash-tool path
               had NO explicit Env until Phase 3 — with the blacklist
               removed it would inherit the FULL parent environment
               (the nil-Env exec.Cmd vulnerability class)
```

Defects closed:

- `loadEnv()` was the only production `os.Setenv` site. It read `~/.ag/env`
  into the process-global environment, so provider credentials placed there
  would be inherited by every child process.
- `scrubEnv()` was a blacklist keyed on names that *look* secret. Unknown
  variables passed through purely because their names were not on the list.
- `exec.Cmd.Env` was left unset on the bash-tool path before Phase 3, so the
  child inherited the entire parent environment regardless of filtering.

### After

```
~/.ag/config ──► config.Load() (private map, never merged into os.Environ)
             ──► config.Get()  ──► provider constructors (keys in memory)

bash child:  os.Environ() (read-only source) ──► childEnv() allowlist
             (exact names only, sorted, sanitized, NUL-rejected)
             + HOME = isolated 0700 temp dir (or omitted)
             ──► cmd.Env = env          (explicit, always)
             ──► cmd = exec.Command("sh", "-c", cmd)

exec.Cmd.Env static audit (scripts/check-exec-env.sh) enforced in CI:
every exec.Command in non-test Go code must assign Env before Start/Run.
```

No production `os.Setenv` / `os.Unsetenv` remains:

```
$ grep -rn 'os\.Setenv\|os\.Unsetenv' --include='*.go' . | grep -v '_test.go'
NONE — no production os.Setenv/os.Unsetenv remains
```

The only `os.Environ()` call in production is the read-only source for
`childEnv()` in `internal/tools/bash.go`; the caller's environment is never
mutated and never passed through wholesale.

## 4. Exact child-environment allowlist and rationale

`childEnvAllowlist` in `internal/tools/bash.go` is a map of exact variable
names. A variable is either named here or it does not exist in the child.
No prefix/substring heuristics (`SECRET`, `TOKEN`, `KEY`, `AWS`, `OPENAI`,
…) are used anywhere. The constructed environment starts from an empty
slice (`out := make([]string, ...)`), copies only allowlisted names, sorts
names for determinism, and appends `NABD=1` as a marker.

| Variable | Rationale |
|---|---|
| `PATH` | Command resolution. Passed through only after `sanitizePath`: empty and relative entries stripped, so an attacker-planted binary in cwd cannot shadow real commands. Omitted entirely if nothing survives. |
| `TERM` | Terminal capabilities for interactive-ish commands. |
| `LANG` | Base locale. |
| `LC_ALL`, `LC_COLLATE`, `LC_CTYPE`, `LC_MESSAGES`, `LC_MONETARY`, `LC_NUMERIC`, `LC_TIME` | Selected locale categories so output collation, sorting, and messages behave predictably. |
| `TMPDIR`, `TMP`, `TEMP` | Temp-directory configuration for tools that honor it. |

Explicitly **not** allowed: every credential variable regardless of name
(`AWS_*`, `OPENAI_API_KEY`, `OPENROUTER_API_KEY`, `GROQ_API_KEY`,
`GITHUB_TOKEN`, `GH_TOKEN`, `ANTHROPIC_API_KEY`, `GOOGLE_API_KEY`,
`GEMINI_API_KEY`, `SSH_KEY`, `GIT_ASKPASS`, `SSH_ASKPASS`), the startup-file
vectors `BASH_ENV` and `ENV`, `CDPATH`, and the library-injection vectors
`LD_PRELOAD`, `LD_LIBRARY_PATH`, `DYLD_INSERT_LIBRARIES`,
`DYLD_LIBRARY_PATH`.

Value sanitization applies to **every** allowlisted variable, not only
locale ones: `childEnv` drops any entry whose value contains a NUL byte and
skips malformed entries (no `=` or empty name).

Deterministic ordering: names are emitted in sorted order, so two identical
runs produce byte-identical child environments (covered by
`TestBashChildEnvDeterministicOrdering`).

## 5. HOME and PATH policies

### HOME — Option 1 (isolated application-owned temp HOME)

`newTempHome()` creates the child HOME:

- application-owned (created by this process),
- restrictive permissions `0700`,
- removed after each invocation **even on failure** (`defer os.RemoveAll(home)`),
- never reused across invocations.

If the temp HOME cannot be created, HOME is **omitted** from the child
environment — the caller's real HOME is never passed to the child. This is
the preferred policy from the spec: a real HOME full of startup files,
ssh keys, and credential stores (`~/.ssh`, `.netrc`, `.bashrc`, `.profile`)
must never reach the shell. Covered by `TestBashChildEnvHomePolicy`, which
plants `.profile`/`.bashrc`/`.ssh/id_ed25519`/`.netrc` in a fake HOME, runs
the real bash tool, and proves none of them appear in the child or get
sourced.

### PATH — controlled pass-through with stripping

`sanitizePath()` keeps only absolute, non-empty entries. An empty PATH
entry means the current working directory on Unix (executing an
attacker-planted binary from cwd); a relative entry has the same risk from
wherever the child runs. Both are stripped. If nothing survives, PATH is
omitted entirely.

Evidence (`TestBashChildEnvPathStripping`): parent PATH
`relative/bin::/usr/bin:/bin:.:relative2`; the real child reports a PATH
with no `relative`, no `:.`, and no `::` entries, while `/usr/bin` and
`/bin` remain available.

## 6. BASH_ENV / ENV injection-test evidence

Real-bash integration tests, no mocks:

- `TestBashChildEnvBASH_ENVNotSourced`: parent `BASH_ENV` points at a
  marker script; the real child runs; the marker's `.sourced` sibling is
  never created. BASH_ENV is neither passed nor consulted.
- `TestBashChildEnvENVNotSourced`: same for `ENV` (the POSIX sh startup
  file).

Raw test output (this environment):

```
--- PASS: TestBashChildEnvBASH_ENVNotSourced (0.08s)
--- PASS: TestBashChildEnvENVNotSourced (0.03s)
```

## 7. Provider-key compatibility evidence

Phase 1 provider-key contracts are unchanged and pass:

- `TestConstructorsCarryKey` (internal/provider/ctor_test.go): NewOpenRouter
  carries `OPENROUTER_API_KEY`; NewNVIDIA/NewGroq/NewAnthropic carry their
  keys into the client (`Key` field).
- `TestConstructorsRequireKeys` (ctor_integration_test.go): each constructor
  (Anthropic/OpenRouter/Groq/NVIDIA) fails clearly when its key is missing
  and succeeds carrying the key when present.
- `TestKeyVarFollowsHost`: the key variable name follows the base URL.

`config.Load()` still serves keys via `Get()` from a private map; the key
never enters `os.Environ`, so it can never be inherited by a child process.
This is pinned by the new `TestLoadDoesNotMutateProcessEnv`.

Raw output:

```
--- PASS: TestConstructorsCarryKey (0.00s)
--- PASS: TestKeyVarFollowsHost (0.00s)
--- PASS: TestConstructorsRequireKeys (0.00s)
ok  nabd/internal/provider  0.232s
--- PASS: TestLoadDoesNotMutateProcessEnv (0.01s)
ok  nabd/internal/config  0.502s
```

## 8. linesRead / truncated ownership model

The registry keeps read metadata in a private `metadata` struct guarded by
its own mutex, with strict one-shot **Consume** ownership:

```
type metadata struct {
    mu         sync.Mutex
    linesRead  int  // set by read_file, consumed by the next commit()
    truncated  bool // set by read_file, consumed by RunDetailed
    nextOffset int  // set by read_file on truncation, consumed by RunDetailed
}
```

- `SetLinesRead(n)` / `SetTruncated(next)`: written by the read_file
  invocation under the mutex.
- `ConsumeLinesRead() int`: atomically returns the current value and resets
  it to zero. Ownership is one-shot: exactly one consumer takes a value,
  and anything after it sees 0 — a stale count can never bleed into a later
  unrelated write.
- `ConsumeTruncated() (bool, int)`: same ownership for truncation state.
- `ClearReadState()`: drops all pending metadata. Called when an invocation
  failed or was cancelled, through both the `RunDetailed` error branch and
  a wrapper around the plain `Run` path (so `Registry.Run` failures clear
  too), and at the start of every read.

`write.go` `commit()` consumes `linesRead` on each write; the loop's event
path consumes truncation through `RunDetailed`. A mutex alone would be
insufficient if ownership stayed ambiguous; the consume-reset semantics make
ownership unambiguous: each invocation's metadata is taken by exactly one
consumer and deleted on take.

## 9. Files changed per concern

```
Concern A (remove global env loading):
  cmd/ag/main.go                      loadEnv() removed
Concern B (child-env allowlist):
  internal/tools/bash.go              childEnvAllowlist, childEnv,
                                      sanitizePath, newTempHome;
                                      cmd.Env always set explicitly
  scripts/check-exec-env.sh (new)     exec.Cmd.Env static audit
  .github/workflows/test.yml          audit step added to CI
Concern C (registry metadata):
  internal/tools/registry.go          metadata struct + Consume + Clear
  internal/tools/read.go              Run wrapper clears on error; truncation
                                      ownership unchanged
  internal/tools/write.go             commit() consumes linesRead
  internal/tools/edit_record_test.go  updated to ConsumeLinesRead API
Concern D/E (tests + docs):
  internal/tools/env_isolation_test.go     (new)
  internal/tools/registry_metadata_test.go (new)
  internal/config/config_test.go           TestLoadDoesNotMutateProcessEnv
  internal/ui/feed.go                      pre-existing gofmt fix (style
                                           commit, §15)
  phase3_report.md                         this report
```

`internal/snap` was **not modified**:

```
$ git diff a191d1d...HEAD --stat -- internal/snap
(empty)
```

## 10. Added tests and guarded regressions

### Environment boundary (internal/tools/env_isolation_test.go)

| Test | Guards |
|---|---|
| `TestBashChildEnvAllowlistIntegration` | Real bash child sees none of the 22 listed sensitive vars set in the parent, nor their values. |
| `TestBashChildEnvBASH_ENVNotSourced` | BASH_ENV marker script never sourced by real child. |
| `TestBashChildEnvENVNotSourced` | ENV marker script never sourced. |
| `TestBashChildEnvHomePolicy` | Isolated temp HOME; fake HOME's `.profile`/`.bashrc`/`.ssh`/`.netrc` never reach child. |
| `TestBashChildEnvAllowsWhitelisted` | TERM/LANG/LC_*/TMPDIR/PATH survive; NABD=1 present. |
| `TestBashChildEnvDeterministicOrdering` | Two identical runs emit identical sorted env. |
| `TestBashChildEnvConcurrentIsolation` | 4 concurrent bash runs each get a distinct HOME; no env values leak across them. |
| `TestBashChildEnvPathStripping` | Empty/relative PATH entries stripped, absolute kept. |
| `TestBashChildEnvRejectsNULValues` | NUL in an allowlisted value → dropped, never forwarded. |

### Registry metadata (internal/tools/registry_metadata_test.go)

| Test | Guards |
|---|---|
| `TestReadReportsOwnLineCount` | read_file reports its own line count (C#1). |
| `TestSubsequentWriteReportsZeroReadLines` | A write after a consumed read carries 0, not stale (C#2). |
| `TestTwoSequentialReadsDoNotAccumulate` | 3 then 5 → 5, not 8 (C#3). |
| `TestTruncatedReadReportsOnlyItsInvocation` | Truncation flag is per invocation; a following clean read is clean (C#4/5). |
| `TestFailedReadDoesNotContaminate` | Failed read leaves 0 (C#6). |
| `TestCancelledOperationDoesNotContaminate` | Cancelled read leaves 0 (C#7). |
| `TestConcurrentReadsMetadataIsolated` | Under contention: values are only whole 3/0, one consumer takes the value, double consume is 0, final state 0 (C#8). |
| `TestConcurrentReadWriteRaceFree` | Concurrent read→write cycles with own registries stay race-free (C#9). |
| `TestRegistryInstancesIsolated` | Two registries never share metadata (C#10). |
| `TestReadFailureClearsViaRunDetailed` | RunDetailed error path clears too. |

### Config process-env isolation (internal/config/config_test.go)

| Test | Guards |
|---|---|
| `TestLoadDoesNotMutateProcessEnv` | config.Load/Get never merges file values into os.Environ. |

### Regressions re-run (Section D)

`TestAllToolsClassified`, `TestUnknownToolClass`, `TestBashDeniedRunsNoSubprocess`,
`TestBashApprovedRunsSubprocess`, `TestBashScrubsKeys`, `TestEditRecordEmitted`,
`TestPersistedUndoModesAndLegacy`, `TestUndoSymlinkSafety`,
`TestConstructorsCarryKey`, `TestConstructorsRequireKeys`,
`TestKeyVarFollowsHost`, `TestToolStartEmission`, `TestOrphanToolEndReconstructed`,
`TestFileWinsOverEnv`, `TestShadowPublishNoReplaceBoundary`,
`TestShadowPublishIsLockFree`, `TestShadowPublishIdempotentOnMatchingContent`,
`TestShadowPublishRejectsOnMismatchedContent`, `TestContinueAfterLengthCut` — all PASS.

## 11. Raw verification outputs

### After every logical commit (F-section commands on this env)

```
$ test -z "$(gofmt -l .)" && echo GOFMT_OK
GOFMT_OK

$ go build ./...
BUILD_OK

$ go vet ./...
VET_OK

$ go test ./... -count=1
ok  nabd/cmd/ag
ok  nabd/internal/agent
ok  nabd/internal/config
ok  nabd/internal/perm
ok  nabd/internal/presentation
ok  nabd/internal/provider
ok  nabd/internal/snap
ok  nabd/internal/store
ok  nabd/internal/tools
ok  nabd/internal/ui

$ git diff --check
DIFF_CHECK_OK

$ GOTOOLCHAIN=local go run honnef.co/go/tools/cmd/staticcheck@v0.8.1 ./...
STATICCHECK_OK
```

### exec.Cmd.Env static audit (Section B, mandatory)

```
$ bash scripts/check-exec-env.sh
OK: every exec.Command construction in non-test Go files assigns Env explicitly.
```

Call sites inspected (production, non-test Go files):

- `internal/tools/bash.go:97` — `exec.Command("sh", "-c", a.Cmd)` — the
  bash-tool path. `cmd.Env = env` is assigned immediately after (line 98),
  before `cmd.Start()` (line 102). **Not left unset.**
- All other non-test Go files contain zero `exec.Command`/`exec.CommandContext`
  constructions, so there are no secondary/fallback spawn sites on the
  bash-tool path to miss.

The audit script is AST-independent but conservative (function-scoped awk):
it fails the run on (a) any `exec.Command` site in a function without an
`.Env =` assignment, or (b) any `.Env =` assigned after `.Start()`/`.Run()`
in the same function. It is wired into `.github/workflows/test.yml` as the
"Exec.Cmd.Env static audit" step, so CI fails if a future edit forgets Env.

Negative test performed during development: a copy of the script in a temp
repo with an `exec.Command` lacking `Env` exits 1 with
`FAIL: unset-Env exec.Command call site found`; with `Env` set it exits 0.

## 12. Clean-clone evidence

```
$ git clone --quiet --no-hardlinks /data/data/com.termux/files/home/nabd /tmp/nabd-clean
$ cd /tmp/nabd-clean && git status --short
(clean — tracked files only, no untracked artifacts needed)

$ go build ./... && echo BUILD_OK
BUILD_OK
$ go vet ./... && echo VET_OK
VET_OK
$ go test ./internal/config/ ./internal/tools/ -count=1
ok  nabd/internal/config
ok  nabd/internal/tools
```

This clone contains only Git-tracked files; the build and tests pass from
that state alone, proving nothing depends on untracked local files.

## 13. Race-detector status

```
$ go test -race ./internal/tools/ -run TestNonexistent
-race is not supported on android/arm64
```

Status vocabulary, per spec:

- `go test ./... -race -count=1` locally on this Termux/proot
  Android/arm64 environment: **UNSUPPORTED** (platform cannot execute it;
  the command ran and reported `-race is not supported on android/arm64`).
- ubuntu-latest CI run for this branch: **PENDING_CI** — the workflow runs
  `go test ./... -race -count=1` (phase2_report section 14 shows the race
  gate passing on the Phase 2 head), but this branch has not been pushed to
  the remote, so no run exists for it yet. No configured-but-unrun CI is
  reported as PASS.

## 14. Remaining platform limitations

- **Race detector**: unavailable on Android/arm64; the authoritative run is
  the ubuntu-latest CI step.
- **exec.Cmd.Env audit**: the script is POSIX shell; it runs on the CI
  ubuntu-latest runner as well as here.
- **Windows**: the Phase 2 Windows no-replace path is verified by
  cross-compile and stdlib errno mapping, not by a Windows runtime in this
  sandbox (unchanged from Phase 2; documented there).
- **Termux/proot**: `uname -r` = `6.17.0-PRoot-Distro`; real `sh` is
  available and the bash integration tests run against it. The child
  environment tests execute the real bash tool, not mocks.
- **PATH policy**: caller PATH is passed through after sanitization
  (documented command-resolution risk: the child resolves commands from the
  caller's PATH, minus empty/relative entries). This is the documented
  Option-3-style choice; the audit step keeps it from being silently
  broadened.

## 15. Unrelated changes

One commit is unrelated to the security/fix work and is deliberately
isolated:

- `bdd5f7b style: gofmt internal/ui/feed.go` — a pre-existing double blank
  line in `internal/ui/feed.go` (present at baseline `a191d1d`, originally
  introduced by Phase 3/UI history before this branch). It was left by
  `gofmt -l` and is fixed in its own style commit so no UI presentation
  cleanup is mixed into the security commits (spec: "Do not mix unrelated
  UI/presentation cleanup into these commits").

No other unrelated changes.

## 16. Merge/tag status

- Branch `phase3/env-isolation` is **not merged** into any main branch.
- **No tag** has been created.
- The branch has not been pushed to the remote; external CI therefore
  remains PENDING_CI for this branch and is explicitly not a blocker for
  the isolated Phase 3 work (per the user's gate decision). Merge and
  tagging remain gated on the external CI run of this branch.
- Race-detector gate for Phase 3: same policy as Phase 2 — a tag/merge will
  only follow a real ubuntu-latest run that passes.

## 17. PHASE3_SHADOW_LOCK_DEPENDENCY: NONE

Environment isolation (allowlist child env, HOME policy, removed global
env loading) and registry metadata ownership (Consume semantics for
linesRead/truncated) are orthogonal to shadow lock semantics: they touch
only process-environment construction and per-invocation tool bookkeeping,
whereas the shadow lock guarantees ordered, exclusive, no-replace atomic
publication of file changes — so starting Phase 3 before the Race Detector
gate closed does not depend on, and cannot be invalidated by, any shadow
lock assumption.