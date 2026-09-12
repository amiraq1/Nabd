# Technical Debt

## G1: write.go diff/output/event baseline (NBD-011 limit selection)

Measured on the reference device/environment to choose the starting ceilings
for `maxDiffLines`, `maxDiffCells`, `maxPatchBytes`, `maxEventBytes`
(`internal/tools/write.go`). These are `var` (not `const`) so they can be tuned;
the values below are the safety-reviewed starting points, NOT frozen finals.

### Environment

- Go: go1.27.0
- GOOS: android
- GOARCH: arm64
- CGO_ENABLED: 0
- race: unavailable locally (BLOCKED_BY_ENVIRONMENT)
- device: Android/Termux (Linux 5.15.180-android13 aarch64)

### Raw benchmark (go test ./internal/tools -run '^$' -bench . -benchmem -benchtime 10x)

Benchmark: `BenchmarkUnifiedDiff` — two fully-different inputs of N lines each
(worst case for the LCS matrix: no shared lines).

| input (each side) | ns/op    | B/op      | allocs/op |
|-------------------|----------|-----------|-----------|
| 100 lines         | 1,081,146 | 125,260  | 329       |
| 500 lines         | 3,082,038 | 2,233,192| 1,544     |
| 1000 lines        | 11,161,115| 8,578,884| 3,048     |
| 2000 lines        | 23,912,223| 33,564,386| 6,054    |

Observation: allocation grows quadratically (the (n+1)*(m+1) LCS `int`
table dominates). At 2000x2000 the matrix is ~4M cells; at 8 bytes/int (arm64)
that is ~32 MB of matrix alone, matching the 33.6 MB total observed.

### Chosen ceilings and rationale

- `maxDiffLines = 3000` — per-side line cap. Far above a typical single edit.
- `maxDiffCells = 4_000_000` — matrix-work cap (n*m). 4M cells * 8 B = 32 MB,
  the measured 2000x2000 cost. Rejects larger edits BEFORE allocating the matrix.
  The ceiling is calibrated AT the measured worst case (0x margin), not below
  it: 2000*2000 == 4_000_000 exactly, so the guard `m > maxDiffCells/n` rejects
  2001x2000 and any larger input before allocation. Any edit exceeding it is
  rejected deterministically rather than risking OOM.
- **peak memory note**: the 32 MB figure is a LOWER BOUND on peak allocation.
  The LCS `int` table dominates, but the diff also allocates: the `ops` slice
  (capacity n+m), per-hunk `lines []string` slices, and the `strings.Builder`
  that buffers the full unified diff. Total peak exceeds the matrix by a
  non-trivial margin. The G1 benchmark B/op column (33.6 MB at 2000x2000)
  captures this real total, not just the matrix.
- **aggregate budget**: each `Registry` owns a shared diff-cell budget.
  Concurrent mutations reserve `n*m` cells before matrix allocation and wait
  cancellably when the aggregate ceiling would be exceeded. Reservations are
  released on every return path, so parallel edits cannot multiply the 4M-cell
  ceiling within one registry.
- **cancellation bounds time, not memory**: the LCS row-allocation loop
  (`lcs := make([][]int, n+1)`) runs BEFORE the first `ctx.Err()` check. A
  cancellation therefore bounds COMPLETION TIME but not PEAK MEMORY — the
  goroutine must still allocate the full matrix before it can observe the
  cancellation. The `maxDiffCells` guard, not ctx, is what prevents the OOM.
- `maxPatchBytes = 1 << 20` (1 MB) — raw unified-diff output. A 2000-line full
  rewrite is well under 100 KB; 1 MB gives >10x headroom.
- `maxEventBytes = 1 << 22` (4 MB) — serialized edit-event JSON. Embeds the 1 MB
  patch with JSON escaping overhead; if exceeded, the Patch is dropped and the
  audit fields (hashes, blobs) survive.

### Event-size estimation constants (NBD-011)

`boundEditEvent` estimates the serialized event size rather than
full-serializing on every edit. Two conservative allowances:

- **jsonEscapeWorstCaseFactor = 6**: every byte of the Patch may become `\u0000`
  (6 bytes) in JSON. Using 6× guarantees we never under-estimate. Tradeoff:
  6× is conservative — a 700 KB plain-text patch (escape factor ~1.05) is
  estimated at 4.2 MB and dropped even though it would serialize to ~735 KB,
  well within 4 MB. Decision favors never emitting an oversized event over
  keeping every Patch (dropped Patch still preserves hashes/blobs).
- **eventEnvelopeAllowance = 128**: the journal (`agent.Loop.emitAt`) stamps
  Seq, Parent, and Time before serializing — fields `boundEditEvent` does not
  set. Measured directly by field (see `internal/agent/event.go`):
  - `Seq    int       json:"seq"`              → `"seq":9223372036854775807` = 18 B
  - `Parent int       json:"parent,omitempty"` → `,"parent":9223372036854775806` = 29 B (omitted when 0)
  - `Time   time.Time json:"t"`               → `,"t":"2026-09-06T00:19:03.703040241Z"` = 10 B
  - Total worst-case envelope = 18 + 29 + 10 = **57 bytes** (typical ~29 B for 5-digit seq).
  - 128 B is ~2.2× the measured worst case, a conservative margin.

The actual journal encodes once (`store.JSONL.Append`); `boundEditEvent`
marshals only the bare record (bounded audit fields) for its baseline.

### Policy

`maxWriteBytes = 1<<20` (write_file) and `maxEditBytes = 2<<20` (edit_file) are
the on-disk output ceilings; edit_file additionally rejects a replacement whose
RESULT would exceed `maxEditBytes` (e.g. `all=true` with `new` larger than
`old`). edit_file input is also bounded by `maxEditBytes` (pre-read stat).

The 2000x2000 measurement is the calibration point; `maxDiffCells` is set
AT that observed cost (0x margin — 2000*2000 == 4_000_000 exactly), so the
n*m guard rejects any input larger than 2000x2000 before allocation. Since
cost grows as O(n*m) beyond it, this ceiling is the deterministic boundary
below which cost has been measured and above which it is not allowed.
Re-measure if workload characteristics change.

### Known gaps [DEFERRED]

- **Null-valued cross-tool fields**: `parseMutatingRequest` rejects cross-tool
  fields only when non-nil. A key present with JSON null (e.g.
  `{"path":"x","content":"y","old":null}`) decodes to nil and is NOT rejected.
  The contract is "non-nil cross-tool fields are rejected", not "cross-tool
  keys rejected regardless of value". Practical impact is nil. Key-presence
  detection would require decoding into a map first.

