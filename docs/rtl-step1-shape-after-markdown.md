# RTL Step 1 — Shape After Markdown (design memo, rev. 4)

Status: implemented in the working tree (uncommitted), awaiting review. The
fail-first baseline, the green run, the wrap probe and the randomized sweep
are captured under `docs/reports/raw/`. `shapeProseRuns` is wired into the
Feed Markdown path; `rtl.Layout` and the shaping engine are untouched.

## Problem (historical, fixed by this step)

`renderAssistant` shaped the whole prose segment before Markdown parsing
(`internal/ui/feed_render.go:123-125` called `shapeArabicText` before
`formatMarkdown`):

- **P1**: bytes inside inline `` `code` `` spans were shaped too, so the code
  carried presentation forms instead of the literal source.
- **P2-bold**: `**` boundaries broke joining (`**كت**ب` shaped ت into its
  final form because `*` followed it), and a Lam-Alef pair split by a style
  boundary never formed the ligature.

## Pipeline (as implemented)

```
logical + []rtl.Span (logical byte offsets, from parseInlineSemantic)
  ▶ 1. split into segments at every non-Prose span (LTR islands)
  ▶ 2. per prose segment: rtl.ShapeArabic(segment) → clusters (logical offsets)
  ▶ 3. per cluster: style = StyleID of the span covering SourceStart
       (Lam-Alef takes the FIRST char's style; the ligature is never split)
  ▶ 4. rebuild spans in visual-offset space (never remap old spans)
  ▶ 5. visual string + rebuilt spans → rtl.Layout (unchanged) → wrap → ANSI
```

Wiring: `parseInlineDisplay` (`internal/ui/markdown.go`) = parse + shape,
used by the heading, list and paragraph branches of `formatMarkdownVisual`;
fenced code never reaches it. ASCII-only mode (`NABD_ASCII_ONLY`) skips
shaping, preserving the previous guard. In Logical mode the logical renderer
is used and nothing is shaped, as before.

Step 1 scopes the cut at Code spans because the parser emits only those;
the implementation generalizes it to every span with `Kind != rtl.Prose`
(Path/URL/Command are reserved kinds, `internal/rtl/layout.go:43-49`) so a
future caller passing them gets island semantics for free — covered by
`TestL1_URLSpanIsLiteralIsland`. Islands are copied verbatim: never shaped,
kind and StyleID preserved (code inside bold stays bold).

## Characterized existing behaviour (not decisions of this step)

**C1 — Code/Path/URL/Command are LTR islands.** `rtl.Layout` gives non-Prose
spans an even embedding level and never breaks them
(`internal/rtl/layout.go:37-50`, even-level override in `buildLineBaseline`).
Content inside a `` `code` `` span is therefore never reordered. On Termux —
where the terminal applies no BiDi itself — an Arabic word inside backticks
renders in logical order, which an Arabic reader perceives as reversed, and
unconnected (no shaping). This is pre-existing engine behaviour; Step 1
neither decides nor changes it, and its test is labelled a characterization:
`TestL2_Characterization_CodeSpanIsLTRIsland`. **Open question, deliberately
unresolved:** is the requirement readability or fidelity for Arabic inside
code? Fidelity matters less than it looks — copy/search/replay already take
the logical source (`TestCopyUsesLogicalSourceWhenDisplayIsReordered`). If
readability wins, it needs its own documented decision and would change the
island contract, not this step.

**C2 — a long bold run wraps mid-word.** One styled run is ONE Prose span,
and every span is one atom for the wrapper, so a bold run wider than the
line is hard-filled at grapheme boundaries (probe block A:
`docs/reports/raw/run-20261011-1317-rtl-step1-d2wrap-probe.txt`; control
block C shows plain prose breaking at spaces with whole words). Pre-existing
and independent of shaping (reproduced with already-shaped input). Recorded
as TECH_DEBT `BOLD_RUN_WRAPS_MID_WORD` with an owner, a target (separate PR)
and a closure criterion; pinned by
`TestL2_Characterization_BoldRunWrapsMidWord`. Not fixed here on purpose.

