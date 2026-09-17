# Changelog

Release notes are generated from conventional commit history by GoReleaser.
Published changes and downloadable artifacts are available on the
[GitHub Releases](https://github.com/amiraq1/Nabd/releases) page.

## Unreleased

- `--permission-mode` now applies to the interactive TUI as well as headless runs, and
  accepts a new `plan` mode: strict read-only that denies every write and command even when
  a session grant or YOLO would allow it. The interactive default stays `ask` and the headless
  default stays `deny`, so an empty flag changes no existing behaviour.
- The interactive session (root, registry, policy, approver, loop, and the five slash-command
  callbacks) is now built by one `interactiveSession` helper used by both `Chat` and `Feed`,
  removing the duplicated wiring in `cmd/ag/main.go`. `Chat` and `Feed` share a single
  `ui.SessionCallbacks` contract, so `/rewind` has one signature on both paths.
- **Groq catalog updated:** the default model is now `openai/gpt-oss-120b` (replacing `qwen-2.5-32b`), and `llama-3.3-70b-versatile` was dropped from the builtin catalog; users with `NABD_MODEL` set to dropped models should update to `openai/gpt-oss-120b` or run `nabd models groq` to select an available alternative.


## v1.5.0

Descriptor-relative file access on Android/Termux (Phase 3).

- `read_file`, `glob`, `grep`, `write_file`, `edit_file`, and `/undo` open through the
  relative path from a root descriptor with `O_NOFOLLOW` and act relative to the parent's
  descriptor, closing the resolve-then-open race. The absolute path is reporting metadata only.
- `glob` and `grep` no longer surface symlinked entries, and `.ag` (the shadow store) is
  excluded from both, so deleted content cannot be read back through a traversal tool.
- **Behavioural change:** `glob` no longer lists symlinks.
- Supply-chain signing, SBOM, provenance, dependency automation, CodeQL, and governance hardening.

## v1.4.0

See the [v1.4.0 release](https://github.com/amiraq1/Nabd/releases/tag/v1.4.0).

## v1.3.0

See the [v1.3.0 release](https://github.com/amiraq1/Nabd/releases/tag/v1.3.0).