### Evidence documentation rule

CI run ids and head SHAs live in PR comments, never in tracked files,
because recording them in a commit moves the head and invalidates the record.
Each limit and its measurement is stated in exactly one tracked file (this
file, `docs/TECH_DEBT.md`).

## U1 / U3: Composer Line-Wrap Cache & Input Latency (Issue #17)

- **Component:** `internal/ui` (`composer`, `feed`), `third_party/bubbles/textarea`
- **Issue:** Pushing large payloads (e.g., thousands of runes without spaces) into the composer causes high input latency on every keystroke. In an unbroken payload of ~8,100 runes, 101 Backspace events took ~1.4s locally and ~3.0s for Arabic text, triggering CI timeouts under race-detector overhead.

### Root Cause Analysis

- **[CONFIRMED] Cache Miss Pattern:** Upstream `bubbles/textarea` wraps text via `memoizedWrap`, which keys an LRU cache by `sha256(fmt.Sprintf("%s:%d", string(runes), width))`. Any Backspace or keystroke mutates the string, causing a 100% cache miss on every single edit.
- **[CONFIRMED] Quadratic Re-Wrap Work:** On every cache miss, `wrap(runes, width)` executes from rune 0 across the entire unbroken string, invoking `uniseg.StringWidth` for every character. For 8,100 unbroken runes, profiling confirmed:
  - **CPU Profile:** 87.25% in `bubbles/textarea.wrap` (74.28% in `uniseg.StringWidth`).
  - **Memory Profile:** 60.13% in `wrap` allocations and 24.77% in `line.Hash` (SHA-256 string hashing).

### Architecture: Line-Wrap Cache & Dependency Adaptation

1. **Why an External Wrapper Was Insufficient:** `textarea.Model.Update` internally invokes private methods `cursorLineNumber()`, `LineInfo()`, and `repositionView()`. Every single keystroke forces multiple internal calls to `memoizedWrap`. An external wrapper around `textarea.Model` cannot intercept or memoize these internal wrap calls.
2. **Dependency Adaptation:** Minimal localized adaptation of `github.com/charmbracelet/bubbles` (tag `v1.0.0`, MIT licensed) vendored at `third_party/bubbles` and wired via `go.mod` replacement directive (`replace github.com/charmbracelet/bubbles => ./third_party/bubbles`). Upstream license and provenance are strictly preserved.
3. **Incremental Wrap Algorithm:**
   - Word-wrapping without lookahead is forward-causal: any wrapped row $i$ ends at `offsets[i+1] = offsets[i] + len(wrapped[i])`.
   - When text changes at or after `prefixLen = commonRunesPrefix(cachedRunes, newRunes)`, any row $k$ with `offsets[k+1] <= prefixLen` is completely unaffected by changes downstream.
   - By reusing rows $0 \dots \max(0, \text{editRow}-1)-1$ and wrapping only the remainder `newRunes[offsets[reuseRows]:]`, unchanged prefix rows are reused directly without re-measuring string widths.
   - For an unbroken 8,100-rune payload at width 76, rows 0..104 (~7,980 runes) are reused as-is; only the final row (~120 runes) is wrapped.
   - **Complexity Consideration:** While width calculation (`uniseg.StringWidth`) is bypassed for reused rows, `commonRunesPrefix`, `cloneRunes`, and row offset slice rebuilding still scan the rune slices and row offsets ($O(N)$ linear scan/copy), to which the cost of re-wrapping the affected downstream segment (`wrap(runes[reuseOffset:], width)`) is added. For trailing edits (e.g. Backspace at line end), the downstream segment is bounded by the tail wrapped row ($R_{\text{tail}}$ runes). If an edit occurs earlier in the buffer, re-wrap cost scales with the length and wrapping complexity of the unreused suffix ($N - \text{reuseOffset}$). Thus, per-operation complexity is $O(N) + \text{Cost}_{\text{wrap}}(\text{unreused suffix})$, rather than strictly bounded by tail-row width alone.
   - **Correctness Scope:** Equivalence to standard wrap has been verified across tested Unicode classes (unbroken ASCII, Arabic with and without harakat combining marks, Latin combining accents, CJK wide characters, single emoji, emoji with skin-tone modifiers, ZWJ sequences, regional indicator flag pairs, and mixed whitespace). This applies to tested classes and grapheme break behaviors handled by the underlying `uniseg` implementation, rather than an unconstrained mathematical claim for all arbitrary or future Unicode specifications.

### Benchmark Evidence (Android arm64, Linux 5.15.180, Go 1.27.0)

Measured via `go test ./internal/ui -run '^$' -bench '^BenchmarkComposerBackspaceOversized$' -benchmem -count=5` and analyzed with `benchstat`:
- **Operation Definition:** Each iteration prepares a fresh feed and recalls the $N$-rune input payload inside the benchmark loop, pausing the timer via `b.StopTimer()` during setup and resuming via `b.StartTimer()` exclusively for the $K$ sequential Backspace keystrokes (and optional `composer.view()`). This guarantees that input allocation and history recall overhead are excluded from the measured latency and allocation numbers.
- **UpdateView Benchmark:** Measures full keystroke handling followed by `composer.view()` (the active input field view rendering), not the outer `Feed.View()`.

