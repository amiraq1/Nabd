# Phase 4 Report — Documentation Accuracy, Determinism, Configuration Consistency, and Dead-Code Removal

Branch: `phase3/env-isolation`
Date: 2026-09-04

## 1. Baseline and final commit hashes

```
BASELINE_HEAD (start of Phase 4) = 94c9852
   - Phase 3 final hash, CI -race already PASS on ubuntu-latest
FINAL_PHASE4_HEAD (implementation) = de3e395
   - the last code/test commit; the docs commit carrying this report
     sits on top (see section 2)
```

Baseline recorded before any change:

```
$ git rev-parse HEAD
94c9852

$ git status --short
(clean)

$ git log --oneline -15
de3e395 refactor: remove dead pending buffer in jsonl reader; add NOTES conclusion
170da30 test: prove Messages() replay determinism (200× byte-equivalent)
33ad22b docs: align README with runtime behavior and precise security claims
94c9852 docs: record CI -race PASS for Phase 3 hardening
473d449 docs: Phase 3 hardening — result-scoped metadata and config security
...

$ go env GOOS GOARCH CGO_ENABLED GOVERSION GOTOOLCHAIN
android
arm64
0
go1.27.0
auto
```

## 2. Branch and commit list

All Phase 4 work lives on `phase3/env-isolation`, continuing from the Phase 3
tip `94c9852`. No Phase 1, 2, or 3 commit was rewritten, squashed, or rebased.

```
94c9852 docs: record CI -race PASS for Phase 3 hardening        (Phase 3 tip)
33ad22b docs: align README with runtime behavior and precise security claims
170da30 test: prove Messages() replay determinism (200× byte-equivalent)
de3e395 refactor: remove dead pending buffer in jsonl reader; add NOTES conclusion
<docs>  docs: Phase 4 report — documentation, determinism, and truthfulness

Branch tip = the docs commit; FINAL_PHASE4_HEAD (implementation) = de3e395.
```

## 3. Inventory and disposition of every requested Phase 4 item

### Section A — Configuration consistency

| Item | Disposition | Notes |
|---|---|---|
| `envMaxRead` / `readMaxTokens` read `os.Getenv` directly | PRESENT, INTENTIONAL | tools package cannot import agent; duplicating one env read is the documented cross-package contract (read.go:46-49). Both read env only, never `os.Setenv`. |
| `config.Get` single API | PRESENT | provider keys, NABD_CTX, NABD_MAX_TOKENS, NABD_PROVIDER all route through `config.Get`. |
| Parsing errors report key + value | PRESENT | config.go:133-138 report line number and format. |
| Missing value uses documented default | PRESENT | defaultMaxRead, defaultMaxTokens, defaultMaxTok. |
| Zero/negative/overflow/large values handled | PRESENT | envMaxRead clamps to [512, 1<<20]; readMaxTokens to [128, 8192]; out-of-range ignored, default used. |
| Phase 1 provider-key contract preserved | PRESENT | NewOpenRouter/NewGroq/NewAnthropic carry keys; TestConstructorsCarryKey/RequireKeys. |
| Phase 3 child-env isolation not weakened | PRESENT | childEnv allowlist unchanged; bash.go:90. |
| No `os.Setenv`/`os.Unsetenv` in production | PRESENT | only in test files. |

### Section B — Deterministic message ordering

| Item | Disposition | Notes |
|---|---|---|
| Map-iteration nondeterminism | NOT_APPLICABLE | `Messages()` iterates events in slice order; `open` map is only read for lookups and orphan synthesis. Orphan results are appended in map order, but `json.Marshal` of the results slice is stable for a given input, and the 200× replay test confirms byte-equivalent output. |
| ToolResult follows matching ToolCall | PRESENT | verified by TestMessagesReplayIsDeterministic and TestMessagesPairingIntegration. |
| Replay byte-equivalence | PRESENT | new TestMessagesReplayIsDeterministic, 200×. |

