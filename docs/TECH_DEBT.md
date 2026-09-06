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
