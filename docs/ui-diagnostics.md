# UI diagnostics dashboard

Enter feed navigation mode with `Esc`, then press `d` to open a local, read-only diagnostics snapshot for troubleshooting long terminal sessions.

The dashboard reports only aggregate UI metrics:

- projected item and rendered-row counts;
- line-cache entries and render-call count;
- terminal dimensions and responsive mode;
- follow, unseen, and tool-expansion state;
- counts of UI notices and diagnostics.

It never includes prompts, assistant text, tool arguments, tool output, file paths, error messages, credentials, or journal content. The snapshot is not persisted and does not modify the journal.

The output adapts to narrow, compact, and wide terminals and remains usable with `NO_COLOR`. Bracketed paste cannot trigger the shortcut.
