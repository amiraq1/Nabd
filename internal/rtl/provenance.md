# internal/rtl provenance and temporary-fork removal plan

Status: **temporary, controlled fork** (Spike 3.2 verdict D; G1 approved with a
limited scope). This file records exactly what was copied, what was changed,
and the conditions under which the fork must be removed.

## Source

| Item | Value |
|---|---|
| Upstream module | `golang.org/x/text` |
| Version | `v0.42.0` (`h1:JbOZXgfeCPU9gacVtYliJqOhD+zhrEqK4LfdpmlUZqI=`) |
| Upstream commit | `fafe4a06967e06550e69ee42787d9902845d2a3f` (`refs/tags/v0.42.0`, go.googlesource.com/text) |
| License | BSD-3-Clause (`LICENSE.xtext`, sha256 `911f8f5782931320f5b8d1160a76365b83aea6447ee6c04fa6d5591467db9dad`) |
| Unicode version | **17.0.0** (`UnicodeVersion` constant in `bidi/tables17.0.0.go`) |
| Package | `golang.org/x/text/unicode/bidi` |

## Copied files (verbatim; `cmp` compared against the module cache)

| File | sha256 | State |
|---|---|---|
| `bidi/core.go` | `ef15872f0cac7702bba67bbba334c4fc85376869e18fadec40e646f1ba8c4493` | identical to upstream |
| `bidi/bracket.go` | `5caa24f9c4e04a54d18875468f976b4b7c09c8700e88286f2fd96828f134107e` | identical to upstream |
| `bidi/prop.go` | `f6391b2f69a1ae2a0ac1f0599a90b260ec350a19f73ad5d6572660100b5950bc` | identical to upstream |
| `bidi/trieval.go` | `bf3e17fca178c7d13139aa7cb7828005a0a1b8f4dca8ef97966236c6469a2136` | identical to upstream |
| `bidi/tables17.0.0.go` | `a49eafd4eb11f7c8ae810ee5d69ca59a0cddd97dc644afbcb3e40563617088fc` | identical to upstream |

**Not copied, deliberately:** `bidi.go` (the historical `Paragraph`/`Ordering`
public API), `tables15.0.0.go` (Unicode 15 tables), upstream tests and
generators. The repository targets Unicode 17 only.

`bidi/tables17.0.0.go` keeps its upstream `//go:build go1.27` tag. The module's
`go` directive is `1.27.0`, so the file is always compiled; a pre-1.27
toolchain fails loudly instead of silently falling back to Unicode 15.

## Modifications

Upstream files are unmodified. The fork differs from upstream only by the
**added** file `bidi/engine.go`, which:

1. exposes `Analyze`/`AnalyzeClasses` over the internal paragraph engine
   (levels after rule L1, rule L2 visual order, resolved paragraph level);
2. builds bracket identifiers (BD16) with the fix that upstream's
   `bidi.go:119` is missing:

```go
// upstream bidi.go:119 (public Paragraph only): closing bracket gets the raw rune
p.pairValues = append(p.pairValues, r)

// bidi/engine.go: closing bracket gets the canonical mirrored opener
p.pairValues = append(pairValues, canonBracketRune(props.reverseBracket(r)))
```

3. canonicalises the two bracket runes with singleton canonical
   decompositions (`U+2329 → U+3008`, `U+232A → U+3009`), exactly the
   normalisation performed by upstream's own conformance test
   (`core_test.go`), so every canonical-equivalent pair in
   `BidiBrackets.txt` unifies.

Without these two lines the engine fails 14,551 of the 91,707
`BidiCharacterTest` cases (every bracket pair) and the four residual
canonical-equivalence cases; with them it passes 91,707/91,707 and
770,241/770,241 `BidiTest` runs (see `cmd/rtlconformance`).

## Why the upstream public API is not used

- `Paragraph.Order()` never matches a closing bracket to an opener because of
  the `bidi.go:119` `pairValues` defect, so rule N0 is effectively skipped.
- The public API cannot force LTR for text containing RTL strong characters
  (only `DefaultDirection(RightToLeft)` sets a level), and it does not expose
  per-rune levels at all.
- Its run model concatenates runs by direction parity; runs may mix levels so
  rule L2 output is not always reconstructible, which the documented Spike 3.2
  analysis quantified (34,320 of 677,752 applicable `BidiTest` runs).

Upstream itself describes the package as under construction and offers no
final API for visual ordering.

## Upstream issue draft

An issue draft with a public-API minimal reproducer
(`BidiCharacterTest-17.0.0.txt` line 403, `a(b)` under a forced-RTL paragraph)
exists and is **not published**:

- Title: "unicode/bidi: Paragraph.Order() visual runs ignore paired brackets
  because closing-bracket pairValues use the raw rune"
- Artifact: Spike 3.2 `UPSTREAM_ISSUE_DRAFT.md`
  (sha256 `52492f48573c4d1fd0927463f33e4c879e333597a70c997324a3d64e0d322705`)
- Filing requires explicit owner approval; once filed, replace this paragraph
  with the issue URL.

## Unicode data files (testdata)

