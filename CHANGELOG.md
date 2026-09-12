# Changelog

Release notes are generated from conventional commit history by GoReleaser.
Published changes and downloadable artifacts are available on the
[GitHub Releases](https://github.com/amiraq1/Nabd/releases) page.

## Unreleased

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
