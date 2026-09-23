# nabd — نبض

وكيل برمجة طرفي يُكتب ويُشغّل من هاتف: ملف Go تنفيذي واحد، سجل جلسة JSONL قابل للتدقيق، أذونات افتراضية بالرفض، وتراجع مستقل عن Git.

**A terminal coding agent built on a phone.** One Go binary, an append-only event journal, default-deny permissions, and git-independent undo.

> Security and containment claims live in [docs/THREAT_MODEL.md](docs/THREAT_MODEL.md), not in this README.

The installable binary is `nabd`; the package path remains `./cmd/ag`.

## Releases

| Release | Status | Notes |
|---|---|---|
| `v2.0.0` | Published with signed `android/arm64` binary and Syft SBOM | Scoped exclusively to Termux (`android/arm64`) |
| `v1.5.0` | Published with binaries and `checksums.txt` | Last release supporting Linux & macOS desktop |
| `v1.4.0` | Published with binaries and `checksums.txt` | |
| `v1.3.0` | Published with binaries and `checksums.txt` | |

Download assets from [GitHub Releases](https://github.com/amiraq1/Nabd/releases). See [docs/RELEASING.md](docs/RELEASING.md) for the release process and verification.

## Installation (Termux)

### Option A: Official signed release (Recommended)

Download and verify the signed release artifact using Cosign:

```sh
V=2.0.0; B=https://github.com/amiraq1/Nabd/releases/download/v$V
curl -LO $B/nabd_${V}_android_arm64 -LO $B/checksums.txt \
     -LO $B/checksums.txt.sig -LO $B/checksums.txt.pem
cosign verify-blob --certificate checksums.txt.pem --signature checksums.txt.sig \
  --certificate-identity-regexp '^https://github\.com/amiraq1/Nabd/\.github/workflows/release\.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com checksums.txt
sha256sum -c checksums.txt --ignore-missing
install -m 0755 nabd_${V}_android_arm64 $PREFIX/bin/nabd
```

### Option B: Build from source

```sh
pkg install golang git
git clone https://github.com/amiraq1/Nabd && cd Nabd
./build.sh && mv nabd $PREFIX/bin/
```

## Build and run

```sh
go build -o nabd ./cmd/ag
# or: ./build.sh

./nabd                            # interactive feed in the current directory
./nabd --continue                 # resume the latest session
./nabd --replay <file.jsonl>      # replay a session; --speed 0 is instant
./nabd -feed                      # scrollable feed UI
./nabd --version                  # version · commit · date

./nabd connect <provider>         # store API key in ~/.ag/auth.json (hidden prompt, mode 0600)
./nabd models <provider>          # list live advertised models from provider endpoint
./nabd provider                   # list configured providers, sources, and key status
./nabd migrate                    # migrate legacy v1 config to ~/.ag/auth.json and providers.json
./nabd purge --dir <sessions>     # dry-run cleanup of session journals
./nabd purge --dir <sessions> --yes

./nabd -p "task text"             # headless answer on stdout
./nabd -p - < file                # task from stdin
./nabd -p "..." --json            # journal JSONL on stdout
./nabd -p "..." --max-turns 8
./nabd -p "count the go files" --permission-mode allow-reads
./nabd "review the plan" --permission-mode plan   # interactive, read-only

./nabd --export <file.jsonl>              # copy a journal to stdout, byte-for-byte
./nabd --export <file.jsonl> --redact     # redact recognized credential patterns
```

`--export` writes the journal as JSONL to stdout and exits; diagnostics go to stderr. Without `--redact` the source is copied verbatim (unknown fields, blank lines, and a truncated final line are preserved) and a stderr warning notes the output may be sensitive. With `--redact` the journal is re-encoded through the same redaction and encoding path as the live journal and `--json`: recognized credential patterns become `[REDACTED]`, but unknown JSON fields are dropped, a truncated final line is ignored, and unrecognized sensitive content stays cleartext. The source file is never written. `--redact` requires `--export`, and `--export` cannot be combined with any run mode (`-p`, `--continue`, `--replay`, `--feed`, `--json`, `--dir`, `--version`, and the headless tuning flags).

`nabd purge` lists regular `*.jsonl` session journals directly inside the
selected directory and performs a dry run by default. Pass `--yes` to delete
the listed files; `--before <RFC3339>` limits deletion by modification time.
It never traverses subdirectories or follows symlinks. Stop active nabd
processes before confirmed cleanup. At session startup nabd also prints a
notice that approved bash commands run with the full authority of the
Termux app user and there is no filesystem sandbox.

## Slash commands

In interactive sessions, commands start with `/`:

| Command | Usage | Description |
|---|---|---|
| `/undo` | `/undo [n]` | undo file edits recorded in the journal |
| `/rewind` | `/rewind [n]` | rewind conversation turns and restore prompt |
| `/ctx` | `/ctx` | show context window token usage |
| `/compact` | `/compact` | compact conversation history in background |
| `/edits` | `/edits` | list pending reversible file edits |
| `/help` | `/help` | show supported slash commands |
| `/connect` | `/connect <provider>` | store API key for provider in `auth.json` via hidden prompt (mode 0600) |
| `/models` | `/models <provider>` | query provider endpoint for live advertised models |
| `/provider` | `/provider` | show configured providers, definition sources, and credential status |

`/models` performs its probe off the UI event loop on both surfaces, so the interface keeps drawing and Ctrl+C keeps working while the request is pending. The surfaces differ in how they present the result: Chat is line-oriented and writes the model list, or an error summary of the form `nabd models: provider_<kind>: …`, to its status line; Feed posts the list as a feed notice and a failure as a four-field error card (code, details, action, wait). The command contract is shared; the result presentation is not identical.

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

The OpenCode registry stores provider definitions in `~/.ag/providers.json` and API credentials in `~/.ag/auth.json` (mode 0600). Builtin catalog providers default to modern endpoints (e.g. Groq defaults to `openai/gpt-oss-120b`). Providers and keys can be inspected and enrolled using `nabd provider`, `nabd models`, and `nabd connect` or their corresponding slash commands. The full `~/.ag/providers.json` schema, including `readCap` and `defaultModel`, is documented in [docs/CONFIG.md](docs/CONFIG.md) (section 8).

Do not put credentials in project files. See [docs/THREAT_MODEL.md](docs/THREAT_MODEL.md) for exact guarantees and residual risks. Report suspected escapes through [SECURITY.md](SECURITY.md), not a public issue.

## Headless behavior

No TTY is read. The default `--permission-mode` is `deny`; a tool that would prompt is denied as a tool result rather than blocking. `allow-reads` auto-allows only ReadOnly tools. `ask` still denies without reading a terminal. `plan` is strict read-only: every write and command is denied, overriding session grants and YOLO, so a headless plan-mode run can inspect the tree but never change it.

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
| android/arm64 | supported (Termux) |

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
- DNS resolution on Termux reads `$PREFIX/etc/resolv.conf` (which defaults to Google Public DNS `8.8.8.8`/`8.8.4.4` in Termux). This bypasses Android Private DNS and VPN-directed DNS. To use custom nameservers, configure `$PREFIX/etc/resolv.conf`. If `$PREFIX/etc/resolv.conf` is missing or empty, public DNS fallback requires explicit opt-in via `NABD_PUBLIC_DNS=1`.
- Session journals are redacted by default for recognized credential patterns,
  but journals and the shadow store still contain sensitive working data even
  with private filesystem modes. Set `NABD_REDACT_JOURNAL=0` only for a
  deliberate diagnostic run.

## License

MIT License. Copyright (c) 2026 nabd contributors. See [LICENSE](LICENSE).
