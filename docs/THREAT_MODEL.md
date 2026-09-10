# Threat model

Sourced from the code as of commit d63da42 (the last code change in the
NBD-306/204 batch; the commits after it are documentation only and change no
code), not from intention.
Primary files: `internal/tools/path.go`, `internal/tools/bash.go`,
`internal/perm/policy.go`, `internal/config/config.go`, `internal/snap/shadow.go`,
`internal/agent/fence.go`, `cmd/ag/main.go`.

This is the only place nabd states security claims. README points here.

## Assets

| Asset | Where it lives |
|---|---|
| Provider API keys | `~/.ag/config` (preferred) or process environment (fallback). The binary never writes the file. |
| Working tree | The directory `Root` was constructed from (`NewRoot("")` uses cwd). |
| Session journal | `~/.ag/sessions/*.jsonl` by default (`--dir` overrides). Append-only events, including file contents and command output in cleartext. Default file mode 0o600, default dir mode 0o700 (NBD-306). The default directory is built by exactly one function (`defaultSessionDir`) and is hardened to 0o700 on a fresh session and on `--continue` alike. For a caller-supplied `--dir` the mode contract is explicit: a directory that already exists keeps whatever mode the caller set, and a directory nabd has to create — along with any missing ancestors — is created private (no group/other bits) with the final element pinned to 0o700 regardless of umask. The file is 0o600 in both cases. |
| Shadow store | `<root>/.ag/shadow`, content-addressed `s256:` blobs. Independent of git. `.ag` and `.ag/shadow` are tightened to 0o700 and nabd writes `/shadow/` to `.ag/.gitignore`. |

## Adversaries

In order of realism:

**(a) A confused model.** The default case. It will invent paths, retry
destructive commands, and treat tool output as instruction. Defence is
`Root.Resolve` for file tools, `perm.Policy` for class, and a human at
the `bash` prompt.

**(b) Hostile content in the repo or in tool output (prompt injection).**
ReadOnly tools (`read_file`, `glob`, `grep`) are auto-allowed. Their
bytes reach the next provider request inside a labeled, nonce-fenced
envelope: a fresh random nonce in both markers, every marker token in the
payload defanged, and the tool name echoed only when it is in the tool
registry's allowlist — anything else is reported as the explicit marker
`unknown`, never trimmed into a plausible-looking name (NBD-204). That is
a semantic signal, not a security boundary: there is no injection
detector, and a determined or confused model may still follow the
content. Residual risk: a README can ask the model to call `bash`; the
remaining defence is the human reading that one prompt.

**(c) A hostile dependency invoked through bash.** After the operator
types `y`, `sh -c` runs with cwd = project root and no `Resolve`.
`cd ..` works. `rm -rf ~/x` works. `/undo` does not cover it. Env
allowlisting reduces credential theft from the child; it does not
contain the filesystem.

**(d) A local attacker with the same uid.** Out of scope. nabd is a
phone-first, single-user process. It cannot defend files the operator
can already `open(2)`. Config-file TOCTOU, 0o600/0o700 discipline that
only excludes other uids, and ptrace are this class. Mitigate with OS
user separation, not with this binary.

## Guarantees

Three columns. **GUARANTEED** requires a test that fails if the claim is
broken. **REDUCED** names the residual. **OUT OF SCOPE** names why.
`bash` is never in the first column.