| Benchmark Case | Baseline sec/op | Optimized sec/op | Time Delta | Baseline B/op | Optimized B/op | Mem Delta | Baseline allocs/op | Optimized allocs/op | Allocs Delta |
|---|---|---|---|---|---|---|---|---|---|
| **Axis A: N=1000, K=101** | 169.51 ms | 26.05 ms | **-84.63% (6.5x)** | 5.36 MiB | 1.51 MiB | **-71.84%** | 58,405 | 7,387 | **-87.35%** |
| **Axis A: N=2000, K=101** | 382.35 ms | 42.14 ms | **-88.98% (9.1x)** | 10.68 MiB | 2.60 MiB | **-75.66%** | 113,727 | 7,867 | **-93.08%** |
| **Axis A: N=4000, K=101** | 530.04 ms | 48.53 ms | **-90.84% (10.9x)** | 21.38 MiB | 4.78 MiB | **-77.63%** | 224,365 | 8,928 | **-96.02%** |
| **Axis A: N=8000, K=101** | 1010.99 ms | 88.64 ms | **-91.23% (11.4x)** | 42.03 MiB | 9.14 MiB | **-78.25%** | 445,380 | 11,118 | **-97.50%** |
| **Axis A: N=8100, K=101** | 1057.81 ms | 93.51 ms | **-91.16% (11.3x)** | 42.37 MiB | 9.15 MiB | **-78.41%** | 450,940 | 11,212 | **-97.51%** |
| **Axis B: N=8100, K=1** | 15.46 ms | 14.33 ms | ~ (p=0.222) | 736.6 KiB | 404.3 KiB | **-45.11%** | 8,857 | 4,474 | **-49.49%** |
| **Axis B: N=8100, K=3** | 55.01 ms | 15.65 ms | **-71.54% (3.5x)** | 1.56 MiB | 584.5 KiB | **-63.30%** | 17,754 | 4,621 | **-73.97%** |
| **Axis B: N=8100, K=20** | 136.09 ms | 28.28 ms | **-79.22% (4.8x)** | 8.65 MiB | 2.06 MiB | **-76.25%** | 93,197 | 5,694 | **-93.89%** |
| **Axis B: N=8100, K=101** | 1404.80 ms | 114.80 ms | **-91.83% (12.3x)** | 42.37 MiB | 9.15 MiB | **-78.41%** | 450,940 | 11,211 | **-97.51%** |
| **UpdateView: N=8100, K=101** | 1938.50 ms | 514.70 ms | **-73.45% (3.8x)** | 60.68 MiB | 29.16 MiB | **-51.94%** | 746,400 | 630,300 | **-15.56%** |
| **Arabic: N=8100, K=101** | 2955.60 ms | 114.40 ms | **-96.13% (25.9x)** | 82.58 MiB | 14.37 MiB | **-82.60%** | 707,900 | 16,322 | **-97.69%** |
| **Geometric Mean** | **392.70 ms** | **56.55 ms** | **-85.60%** | **14.13 MiB** | **3.85 MiB** | **-72.72%** | **152,600** | **12,180** | **-92.02%** |

### Resolution of Growth Shape Questions

1. **Axis A Scaling (Payload Length $N$ with fixed $K=101$ Backspaces):**
   - In the baseline, re-wrap cost scaled as $O(N)$ per keystroke. For a session of $K$ edits, cumulative work scaled as $O(K \cdot N)$. When $K$ is fixed (here $K=101$), work scaled linearly in $N$; if $K$ scaled with $N$ (e.g. clearing half an input of size $N$), total session work became quadratic $O(N^2)$.
   - With the line-wrap cache, repeated edits reuse unaffected prefix rows, avoiding $O(N)$ width re-computations. Increasing $N$ by 8x (from 1,000 to 8,000 runes) increases allocations across the 101 edits by only 1.5x (7.39k to 11.12k) instead of 7.6x (58.4k to 445.4k).
2. **Axis B Scaling (Keystroke Count $K$ with fixed $N=8,100$ Runes):**
   - In the baseline, each additional Backspace imposed a re-wrap cost of $\sim 14$ ms and $\sim 4,400$ allocations.
   - **Derived Estimates:** Calculating marginal slope between $K=1$ and $K=101$ (`(Value_{K=101} - Value_{K=1}) / 100`):
     - Baseline marginal cost: $(1404.80 - 15.46) / 100 \approx \mathbf{13.9\text{ ms}}$ and $(450,940 - 8,857) / 100 \approx \mathbf{4,421\text{ allocs}}$ per keystroke.
     - Optimized marginal cost: $(114.80 - 14.33) / 100 \approx \mathbf{1.0\text{ ms}}$ and $(11,211 - 4,474) / 100 \approx \mathbf{67\text{ allocs}}$ per keystroke.
     - This represents a derived **$\sim 14$x reduction in marginal keypress latency** and **$\sim 66$x reduction in marginal memory allocations**.
3. **Severe Workload Pass:**
   - The severe regression test `TestOversizedHistoryRecallEditableDown` ($N=8100$, $K=101$ real Backspaces through the limit boundary) passes deterministically in **$\sim 0.25$s** locally (down from several seconds that previously hung CI), and is guarded by `testing.Short()` when run with `-short`.


## U2: Provider-route presentation and observability (Issue #16)

- **Component:** `internal/presentation`, `internal/display`, `internal/ui`
- **Scope:** Observability of provider route failures and fallback selections across both Feed UI and Classic/Replay presentation paths.
- **Architecture & Invariants:**
  - Neutral sanitizer extracted into `internal/display` (`SanitizeForDisplay`, `DisplayPolicy`, secret redaction patterns) to maintain a strict unidirectional dependency graph: `internal/ui` -> `internal/presentation` -> `internal/display`, preventing package import cycles.
  - Single source of truth formatting implemented in `internal/presentation.FormatRouteNotice`:
    - `failed`: visible (`route failed: <provider>/<model> (attempt <N>): <reason>`).
    - `selected`: visible only when `Attempt > 1` (`route selected: <provider>/<model> (attempt <N>)`). Reason and StreamID are strictly omitted.
    - `attempted`, `exhausted`, `nil` route pointer, and unknown statuses: hidden (`"", false`).
  - Feed UI (`presentation.Projector`) maps visible route notices to `ItemNotice`.
  - Classic/Replay UI (`ui.RenderEvent`) formats visible route notices as notice blocks (`⚑`).
  - Notice badge (`⚑`) prefix ownership is strictly held by UI renderers (`feed_render.go` and `render.go`), never prepended by `FormatRouteNotice`.
  - Model context isolation preserved: `agent.Messages` continues to ignore `EventProviderRoute`.
  - Immutability: `FormatRouteNotice`, `Projector`, and `RenderEvent` treat `agent.Event` and `agent.ProviderRoute` as read-only.
- **Security & Sanitization:**
  - All displayed fields (`Provider`, `Model`, `Reason`) undergo secret redaction (Anthropic, OpenRouter, Groq, NVIDIA, GitHub, Bearer tokens) and terminal control sequence normalization (CSI, OSC8 hyperlinks, Bidi overrides, raw newlines).
- **Non-goals & Deferred:**
  - Router fallback status eligibility policy (treating 401, 403, 404, 429, etc. as eligible for fallback) is left unmodified in `internal/provider/router.go`.

## NBD-034: Composite read-credit key and modification-time validation (Issue #15)

- **Component:** `internal/agent`, `internal/tools`
- **Issue:** `Registry.metadata` stored a single global integer `linesRead`. When `read_file` executed, its line count was staged and subsequently consumed by the next `write_file` or `edit_file` via `ConsumeLinesRead()`. Because this credit was unbound to path, content hash, or line range:
  1. Reading file A and then mutating file B incorrectly attributed A's read line count to B's `EditRecord.ReadLines`.
  2. Reading a file that was subsequently modified externally on disk before mutation allowed the stale read credit to be claimed even though the model never saw the modified content.
  3. Creating a brand new file after reading an unrelated file falsely reported a non-zero `ReadLines`.

