# MCP Phase S Measurement Report

**Status:** partial — S2/S3/S4 complete; S1a pending rerun after harness fix
**ADR:** docs/DECISIONS/0003-mcp-integration.md (v5)
**Branch:** docs/mcp-phase-s-harnesses
**HEAD:** 90ba282529d5b2d8ed576b43766f26cbde0784b7 (S1a fix: 082bebf — rerun pending)

## Environment

| Field | Value |
|---|---|
| Device | 23078PND5G (Xiaomi) |
| Termux | F-Droid, versionCode 1022 |
| Android | 16 |
| Go | go1.27.1 android/arm64 |
| Date | 2026-10-10 |

**Environment deviations:** `~/nabd` does not exist (correct: `~/Nabd`); `/tmp` is root-owned non-writable → outputs to `docs/reports/raw/` (gitignored).

## S1: Process-group kill — RESERVED (No-Go pending)

**Raw (run-20261010-2146-s1s2.txt):**
- S1a: SKIP — `needs /proc` (harness checked `/proc/1/stat`, denied on Android)
- S1b: PASS — Wait returned (signal: killed), no hang (2.01s)

**Findings:** Two harness bugs found on Termux, fixed in 082bebf:
1. `/proc/1/stat` is Permission denied for Termux apps; `/proc/self/stat` works.
2. `countProcsInGroup` read `fields[3]` (SID) not `fields[2]` (PGID) — would return 0 even with orphans (false success). Also added pre-kill sanity (≥2 procs visible).

**Interpretation:** S1b proves Wait doesn't hang on pipe held by dead leader's child. S1a — the core "no orphans after killGroup" — was never measured.

**Go/No-Go:** **No-Go (temporary)** — rerun S1a with fixed harness before evaluating.

## S2: Pipes and timeouts under load — PASS

**Raw:**
- S2a: PASS — discarded=67108864 bytes (64 MiB), heap before=281968 after=214656 delta=-67312 (no deadlock, no growth)
- S2b: PASS — child reaped after kill; goroutines before=2 after=2 (no leak)
- S2c: PASS — no freeze, child killed cleanly (2.04s)

**Interpretation:** Pipe handling under load is sound. 64 MiB discard with negative heap delta (GC) proves no buffering blowup. Timeout path reaps cleanly with no goroutine leak.

**Go/No-Go:** **Go** — S2 imposes no design changes.

## S3: Env allowlist — PASS (after D3/D4 fix)

**Raw (run-20261010-2147-s3.txt):**

| server | list A (minimal) | list B (+lang) | list C (+real HOME) |
|---|---|---|---|
| node | OK(495ms) | OK(474ms) | OK(362ms) |
| python | OK(156ms) | OK(103ms) | OK(95ms) |

**Interpretation:** After fixing the harness (absolute binary path — bare-name exec broke Python's self-lookup on Android), both Node and Python mock servers start under all three allowlists, including the minimal one. The minimal list (PATH with $PREFIX/bin, HOME, TMPDIR, LANG, TERM) suffices.

**Go/No-Go:** **Go** — the restricted allowlist works for common servers. ADR §5.3's absolute-path requirement is validated as load-bearing (it prevents the exact failure mode observed).

## S4: SDK vs stdlib — DECISION: stdlib scaffold

**Raw (run-20261010-2147-s4.txt):**

| Metric | MCP Go SDK v1.8.0 | stdlib scaffold |
|---|---|---|
| go list -deps count | 235 | 76 |
| go mod graph edges | 34 | 2 |
| external modules | 13 | 0 |
| network deps | net/http, crypto/tls | none |
| module cache | 96M | — |

**Interpretation:** The SDK pulls 235 transitive deps including network and TLS stacks, 13 external modules, 96M cache. The stdlib scaffold (JSON-RPC lines + pipes) needs 76 deps, 0 external, 2 graph edges.

**Go/No-Go:** **Go with decision** — adopt the **stdlib-only scaffold** per ADR-0003 §4.5. Rationale: 3× fewer deps, zero external modules, zero network surface in the dependency tree. The SDK's network deps (net/http, crypto/tls) contradict the v1 "STDIO only, no network" posture at the dependency level.

**Pinned:** `github.com/modelcontextprotocol/go-sdk` @ `v1.8.0` is **rejected** for v1; revisit only via new ADR if network transport is later approved.

## Summary

| Item | Verdict |
|---|---|
| S1 | No-Go (temporary) — rerun S1a |
| S2 | Go |
| S3 | Go |
| S4 | Go — stdlib scaffold chosen |
| Repo guards + isolation guard | Pass |

**Next:** rerun S1a on Termux with 082bebf, then S1 verdict. Nothing else blocks Phase 0 entry except owner naming (W-08).