| Claim | Column | Proof or reason |
|---|---|---|
| `Root.Resolve` refuses `..`, absolute paths outside the root, NUL bytes, and empty paths | GUARANTEED | `internal/tools/path_test.go` `TestResolveRefusesTraversal` |
| Symlink to a file or directory outside the root is refused, including a missing tail under a linked parent | GUARANTEED | `TestResolveRefusesSymlinkEscape`, `TestResolveRefusesEscapeViaMissingTail` |
| A root that is itself a symlink still contains its real children | GUARANTEED | `TestRootBehindSymlink` |
| `read_file` / `write_file` / `edit_file` / `glob` / `grep` go through `Resolve` | GUARANTEED | package comment in `path.go`; a tool that opens another way is a bug. Covered per-tool by path tests plus write/read tests |
| Unknown tool name is Deny; empty name is Deny | GUARANTEED | `internal/perm/policy_test.go` `TestUnknownToolIsDenied` |
| ReadOnly tools Allow without a prompt; Mutating and Executing Ask | GUARANTEED | `TestReadIsFreeWritesAsk` |
| `bash` cannot take a session grant; `a` becomes AllowOnce | GUARANTEED | `TestSessionGrantAppliesToWritesOnly`, `TestRawDecisionForBash` |
| Denied `bash` starts no subprocess and writes no file | GUARANTEED | `internal/tools/bash_gate_test.go` `TestBashDeniedRunsNoSubprocess` |
| `agent.Decision(0) == Deny` | GUARANTEED | independent zero-value test in `internal/agent` (see README architecture; the type is `Deny` at iota 0) |
| `perm.Verdict(0) == Ask` | GUARANTEED | `internal/perm/policy.go` const block; unclassified tools Ask, unknown names Deny |
| bash child env is an allowlist from an empty slice (PATH, TERM, LANG, LC_*, TMPDIR/TMP/TEMP), sorted, no credentials, no `BASH_ENV`/`ENV`/`LD_*` | GUARANTEED | `TestBashChildEnvAllowlistIntegration`, `TestBashChildEnvBASH_ENVNotSourced`, `TestBashChildEnvENVNotSourced` |
| Relative and empty PATH entries are stripped | GUARANTEED | `TestBashChildEnvPathStripping` |
| Child HOME is a fresh 0700 temp dir, not the caller's HOME | GUARANTEED | `TestBashChildEnvHomePolicy` |
| Config file mode `& 0o077 != 0` is refused; symlink refused; non-regular refused | GUARANTEED | `internal/config` tests around `ParseFile` |
| Config package never writes the file | GUARANTEED | package comment and the absence of create/write APIs; `scripts/check-exec-env.sh` is a related env discipline, not this file |
| Shadow blobs are `s256:` SHA-256; restore verifies digest; no git | GUARANTEED | `internal/snap` `UsesGit() bool` is false; `get` checksum mismatch returns `ErrShadowCorruption` |
| `/undo` refuses if on-disk bytes no longer match the recorded hash | GUARANTEED | `internal/tools/undo_test.go` / `persisted_undo_test.go` |
| `bash` cannot escape the project via `Resolve` | OUT OF SCOPE | `bash.go` never calls `Resolve`. cwd is `root.Dir()`. `cd ..` is a shell builtin |
| `/undo` covers bash side effects | OUT OF SCOPE | snap never sees the blast radius; stated in `bash.go` package comment |
| Network, resource exhaustion, or killing unrelated processes from an approved bash | OUT OF SCOPE | no namespace, no cgroup, no Landlock in this version |
| Prompt injection via ReadOnly tool output | REDUCED | tool output is labeled and fenced at the provider boundary (NBD-204): a per-call random nonce in both markers, every marker token in the payload defanged, and the tool name echoed only from the registry allowlist (`unknown` otherwise), the same value used for the tool call and both markers. **The fence is a semantic signal, not a security boundary**: a determined or confused model may still follow content despite the marker, so user approval remains the barrier for sensitive actions |
| Tool-call repair changes what runs relative to what the model asked for (NBD-420) | REDUCED | `internal/tools/repair.go` corrects malformed calls before the registry lookup, so the gate classifies and prompts on the corrected call. It is a new surface between the model and the gate, bounded by design: inference is one-directional (an unrecognised name resolves only to a ReadOnly tool — `read_file`, `glob`, `grep` — and `write_file`/`edit_file`/`bash` require a literal match); the name map is explicit, never edit distance or string similarity, and every target is verified against `known`; path resolution is untouched, because `Root.Resolve` alone decides inside/outside the root and the layer never expands `~`, absolutises or relativises anything; and a call needing more than three corrections is returned unchanged. A name still unknown after the map returns the existing unknown-tool error, which the model reads and recovers from. Proof: `TestRepairInferenceIsReadOnlyOnly`, `TestRepairNameMapTargetsAreDeclared`, `TestRepairPathResolutionIsUntouched`, `TestRepairCapReturnsUnchanged`, `TestRepairRulesSaveARound` |
| Same-uid local attacker (TOCTOU on `~/.ag/config` between `Lstat` and `Open`) | OUT OF SCOPE | see Path and key handling below |
| Windows NT ACL ownership of the config file | OUT OF SCOPE | `owner_other.go` is a documented no-op |
| bash filesystem reach after the operator types `y` | REDUCED | prompt + Executing class + no session grant. Residual: the operator's eye |
| Config TOCTOU | REDUCED | `Lstat` then `Open`. Symlink at Lstat time is refused. Swap after Lstat is a same-uid race |
| Key in the environment | REDUCED | file wins when both are set (`Conflicts()`). Env remains a fallback. Child bash does not inherit it |
| Multi-process `/undo` | REDUCED | pending-edit log is process memory (IDEAS.md). Journal-backed undo after restart still works for committed edits |
| Journal cleartext (file contents, command output, keys if a tool printed them) | REDUCED | NBD-306 lands the storage hardening only: file 0o600 and default dir 0o700, so disclosure is confined to the same uid. Redaction on the journal write path is NOT implemented; display-layer redaction in `internal/ui` does not touch the journal. |
| Shadow store accidentally staged by git | REDUCED | nabd writes `/shadow/` to `<root>/.ag/.gitignore` (atomic, idempotent), so `git add -A` skips it. `git add -f` bypasses `.gitignore`, so this is a guard against accidental tracking, not a security boundary; the 0o700 directory mode is the storage boundary and same-uid readers are class (d). |