### Architecture & Resolution

1. **Composite Key (`agent.ReadCredit`):**
   - Introduced `agent.ReadCredit` containing `Path string`, `Hash string` (full-file SHA-256 hex at read time), `Offset int`, `Limit int`, and `LinesRead int`.
   - `read_file.run` computes the SHA-256 hash of the target file at read time and populates `agent.Outcome.ReadCredit`. Reads are result-scoped and do not mutate global registry slots directly.
2. **Audit Handoff via Agent Loop:**
   - The sequential agent loop (`Loop.runCalls`) observes successful `read_file` outcomes and stages the full credit via `Registry.SetReadCredit(out.ReadCredit)`.
   - Preserves `Registry.SetLinesRead(int)` for backward compatibility.
3. **Atomic Validation at Mutation Boundary (`commit`):**
   - In `commit()` (`internal/tools/write.go`), before committing an edit or write, the pre-mutation shadow content is inspected. If `!before.Absent`, the pre-mutation SHA-256 hash is computed.
   - `Registry.ConsumeLinesRead(abs, beforeHash)` validates the staged credit:
     - Target path mismatch (`credit.Path != "" && credit.Path != abs`): returns `0`.
     - Pre-mutation content hash mismatch (`credit.Hash != "" && credit.Hash != beforeHash`): returns `0`.
     - If both match (or if credit was unstaged), returns `credit.LinesRead`.
     - In all cases, staged credit is atomically reset to empty so it cannot leak to subsequent mutations.
4. **Parity and Cleanup Invariants:**
    - `Registry.ClearReadState()` completely resets the staged credit to empty (`agent.ReadCredit{}`).
    - Error and cancellation paths in `readFile.Run` and `readFile.RunDetailed` invoke `ClearReadState()`, preventing partial metadata leakage.
    - Regression coverage in `internal/tools/nbd034_read_credit_test.go` and `internal/agent/edit_record_loop_test.go` guards against cross-file attribution, external disk modification, brand-new file creation, and loop propagation.

## NBD-xxx: BPE tokenizer, calibration error, and adaptive read budget

- **Component:** `internal/token`, `internal/agent`, `internal/tools`, `cmd/ag`
- **Issue:** Token counting used a chars/4 (ASCII) + chars/1.6 (non-ASCII) heuristic with a stated 10-20% error, and `NABD_MAX_READ` was a fixed 3072 bytes — calibrated for one provider's 8000 tokens/minute ceiling. Reading one average source file cost many round trips, each spending turns and tokens to save tokens. The user had no visibility into whether the estimate was trustworthy this session.

### Architecture & Resolution

1. **Pure-Go BPE tokenizer (`internal/token`):**
    - `Tokenizer` interface with `Count(text string) int`. `HeuristicTokenizer{}` is the zero-value fallback (wraps the chars/4 rule via a registrable function — `agent.EstimateText` registers itself at init to break the import cycle).
    - `BPETokenizer` implements the standard tiktoken merge-loop: pre-tokenize, start each unit as byte ranks (0-255), repeatedly collapse the lowest-ranked mergeable pair. Merge table stored as `[]Merge{A,B,NewRank}` (3 integers/merge), NOT a 100k-entry string map.
    - `Registry` maps `"provider/model"` → `Tokenizer`; unknown key → `HeuristicTokenizer{}` (degrade, never fail).
    - `Budget.SetTokenizer(t)` installs a real tokenizer; when set, `Budget.Estimate` uses it instead of the heuristic. `agent.RegisterTokenizer(provider, model, t)` registers tables for known models.
    - **Binary-size budget:** the test encoder (`internal/token/ranks_test.go`, ~5 merges) adds ~2 KB. Production cl100k_base (~100k merges) would add ~300 KB compressed / ~10 MB uncompressed as a Go map. **Not vendored in this PR** — a phone-first project cannot ship a 40 MB binary for 15% accuracy. The architecture supports it (swap the embedded ranks table); the data is deferred.

2. **Calibration error exposure (`/ctx`):**
    - `Budget.Calibrate` now tracks `lastError` (|actual − estimated| / actual) and `worstError` (session max) for every valid observation.
    - `Budget.LastError()`, `WorstError()`, `Calibrated()` expose them. `/ctx` appends `· cal err +N%` when calibrated, `· cal uncalibrated` otherwise.

3. **Adaptive read budget:**
    - `Loop.readBudget(ms)` derives the per-call ceiling: baseline is the live-calibrated default (3072 bytes); `fraction = clamp(remaining/usable, 0.1, 1.0)`; `scale = 0.5 + fraction`; `budget = baseline × scale`, clamped to `[512, 1<<20]`. Empty context reads at 1.5× baseline (4608 bytes → fewer round trips); near-full reads at 0.6× (1843 bytes). A 40KB file reads in ~9 calls empty vs ~14 with the old fixed default.
    - `Loop.updateReadLimit(ms)` recomputes per turn; emits a `Notice` once when the ceiling changes (a limit that moves silently is a limit the user files a bug about).
    - `NABD_MAX_READ` set explicitly disables adaptation (constant function installed); unset → adaptive.
    - `readFile.limit func() int` field + `Registry.SetLimit(fn)` wire the loop's per-turn cap into the tool. Truncation logic (`TruncTail`) is unchanged — only the cap value varies.

### New config keys

| Key | Default | Meaning |
|-----|---------|---------|
| `NABD_MAX_READ` | (unset) | **Override**: disables adaptation, sets a fixed cap (existing semantics) |

(The TPM ceiling, overhead, and bytes-per-token are the measured constants already hardcoded in `read.go`; they are not yet exposed as config keys — the derivation converges on the fixed default without them.)

### Known gaps [DEFERRED]

- **Production encoder tables:** cl100k_base (~100k merges) is not vendored. The architecture supports it; the data is deferred to keep the binary phone-friendly. When a real table is available, register it via `agent.RegisterTokenizer("anthropic", "claude-sonnet-5", token.NewBPETokenizer(merges))`.
- **Provider rate-limit header parsing:** the TPM ceiling is the hardcoded 8000 (measured Groq value). Parsing provider-specific headers (Anthropic `anthropic-ratelimit-tokens-*`, OpenAI `x-ratelimit-limit-tokens`) to set it live is a follow-up that feeds the same derivation.

