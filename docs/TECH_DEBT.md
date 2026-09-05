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
  Safety margin: ~2x below the 2000x2000 observed cost; keeps the worst case
  well under phone per-app memory limits. Any edit exceeding it is rejected
  deterministically rather than risking OOM.
- `maxPatchBytes = 1 << 20` (1 MB) — raw unified-diff output. A 2000-line full
  rewrite is well under 100 KB; 1 MB gives >10x headroom.
- `maxEventBytes = 1 << 22` (4 MB) — serialized edit-event JSON. Embeds the 1 MB
  patch with JSON escaping overhead; if exceeded, the Patch is dropped and the
  audit fields (hashes, blobs) survive.

### Policy

`maxWriteBytes = 1<<20` (write_file) and `maxEditBytes = 2<<20` (edit_file) are
the on-disk output ceilings; edit_file additionally rejects a replacement whose
RESULT would exceed `maxEditBytes` (e.g. `all=true` with `new` larger than
`old`). edit_file input is also bounded by `maxEditBytes` (pre-read stat).

The 2000x2000 measurement is the calibration point; limits were chosen to keep
the matrix at or below that observed cost with margin, since cost grows as
O(n*m) beyond it. These values should be re-measured if workload characteristics
change.
