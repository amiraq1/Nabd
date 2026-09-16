# UI acceptance matrix

This matrix is the release gate for the terminal UI after phases 1–7.

## Default UI and rollback

The projected Feed UI is the default since the `@` picker shipped. The legacy
chat UI remains available via `--feed=false` at runtime — no recompilation, no
reinstall. This flag is the operational rollback path for at least one full
release cycle after Feed becomes default; `git revert` is a source-control
operation, not a user rollback.

| Condition | Behavior |
|-----------|----------|
| `nabd` (no flags) | Feed UI (default) |
| `nabd --feed` | Feed UI (explicit) |
| `nabd --feed=false` | Legacy chat UI (rollback) |

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
- `@` path picker: open, filter, select, and dismiss from the Feed composer.
- `@` path picker on a partial index: the picker must disclose truncation in its header.

## Invariants

1. Live projection and replay projection produce the same semantic feed.
2. Status is never conveyed by color alone.
3. Commands and paths stay copyable and are not reordered by hidden bidi controls.
4. The viewport never exceeds the configured width or retained-line cap.
5. Tool output remains summarized until explicitly expanded.
6. A retry never replays or approves a tool automatically.
7. The journal remains the full source of truth when UI retention is bounded.
8. `--feed=false` routes to `doChat`; the default routes to `doChatWithFeed`. The two entry points cannot be selected in the same invocation.

## Performance evidence

Benchmarks cover replay at 1,000 and 10,000 events. Existing streaming, full-screen view, Markdown, and render-item benchmarks remain part of CI's benchmark suite.