- **NARROW_OVR_12 — Overflow at widths below minViewportWidth:** `computeLayout`
  raises `TerminalWidth` to `minViewportWidth` (20) when the real terminal is
  narrower. The rendered frame (separators, footer text) is then wider than the
  actual terminal, causing overflow/clipping. Deferred because fixing it
  requires a design decision: clamp `m.width` to the real terminal size and let
  all chrome degrade at 16 cells, or keep the floor. Covered by
  `TestFrameHeightNarrowerThanMinWidth` (skipped) in
  `internal/ui/layout_contract_test.go`.

## PERF_CLAIM_1e6916f - refresh dirty detection is not a measured win

Commit 1e6916f is tagged `perf(ui):` ("derive refresh changes from rendered
output"). The tag is not supported by measurement. Manual runs of
BenchmarkRefreshStreaming (5 runs each, no benchstat) gave:

  ns/op    2366399 -> 1691751
  B/op      418235 ->  417873   (-0.1%)
  allocs/op   2070 ->    2062   (-0.4%)

The B/op and allocs/op deltas are within run-to-run noise, and the ns/op
delta was measured without benchstat on an unclean tree, so it is not
attributable to the change. The benchmark also calls renderItemsCached
directly instead of going through applyBatch/refresh, so it does not
exercise the path the commit touches.

Treat 1e6916f as `refactor(ui):` - it removes a slices.Clone/slices.Equal
pair in favour of an FNV-1a fingerprint of the rendered lines, which is a
correctness/clarity change. The performance question is still open and
needs a benchmark driven through applyBatch plus benchstat before any
perf claim is made.

## LOST_RESTORE_TEST - cmd/ag/restore_test.go deleted untracked

cmd/ag/restore_test.go was deleted in commit 88af924 as "orphaned" but
it was never tracked by git. The file is not recoverable from git
history — it existed only in the working tree and is gone permanently.

The file referenced makeRestoreHandler(loop, reg) which was never
implemented. If restore functionality is needed, it must be written
from scratch; there is no prior art in the repo to recover.

## MENU_ROW_ACCOUNTING_NOT_A_BUG - the divergence never existed

The branch narrative (d30551f "fix menu min-rows", 3292a5c "drop the slash
menu below its physical floor") describes an accounting bug: the menu
reserved 2 rows but rendered 3. Measurement contradicts this. slashMenuShape
predates the branch (present in d30551f~1) and is the single source of truth:
lineCount returns shape().rows, view() renders from the same shape, and at
rows=2 itemRows is 0 so view() emits header+footer only. Reserved 2, drawn 2.

What d30551f actually did was raise the floor from 2 to 3, which pushed chrome
above the terminal height at h=4 (composer 1 + footer 1 + menu 3 = 5 > 4) and
so caused the clamp that 3292a5c then handled by dropping the menu.

The resulting behaviour is kept, but on UX grounds rather than as a bug fix:
a 2-row menu is header plus footer with zero commands listed, i.e. chrome with
no content. Dropping it below three rows is the better degradation. This is a
design decision, not a defect repair, and TestClampNeverFires guards the
arithmetic either way.

Method note: the original finding was derived by reading lineCount and view
without reading shape() between them. Remaining items from the same UI audit
(prefix width in feed_render.go, hidden unseen counter, separator glyph
consistency) were derived the same way and are unverified. Each needs a
measurement independent of the helper under test before any code change.

## MODAL_FLOOR_OVERFLOW - permission modal overflows below five rows

TestClampNeverFires skips modal_and_menu at h in {2,3,4} (nine subcases). This
is a real limit, not a vacuous skip: modal 3 + composer 1 + footer 1 = 5, so a
four-row terminal overflows by one row and the defensive clamp fires. The
modal is not droppable the way the slash menu is, because it carries a pending
permission decision - dropping it would mean either a blind decision or a
silently withheld prompt. requiredFloor documents the boundary; terminals
shorter than five rows with a modal open are out of contract.

## MENU_IGNORES_NABD_ASCII_ONLY - ASCII fallback is not applied consistently

separatorLine honours NABD_ASCII_ONLY and falls back to '-', but
slash_menu.go:138 and :141 write U+2500 unconditionally. On a terminal that
sets the variable the feed separators degrade to ASCII while the command menu
stays Unicode. Unverified and untested; fixing it touches production code and
needs its own red case, so it is out of scope for the current test batch.

## TWO_INTERACTIVE_UIS - the layout work targets the experimental path

cmd/ag/main.go carries two interactive TUIs and a third replay model:
doChat (line 111) runs ui.Chat, doChatWithFeed (line 237) runs ui.Feed, and
--replay runs ui.NewReplay. The -feed flag defaults to false, so the default
interactive path is Chat, not Feed.

Everything measured and fixed in this batch - computeLayout, the slash menu
floor, visualRowsOf, the frame contract, separator width - lives in the Feed
path. Users on the default path do not see it. This is the right order for
promoting Feed to default, but it must not be described as a production fix.

Chat (internal/ui/chat.go, 284 lines) has no computeLayout, no frame contract
and no layout tests at all, while Feed (feed.go, 427 lines) now has both. The
callbacks are wired on both paths (Approve/OnUndo/OnRewind/OnCtx/OnEdits at
main.go:185-214 for Chat and :312-332 for Feed), so there is no functional gap;
OnRewind returns two strings on Feed versus one on Chat.

Open decision: promote Feed to default and retire Chat, or keep both and
duplicate every layout contract. Until it is decided, no layout finding should
be acted on without stating which path it applies to. The Replay model has not
been read at all.

## TWO_INTERACTIVE_UIS - correction to the entry above
Two absolute claims in the previous entry were written without measurement and
are retracted. "No functional gap" overstated slash_parity_test.go, which
builds both paths and compares the registered command set; full behavioural
parity is not established, and OnRewind returns two strings on Feed versus one
on Chat, so the contracts are not literally identical. "No layout tests at all"
for Chat should read: no frame-height or computeLayout contract was found for
Chat, which is not the same as no tests.

The Feed promotion gate is mostly automated already, not yet to be written.
real_tty_altscreen_test.go, pty_test.go and touch_test.go carry test *names*
covering alt-screen entry and exit, Ctrl+C exit, primary-screen restore,
20x12 without overflow, the permission modal, touch drag and NABD_NO_MOUSE.
Only the names were read, not the bodies. What looks genuinely unautomated is
narrow: text selection and copy inside the alternate screen, and Android
keyboard variance on Alt+Enter / Ctrl+J.

Two unverified suspicions, recorded as hypotheses: feed_test.go:251 compares
f.scrollTop against f.bottomStart(lm.ViewportRows), i.e. against the production
expression itself, and with three messages at height 10 both sides may be zero
so nothing is measured - the per-card line count was never measured, so this is
not asserted. And internal/ui/feed_layout.go holds bottomStart yet never
appeared in this batch's inventory of layout files, so the production-side
inventory is as incomplete as the test-side one was.

## READ_CAP_TURN_COST (NBD-400) - the shipped read defaults cannot read a mid-sized file in one run

NBD-400 reviewed the read_file cap and the MaxTurns default by measurement.
The result is a measured limitation, and the decision was to keep both
defaults rather than trade a bounded failure for an unbounded one.

Measured on an 800-line / 37014-byte Go-like fixture, driving the real
Registry through the real Loop with a scripted sequential reader (one
truncation segment per turn — the mechanical lower bound for a reader that
does not guess offsets; it is not a claim about model behaviour):

| cap   | calls | turns | fits MaxTurns=12 | longest call | tok_est | delivered |
|-------|-------|-------|------------------|--------------|---------|-----------|
| 3072  | 14    | 15    | no               | 3156         | 10838   | 41582     |
| 8192  | 5     | 6     | yes              | 8290         | 10299   | 40564     |
| 16384 | 3     | 4     | yes              | 16495        | 10178   | 40334     |
| 24576 | 2     | 3     | yes              | 24651        | 10118   | 40219     |

Reproduce: `go test ./internal/tools -run 'TestReadCapEval|TestReadCapPinsMeasuredTurnCost_NBD401' -count=1 -v`

Two of these invert the intuition:

- A smaller cap is not the cheap one in TOTAL tokens. It is the cheap one
  PER REQUEST: each truncation re-sends its tail, so 3072 delivers 1363 more
  bytes and ~720 more estimated tokens than 24576 for the same file.
- The real cost of a larger cap is the longest call — the per-request input a
  provider's TPM ceiling actually sees. The derivation in read.go is
  calibrated against an 8000 TPM free key, where the per-request input budget
  is already below 3072; a larger cap trades turns against 413s.

NBD-401 then measured what NBD-400 left open: the CUMULATIVE bill. NBD-400's
`delivered` column is bytes sent once; the provider bills every turn, because
each request re-sends the fixed prompt and the (squeezed) history. The
measurement drives the real Loop and sums the requests it actually sent, so
Squeeze, DedupeReadTails, keepFullRounds and the fence are the production
ones — not a rebuilt approximation. Same fixture, same caps:

| cap   | calls | delivered | cumulative_in | hist_in | cached_in | ratio  | cache_ratio |
|-------|-------|-----------|---------------|---------|-----------|--------|-------------|
| 3072  | 15    | 41582     | 58431         | 47136   | 15810     | 3.11×  | 1.43×       |
| 8192  | 6     | 40564     | 34626         | 30108   | 12814     | 1.84×  | 1.16×       |
| 16384 | 4     | 40334     | 26001         | 22989   | 11814     | 1.39×  | 1.07×       |
| 24576 | 3     | 40219     | 18769         | 16510   | 11023     | 1.00×  | 1.00×       |

Reproduce: `go test ./internal/tools -run TestReadCapCumulativeCost -count=1 -v`

**Correction (NBD-402).** As first published in NBD-401 the `cumulative_in`
column read 80286 / 43368 / 31829 / 23140 and the spread was 3.47×, because
the fixed per-request payload was taken from `readOverhead = 2210` — a figure
with no reproducible provenance. NBD-402 measured the payload on the wire and
the constant became 752; every row above is recomputed and the spread moved to
3.11× (−10.3%). The direction of every conclusion is unchanged, and the
monotonicity assertion was re-checked against the new column. `hist_in` never
depended on the constant and is unchanged.

**NBD-403** then took the constant out of this package entirely: the overhead is
now read from `internal/payload` at measurement time, and the eval loop sends
the shipped prompt rather than a stub. The rows moved by single digits
(58431 vs 58416) because the placeholder model differs by one token from the
captured one; the ratio is unchanged at 3.11×, and the assertion now also
checks the overhead against the same package's budget.

`cumulative_in` = Σ per-turn (promptOverhead + EstimateMessages(request));
`hist_in` omits the constant overhead to isolate what Squeeze decides;
`cached_in` applies the published cache-read discount to everything but the
newest message. The estimator (chars/4 ASCII) is a heuristic, and Budget.Ratio
— a single multiplicative calibration shared by every column — is left out
because it cannot change the ordering; these are relative figures, not
absolute token counts.

### Fixed per-request payload (NBD-402, restructured in NBD-403)

`promptOverhead` had no reproducible source. It does now. The measurement lives
in `internal/payload`, which is the single source both the cmd/ag guard and the
cumulative measurement read; `TestFixedPayloadHasASingleSourceInNonTestCode`
fails the build if a second definition appears in non-test source. The request
is built by the real session loop and captured at the transport, and
`TestFixedPayloadMeasurementsMatchTheWire` proves `payload.Encode` reproduces
that body byte for byte. Decomposition in the project's estimator units:

| format            | system | schema | model | framing | residue | encoded | code_owned |
|-------------------|--------|--------|-------|---------|---------|---------|------------|
| anthropic         | 61     | 660    | 3     | 28      | +1      | 752     | 750        |
| openai-compatible | 66     | 704    | 4     | 36      | 0       | 809     | 806        |

Reproduce: `go test ./cmd/ag -run TestFixedPayloadMeasurementsMatchTheWire -count=1 -v`

The framing differs between the two formats, which the single constant could
not represent. `model` is reported separately and excluded from the code-owned
total: the model name comes from configuration, not from code, and the budget
must not silently follow whatever a user sets.

`readOverhead = 2210` is ~2.9× the measured value and is now gone. NOTES.md
records that the 413 request bodies were never captured, the journal stores
neither the system prompt nor the tool schemas, and those sessions ran
MaxToks=4096 (before df48305), so the figure could not be reproduced from any
artifact in the repo. NBD-403 deleted the derivation that consumed it
(`defaultMaxReadDerived`, reachable only from a test) and recorded it here as
history. The derivation it implemented, verbatim from commit 1485e2d:

    safeInput      = tpmLimit − maxTok − overhead      // = 8000 − 1024 − 450 = 6526
    defaultMaxRead = safeInput × bytesPerTok × safety  // = 6526 × 3.2 × 0.5 = 10441
    at MaxTok=4096:  (8000 − 4096 − 450) × 3.2 × 0.5   // = 5526

Two corrections to the record, because the comment carried a second error.
First, the subtraction form above is the reference: it is what the code
implemented and what reproduces that commit's own stated outputs (10441 and
5526). The comment block had drifted to `(tpmLimit/maxTok − overhead) /
roundsPerMin`, which divides tokens/minute by tokens and then subtracts tokens
— dimensionally meaningless, and it evaluates to −1326. Second, the shipped
3072 was never the output of any form: 1485e2d's own comment says 5526 was
"clamped by live 413s down to 3072", and reproducing exactly 3072 would require
`safeInput ≈ 1920`, i.e. `maxTok + overhead ≈ 6080`, which no documented
constants satisfy. So the cap was a live observation, not a derivation — which
is why deleting the derivation changes no shipped value.

What the numbers say:

- The worst÷best spread is 3.11× uncached. A naive accumulation (no Squeeze)
  would be at least the request-count ratio, 5.00×. Squeeze does absorb part
  of it — the history-only spread is 2.85× — but it cannot touch the fixed
  per-request overhead, which is multiplied by the request count. The dominant
  term is therefore the number of round trips, not the size of the history.
- Prompt caching compresses the spread to 1.43×. It more than halves the
  penalty but does not remove it, and it only applies where the provider
  supports it; the OpenAI-compatible path's behaviour is exactly what NBD-430
  must establish before any policy is set on it.
- So the fixed cap is a real per-session cost on an uncached provider, not
  merely a turns problem.

### Budgets (NBD-403)

One ceiling was not a guard. NBD-402's single `1100` left ~290 tokens for a
system prompt the prompt spec puts at 800–1500, so it would have been raised
every stage; and a ceiling sized for the prompt leaves nothing for rules. There
are now two, each derived in code from named inputs and each owned by a
different party:

| budget | owner | derived from | value |
|---|---|---|---|
| `CodeBudgetTokens()` | the code: system prompt + schemas + framing | `round_up_100(systemPromptAllowance 1500 + recordedSchema 704 + recordedFraming 36 + recordedResidue 1)` | 2300 |
| `RulesBudgetTokens()` | the user: a project's AGENTS.md (NBD-410) | `round_down_100(measured 809 + allowance 2115 − codeBudget 2300)` | 600 |

The two partition one allowance. `FixedPayloadAllowanceTokens()` is how much may
be added to every request before the spread reaches the bound, and
`TestBudgetsAreDerivedAndConsistent` asserts that consuming both keeps the
spread inside it (2900 tokens → 3.595 ≤ 3.599). Rounding the code budget up and
the rules budget down is what makes the sum safe.

The rules budget has no consumer yet, deliberately: NBD-410 spends it instead of
inventing a ceiling.

### Read cap and turn ceiling: the policy (NBD-404)

The measurements above produced a decision.

**The cap now follows the provider.** `provider.ReadCapper` is an optional
interface; a provider that declares a ceiling is asked for it, and:

| source | when it applies | value |
|---|---|---|
| `NABD_MAX_READ` | whenever it is set | the operator's value; it outranks everything |
| the provider's declaration | otherwise | Groq 3072; Anthropic/OpenRouter/NVIDIA 16384 |
| `defaultMaxRead()` | no provider declares anything | 3072 |

A **Router takes the minimum over its routes**, because every request goes to
one route and the choice is made by fallback at runtime: a cap sized for the
most permissive route would be sent to the strictest one and trip its ceiling.
The cap is read from each route's provider object, never parsed out of a name —
`Router.Name()` is a composite display string and cannot express "strictest of
several". `TestProviderReadCaps`, `TestRouterReadCapIsTheStrictestRoute` and
`TestReadCapIsNotDerivedFromName` pin all of that, and
`TestReadCapPinsProviderIndependence_NBD401` still holds for the selection
string.

**16384 is a declared default, not a derived one.** No TPM measurement exists in
this repository for Anthropic, OpenRouter or NVIDIA, so the larger value is a
judgment: the constraint it stands in for — a per-minute input ceiling — is
absent, leaving the context window as the only bound. That is the permissive
direction, which is exactly why `NABD_MAX_READ` outranks it and why a custom
base URL pointed at a metered clone should set it.

**The turn ceiling is now 40** (`agent.DefaultMaxTurns`), and that is a
deliberate reversal of NBD-400's reasoning. At 12, a session reading a
mid-sized file spent every turn it had and returned ErrMaxTurns: the full cost
was paid and the task failed anyway. NBD-400 measured exactly that — 15 turns
needed for an 800-line file at the default cap — so the shipped pair could not
finish it.

The counter-argument has not gone away: this loop bounds **waiting** (the
rate-limit budget) and **context** (the window plus compaction), but nothing
bounds **spend**, so the ceiling was the only spend proxy. Raising it gives that
up knowingly: a looping model may now spend 40 turns. That is recorded as a
decision rather than left to read as an oversight. Reclaiming it means adding a
spend bound, not lowering the ceiling again.

`TestReadCapPinsMeasuredTurnCost_NBD401` now asserts both directions: the
shipped pair finishes the fixture, and the same cap at the old ceiling of 12
still does not. Its `fits_turns` column is named for what it measures — the
TURN budget — because whether the same schedule fits the provider's
tokens-per-minute ceiling is a different question, measured below.

### Does the shipped cap fit Groq's per-minute ceiling? (NBD-404)

The captured refusal, not an illustration: `~/.ag/sessions/20260901-133251.jsonl`
line 8 holds the real 413 —

    http 413: Request too large for model `qwen/qwen3.8-27b` … on tokens per minute
    (TPM): Limit 8000, Requested 8968, please reduce your message size and try again.

— and NOTES.md cites the same session for `Requested 8968`. That request was
made with `max_tokens=4096` (before `df48305`), so its prompt was ~4872 tokens;
NOTES.md records the governing rule, `prompt + max_tokens ≤ 8000`, applied
per request. With today's `max_tokens=1024` the input budget is therefore 6976.

`TestReadCapCumulativeCost` now reports the largest single request per cap and
compares it with that budget. Largest request and margin, estimated with the
project's own counter:

| cap | largest request | available | verdict |
|---|---|---|---|
| 3072 | 4902 | 6976 | under by 2074 |
| 8192 | 9386 | 6976 | OVER by 2410 |
| 16384 | 11105 | 6976 | OVER by 4129 |
| 24576 | 10991 | 6976 | OVER by 4015 |

Two estimates are being compared — the project's chars/4 counter and the
provider's own — so this is reported as a margin, not asserted as a pass. The
margins are large in both directions, so the direction is not in doubt even if
the counter is off by tens of percent.

The consequence is that **3072 is not conservatism on Groq; it is the largest
cap that fits.** Raising a Groq session to 8192 or beyond would trip 413s
rather than read more, which is what the cap was protecting against all along —
now measured rather than assumed. And it is exactly why the policy is keyed to
the provider: on Anthropic, OpenRouter and NVIDIA the same 8192 request has no
equivalent per-minute ceiling to hit, so the larger caps are usable there.

A live confirmation is still outstanding: this comparison is arithmetic over
estimates, not a captured 413 from the current cap. The per-request TPM
behaviour on a live Groq key at 3072 remains unverified, and the honest
statement is "under by an estimated 2074 tokens", not "safe".

### Constraint this places on NBD-410 (the rules layer)

With the measured figures, the spread as a function of what a rules layer adds
to every request, Δ:

    ratio(Δ) = (15 · (752 + Δ) + 47136) / (3 · (752 + Δ) + 16510)
             = (58416 + 15Δ) / (18766 + 3Δ)

It is monotone increasing in Δ with asymptote 5.00 (= 15/3, the request-count
ratio). Solving for the bound the code now uses, 3.5994:

    58416 + 15Δ = 3.5994 · (18766 + 3Δ)
    Δ = (18766 · 3.5994 − 58416) / (15 − 3 · 3.5994) ≈ 2115 tokens

That is the whole allowance, and `RulesBudgetTokens()` spends the part left
after the code's own budget: 600 tokens today. Concretely, +2048 tokens (an
8 KiB file) would cost the spread 3.58× — most of the headroom — which is why
the budget is 600 and not "8 KiB per file": that figure was invented, and this
derivation is what replaces it.

