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
- **per-call budget**: these limits are PER CALL, not global. Concurrent
  mutations multiply the allocation — N parallel edits each at the ceiling
  consume N× the budget. There is currently no aggregate ceiling across
  concurrent tool calls. [DEFERRED]
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
