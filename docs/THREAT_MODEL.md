# Threat model

Last reviewed: 2026-09-12 · `7d9e2f07f9561d4e79b2db6164387b3eac6660ff`

This document is based on that reviewed `master` baseline (the parent of this
documentation change), not on intention. Primary files reviewed:
`internal/tools/path.go`, `internal/tools/bash.go`, `internal/perm/policy.go`,
`internal/config/config.go`, `internal/snap/shadow.go`,
`internal/agent/fence.go`, and `cmd/ag/main.go`.

This is the only place nabd states security claims. README points here.

## Assets

| Asset | Where it lives |
|---|---|
| Provider API keys | v1: `NABD_CONFIG` or `~/.ag/config`, with process environment fallback. v2: `NABD_CONFIG_V2` or `~/.ag/config.v2.json`, with explicit `env` or secure file credential sources only. |
| Working tree | The resolved project directory used to construct `tools.Root`. |
| Session journal | `~/.ag/sessions/*.jsonl` by default. Append-only events can include file contents and command output in cleartext. Default directory mode is 0700 and file mode is 0600. |
| Shadow store | `<root>/.ag/shadow`, content-addressed `s256:` blobs containing full pre/post-edit content. `.ag` and `.ag/shadow` are tightened to 0700 and `/shadow/` is added to `.ag/.gitignore`. |

The journal and shadow store are high-sensitivity assets. Filesystem modes
reduce cross-user disclosure; they do not protect against processes running as
the same uid.

## Adversaries

1. **Confused model.** It may invent paths, retry destructive operations, or
   treat tool output as instructions. Path resolution, permission classes, and
   human approval for shell execution reduce this risk.
2. **Hostile repository/tool content.** ReadOnly output reaches the provider in
   a nonce-fenced envelope. The fence is a semantic signal, not an injection
   boundary; sensitive actions still rely on permission approval.
3. **Hostile dependency invoked through bash.** Once approved, `sh -c` runs as
   the current user with project-root cwd and no `Root.Resolve` containment.
   Environment allowlisting protects provider keys but not the filesystem.
4. **Same-uid local attacker.** Out of scope. OS user separation is the
   mitigation.

## Claims and evidence

**GUARANTEED** claims must name a test or mechanically auditable invariant.
**REDUCED** claims state the residual risk. `bash` is never treated as a
filesystem sandbox.