Two consequences for NBD-410:

- A project-instructions layer spends `RulesBudgetTokens()`; it must not define
  a second ceiling. If 600 tokens is too small for the intended feature, the
  change to make is the shared allowance — measured, in `internal/payload` —
  not a new constant next to the rules loader.
- Rules are re-sent every turn, so a per-file byte limit sits in the term that
  is multiplied by the request count. The unit that matters is tokens per
  request, which is what both budgets are stated in.

Decision: defaults unchanged. Raising MaxTurns would drop the turn ceiling
without putting any token or cost bound in its place (the loop's other bounds
— the rate-limit budget and the context window with compaction — bound
waiting and context, not spend). Raising the read cap would trade a bounded,
visible failure — the model is handed next_offset and the loop returns
ErrMaxTurns — for an unbounded, provider-specific one. Both escape hatches
stay documented and tested: NABD_MAX_READ and --max-turns.

The read cap also must not become provider-keyed: Router.Name() is a
composite display string, so any policy parsed from it would mis-key for the
multi-provider case. TestReadCapPinsProviderIndependence_NBD401 pins that the
cap depends only on the read and token settings.

## COMPACT_BOUNDARY_STALE (Finding 2) — reject boundaries removed by rewind

`Compact` picks a boundary from a snapshot, releases `l.mu` for the provider
summarisation call, then re-validates the boundary against the *fresh* history
under `l.mu` and appends the `Compact` event in the same critical section
(`internal/agent/compact.go`). If the selected `firstKept` is absent from the
current live branch, `Compact` returns `ErrCompactBoundaryStale` and appends
nothing.

