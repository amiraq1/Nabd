# Changelog

Release notes are generated from conventional commit history by GoReleaser.
Published changes and downloadable artifacts are available on the
[GitHub Releases](https://github.com/amiraq1/Nabd/releases) page.

## Unreleased

## v2.1.2

### Security
- The streamed redactor no longer cuts inside a complete recognized credential or configured exact key, so a secret split across chunks can no longer survive as an unredacted emitted prefix plus a held suffix. This closes an additional low-impact edge case at hold-back boundaries.

## v2.1.1

### Security
- Fixed streaming redaction edge cases at hold-back boundaries (see GHSA advisory).

### Fixed
- Feed session banner shows the project name instead of the journal file name.

## v2.1.0

- **Streamed-chunk redaction:** Secrets split across streamed chunks are now redacted in the journal, `--json` output, and the interactive feed. Streamed `text_delta` events are joined through a bounded hold-back (`internal/redact.Stream`) before they reach a sink, so a credential or PEM block split across two deltas is redacted as one value instead of surviving piecewise.
- Token runs longer than 4096 characters are replaced with `[REDACTED]` in the journal, `--json` output, and the interactive feed.
- Plain headless stdout is still printed verbatim.
- Journals written before this release may contain secrets split across chunks. Delete them or rotate the keys.
- Extended credential redaction (fix(redact)): Recognizes OpenAI keys (sk-proj-… and legacy sk-…), AWS access key IDs (AKIA/ASIA), PEM private-key blocks, and bare JWTs. Redaction remains best effort.
- Error cards with remedy guidance (feat(ui)): Provider errors, including endpoint_refused, show a remedy line in the interactive feed. Headless run_end distinguishes a stopped session (exit 130), a failed session, and a normal end.
- NABD_ASCII_ONLY: Environment variable that replaces decorative UI glyphs with ASCII (see docs/CONFIG.md).
- Desktop pointer corrected: Linux and macOS users should stay on v1.6.1, the last desktop release.
- YOLO no longer auto-approves executing tools. YOLO is not exposed through any flag or setting, so users see no change.

## v2.0.0

Scoped exclusively to Termux (`android/arm64`).

> **Notice:** Linux and macOS desktop users should stay on `v1.6.1`. `v2.0.0` removes the desktop targets and the Landlock sandbox in order to scope Nabd strictly to Termux on Android.

- **BREAKING: Scope restricted to Termux (`android/arm64`)**: Dropped desktop Linux and macOS build targets. All builds now target `android/arm64`.
- **BREAKING: Landlock bash sandbox removed**: Termux runs as an unprivileged Android application user where Landlock sandbox is unavailable. The Landlock sandbox implementation in `internal/sandbox` has been removed. Approved bash commands run with the full authority of the Termux app user.
- **Fail-closed on removed bash sandbox settings (`fix(config)`)**: Configuration keys `NABD_BASH_SANDBOX=on`, `NABD_BASH_NETWORK=deny`, and `NABD_BASH_RESOURCES=limit` (in v1 config, v2 config, or process environment) now fail closed immediately at startup across all interactive and headless entry points before any provider call or tool execution, with an actionable error. Neutral/permissive values (`auto`, `off`, `allow`, empty) emit a one-line warning on stderr.
- **Pure-Go DNS resolver for Termux (`fix(net)`)**: When compiled with upstream Go (`CGO_ENABLED=0`), Nabd now parses nameservers from `$PREFIX/etc/resolv.conf` (default `/data/data/com.termux/files/usr/etc/resolv.conf`) on Android, preventing DNS resolution failure. Public DNS fallback (`1.1.1.1`, `8.8.8.8`) is disabled by default and requires explicit opt-in via `NABD_PUBLIC_DNS=1`.
- **Guarded provider HTTP client by default (`fix(provider)`)**: `NewOpenAIDialect` and `NewAnthropicDialect` now construct providers with `Client: endpoint.Client(0)` by default instead of an unguarded `http.Client`. Redundant `controlledHTTPClient()` calls and manual `p.Client` assignments in `newroute.go` have been removed. Enforced in CI by `scripts/check-security-invariants.sh` and verified by `TestConstructorDefaultsToGuardedClient`.

## v1.6.1

Checksum signing restored in the release pipeline.

- **Release signing fixed (`fix(release)`):** `release.yml` now pins the cosign
  major (`cosign-release: v2.6.5`) that the `signs` block in `.goreleaser.yaml`
  targets. The cosign-installer bump to v4 had silently moved the runner to
  cosign v3, whose bundle format ignores `--output-signature` and
  `--output-certificate`, so `v1.6.0`'s release aborted with
  `create bundle file: open : no such file or directory` and `checksums.txt`
  was never signed. `TestReleasePipelineContracts` now fails if the workflow and
  the sign config drift apart on the cosign major again.
- **`v1.6.0` has no published release.** The tag exists but its signing step
  failed, so no release and no assets were ever created for it; do not expect
  `v1.6.0` assets. `v1.6.1` is the first published release after `v1.5.0`, so
  the BREAKING provider-endpoint policy documented under `v1.6.0` applies in
  full to anyone arriving from `v1.5.0`.
- **Release-dryrun signing gap recorded:** `release-dryrun` runs with
  `--skip=publish,sign,announce`, so the signing path is never exercised before
  a tag is cut. Recorded in `docs/TECH_DEBT.md` as `RELEASE_DRYRUN_SKIPS_SIGN`,
  with the reason the gap is accepted and the static guard that replaces it.

## v1.6.0

Provider endpoint policy, journal redaction, and crash-recovery hardening.

- **Crash recovery matrix:** added a subprocess-level regression harness that
  kills a process after a durable `edit_intent` and verifies
  `--continue`-path reconciliation classifies not-published, published,
  missing, and conflicting targets without automatic replay or filesystem
  writes.
- **Mutation publish failure matrix:** added regression coverage for file-fsync,
  rename, and parent-directory-fsync failures, plus the durable
  `edit_intent` → `edit_abort` path when a filesystem change occurs before
  publication. Pre-publish failures leave the target unchanged; post-publish
  durability failures remain classified as published for recovery.
- **Mutation recovery visibility:** `--continue` now reports unresolved
  mutation intents from the active branch without replaying them automatically;
  the operator must verify the working tree before proceeding.
- **Default journal redaction:** recognized credentials are now redacted from
  newly written journal events by default. `NABD_REDACT_JOURNAL=0` is the
  explicit diagnostic opt-out, and the setting can be stored in Config v1
  (`~/.ag/config`).
- **BREAKING: Provider endpoint policy (`feat(endpoint)!`):** `providers.json` and `NABD_BASE_URL` overrides enforce a two-layer endpoint acceptance policy by default. Layer 1 (load-time): `baseURL` must use HTTPS and must not be a loopback, RFC 1918, link-local, CGNAT, ULA, cloud-metadata, or `.internal`/`.local`/`localhost` address. Layer 2 (connect-time): the HTTP transport's `net.Dialer.Control` hook inspects the socket's literal address immediately before `connect(2)` after DNS resolution, eliminating the TOCTOU window and closing DNS rebinding completely across all inference routes and `/models` catalog probes. **Migration:** set `NABD_ENDPOINT_POLICY=loopback` for a local runtime such as ollama (HTTPS still required at the loopback address). Set `NABD_ENDPOINT_POLICY=open` to disable both layers entirely (not recommended for production). The default is `strict`. `THREAT_MODEL.md` updated: four GUARANTEED rows added, one REDUCED row for `NABD_ENDPOINT_POLICY=open`.
- **Endpoint policy hardening (`fix(endpoint)`):** Environment proxy URLs (`HTTPS_PROXY` / `HTTP_PROXY`) are now validated against the endpoint policy at request time, refusing unvalidated plaintext http or private proxy addresses under PolicyStrict. Provider constructors (`NewOpenAIDialect`, `NewAnthropicDialect`) now default to a guarded `endpoint.Client(0)`, preventing unguarded clients on direct constructor invocations. The deprecated `DialControl` wrapper has been removed in favor of `Policy.Dialer` / `endpoint.Client`, and `2002::/16` (6to4) is explicitly blocked.
- **Endpoint allowlisting and loopback refinement (`fix(endpoint)`):** Added `NABD_ENDPOINT_ALLOW` supporting explicit allowlisting of local/private endpoints (URLs, CIDRs, or host:port) while keeping default `PolicyStrict` active for all other cloud providers. Refined `PolicyLoopback` to permit plaintext HTTP strictly for loopback hosts (`127.0.0.1`, `::1`, `localhost`) so local runtimes like Ollama work without TLS, while non-loopback destinations still require HTTPS and cloud metadata (`169.254.169.254`, `100.100.100.200`), link-local, and 6to4 addresses remain refused at both load time and connect time.
- **Provider configuration schema documented:** `docs/CONFIG.md` now documents the complete `~/.ag/providers.json` schema (`api`, `name`, `options.baseURL`, `models.<key>.{name,id}`, `readCap`, `defaultModel`) with the rules enforced at load time and the documented precedence, and `README.md` points at it.
- **Dependency tree narrowed:** the hidden API-key prompt now reads the terminal through `github.com/charmbracelet/x/term`, which the TUI stack already carried indirectly, so `golang.org/x/term` is dropped from `go.mod` and `go.sum` entirely. Non-TTY behaviour is unchanged.
- **Source-inspection guards hardened:** the three write-side contract tests in `internal/tools/write_commit_source_test.go` (`TestWriteCommitDelegatesToAdapters`, `TestWriteCommitUnixAdapterIsDescriptorOnly`, `TestWriteCommitOtherIsCompatibilityPath`) now assert on the parsed AST instead of raw source text, so innocent refactoring and variable renames no longer break them and comments can no longer fake a required call. Test names are unchanged. Recorded in `docs/TECH_DEBT.md`.

- **Replay output ordering:** fixed a race condition where replay printed lines out of order in roughly one run in eight due to concurrent command batching; sequenced execution (`tea.Sequence`) now structurally guarantees print delivery before the next event tick.
- **Event rendering:** Completed file reads (`EventRead`) no longer fall through to produce a spurious unknown-event marker (`· read_record`); truncated reads continue to display their standard warning.
- **Event rendering (A2):** The remaining five known event types (`TextDelta`, `EventProviderUsage`, `EventSkillBody`, `EventSkills`, `Rewind`) now have explicit rendering cases. `EventProviderUsage` is emitted once per successful request, so the spurious `· provider_usage` line was visible in essentially every session. Rewind events now display `── rewind` (or `── rewind to #N`) and skill-load events display a count summary (`⚑ N skills · M project`). The hand-written guard test is replaced by a source-derived coverage guard that parses `internal/agent/event.go` at test time, so future event types cannot silently fall through.
- **Feed Ctrl-C hint:** Clearing non-empty composer input via Ctrl-C now displays the status hint "input cleared · press ctrl+c again to quit" to guide exiting.
- **Deterministic Ctrl-C cancellation policy (ADR-0001, Rule 12):** Enforces a deterministic 6-barrier cancel hierarchy (cancelling secret prompts, in-flight runs, and search before clearing composer input or exiting) with comprehensive contract test matrix; clearing composer input displays the hint "input cleared · press ctrl+c again to quit".
- `--permission-mode` now applies to the interactive TUI as well as headless runs, and
  accepts a new `plan` mode: strict read-only that denies every write and command even when
  a session grant or YOLO would allow it. The interactive default stays `ask` and the headless
  default stays `deny`, so an empty flag changes no existing behaviour.
- The interactive session (root, registry, policy, approver, loop, and the five slash-command
  callbacks) is now built by one `interactiveSession` helper used by both `Chat` and `Feed`,
  removing the duplicated wiring in `cmd/ag/main.go`. `Chat` and `Feed` share a single
  `ui.SessionCallbacks` contract, so `/rewind` has one signature on both paths.
- **Groq catalog updated:** the default model is now `openai/gpt-oss-120b` (replacing `qwen-2.5-32b`), and `llama-3.3-70b-versatile` was dropped from the builtin catalog; users with `NABD_MODEL` set to dropped models should update to `openai/gpt-oss-120b` or run `nabd models groq` to select an available alternative.
- **OpenAI wire compatibility (`compat`):** `compat.temperature` is now a tri-state type supporting numbers in `[0, 2]`, `"omit"` (omits the `temperature` field from the wire payload for `o1`/`o3`-class reasoning models that reject explicit values), or absent (preserving the `0.2` baseline). Existing `"temperature": null` values now silently degrade to absent (sending `0.2`) rather than causing a decode error. Setting `compat.includeUsage: false` now completely omits the `stream_options` object from the request.
- **The dialect is a fact a model may declare (stage 6):** `Provider.API` is renamed `Provider.DefaultAPI` because its meaning changed — `api` can now be declared per model in `providers.json` (`"models": {"claude-sonnet-5": {"api": "anthropic"}}`), and the model wins. One providers.json entry can now serve an OpenAI-dialect and an Anthropic-dialect catalog on one base URL and one key; `nabd provider add --model` is repeatable and accepts `"key=anthropic"`. Load-time rules apply to the set of declared dialects: the base URL must be legal for all of them, a declared `options.auth` must be accepted by all of them (an absent one resolves per model), and `compat` is judged against the model's resolved dialect. `nabd models` still asks with the provider's default dialect — documented in TECH_DEBT as `CATALOG_FOLLOWS_DEFAULT_API`, not an oversight.


- **Touch drag-scroll and navigation keys:** Pointer drag now scrolls the feed viewport by vertical motion delta; Home and End keys scroll the viewport or jump card selection in navigation mode; viewport top padding on short content remains stable when clearing follow.
- **Clipboard transport in the feed is environment-gated (documented behaviour, review item N1):** OSC 52 is written to stdout only in remote sessions (`SSH_CONNECTION` set) or to an explicitly supplied writer (tests); on Termux the redacted payload is piped to `termux-clipboard-set` asynchronously with a 10s deadline; every other environment reports `copy unavailable` rather than emitting an escape sequence into a local terminal. Credential redaction and display sanitization always run before any transport. See `docs/reports/ui-parity.md` §8.
- **Clipboard export rescue and error classification (N2, N3, F12):** Failed clipboard deliveries rescue redacted text to private files reporting "report saved: <path>" or explicitly flag unrescuable failures with "export failed; text not saved" rather than staying silent; unexpected clipboard command failures are classified as "termux-clipboard-set failed; check the termux-api install".

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
