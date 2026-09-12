# Release Readiness Report — Nabd

Date: 2026-09-04
Branch: `phase3/env-isolation` → `master`

## 1. Source and merge identity

```
SOURCE_BRANCH           = phase3/env-isolation
BRANCH_HEAD_HASH       = e6b55e5cee66823c5d94b1cf562c30f6e422168b
BASE_MASTER_PRE_MERGE  = af5504213f92c29fe1dab2b6b2582960eb543f42
MERGE_COMMIT_HASH      = c6b81cddda7798d3f718921fbb06e1f71eb29743
MERGE_TYPE             = merge commit (history preserved, 118 commits)
LATEST_PREVIOUS_TAG    = v1.0.1
```

## 2. Pull request

- Consolidated PR: https://github.com/amiraq1/Nabd/pull/8
- Superseded-and-closed PR #7 (head `c65dbcf` already an ancestor of this branch):
  https://github.com/amiraq1/Nabd/pull/7

## 3. Master CI (post-merge, authoritative)

| Run | Title | Result |
|---|---|---|
| 33911985544 | Go Tests (`go test ./... -race -count=1`, staticcheck, build, vet) | **completed / success** |
| 33911985534 | CI (exec.Cmd.Env audit, staticcheck) | **completed / success** |

URLs:
- https://github.com/amiraq1/Nabd/actions/runs/33911985544
- https://github.com/amiraq1/Nabd/actions/runs/33911985534

## 4. Local gate outputs (on merge commit c6b81cd)

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

$ bash scripts/check-exec-env.sh
OK: every exec.Command construction in non-test Go files assigns Env explicitly.
```

Local `-race` is **UNSUPPORTED** on android/arm64; the authoritative race gate is the
ubuntu-latest master CI run above (PASS).

## 5. Clean-clone evidence

```
$ git clone --no-local /data/.../nabd /tmp/nabd-merged
clone: OK
HEAD: c6b81cddda7798d3f718921fbb06e1f71eb29743   (exact merge commit)
tracked-only: CLEAN
$ go build ./...
BUILD_OK
$ go test ./internal/config/ ./internal/tools/ ./internal/agent/ ./internal/snap/ -count=1
ok  nabd/internal/config
ok  nabd/internal/tools
ok  nabd/internal/agent
ok  nabd/internal/snap
```

Build and tests pass from tracked files only; nothing depends on untracked local files.

## 6. Smoke-test results

| Check | Result |
|---|---|
| Application builds and `--version` displays | PASS |
| Missing provider key fails clearly (TestConstructorsRequireKeys) | PASS |
| Configured provider constructor carries its key (TestConstructorsCarryKey) | PASS |
| `read_file` works (byte-cap truncation, line-boundary, next-offset) | PASS |
| write/edit permission gate asks (TestBashApprovedRunsSubprocess) | PASS |
| `bash` classified `Executing`, denied runs no subprocess (TestBashDeniedRunsNoSubprocess) | PASS |
| Denied + unknown tools preserve ToolCall/ToolResult pairing (TestUnknownToolIsDenied, TestMessagesPairingIntegration) | PASS |
| Persistent undo restores content and 0755/0644 mode (TestPersistedUndoModesAndLegacy) | PASS |
| Undo survives restart and `git gc` (shadow_durable_test) | PASS |
| `--continue` selects only a matching project root (TestLatestSessionProjectIsolation) | PASS |
| `bash` child does not inherit secrets (TestBashChildEnvAllowlistIntegration) | PASS |
| `BASH_ENV`/`ENV` not sourced in child (TestBashChildEnvBASH_ENVNotSourced / ENVNotSourced) | PASS |
| HOME isolated per invocation, 0700 (TestBashChildEnvHomePolicy) | PASS |
| Legacy orphan ToolEnd reconstructed (TestOrphanToolEndReconstructed) | PASS |
| `Messages()` replay deterministic 200× and multi-open journal-ordered 1000× | PASS |

## 7. Diff review (master...branch)

- No accidental credential material; no hardcoded API keys; `gh_log.txt` is a CI log with redacted tokens only.
- No debug logging left in production paths.
- No broad `//lint:ignore` / `//nolint` directives.
- No `os.Setenv`/`os.Unsetenv` in production (test files only).
- Single production `exec.Command` (bash.go:97) assigns `cmd.Env` explicitly.
- Shadow storage is git-independent (SHA-256, `s256:` ids); atomic no-replace via
  `renameat2(RENAME_NOREPLACE)` with explicit `ErrAtomicPublishUnsupported` — no silent
  plain `os.Rename` fallback.
- No undocumented JSON/event schema change: `Outcome.LinesRead` is not serialized to
  the journal (ToolEnd emits individual fields only).
- No Phase 5 performance changes mixed in.
- `internal/snap` unmodified (atomic no-replace publication intact).

## 8. Branch protection

Attempted; not configurable with current token (requires admin). Recommend an admin
configure on `master`: require PR before merge, require the `Go Tests` status check,
require branch up to date, prevent force pushes and deletion.

## 9. Security, undo, isolation, replay, compatibility

- **Environment isolation**: no global `.env` import; `bash` child built from an explicit
  allowlist; HOME isolated (0700, removed after each call); BASH_ENV/ENV excluded.
- **Undo durability**: content-addressed `.ag/shadow` (independent of `git gc`); atomic
  no-replace publication; mode (0755/0644) preserved; survives restart.
- **Session isolation**: `--continue` scoped to explicit session dir and normalized
  project root; legacy sessions without a root are not auto-resumed across projects.
- **Deterministic replay**: `Messages()` preserves journal order (openOrder slice);
  byte-equivalent across replays.
- **Compatibility limits**: Linux/Android/macOS only (`Setpgid`, `sh -c`); token
  estimation is a calibrated heuristic (~10-20% error until calibrated); `bash` is not
  covered by `/undo`; TOCTOU window reduced but not fully eliminated (Lstat + open-path).

## 10. Open issues and deferred Phase 5

- Config-file TOCTOU: Lstat then open-path reduces but does not eliminate the swap race;
  stronger `openat2(RESOLVE_BENEATH)` / `O_NOFOLLOW` + `Fstat` on the open descriptor deferred.
- Windows config-ownership check is a documented no-op under NT ACLs.
- `gh_log.txt` (CI log artifact) is tracked — cosmetic, not a secret; consider `.gitignore`.
- Phase 5 performance optimizations explicitly deferred and not started.

## 11. Recommended semantic version

`v1.1.0` — additive security hardening, new correctness guarantees (deterministic replay,
result-scoped metadata, config alignment), and documentation accuracy on top of v1.0.1.
No breaking change to the public CLI/journal contract. (Requires explicit approval before
tagging.)

## 12. GO / NO-GO

**GO** — all gates green on merged master, master CI green (including `-race`), clean-clone
verified, smoke tests pass, diff review clean.

Tagging is deferred pending explicit version approval.