The load-bearing defect is the `/compact × /rewind` overlap: `/compact` runs in
a detached goroutine that is **not** covered by the `m.running` interlock
(`cmd/ag/main.go`, `chat.OnCompact`). So a concurrent `/rewind` can remove
`firstKept` from the live branch while summarisation is blocked. Baseline
`Compact` (HEAD) searched the stale snapshot, never detected the loss, and
appended a `Compact` event whose `FirstKept` named an event no longer on the
live branch. The corrected `Compact` searches fresh history under `l.mu` and
rejects with `ErrCompactBoundaryStale`, appending nothing.

`Live()` (`internal/agent/event.go`) applies a `Compact` by flooring the live
branch at `FirstKept`. If that sequence is absent from the current branch, the
floor refers to an unreachable event — a journal may still hold it somewhere,
but it is not on the live projection.

Raw tool-event-pairing validation (`rawPairingInvariantHolds`) remains as
**defense in depth only**. Under current production ordering it cannot fire: a
compaction boundary is always a `UserMsg`, `Seq` increases in emission order,
and a `UserMsg` is emitted only at the top of `Loop.Run`, so no boundary can
land between a `ToolStart` and its `ToolEnd`. The orphaned-ToolEnd sequence is
only reachable through direct event injection or a `--continue` of a journal
that was already structurally corrupt. The harm prevented by raw pairing is a
fabricated `tool_use` attributed to the assistant — `Messages()` synthesises a
wire-valid `tool_use` for an unmatched `ToolEnd`, so the request is never
provider-rejected — not an orphaned `tool_result` the provider would refuse.

