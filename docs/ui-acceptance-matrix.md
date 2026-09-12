# UI acceptance matrix

This matrix is the release gate for the terminal UI after phases 1–7.

## Widths

Every scenario is exercised at 20, 39, 40, 79, 80, and 120 columns. No rendered line may exceed the terminal width.

## Required scenarios

- Arabic user message with a mixed Arabic/Latin path.
- Multiline assistant response.
- Truncated `read_file` result with `next_offset` guidance.
- Failed `bash` tool.
- Denied permission request.
- Recoverable provider error with a visible next action.
- Color-disabled semantic states.

## Invariants

1. Live projection and replay projection produce the same semantic feed.
2. Status is never conveyed by color alone.
3. Commands and paths stay copyable and are not reordered by hidden bidi controls.
4. The viewport never exceeds the configured width or retained-line cap.
5. Tool output remains summarized until explicitly expanded.
6. A retry never replays or approves a tool automatically.
7. The journal remains the full source of truth when UI retention is bounded.

## Performance evidence

Benchmarks cover replay at 1,000 and 10,000 events. Existing streaming, full-screen view, Markdown, and render-item benchmarks remain part of CI's benchmark suite.