## Path layer

`internal/tools/path.go`, type `Root`. There is no `root.go`.

`Resolve` is the only function allowed to accept a path:

1. Reject empty / NUL.
2. Absolute input is kept only if `within(root, Clean(p))`; otherwise `ErrAbsolute`.
3. Relative input is joined to the already-resolved root, never to process cwd.
4. `resolveDeepest`: `EvalSymlinks` on the deepest existing ancestor, then
   append the missing tail (the tail cannot contain a symlink because it
   does not exist).
5. `within` uses `filepath.Rel`, not `HasPrefix`, so `/home/user2` does not
   match `/home/user`.

TOCTOU that remains: `Resolve` returns a string. The later `open` is a
second lookup. A symlink can be planted between those two syscalls by
anything that shares the uid. Closing that window needs `openat2(RESOLVE_BENEATH)`
or `O_NOFOLLOW` + `Fstat` on the fd (IDEAS.md). Until then the claim is
**reduced**, not eliminated. Quantified: one rename/symlink between
`EvalSymlinks` and `open` on a cooperative filesystem. On a single-user
phone this is (d). On a shared uid it is in scope for a local attacker
and out of scope for this binary.

`bash` does not use this layer.

## Key handling

`internal/config`:

- Path: `NABD_CONFIG` or `~/.ag/config`.
- `Lstat` first: symlink refused, non-regular refused, `mode & 0o077 != 0`
  refused, Unix owner must equal `Getuid()` (`owner_unix.go`). Windows
  ownership check is a no-op (`owner_other.go`).
- Then `os.Open` of the same path. Residual TOCTOU as above.
- The package never writes.
- File wins over environment; `Conflicts()` reports key **names** only.
- Environment remains a fallback for keys absent from the file.

`bash` child construction (`childEnv`) starts from `[]string{}` and copies
only the allowlist. That is a separate guarantee from config loading: a
key in the parent env does not reach `sh -c`. It can still reach the
provider HTTP client, which is the point of the fallback.

## What an operator should do

These compensate for REDUCED and OUT OF SCOPE rows. They are not extra
guarantees in the binary.

1. Put keys in `~/.ag/config` with `chmod 600`. Do not export them in `.bashrc`.
2. Run nabd as a dedicated OS user if the machine is shared.
3. Point it at a throwaway clone, not at a tree that holds production
   credentials or irreplaceable uncommitted work.
4. Keep secrets out of the working tree. ReadOnly tools will send file
   bytes to the model.
5. Treat every `bash` prompt as a root-equivalent for your uid. `n` is the
   containment mechanism.
6. Do not enable YOLO (`perm.Policy.SetYOLO`) on a tree you care about.
7. `session.jsonl` is 0o600 inside a 0o700 default dir, but redaction is
   not implemented: assume it holds file excerpts and command output in
   cleartext, readable by anything running as the same uid.
