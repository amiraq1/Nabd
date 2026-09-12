# Terminal UI release checklist

The UI is release-ready only when every item below is satisfied.

## Automated gates

- [ ] All Go sources are formatted.
- [ ] UI and presentation unit tests pass.
- [ ] Race tests pass for UI and presentation packages.
- [ ] Acceptance scenarios pass at widths 20, 39, 40, 79, 80, and 120.
- [ ] Viewport contracts pass at heights 8, 12, 24, and 40.
- [ ] `NO_COLOR=1` preserves visible semantic labels.
- [ ] Live and replay projections remain semantically identical.
- [ ] Help and diagnostics output never exceed terminal width.
- [ ] Replay benchmarks complete for 1,000 and 10,000 events.

## Manual smoke test

1. Send an Arabic prompt containing an English command and mixed-language path.
2. Expand and collapse a successful and a failed tool card.
3. Deny a permission request, then continue typing.
4. Trigger a recoverable provider error and verify the next action.
5. Enter navigation mode and visit the next error and last permission.
6. Open diagnostics with `Esc`, then `d`, and verify no prompt or tool content appears.
7. Repeat with `NO_COLOR=1` and at less than 40 columns.
8. Replay the journal and compare the semantic feed with the live session.

## Release rule

Do not release when the dedicated **UI release gate** workflow is red. Benchmark numbers are evidence for regression review; correctness, bounds, privacy, and race checks are blocking.
