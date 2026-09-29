# RTL feed integration design

## Integration point

`internal/ui/rtl_layout.go` is the single adapter between structured UI text
and `internal/rtl`. Feed cards continue to own their semantic content, while
the adapter owns display policy, conversion to visual lines, and late style
emission.

## Pipeline

1. Journal and projector data remain logical source text.
2. The display boundary sanitizes untrusted text.
3. The minimal Markdown renderer emits logical text plus semantic
   `rtl.Span` values. It does not send ANSI escapes to the RTL engine.
4. `rtl.Layout` wraps on grapheme boundaries, resolves BiDi order, mirrors
   when requested, and returns source-mapped visual runs.
5. The UI adapter converts `StyleID` values to ANSI only after layout.

Plain feed text uses the same adapter through `wrap`. Existing callers that
already contain ANSI use the legacy ANSI-aware wrapper; structured Markdown
never takes that fallback.

## Source ownership

All public ranges are UTF-8 byte offsets. `internal/rtl` validates rune and
extended-grapheme boundaries. Copy, search, journal persistence, and replay
continue to use the logical `FeedItem` and `ToolCard` fields; visual text is
never written back.

Live events and journal replay already converge in `presentation.Projector`
and therefore use the same `renderItem` path.

## Display policy

`NABD_RTL` is an environment-only, process display policy:

- unset, `logical`, or `off`: legacy logical rendering;
- `reorder`: UAX #9 visual ordering without mirroring;
- `mirror`, `auto`, or `reorder-and-mirror`: visual ordering with contextual
  mirroring.

Unknown values fail closed to logical rendering. The selected mode is part of
the per-card render-cache key.

Arabic shaping is deliberately disabled. The adapter performs no joining-form
conversion, Presentation Forms substitution, or GSUB. A later change may add
an explicit shaping policy after Termux behavior is measured; it must not be
an implicit side effect of BiDi layout.

## Unchanged areas

- journal and event schemas;
- composer/input cursor behavior;
- Unicode conformance data and the corrected BiDi core;
- copy and search canonical source;
- provider, agent, and presentation projections.