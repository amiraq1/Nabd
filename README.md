# nabd — نبض

وكيل برمجة طرفي يُكتب ويُشغّل من هاتف: ملف Go تنفيذي واحد، سجل جلسة JSONL قابل للتدقيق، أذونات افتراضية بالرفض، وتراجع مستقل عن Git.

**A terminal coding agent built on a phone.** One Go binary, an append-only event journal, default-deny permissions, and git-independent undo.

> Security and containment claims live in [docs/THREAT_MODEL.md](docs/THREAT_MODEL.md), not in this README.

The installable binary is `nabd`; the package path remains `./cmd/ag`.

## Releases

| Release | Status |
|---|---|
| `v1.4.0` | Published with binaries and `checksums.txt` |
| `v1.3.0` | Published with binaries and `checksums.txt` |

Download assets from [GitHub Releases](https://github.com/amiraq1/Nabd/releases). Verify a downloaded release in the directory containing its assets:

```sh
sha256sum -c checksums.txt
```

See [docs/RELEASING.md](docs/RELEASING.md) for the release process.

## Build and run

```sh
go build -o nabd ./cmd/ag
# or: ./build.sh

./nabd                            # new conversation in the current directory
./nabd --continue                 # resume the latest session
./nabd --replay <file.jsonl>      # replay a session; --speed 0 is instant
./nabd -feed                      # experimental full-screen UI
./nabd --version                  # version · commit · date

./nabd -p "task text"             # headless answer on stdout
./nabd -p - < file                # task from stdin
./nabd -p "..." --json            # journal JSONL on stdout
./nabd -p "..." --max-turns 8
./nabd -p "count the go files" --permission-mode allow-reads

./nabd --export <file.jsonl>              # copy a journal to stdout, byte-for-byte
./nabd --export <file.jsonl> --redact     # redact recognized credential patterns
```

`--export` writes the journal as JSONL to stdout and exits; diagnostics go to stderr. Without `--redact` the source is copied verbatim (unknown fields, blank lines, and a truncated final line are preserved) and a stderr warning notes the output may be sensitive. With `--redact` the journal is re-encoded through the same redaction and encoding path as the live journal and `--json`: recognized credential patterns become `[REDACTED]`, but unknown JSON fields are dropped, a truncated final line is ignored, and unrecognized sensitive content stays cleartext. The source file is never written. `--redact` requires `--export`, and `--export` cannot be combined with any run mode (`-p`, `--continue`, `--replay`, `--feed`, `--json`, `--dir`, `--version`, and the headless tuning flags).

## Configuration

Config v1 uses `NABD_CONFIG` or `~/.ag/config`:

```sh
mkdir -p ~/.ag && touch ~/.ag/config && chmod 600 ~/.ag/config
cat >> ~/.ag/config <<'EOF'
NABD_PROVIDER=router
NABD_ROUTER_MODE=fallback
NABD_ROUTES=groq:model-a,openrouter:model-b:free,nvidia:model-c
GROQ_API_KEY=...
OPENROUTER_API_KEY=...
NVIDIA_API_KEY=...
EOF
```

Config v2 uses `NABD_CONFIG_V2` or `~/.ag/config.v2.json`. It is strict JSON, rejects unknown fields and trailing documents, requires explicit credential sources (`env` or an absolute secure file), rejects command credentials, and cannot be enabled together with v1. Custom `base_url` is deliberately unsupported by the minimal v2 schema.

Do not put credentials in project files. See [docs/THREAT_MODEL.md](docs/THREAT_MODEL.md) for exact guarantees and residual risks. Report suspected escapes through [SECURITY.md](SECURITY.md), not a public issue.

## Headless behavior

No TTY is read. The default `--permission-mode` is `deny`; a tool that would prompt is denied as a tool result rather than blocking. `allow-reads` auto-allows only ReadOnly tools. `ask` still denies without reading a terminal.

stdout contains only the final assistant text, or JSONL with `--json`. Notices and `session:` go to stderr. Sessions are still written under `~/.ag/sessions`, so `--continue` and `--replay` work.

| Exit code | Meaning |
|---|---|
| 0 | settled |
| 1 | provider or tool error |
| 2 | turn ceiling |
| 3 | rate-limit budget exhausted |
| 4 | permission denied with no model answer |
| 130 | interrupted |

## Supported platforms

| GOOS/GOARCH | Status |
|---|---|
| android/arm64 | reference (Termux) |
| linux/amd64 | supported |
| linux/arm64 | supported |
| darwin/arm64 | supported |
| darwin/amd64 | supported |
| windows/* | **not supported** |

Windows is not a release target. Some platform helper files compile there, but the agent's shell execution contract is Unix-oriented.

## Core architecture

- `internal/agent`: event contract, loop, history tree, compaction, budgets.
- `internal/config`: secure v1 and strict v2 configuration loading.
- `internal/store`: append-only JSONL journal.
- `internal/provider`: Anthropic and OpenAI-compatible providers plus ordered fallback router.
- `internal/tools`: path containment and read/write/edit/grep/bash tools.
- `internal/perm`: permission gate.
- `internal/snap`: content-addressed shadow store and undo.
- `internal/ui`: live display and replay.
- `cmd/ag`: wiring; the output binary is `nabd`.

The event is the contract: live rendering and replay consume the same append-only records. `/rewind` appends a new branch point rather than deleting history. `/undo` is intentionally separate and covers tracked file edits, not arbitrary approved shell effects.

## Operational limits

- Token counts are estimates until calibrated from provider usage.
- File reads are bounded; provider-specific limits and `NABD_MAX_READ` determine the cap.
- Compaction may call the model and falls back to a mechanical summary on failure.
- `bash` runs after explicit permission, outside path containment. Treat approval as access equivalent to the current OS user.
- Session journals and the shadow store contain cleartext working data and are sensitive even with private filesystem modes.

## License

MIT License. Copyright (c) 2026 nabd contributors. See [LICENSE](LICENSE).