## Decisions (D2, unchanged from rev. 3)

Rebuilt spans carry Code islands and **styled** Prose only; plain prose
clusters stay gaps (no span, StyleID 0). Reason: every span is an atom for
wrapping — "every span is one atom … Gaps between spans are prose with
StyleID 0 and are breakable" (`internal/rtl/layout.go:139-150`; `atomBounds`
at `internal/rtl/layout.go:691-705`). Emitting a Prose span per plain run
would make whole sentences unbreakable and change mid-word wrapping. Case 2
(`**كت**ب`) expects `[{0,6,Prose,bold}]`, not `[{0,6} {6,9}]`; case 4
(`ل**ا**`, ligature takes the plain Lam style) emits no span at all.

**D2 answered — can the wrapper cut a word at the span boundary?** Yes. At
width 2, `**كت**ب` splits between the bold span and the plain gap; at width 1
it also splits inside the bold span (documented overflow fallback). Golden:
`TestL2_NarrowWidthSplitsWordAtSpanBoundary` (widths 3/2/1); probe block B
shows the same break structure on the pre-Step-1 pipeline.

## Streaming (corrected during review)

- The Chat/Replay live tail (`partialTail`, `internal/ui/render_text.go`) is
  never shaped: it wraps (and may reorder) the raw buffer, and raw letters
  survive mid-stream — `TestL2_Protection_StreamingLiveTailIsNeverShaped`.
- The Feed does not use `partialTail`: every event batch re-renders the
  accumulated buffer through `renderAssistant` (`Feed.applyBatch` →
  `refresh`). Prefixes are therefore shaped after Step 1, deterministically:
  the same prefix always renders identically, and closing the markers
  converges to the final connected forms, identical to a direct render —
  `TestL2_StreamingClosedTextIsStable`. Forms do evolve as the prefix grows
  (isolated/final → medial); that is the accepted "jump", not instability.

## Test inventory

- **L1** (`shapeProseRuns` on parser output): Code-span literal + offsets
  (`TestL1_CodeSpanStaysLiteral`, `_CodeBreaksProseJoining` [3,7),
  `_CodeAfterLamAlefShrinksOffsets`, `_CodeAfterArabicShiftsOffsets`),
  joining across style boundaries (`_BoldBoundaryJoinsWord`), Lam-Alef style
  rules (`_LamAlefTakesBoldLamStyle`, `_LamAlefTakesPlainLamStyle`),
  diacritics (`_DiacriticsFollowTheirLetter`), literal/unclosed markers
  (`_UnclosedBoldIsLiteralProse`), list content (`_ListContentShapes`), URL
  text and URL island (`_URLInProsePassesThrough`, `_URLSpanIsLiteralIsland`),
  code inside bold keeps style (`_CodeInsideBoldKeepsStyle`), engine
  passthrough (`_NonArabicPassthrough`, `_PresentationFormsArePreserved`),
  parser kind contract (`_ParserEmitsNoReservedSpanKinds`).
- **L2** (`renderAssistant`, `NABD_RTL=reorder`): `_BoldStyleSurvives`
  (text + SGR), `_CodeSpanNotShaped`, `_CodeSpanIsLTRIsland`,
  `_LamAlefLigature` (text + SGR), `_CodeBreaksJoining`,
  `_HeadingBoldBoundary`, `_NarrowWidthSplitsWordAtSpanBoundary`.
- **Protections** (green on both pipelines): `_Protection_ListWithArabic`,
  `_UnclosedBold`, `_Blockquote`, `_TableRow`, `_FencedCodeBlock`,
  `_WrappedArabic`, `_StreamingLiveTailIsNeverShaped`.
- **Characterizations**: `_Characterization_CodeSpanIsLTRIsland` (C1),
  `_Characterization_BoldRunWrapsMidWord` (C2).