`ErrCompactBoundaryStale` is deliberately `errors.New`, so callers distinguish a
stale-boundary refusal from a provider or journal failure via `errors.Is`. The
manual `/compact` path surfaces only a status string today; wiring the sentinel
into user-facing text and a command-level `/compact` interlock are out of scope
here and recorded as follow-ups.

## SESSION_PATH_COLLISION (Finding 1) — PID+counter naming

New sessions use `sessionPathAt` → `newSessionName`: `<timestamp>-p<PID>-c<NNNN>.jsonl`.
The old naming (`<timestamp>.jsonl`) is unreachable for new sessions.

**Residual collision is PID-NAMESPACE scoped, not filesystem scoped.**
Two processes in separate PID namespaces (containers) sharing a session
directory via `--dir` on a common volume can hold the same PID and start in
the same millisecond — reproducing the original collision silently because
`store.NewJSONL` still opens `O_CREATE|O_WRONLY|O_APPEND` (no `O_EXCL`).
The collision manifests as duplicate Seq values and multiple Parent roots in
one journal, exactly as proven in E1. This residual is **collision-resistant**,
not collision-free.

Closing it requires exclusive creation on the new-session path only (a
separate constructor), which was deliberately deferred to keep this commit
revertable and to avoid changing the constructor shared with `--continue`.

The same-millisecond `'-'` (0x2D) sorts before `'.'` (0x2E), so a new
`<ts>-pPID-cNNNN.jsonl` sorts BEFORE a legacy `<ts>.jsonl` in `os.ReadDir`
order; `latestSession`'s reverse scan would prefer the legacy file if a
same-timestamp legacy sibling exists. Harmless in practice, ordering across
distinct timestamps is unaffected because the timestamp prefix is fixed width.