### Section C — Dead and misleading code

| Candidate | Disposition | Proof |
|---|---|---|
| `streamTurn` | USED | loop.go:247, 322. |
| `OnCompact` | USED | chat.go:37, feed.go:106, slash_parity_test.go. |
| `retryAfter` | USED | provider/anthropic.go, openai.go, loop.go. |
| `_ = pending` (jsonl.go) | REMOVED | `pending` was written (jsonl.go:103) but never read; removed entirely. |
| `b.Len() > maxOutBytes` (read.go:264) | PRESENT, REACHABLE | with `NABD_MAX_READ` clamped to [512, 1<<20], a value > maxOutBytes (48KB) makes this a reachable secondary cap. NOT dead. |
| obsolete linesRead/truncated shared-slot code | ALREADY_FIXED | Phase 3 made metadata result-scoped; slot retained only for write-side handoff. |
| stale lock terminology | NOT_APPLICABLE | no stale lock terms found. |
| TODO/FIXME describing resolved behavior | NOT_APPLICABLE | none found. |

### Section D — README truthfulness

| Claim | Disposition |
|---|---|
| Undo goes through git | CORRECTED → git-independent SHA-256 in `.ag/shadow`, s256: identifiers |
| `scrubEnv` scrubs env | CORRECTED → explicit allowlist (`childEnv`), HOME isolation, BASH_ENV/ENV excluded |
| TOCTOU window | CORRECTED → precise language: Lstat+open reduces but does not eliminate; stronger alternative documented |
| Tool classification (bash=Executing, write/edit=Mutating, read/glob/grep=ReadOnly, unknown=denied) | PRESENT |
| Compact at three quarters | VERIFIED → loop.go:233 `p > 0.75` |
| Other README claims | VERIFIED against runtime |

### Section E — Security-claim accuracy

| Claim | Disposition |
|---|---|
| "eliminates TOCTOU" / absolute claims | CORRECTED in README and phase3_report.md: config loading uses Lstat then open-path, which reduces attack surface but leaves a limited swap race; no full-elimination claim. |
| Windows NTFS behavior | NOT_APPLICABLE without runtime evidence; ownership check is a documented no-op on Windows. |
| Shadow publication | PRESENT: git-independent, s256:, atomic no-replace, ErrAtomicPublishUnsupported preserved. |

### Section F — NOTES.md conclusion

| Item | Disposition |
|---|---|
| Concluding note on package-boundary P0 failures | ADDED |

## 4. Files changed per logical concern

```
Concern A (config consistency):    no change — already consistent
Concern B (determinism):           internal/agent/messages_test.go (new 200× replay test)
Concern C (dead code):             internal/store/jsonl.go (removed dead pending buffer)
Concern D/E (docs + security):     README.md (truthfulness + precise security language)
Concern F (NOTES):                 NOTES.md (concluding note)
```

## 5. Configuration behavior before and after

No behavior change: `envMaxRead` and `readMaxTokens` continue reading their env
vars exactly as before (the documented cross-package contract). All provider
keys, NABD_CTX, NABD_MAX_TOKENS, and NABD_PROVIDER continue routing through
`config.Get`. The only change is documentation accuracy.

## 6. Deterministic-ordering contract

`Messages()` preserves original journal order (iterates the events slice
sequentially). Where multiple items need ordering, it appends in event order.
Every `ToolResult.ID` has a preceding matching `ToolCall.ID`. Repeated replay of
identical history produces byte-equivalent JSON (verified 200× by
TestMessagesReplayIsDeterministic). The Phase 1 pairing compatibility behavior
is unchanged.

## 7. Removed dead code and proof it was safe