| Claim | Status | Evidence or residual |
|---|---|---|
| `Root.Resolve` rejects traversal, outside absolute paths, NUL, and empty paths | GUARANTEED | `TestResolveRefusesTraversal` and path tests |
| Symlink escapes, including a missing tail under a linked parent, are rejected at resolution time | GUARANTEED | `TestResolveRefusesSymlinkEscape`, `TestResolveRefusesEscapeViaMissingTail` |
| A root that is itself a symlink contains its real children | GUARANTEED | `TestRootBehindSymlink` |
| File tools are expected to pass paths through `Resolve` | GUARANTEED | package contract in `internal/tools/path.go` and per-tool tests |
| Unknown or empty tool names are denied | GUARANTEED | `TestUnknownToolIsDenied` |
| ReadOnly tools allow without a prompt; Mutating and Executing tools ask | GUARANTEED | `TestReadIsFreeWritesAsk` |
| `bash` cannot receive a session-wide grant | GUARANTEED | `TestSessionGrantAppliesToWritesOnly`, `TestRawDecisionForBash` |
| Denied `bash` starts no subprocess | GUARANTEED | `TestBashDeniedRunsNoSubprocess` |
| Bash child environment is an allowlist, strips unsafe PATH entries, and uses an isolated HOME | GUARANTEED | `TestBashChildEnvAllowlistIntegration`, `TestBashChildEnvPathStripping`, `TestBashChildEnvHomePolicy` |
| Opened config must be regular, user-owned on Unix, and have no group/other permission bits | GUARANTEED | `internal/config` ParseFile and secure-open tests |
| Config v1 and Config v2 default files coexistence on disk is fatal at startup | GUARANTEED | `TestV2CoexistenceOnDiskIsFatal` |
| Config v2 rejects unknown fields, trailing JSON, and custom `base_url` | GUARANTEED | `TestV2RejectsUnknownFieldsAndTrailingJSON`, `TestV2CredentialFileAndClosedEndpointPolicy` |
| Config package never writes configuration or credentials to disk | GUARANTEED | package API and configuration tests |
| Config descriptor opening rejects symlinks and blocking special files | REDUCED | `O_NOFOLLOW` and `O_NONBLOCK`, followed by descriptor validation |
| Config v2 has no implicit environment credential fallback | GUARANTEED | credential sources must explicitly use `env` or a secure absolute `file` |
| Shadow blobs are SHA-256 addressed and verified on read; publication never silently replaces an existing blob | GUARANTEED | `internal/snap` checksum and rename capability tests |
| Undo refuses when current bytes no longer match the recorded hash | GUARANTEED | undo and persisted-undo tests |
| Prompt injection through ReadOnly output | REDUCED | nonce fencing plus approval for sensitive actions; model behavior is not guaranteed |
| Tool-call repair changes what runs relative to what the model asked for (NBD-420) | REDUCED | `internal/tools/repair.go` corrects malformed calls before the registry lookup, so the gate classifies and prompts on the corrected call. It is a new surface between the model and the gate, bounded by design: inference is one-directional (an unrecognised name resolves only to a ReadOnly tool — `read_file`, `glob`, `grep` — and `write_file`/`edit_file`/`bash` require a literal match); the name map is explicit, never edit distance or string similarity, and every target is verified against `known`; path resolution is untouched, because `Root.Resolve` alone decides inside/outside the root and the layer never expands `~`, absolutises or relativises anything; and a call needing more than three corrections is returned unchanged. A name still unknown after the map returns the existing unknown-tool error, which the model reads and recovers from. Proof: `TestRepairInferenceIsReadOnlyOnly`, `TestRepairNameMapTargetsAreDeclared`, `TestRepairPathResolutionIsUntouched`, `TestRepairCapReturnsUnchanged`, `TestRepairRulesSaveARound` |
| Path opening after `Resolve` | REDUCED | `Resolve` returns a string and later open is a second lookup; a same-uid rename/symlink race remains |
| Journal and shadow cleartext | REDUCED | private default modes; same-uid readers and deliberately printed secrets remain exposed |
| Bash filesystem reach after approval | OUT OF SCOPE | approved shell commands run with the current user's filesystem authority |
| Network/resource exhaustion from approved bash | OUT OF SCOPE | no namespace, cgroup, or Landlock boundary |

## Path layer

`internal/tools/path.go` owns path acceptance. `Resolve` rejects empty/NUL
input, anchors relative input to the real project root, resolves the deepest
existing ancestor, and verifies containment using `filepath.Rel`.

`Resolve` currently returns a string. Consumers perform a later filesystem
lookup, so a cooperative same-uid process can race resolution and opening.
This is **reduced, not eliminated**. Closing it requires descriptor-relative
opening such as `openat2(RESOLVE_BENEATH)` plus a classified fallback.

`bash` deliberately does not use this layer.

## Configuration handling

### Config v1

- Path: `NABD_CONFIG` or `~/.ag/config`; explicit paths must be absolute.
- The file is opened through the platform secure-open helper using descriptor
  flags equivalent to `O_NOFOLLOW | O_CLOEXEC | O_NONBLOCK` on Unix.
- Regular-file type, mode, size, and ownership are checked from metadata
  obtained from the opened descriptor (`f.Stat`), not from a pre-open `Lstat`.
- The file wins over environment values; conflicts disclose key names only.
- Environment fallback remains available for values absent from the file.
- Residual: parent-directory symlinks are still traversed. A hostile same-uid
  process that can replace path components remains out of scope.

### Minimal strict Config v2

- Selected by `NABD_CONFIG_V2` or the default `~/.ag/config.v2.json`.
- v1 and v2 cannot be active together.
- JSON rejects unknown fields and trailing documents.
- Route-array order is provider priority; no separate `priority` field exists.
- Credential sources are explicit `env` or an absolute secure `file` only.
  Command-based credential sources are rejected.
- Credential files are descriptor-opened and must be regular, user-owned,
  mode 0600, and contain exactly one line.
- Implicit environment fallback is disabled.
- Custom `base_url` is recognized but rejected by the minimal schema until
  redirect and post-DNS transport controls exist.

The config package never writes configuration files. Bash child construction
starts from an empty environment and copies only its allowlist, independently
of config loading.

### Config security invariants