`testdata/unicode/17.0.0/` holds the official files used by the conformance
gate. The two large corpora are stored **deterministically gzip-compressed**
(`gzip -n -9`: no mtime, no name), so the repository carries the official bytes
without the multi-megabyte plain text; the conformance command streams and
decompresses them in memory and verifies the decompressed SHA-256 before any
gate runs.

| File | sha256 (decompressed / as stored) |
|---|---|
| `BidiCharacterTest.txt.gz` | `a3e6e905ab5afbe318a96df5401d0372a04cd73ef139ab5e3cf0ae241c255488` (official bytes; gz artifact `cbc1b3234d027dbcdd1383d1777d239e4b6e84ff91a0c5b665a95f00d1539e39`, 400,915 B) |
| `BidiTest.txt.gz` | `888bdfc8090652272d1f859cdb00ae659e2dc6c26740be61ef1d03998a687620` (official bytes; gz artifact `8c6423f74aab86045ec1b4283654f235b18ae548f91975854c41524383453345`, 1,315,854 B) |
| `BidiBrackets.txt` | `dadbaf38a0d0246e5b805bf8725cb81b7c621f93d030595635f5ba2c2f179428` (plain text) |
| `BidiMirroring.txt` | `a2f16fb873ab4fcdf3221cb1a8a85a134ddd6ed03603181823ff5206af3741ce` (plain text) |

The decompressed hashes above are the authoritative data hashes and do not
change when the storage format changes.

## Table update procedure

1. **x/text tables** — copy the five files above from the new upstream tag into
   `internal/rtl/bidi/` unchanged, record the new version/commit/hashes here,
   and re-run `go run ./cmd/rtlconformance`. The gate must stay at
   `BidiCharacterTest 91707/91707` and `BidiTest 770241/770241` (thresholds are
   pinned in the command).
2. **`mirror_table.go`** — regenerate from the `BidiMirroring.txt` of the same
   Unicode version: parse `code; mirror` pairs, sort by source code point, emit
   `{0xXXXX, 0xYYYY},` entries into the existing array literal, and keep the
   header comment's sha256 current. The file's entry count must equal the data
   file's entry count; the Mirroring gate must stay `428/428` for 17.0.0.
3. **testdata** — refresh the four files together with the tables, compress the
   two large ones with `gzip -n -9`, update the hashes above, and re-run the
   conformance command. Never substitute the gzip artifact hash for the
   decompressed data hash.

## Arabic shaping gate for PR 2

Measured baseline (recorded in the Spike environment evidence): the Termux
default terminal renders Arabic with **no shaping and no bidi** — letters stay
isolated and logical order is not reordered — which is why this engine exists
in the first place.

- PR 1 implements bidi levels (through rule L2), cluster-safe rule L3 and rule
  L4 mirroring only. It performs **no Arabic shaping**: no joining forms, no
  presentation forms, no GSUB.
- PR 2 must **not** merge before a shaping mode is chosen and tested
  (`ShapingMode`); `ReorderAndMirror` on its own must never be presented as a
  complete Arabic solution.
- Candidate modes for that decision: `ShapingOff` and
  `ShapingArabicPresentation`. The choice must follow a measurement of the
  Termux rendering path so that shaping is applied exactly once.
- A double-shaping test is required: when the terminal (or font stack) already
  shapes, the engine must not apply presentation forms a second time.
- Shaping will be an independent `Policy` field, not a `Mode` value, so callers
  opt in explicitly.
- `RestoreFromSource` remains the only logical copy path. Presentation forms
  must never enter the journal, search indexes, or any copy source; they are
  display-only.

## Removal condition (fork must not become permanent)

Replace `internal/rtl/bidi` with a dependency when **either**:

- upstream `golang.org/x/text` ships the `pairValues`/canonicalisation fix and
  exposes what `Layout` needs (per-rune levels and a way to force the paragraph
  direction); or
- a maintained pure-Go engine passes the full gate.

Acceptance for removal: `cmd/rtlconformance` reports PASS with no thresholds
lowered, and `internal/rtl` tests are green. The semantic Layout behavior
remains stable. API signatures may evolve until PR 2 integration is complete.
Delete `LICENSE.xtext` and this file's fork sections in the same change. The
Unicode data files may stay (they are plain data with a stable format).

## Impact record

- `go run ./cmd/rtlconformance`: verifies the decompressed SHA-256 of all four
  corpora (two streamed from deterministic gzip), then
  `BidiCharacterTest 91707/91707`, `BidiTest 770241/770241`,
  `BidiBrackets 1152/1152`, `Mirroring 428/428`, ~6.6 s on the development
  device.
- Binary size (`CGO_ENABLED=0 GOOS=android GOARCH=arm64`,
  `-trimpath -ldflags="-s -w"`): cmd/ag is unchanged in PR 1 (+0 bytes; the
  engine is not linked yet). A minimal program importing `internal/rtl` grows
  by 196,608 bytes (~192 KiB).
- Benchmarks (android/arm64, semantic-span pipeline): `Layout` mixed content
  ~180 µs/op (51.6 KB, 139 allocs), ASCII fast path ~102 µs/op, logical mode
  ~216 µs/op.