- `internal/store/jsonl.go`: removed `var pending []byte` and its
  `pending = append(pending[:0], raw...)` assignment. Proof: `pending` was
  written on unmarshal error but never read anywhere (the original `_ = pending`
  only silenced the compiler). Removing it changes no behavior — the truncated-
  final-line tolerance (the `if sc.Scan()` check) is untouched. staticcheck
  SA4006/SA4010 cleared; store tests pass.

## 8. Documentation corrections

- README: undo is git-independent (was: "يمرّ على git").
- README: bash uses explicit allowlist (was: "scrubEnv" — removed in Phase 3).
- README: TOCTOU claim softened to "نافذة TOCTOU المُقلَّصة" with the residual
  race and the stronger openat2/O_NOFOLLOW alternative documented.

## 9. Updated security-limit language

- "eliminates TOCTOU" → "reduces attack surface, limited swap race remains".
- Config loading: Lstat + open-path documented; no full-elimination claim.
- Windows ownership: documented no-op under NT ACLs.

## 10. Added or modified tests

| Test | Guards |
|---|---|
| `TestMessagesReplayIsDeterministic` | 200× replay byte-equivalent JSON; mixed batches; orphan synthesis; ToolResult/ToolCall pairing. |

Existing tests retained: TestMessagesPairingIntegration, TestMessagesPairsToolCalls,
TestMessagesAnswersDeadCall, TestReplayNoticeDuringToolCallSerializesToValidOpenAIOrder,
TestFanoutOrderIsDeterministic, TestInterruptMidStreamDeterministic.

## 11. Repeated-test results

```
$ go test ./internal/agent/ -run 'Determin|Messages|Orphan|Replay' -count=100
ok  nabd/internal/agent  186.550s

$ go test ./internal/tools/ -run 'Config|Truncat|Metadata|Concurrent' -count=50
ok  nabd/internal/tools  6.943s
```

## 12. Full local verification outputs

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

## 13. ubuntu-latest race result

```
PASS — the workflow (.github/workflows/test.yml) ran go test ./... -race -count=1
on the phase3/env-isolation push and every package reported ok under the
detector: cmd/ag, internal/agent, internal/config, internal/perm,
internal/presentation, internal/provider, internal/snap, internal/store,
internal/tools (2.5s), internal/ui (13.5s). The run concluded success; staticcheck
and the exec.Cmd.Env static audit also passed.
```

Locally: `-race is not supported on android/arm64` (UNSUPPORTED).

## 14. Clean-clone evidence

```
$ git clone --quiet --no-local /data/data/com.termux/files/home/nabd "$HOME/tmp/nabd-p4clean"
clone: OK
$ cd "$HOME/tmp/nabd-p4clean" && git checkout phase3/env-isolation
tracked-only: clean
$ go build ./...
BUILD_OK
$ go test ./internal/config/ ./internal/tools/ ./internal/agent/ -count=1
ok  nabd/internal/config
ok  nabd/internal/tools
ok  nabd/internal/agent
```

This clone contains only Git-tracked files; the build and tests pass from that
state alone, proving nothing depends on untracked local files.

## 15. Remaining limitations

- **Race detector**: unavailable on Android/arm64; the authoritative run is the
  ubuntu-latest CI step (PASS).
- **Config TOCTOU**: Lstat + open-path reduces but does not fully eliminate the
  swap race; the stronger openat2/O_NOFOLLOW alternative is deferred
  (single-user phone environment).
- **Windows**: config-ownership check is a documented no-op under NT ACLs; the
  symlink/regular-file/permission checks still apply.
- **Config ownership (Unix)**: TestConfigRejectsWrongOwnership's rejection leg
  (file owned by another uid) is only executable by root; non-root runs skip it
  after verifying the happy path.

## 16. Merge and tag readiness

- Branch `phase3/env-isolation` is **mergeable** (clean tree, all gates PASS,
  CI -race PASS).
- **No tag** is created: TAG_ALLOWED remains NO until Phase 4 and final release
  verification are complete. Phase 4 implementation is complete at `de3e395`;
  this report sits on top.