- **Tooling/invariants**: `TestSGRRunsTracksBoldState` (bold state machine,
  10 cases), `TestShapeProseRunsInvariants` (deterministic seed 20261011),
  `TestShapeProseRunsRandomizedSweep` (time-seeded; `NABD_RTL_SWEEP=<n>`
  raises the count for evidence runs), `FuzzShapeProseRuns` (15 seeds).
  The sweep asserts ordered, bounded, non-overlapping spans and literal
  island bytes.

## Golden cases (hand-computed; forms blessed in rev. 2)

| Input | L1 visual (logical order) | Rebuilt spans (visual offsets) |
|---|---|---|
| `` `مرحبا` `` | `` `مرحبا` `` | `[{0,12,Code}]` |
| `**كت**ب` | FEDB FE98 FE90 | `[{0,6,Prose,bold}]` |
| `**ل**ا` | FEFB | `[{0,3,Prose,bold}]` |
| `ل**ا**` | FEFB | `[]` (plain → gap, D2) |
| `ب`ت`ت` | FE8F `` `ت` `` FE95 | `[{3,7,Code}]` (was [2,6) logical) |
| `**كَ**تَبَ` | FEDB 064E FE98 064E FE90 064E | `[{0,5,Prose,bold}]` |
| `**كتاب` (unclosed) | `**` FEDB FE98 FE8E FE8F | `[]` |
| `- كتاب` (list content) | FEDB FE98 FE8E FE8F | `[]` |
| `` لا`x` `` | FEFB `` `x` `` | `[{3,6,Code}]` (offset shrinks by 1) |
| `` كتاب `x` `` | FEDB FE98 FE8E FE8F `` `x` `` | `[{13,16,Code}]` |
| `كتب https://example.com` | FEDB FE98 FE90 + literal URL | `[]` (URL is prose today) |

L2 (visual order, ANSI stripped): `` `مرحبا` `` → unchanged; `**كت**ب` →
FE90 FE98 FEDB with bold on the first two visual clusters; `**ل**ا` → FEFB
(bold); `ب`ت`ت` → FE95 `` `ت` `` FE8F; `**كت**ب` at width 2 →
FE98 FEDB ⏎ FE90.

## Evidence

- `run-20261011-1252-rtl-step1-failfirst.txt` — pre-implementation:
  the 15 designed failures with got/want; protections green.
- `run-20261011-1326-rtl-step1-green.txt` — implemented: gofmt/build/vet
  clean, focused suite all green, full `internal/ui` green, `internal/rtl`
  green, citation guard green.
- `run-20261011-1317-rtl-step1-d2wrap-probe.txt` — wrap probe (C2 + narrow
  width break structure).
- `run-20261011-1323-rtl-step1-fuzz.txt` — `-fuzz` is refused on
  android/arm64 ("-fuzz flag is not supported"); substitute: a 13-million
  input time-seeded randomized sweep (80.45s, seed 1791714285150583413), no
  invariant violation, so nothing was added to the corpus. The
  `FuzzShapeProseRuns` target remains for linux/CI.
- `run-20261011-1253-rtl-step1-package.txt` — pre-implementation package
  baseline and the local `-race` limitation (also android/arm64).
- Local `-race` and `-fuzz` are both unsupported on this device. CI covers
  race on linux: `.github/workflows/ci.yml` lines 76/78/83/89/119
  (`go test ... -race`) and `.github/workflows/ui-contracts.yml`
  (`go test -race ./internal/ui ./internal/presentation`). The fuzz target
  is runnable on linux with `go test ./internal/ui/ -fuzz FuzzShapeProseRuns`.

## Retired

- `shapeArabicText` (whole-text shaper) and `internal/ui/shaping_test.go`:
  replaced by `shapeProseRuns` and the suite above; the engine-level
  behaviours they covered live in `internal/rtl` tests, and the UI-level
  gates were re-pointed (`_NonArabicPassthrough`,
  `_PresentationFormsArePreserved`). The `Policy` comment in
  `internal/rtl/layout.go` now names `shapeProseRuns`.

## Out of scope (unchanged)

User echo (Step 2), docs/NABD_RTL contract (Step 3), Forms-A incl.
ZWNJ/Persian (Step 4).
