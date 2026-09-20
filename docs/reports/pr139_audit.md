# PR #139 Post-Merge Audit

**Commit:** `d5fa0a353ee75ccfcccd9464a4a3dbeb1dd242ef`
**Title:** fix: harden permission journaling and read provenance (#139)
**Merged:** 2026-09-17 ~09:10 +0300, squashed into master
**Files:** 14 (+135/−77)
**pr-checklist status at merge:** failing (check run 104995793401)

## Source of the count

The commit message claims "harden permission journaling and read provenance" as a single title. The PR body (not reviewed before merge) claimed nine fixes. Independent analysis of the diff identifies **eight distinct behavioural changes** and **two documentation/comment-only changes**. The number eight, not nine, is the result of reading the diff hunk by hunk; one of the claimed "fixes" (the `ModeAllowReads` comment correction) changes no runtime behaviour.

---

## Change Inventory

| # | Short Name | File(s) in master today (line) | Guarantee (before → after) | Guard Test | Depends On | Revertable Alone? |
|---|---|---|---|---|---|---|
| 1 | **gate-emit-fail-closed** | `internal/agent/gate.go:57,64,73,83` | Before: `emit()` return value was discarded; a journal-write failure silently continued and the tool could execute with an unjournaled permission. After: every `emit()` return is checked; failure returns `Deny` and a descriptive error, so no tool runs without a journaled permission event. | `TestDecideLogsEffectiveDecision`, `TestDecideRefusesWhenPermissionQuestionCannotBeJournaled` | None | Yes |
| 2 | **gate-return-effective** | `internal/agent/gate.go:89` | Before: `decide()` returned `d` (the raw user click). After: returns `effective` (policy-constrained decision). A bash `AllowSession` click that the policy downgrades to `AllowOnce` was previously returned as `AllowSession` to the caller; now it is `AllowOnce`. | `TestDecideLogsEffectiveDecision` (extended: asserts `AllowOnce` return and `recordCalls == 0` for downgraded bash) | #1 (shared function) | No — shares `gate.go:decide` function body with #1 |
| 3 | **bash-no-post-reap-sweep** | `internal/tools/bash.go:141` | Before: `killGroup(pgid)` was called unconditionally after `Wait`, including after clean exit. A recycled process-group ID could signal unrelated processes. After: the post-reap `killGroup` is removed; only timeout/cancellation paths signal the group before `Wait`. | `TestBashCleanExitDoesNotKillProcessGroup` | None | Yes |
| 4 | **read-single-pass** | `internal/tools/read.go:248–264` | Before: `read_file` performed three seeks and two passes (head-peek for binary detection, full hash, then line rendering). After: a single `io.ReadAll` (bounded by `maxHashBytes = 8 MiB`) reads once; hash, binary check, and rendering all use the same `src` buffer. Eliminates seek-dependent divergence where the hash covered different bytes than the renderer. | `UNGUARDED` — no test directly asserts single-read or same-bytes-hash-render equivalence. `TestReadCreditCompositeKeyStructure` and `TestReadFileCreditPathForAbsoluteInside` test credit keys, not the read-pass count. | None | Yes, but #5 depends on the `rel` key introduced in the same file |
| 5 | **read-credit-rel-key** | `internal/tools/read.go:337,353` | Before: `ReadCredit.Path` was set to the absolute path `p`. After: set to the normalized root-relative `rel`. This makes the read-credit key match the write-commit key, so `ConsumeLinesRead` can validate provenance across read → write. | `TestReadCreditCompositeKeyStructure` (updated: asserts `relPath`), `TestReadFileCreditPathForAbsoluteInside` (updated: asserts `filepath.ToSlash("target.txt")`) | #4 (introduced `rel` variable in same function) | No — `rel` computed in #4's rewrite |
| 6 | **grep-readPathFromRoot** | `internal/tools/grep.go:75` | Before: `grep` resolved its start path via `t.root.Resolve(start)`. After: uses `readPathFromRoot` adapter, aligning grep with the descriptor-relative path authority used by all other file tools. | `TestToolPathAuthorityDoesNotCallResolveDirectly` (source scan: asserts `grep.go` does not contain `.Resolve(`) | None | Yes |
| 7 | **write-commit-rel-key** | `internal/tools/write.go:195,245`; `internal/tools/write_commit.go:141,164,223` | Before: `write_file`/`edit_file` callers passed `abs` (from `Resolve`) to `commit()`; `commit()` called `writePathFromRoot(root, abs)` to decompose it back. `ConsumeLinesRead` received `abs`. After: callers pass `rel` directly from `writePathFromRoot`; `commit()` parameter renamed from `abs` to `path`; `ConsumeLinesRead` receives `relative`. This ensures write-side credit matching uses the same key as the read side (#5). | `TestReadCreditValidMatchingCycle` (end-to-end read→write credit cycle), `TestToolPathAuthorityDoesNotCallResolveDirectly` | #5 (key must match) | No — entangled with #5 and #6 via shared key invariant |
| 8 | **reset-clears-yolo** | `internal/perm/policy.go:218` | Before: `Reset()` cleared `granted` but left `yolo` intact; a YOLO session that reset retained full auto-approval. After: `p.yolo = false` added to `Reset()`. | `TestResetRevokes` (extended: asserts `p.YOLO() == false` after Reset) | None | Yes |

### Non-behavioural changes (not counted above)

| # | Short Name | File(s) | Nature |
|---|---|---|---|
| A | **ModeAllowReads-comment** | `internal/perm/policy.go:37-38` | Comment text correction only; `ModeAllowReads` value and behaviour unchanged. No test needed. |
| B | **THREAT_MODEL-doc** | `docs/THREAT_MODEL.md` | Updated 3 guarantee rows, added 2 new rows, rewrote Path-layer prose. Documentation only. |
| C | **ParseMode-test-tighten** | `internal/perm/mode_test.go:91-93` | Added `ModeAllowReads` assertion in existing `TestParseModeRoundTrip`. Test-only change. |

---

## Revertability

### Non-revertable in isolation

| Fix | Reason |
|---|---|
| #2 (gate-return-effective) | Shares the `decide()` function body in `gate.go` with #1; reverting the `return effective` → `return d` line would also need to revert the `emit()` error-checking lines unless manually separated. The test `TestDecideLogsEffectiveDecision` asserts both the emit-fail-closed and the effective-return behaviours. |
| #5 (read-credit-rel-key) | The `rel` variable it uses was introduced by #4's rewrite of the read function. Reverting #5 without reverting #4 would require re-introducing `p` in the credit assignment while keeping the single-read structure. |
| #7 (write-commit-rel-key) | The `ConsumeLinesRead(relative, ...)` call depends on callers passing `rel` not `abs`. Reverting #7 without reverting #5 would break the read→write credit matching invariant, causing `ConsumeLinesRead` to return 0 on valid reads. |

### Revertable in isolation

| Fix | Notes |
|---|---|
| #1 (gate-emit-fail-closed) | Self-contained emit error checks; would also need to revert #2 since they share function body. |
| #3 (bash-no-post-reap-sweep) | One deleted line, no dependencies. |
| #4 (read-single-pass) | Self-contained rewrite; but reverting would also break #5. |
| #6 (grep-readPathFromRoot) | One line change, self-contained. |
| #8 (reset-clears-yolo) | One added line, self-contained. |

**Summary:** Fixes #1/#2, #4/#5/#7, and the THREAT_MODEL documentation form two entangled clusters. Only #3, #6, and #8 are truly independently revertable.

---

## UNGUARDED guarantees

| Fix | Guarantee without a guard test | Risk |
|---|---|---|
| **#3 — bash-no-post-reap-sweep** | "A bash cleanup signal cannot target a recycled process-group ID after the leader has been reaped." The guarantee is now guarded by `TestBashCleanExitDoesNotKillProcessGroup`, which injects the group-kill seam and asserts a successful command never signals its reaped process group. | Medium — the window for a recycled PGID to be signaled is narrow but the consequence (killing unrelated processes) is severe. Resolved by follow-up test coverage. |
| **#4 — read-single-pass** | "Hashing and rendering use the same bytes" (single `io.ReadAll`). No test asserts that the hash covers exactly the bytes the model sees — only that the credit keys are correct. A refactor re-introducing a seek-based split could silently break the hash-render correspondence. | Low — the single-read pattern is structurally obvious, but the guarantee is implicit in code shape rather than in a test. |

### Disposition for UNGUARDED items

- **#4** → `docs/TECH_DEBT.md` row: `READ_SINGLE_PASS_HASH_RENDER_EQUIVALENCE`

---

## Step 3 — Cross-verification of evidence points

| Evidence point | Expected | Actual in master (`c872c01`) | Status |
|---|---|---|---|
| `internal/tools/bash.go:141` — "Do not sweep a process group…" comment | Present at ~line 141 | **Line 141**: `// Do not sweep a process group after Wait has reaped its leader. A reused` | ✓ Present, same line |
| `internal/agent/gate.go:82` — `effective := l.Gate.Effective(...)` | Present at ~line 82 | **Line 82**: `effective := l.Gate.Effective(c.Name, d)` | ✓ Present, same line |
| `gate.go:57` — emit fail-closed (VerdictDeny branch) | Present at ~line 57 | **Line 57**: `if err := emit(Event{Type: PermReply, ...Deny...}); err != nil {` | ✓ Present, same line |
| `gate.go:64` — emit fail-closed (no-prompt branch) | Present at ~line 64 | **Line 64**: `if err := emit(Event{Type: PermReply, ...Deny...noPrompt}); err != nil {` | ✓ Present, same line |
| `gate.go:73` — emit fail-closed (PermAsk branch) | Present at ~line 73 | **Line 73**: `if err := emit(Event{Type: PermAsk, ...}); err != nil {` | ✓ Present, same line |
| `gate.go:83` — emit fail-closed (PermReply post-human branch) | Present at ~line 83 | **Line 83**: `if err := emit(Event{Type: PermReply, ...effective...}); err != nil {` | ✓ Present, same line |

All six evidence points are in their expected positions. No drift detected.
