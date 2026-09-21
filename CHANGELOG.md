# Changelog

Release notes are generated from conventional commit history by GoReleaser.
Published changes and downloadable artifacts are available on the
[GitHub Releases](https://github.com/amiraq1/Nabd/releases) page.

## Unreleased

- **BREAKING: Provider endpoint policy (`feat(endpoint)!`):** `providers.json` and `NABD_BASE_URL` overrides enforce a two-layer endpoint acceptance policy by default. Layer 1 (load-time): `baseURL` must use HTTPS and must not be a loopback, RFC 1918, link-local, CGNAT, ULA, cloud-metadata, or `.internal`/`.local`/`localhost` address. Layer 2 (connect-time): the HTTP transport's `net.Dialer.Control` hook inspects the socket's literal address immediately before `connect(2)` after DNS resolution, eliminating the TOCTOU window and closing DNS rebinding completely across all inference routes and `/models` catalog probes. **Migration:** set `NABD_ENDPOINT_POLICY=loopback` for a local runtime such as ollama (HTTPS still required at the loopback address). Set `NABD_ENDPOINT_POLICY=open` to disable both layers entirely (not recommended for production). The default is `strict`. `THREAT_MODEL.md` updated: four GUARANTEED rows added, one REDUCED row for `NABD_ENDPOINT_POLICY=open`.

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