- Config v1 uses `NABD_CONFIG` or `~/.ag/config`.
- Config v2 uses `NABD_CONFIG_V2` or `~/.ag/config.v2.json`.
- Explicitly selecting both versions, or finding both default files, is a fatal startup error.
- Config and credential files are opened with `O_RDONLY`, `O_CLOEXEC`, `O_NOFOLLOW`, and `O_NONBLOCK`, then validated through the opened descriptor.
- Files must be regular, owned by the current Unix user, inaccessible to group and others, and within their configured size limits.
- Config v2 rejects unknown fields, trailing JSON, command credential sources, and implicit environment fallback.
- Config v2 credential files must contain exactly one non-empty line.
- Loaded credentials remain in package memory and are never copied into the environment of `bash` children.

## Future base_url admission conditions

Minimal strict Config v2 rejects `base_url` unconditionally. If custom endpoints are admitted in future versions to support private or enterprise inference gateways, the implementation MUST satisfy all five security admission conditions before admittance:

1. **HTTPS mandatory**:
   The endpoint URL scheme must be strictly `https://`. Plaintext `http://` or any other URI schemes are rejected. Unencrypted network transmission of provider API credentials is forbidden.

2. **Host and IP admission restrictions**:
   The host must resolve to a globally routable public address. The system must explicitly reject:
   - RFC 1918 private IPv4 subnets (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`).
   - Loopback addresses: IPv4 `127.0.0.0/8` and IPv6 `::1/128`.
   - Link-local addresses: IPv4 `169.254.0.0/16` (specifically defending against cloud metadata endpoints such as `169.254.169.254`) and IPv6 `fe80::/10`.
   - IPv6 Unique Local Addresses (`fc00::/7`).
   - Local network and mDNS suffixes: `.local`, `.internal`, `.lan`, `.home.arpa`, or unqualified single-label hostnames.
   - Any address that re-resolves (DNS rebinding) to non-public space at connection dial time.

3. **No embedded credentials**:
   The URL must not include userinfo components (`user:password@host`). Embedded credentials in URLs risk leakage in log messages, metrics, and proxy logs.

4. **No query parameters or fragments**:
   The URL must contain only scheme, host, optional non-privileged port, and an optional clean path prefix. Query parameters (`?`) and fragment identifiers (`#`) are rejected.

5. **Cross-host redirect containment at HTTP runtime**:
   Redirect containment cannot rely solely on upfront URL parse validation. The HTTP client's `CheckRedirect` policy function must terminate the request if any HTTP redirect points to a host different from the original validated endpoint host, preventing redirect-based credential leakage or SSRF pivot.

## Journal, shadow, and history concurrency

Session events are append-only. Compaction and rewind are serialized against
history mutations; compact boundary staleness has dedicated regression
coverage. The shadow store keeps complete file bytes for undo and verifies
content digests. Neither journal nor shadow content is redacted on write in
this baseline.

## Operator guidance

1. Prefer secure credential files over shell startup exports.
2. Run nabd as a dedicated OS user on shared systems.
3. Use a disposable clone rather than a tree containing production secrets.
4. Treat every `bash` approval as authority equivalent to the current user.
5. Assume `session.jsonl` and `.ag/shadow` contain sensitive cleartext.
6. Do not attach raw journals to public issues.

### Inherited directory permissions

Write tools create missing parent directories with the permission bits of the nearest existing ancestor. This prevents a private `0700` project subtree from silently gaining `0755` descendants; `0755` is retained only as a documented root-level fallback when no usable ancestor exists.

### Aggregate diff memory budget

Each tool registry shares one cancellable LCS cell budget across concurrent mutations. A diff reserves its `n*m` work before allocating the matrix and releases the reservation on every return path, preventing parallel writes from multiplying the per-diff memory ceiling.

### UI text-width contract gate

The standalone UI width-contract gate checks text measurement, truncation bounds, composed frame width, Unicode segmentation, Arabic combining marks, CJK width, emoji sequences, and cursor alignment. The gate runs on every pull request, including dependency-only changes, so text-width dependency upgrades cannot bypass the UI regression suite.

Textarea vertical cursor navigation uses grapheme-cluster boundaries rather than raw rune-width accumulation. Arabic combining marks must remain zero-width during vertical movement, and the visible target column must be preserved across ASCII and Arabic lines. `TestTripwire_TextareaColumnMappingChanged` detects dependency or local-fork behavior changes, while `TestCorrectness_ComposerNavigationColumnAlignment` enforces the intended cursor-alignment contract.
