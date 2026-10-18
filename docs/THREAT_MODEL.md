# Threat model — v14

Last reviewed: 2026-10-15 (draft — claims-affecting documentation revision).
Base text: v13 plus the supplied editorial revision, reconciled against
`master` at `51859a06b5e4ba7d35babad700a4376acaa84a7d`.
The supplied text's unmerged PR #279 skill-entry-cap paragraph is excluded.
Review references: targeted source review at `23c7b642`; silent-turn work merged
in PR #278 at `51859a06`. These are separate evidence references, not a claim
that every row was audited against one combined master tree. PR #270 branches
from the earlier base `fb7a038`. Open-PR changes are not treated as merged fixes.

Validation scope: compared the base document and open-PR listing on GitHub,
checked patch application locally, and ran the existing test-citation existence
guard on the pinned source snapshot. Follow-up review inspected decodeStrict,
glob, the file-tool decoder call sites, purgeCandidates and its archive fixture,
the rotation comment, and shadow ignore-file creation. This does not establish test execution,
coverage, CI success or the correctness of inherited source observations.
No runtime security reproduction or full code audit was performed. A PR changing a claim must
update the corresponding row in the same PR. See "Revision log".

This is the only place nabd states security claims. README points here.

## Contents

1. [How to read this document](#how-to-read-this-document)
2. [Global assumptions](#global-assumptions)
3. [Assets](#assets)
4. [Adversaries](#adversaries)
5. [Trust boundaries at a glance](#trust-boundaries-at-a-glance)
6. [Claims and evidence](#claims-and-evidence)
7. [Residual-risk register](#residual-risk-register)
8. [Path layer](#path-layer)
9. [Configuration handling](#configuration-handling)
10. [Custom endpoints: policy-controlled](#custom-endpoints-policy-controlled)
11. [Skills](#skills)
12. [Journal, shadow, and history concurrency](#journal-shadow-and-history-concurrency)
13. [MCP integration](#mcp-integration)
14. [Operator guidance](#operator-guidance)
15. [Open verification items (not claims)](#open-verification-items-not-claims)
16. [Revision log](#revision-log)

## How to read this document

- A **claim** is one row in a claims table. Each row has exactly one status.
  A guaranteed part and an unprovided boundary belong in separate rows.
- **GUARANTEED** — holds under the global assumptions below, and is backed by a
  backticked `Test*` function or a named script/AST guard. A phrase such as
  "package tests" is not a specific citation.
- **GUARANTEED†** — an inherited maintainer assertion whose evidence column
  has no specific test or guard citation yet. It is evidence debt, not a newly
  verified guarantee. Open item EVIDENCE-01 (not the previous document version)
  lists every such claim row; add evidence or downgrade.
- **REDUCED** — a mitigation with a stated residual risk. It is not a boundary.
  A REDUCED row without a residual is a documentation defect.
- **NOT PROVIDED** — a boundary that does not exist on the platform. Listed so
  nobody assumes it does.
- Test names in backticks are checked mechanically to resolve to a real Test*
  function (`scripts/check-threat-model-tests.sh`). That check proves the test
  exists; whether it is sufficient to prove the claim is a maintainer judgement
  made at review time.
- Sections before "Open verification items" state what the project claims.
  "Open verification items" separates reported source observations, unresolved
  questions and proposed designs. An item is not itself a verified guarantee
  or a severity classification.

## Global assumptions

These apply to every GUARANTEED row unless the row says otherwise.

- Platform: descriptor-relative filesystem guarantees are stated for Unix
  (`On unix` in the rows). The `!unix` compatibility adapter carries no
  descriptor guarantee.
- Storage assumption: use Termux private storage for `~/.ag` and the project
  root. Do not rely on Unix `0600`/`0700` confidentiality or case-sensitive
  matching on Android shared/emulated storage (`/storage/emulated/...`,
  `~/storage/shared`) without checking the actual filesystem and mount.
  Storage-dependent claims do not apply where those prerequisites fail (V8).
- Adversary scope: a same-uid local attacker is out of scope (Adversary 4). File
  modes limit cross-user access only.
- `bash` is never a filesystem sandbox. After approval it runs with the full
  authority of the current user.
- Evidence means the named tests pass in CI, or the named AST or script guard
  passes. Race-detector evidence comes from the GitHub Actions Linux run, not
  from Termux (see "Baseline measurement harness").

## Assets

| Asset | Where it lives |
|---|---|
| Provider API keys | v1: `NABD_CONFIG` or `~/.ag/config`, with process environment fallback. v2: `NABD_CONFIG_V2` or `~/.ag/config.v2.json`, with explicit `env` or secure file credential sources only. OpenCode registry: `~/.ag/auth.json` (mode 0600, owner checked, atomic write via temp file with fsync and sidecar file lock `~/.ag/auth.json.lock`) with legacy environment fallback, and `~/.ag/providers.json` (mode 0600) with builtin catalog fallback. |
| Working tree | The resolved project directory used to construct `tools.Root`. |
| Repository execution hooks | Working-tree files that programs may execute or interpret later: `.git/hooks/*`, `.git/config`, `.gitattributes` filter references, `.gitmodules`, `Makefile`, `package.json` scripts and `.envrc`. Their execution depends on the consuming program; writing them can enable deferred execution. |
| Session journal | `~/.ag/sessions/*.jsonl` by default. Append-only events can include file contents and command output in cleartext; directory mode 0700 and file mode 0600 limit cross-user reads. Journal redaction is enabled by default and removes recognized credential patterns from new events before write; `NABD_REDACT_JOURNAL=0` is an explicit diagnostic opt-out. |
| Shadow store | `<root>/.ag/shadow`, content-addressed `s256:` blobs containing full pre/post-edit content, including excluded files, for rollback. `.ag` and `.ag/shadow` are tightened to 0700. On each store write-open, the shield attempts to create `.ag/.gitignore` with the single rule `*`, using exclusive creation (`os.O_CREATE` and `os.O_EXCL`). An existing ignore file is never read, appended to or replaced. Creation failure is ignored; the ignore rule is no guarantee against forced addition or conflicting existing rules. Shadow blobs are not redacted. |
| MCP configuration and approvals | `~/.ag/mcp.json` selects server programs and enabled servers; `~/.ag/mcp/` holds MCP approval/journal data. Whoever can modify the configuration controls which server programs may start. |
| Skills | User scope `~/.config/nabd/skills` loads without a project opt-in; project scope `<root>/.nabd/skills` is off by default. Protect the user scope as trusted instruction configuration. |

The journal and shadow store are high-sensitivity assets. Filesystem modes
reduce cross-user disclosure; they do not protect against processes running as
the same uid.

## Adversaries

1. **Confused model.** It may invent paths, retry destructive operations, or
   treat tool output as instructions, or write files that other programs later
   execute. Path resolution, permission classes, and
   human approval for shell execution reduce this risk.
2. **Hostile repository/tool content.** ReadOnly output reaches the provider in
   a nonce-fenced envelope. The fence is a semantic signal, not an injection
   boundary; sensitive actions still rely on permission approval. Compaction
   can also lose the original label (V17).
3. **Hostile dependency invoked through bash.** Once approved, `sh -c` runs as
   the current user with project-root cwd and no `Root.Resolve` containment.
   Environment allowlisting keeps provider keys out of the child's environment;
   it does not stop the command from reading anything the current user can
   read, including `~/.ag/auth.json`, `~/.ag/config`, the session journal, and
   `<root>/.ag/shadow`.
4. **Same-uid local attacker.** Out of scope. OS user separation is the
   mitigation.
5. **Hostile endpoint or MCP server.** The selected provider endpoint receives
   the request content and applicable credential and can return tool calls.
   Permission checks still apply to those calls. An MCP STDIO server runs as
   the user; local classification is not an OS sandbox or network boundary.

## Trust boundaries at a glance

This table summarises where each boundary is enforced. The claims below are
authoritative; this table adds no claim.

| Boundary | Enforced by | Main residual |
|---|---|---|
| Model → tools | `perm.Policy` classes (ReadOnly / Mutating / Executing), human approval, deny modes | Prompt injection is REDUCED; the fence is a semantic signal |
| Tool → filesystem (file tools) | `internal/safefs` descriptor-relative operations; `Resolve` is reporting-only | `!unix` adapter; config reading traverses parent symlinks |
| Tool → filesystem (`bash`) | Approval of the displayed `cmd` only | NOT PROVIDED: no sandbox |
| Tool → deferred execution | Ordinary Mutating permission | No separate protected-build-file consent boundary is claimed (V15) |
| Tool-call arguments → tool | Decoder-specific: decodeStrict after repair for its callers; json.Unmarshal for glob | decodeStrict refuses duplicate keys at any depth (case-insensitive), nesting past `maxJSONDepth`, and any data after the first value; `glob` keeps its own decoder. Whether the permission prompt shows the payload the decoder acted on remains open (V14) |
| History → model (compaction) | Summary projection | Original untrusted label is not preserved (V17) |
| Process → provider endpoint | Endpoint policy at load time and dial time; guarded HTTP clients | Policy `open`; redirect containment is not claimed |
| Credentials | 0600 owner-checked files, env allowlist for children, redaction | Same-uid readers; best-effort redaction |
| Journal / shadow store | 0700/0600 modes, redaction of recognized patterns, git shielding | Shadow store is not redacted; cleartext content |
| Skills | User scope trusted; project scope off by default; hash re-verified; fenced projection | Project skills are repository-authored instructions |
| MCP | Local config only, local classification, fingerprinted approvals, minimal child env | STDIO servers uncontained; active when configured; approval scope; independent review unverified |

## Claims and evidence

**GUARANTEED** claims must name a test or mechanically auditable invariant.
**REDUCED** claims state the residual risk.
**NOT PROVIDED** explicitly marks boundaries that do not exist on the platform. `bash` is never treated as a filesystem sandbox.

| Claim | Status | Evidence or residual |
|---|---|---|
| `Root.Resolve` rejects traversal, outside absolute paths, NUL, and empty paths | GUARANTEED | `TestResolveRefusesTraversal` and path tests |
| Symlink escapes, including a missing tail under a linked parent, are rejected at resolution time | GUARANTEED | `TestResolveRefusesSymlinkEscape`, `TestResolveRefusesEscapeViaMissingTail` |
| A root that is itself a symlink contains its real children | GUARANTEED | `TestRootBehindSymlink` |
| File tools use normalized root-relative paths and descriptor-relative operations as their filesystem authority | GUARANTEED | `TestToolPathAuthorityDoesNotCallResolveDirectly`, `TestReadFileUsesDescriptorStat`, and per-tool safefs tests; `Resolve` remains reporting/compatibility-only. Both guards are AST-based: they match call sites, not text, so renames and reformatting cannot fake or break them |
| Unknown or empty tool names are denied | GUARANTEED | `TestUnknownToolIsDenied` |
| ReadOnly tools allow without a prompt; Mutating and Executing tools ask | GUARANTEED | `TestReadIsFreeWritesAsk` |
| `bash` cannot receive a session-wide grant | GUARANTEED | `perm.Policy.SessionGrantAllowed` excludes `bash` (and all non-Mutating tools) so standing grants are never offered, and `Effective` downgrades any `AllowSession` for `bash` to `AllowOnce`. Interactive surfaces (Chat and Feed modal) derive choices from policy so the session option is neither shown nor accepted by key ('a' / 'A'), and fail closed when session grants are unknown. Evidence: `TestSessionGrantAppliesToWritesOnly`, `TestRawDecisionForBash`, `TestSessionGateWiresSessionGrantPolicy`, `TestPermissionModalHidesUnsupportedSessionGrant`, `TestPermissionModalFailsClosedWhenSessionGrantUnknown`, `TestBashSessionKeyNeitherShownNorAccepted`, `TestChatBashSessionKeyRejected` |
| YOLO never auto-approves Executing tools, and Deny modes override YOLO | GUARANTEED | `perm.Policy.Check` widens the verdict only for `Mutating` calls: an `Executing` tool under `SetYOLO(true)` falls through to the ordinary ladder, so it asks under `ModeAsk` and is denied under `ModeDeny`/`ModeAllowReads`/`ModePlan`, and accepts no standing grant. Headless runs never block on the resulting question: `silentAsker` answers `Deny` without reading a terminal. Furthermore, explicit deny modes (`ModeDeny`, `ModeAllowReads`, `ModePlan`) evaluate before YOLO so a blanket YOLO flag cannot auto-approve mutating tools under a deny mode. Explicit per-tool session grants for Mutating tools can still stand under ModeDeny and ModeAllowReads. ModePlan is the exception: it is checked before standing grants and denies all Mutating/Executing calls even when a grant or YOLO is present. Source order: [Policy.CheckReason](https://github.com/amiraq1/Nabd/blob/51859a06b5e4ba7d35babad700a4376acaa84a7d/internal/perm/policy.go#L191-L198). Evidence: `TestYOLODoesNotBypassExecuting`, `TestYOLOStillAllowsMutating`, `TestYOLOVerdictMatrix`, `TestHeadlessYOLOBashDoesNotHang`, `TestModeDenyOverridesYOLO`, `TestModePlanOverridesGrants` |
| Denied `bash` starts no subprocess | GUARANTEED | `TestBashDeniedRunsNoSubprocess` |
| Permission decisions are journal-first and effective decisions are returned to the loop | GUARANTEED | `TestDecideLogsEffectiveDecision`, `TestDecideRefusesWhenPermissionQuestionCannotBeJournaled`; failed permission events deny execution and do not record a session grant |
| Permission reasons are stable codes translated only at the presentation boundary, while old journals preserve their text | GUARANTEED | New `PermAsk`/`PermReply` events from the built-in policy carry a closed `PermissionReason` code plus an English `Text` fallback. `internal/presentation.PermissionReasonText` is the only Arabic catalog: a known code is authoritative, an unknown non-empty code renders nothing, and an empty code identifies a legacy event whose `Text` remains unchanged. Permission events remain audit-only and do not enter provider messages. Evidence: `TestPermissionReasonClosedVocabulary`, `TestDecidePersistsStablePermissionReason`, `TestLegacyPermissionEventKeepsTextAndEmptyReason`, `TestPermissionReasonCatalogCoversClosedVocabulary`, `TestPermissionReasonTextLegacyAndUnknownBehavior`, `TestProjectorCarriesLocalizedPermissionReason`, `TestRenderPermissionReasonUsesCatalog`, `TestRenderLegacyPermissionTextUnchanged`, `TestPermissionModalShowsLocalizedReason` |
| Bash child environment is an allowlist that strips unsafe PATH entries, uses an isolated HOME, and points TMPDIR/TMP/TEMP at one private per-invocation directory | GUARANTEED | `childEnv` starts from an empty environment and copies only its allowlist. The temp-directory variables are no longer inherited: each invocation gets its own `nabd-tmp-*` directory, created 0700, never shared across invocations, and removed when the command finishes; if it cannot be created the child gets no temp variable at all rather than the caller's value. Evidence: `TestBashChildEnvAllowlistIntegration`, `TestBashChildEnvPathStripping`, `TestBashChildEnvHomePolicy`, `TestBashChildTempDirIsIsolatedAndRemoved` |
| Tool argument decoding rejects a key repeated anywhere, a case-varied spelling of a declared field, an undeclared field, and nesting past its ceiling before anything executes | GUARANTEED | [decodeStrict and rejectDuplicateKeys](https://github.com/amiraq1/Nabd/blob/a7acae7039343172572fcb5a9c7d1b4db2964728/internal/tools/decode.go) walk the raw payload before the typed decode, so the check covers every object including opaque fields (`mcp_tool`'s `args`). Keys are compared case-insensitively because encoding/json matches struct fields that way, so `{"cmd":"a","Cmd":"b"}` cannot land twice on one field with the last value winning; the refusal names the repeated key; a document nested past `maxJSONDepth` is refused by the walk, not by a syntax or type error. `DisallowUnknownFields` still rejects an undeclared field. Repair may still drop an undeclared key for ReadOnly and Executing tools, where that key is inert by construction, and never for write_file/edit_file, which share live fields. `glob` uses `json.Unmarshal`, not this decoder. Evidence: `TestDecodeStrictRejectsDuplicateKeysAtAnyDepthOrSpelling`, `TestDecodeStrictNamesTheDuplicateKey`, `TestDecodeStrictAcceptsManyUniqueKeys`, `TestBashRejectsNonStrictArgs`, `TestBashStrictDecodeStillFailsThroughLoop`, `TestReadFileStrictArgs`, `TestReadFileDecoderBoundaryWithRepairOff`, `TestWriteValidationRejectsBeforeSideEffects`, `TestMCPArgumentsStrictBoundary`. |
| The decodeStrict argument decoder rejects data after the first JSON value | GUARANTEED | [decodeStrict](https://github.com/amiraq1/Nabd/blob/a7acae7039343172572fcb5a9c7d1b4db2964728/internal/tools/decode.go) performs a second Decode that must return `io.EOF`, so an adjacent object, a trailing primitive, a trailing null or a trailing array is refused while trailing whitespace and a trailing newline are not. Scope: the tools that decode through decodeStrict — bash, read_file, write_file, edit_file, skill and mcp_tool. This is not a claim about every tool: `glob` uses `json.Unmarshal`, which rejects trailing data on its own. The repair/gate path and the payload the permission prompt displays remain open in V14. Evidence: `TestDecodeStrictRejectsTrailingData`, `TestBashRejectsNonStrictArgs`, `TestReadFileDecoderBoundaryWithRepairOff`, `TestMCPArgumentsStrictBoundary`, `TestWriteValidationRejectsBeforeSideEffects`. |
| Repository execution-hook and build-file writes require a separate execution approval | NOT PROVIDED | Ordinary Mutating approval, session grants or YOLO do not establish a separate deferred-execution consent boundary. Writing hooks, build scripts or environment hooks may lead to execution later, inside or outside nabd. Proposed protected-path policy is V15, not an implemented guarantee. |
| Mutation recovery intent is journaled and synced before a file publish; failed pre-publish attempts are marked separately, while a committed edit remains undoable after restart | GUARANTEED | `edit_intent` and `edit_abort` are journal-only recovery records; critical journal events use the durable sink path before the filesystem mutation proceeds. The failure matrix distinguishes file-fsync and rename failures before publication from a parent-fsync failure after publication, so recovery never guesses from an ambiguous error. Evidence: `TestMutationIntentFailureDoesNotPublish`, `TestMutationAbortEventsTrackPrePublishFailure`, `TestWriteFileAtomicFailureMatrix`, `TestWriteFileAtomicReportsPublishedWhenParentFsyncFails`, `TestCriticalEventsSyncDurableSink` |
| Git header subprocess inherits no parent environment: only PATH/TERM/LANG/LC_ALL are forwarded, secrets and `GIT_CONFIG_GLOBAL` are dropped, and Env is never nil | GUARANTEED | `TestGitChildEnvForwardsOnlyAllowlist` |
| Git status header never executes commands defined by repository-controlled config | GUARANTEED | `core.fsmonitor=false` is forced via `-c` (highest precedence); status runs with `--no-optional-locks` and `--ignore-submodules=all`; filter detection applies to repository-controlled scopes (local, worktree, their includes, and any unknown scope) via `--show-scope`, trusting system and global scopes as user-installed; when a repository-controlled scope defines a `filter.*.clean` or `filter.*.process` driver, status is skipped and the header stays hidden; a failed config read or malformed output fails closed. Evidence: `TestGitHeaderIgnoresRepoFsmonitor`, `TestGitHeaderSkipsRepoCleanFilter`, `TestGitHeaderStillWorksOnPlainRepo`, `TestRepoConfigDefinesCommands` |
| Git status header never runs user-installed filters or future config-driven commands | NOT PROVIDED | A user-installed system/global filter referenced by repository attributes may run; future Git config-driven execution paths are not covered until reviewed. |
| Opened config must be regular, user-owned on Unix, and have no group/other permission bits | GUARANTEED† | `internal/config` ParseFile and secure-open tests (EVIDENCE-01). |
| Config v1 and Config v2 default files coexistence on disk is fatal at startup | GUARANTEED | `TestV2CoexistenceOnDiskIsFatal` |
| Config v2 rejects unknown fields, trailing JSON, and custom `base_url` | GUARANTEED | `TestV2RejectsUnknownFieldsAndTrailingJSON`, `TestV2CredentialFileAndClosedEndpointPolicy` |
| Config package never writes configuration or credentials to disk | GUARANTEED† | package API and configuration tests (EVIDENCE-01). |
| Config descriptor opening rejects final-component symlinks and blocking special files | REDUCED | `O_NOFOLLOW` and `O_NONBLOCK`, followed by descriptor validation. Residual: parent-directory symlinks may still be traversed; this is not descriptor-relative containment of the whole config path. |
| Config v2 has no implicit environment credential fallback | GUARANTEED† | Credential sources must explicitly use `env` or a secure absolute `file`. This is a credential-source claim, not a promise that Config v2 ignores all environment variables; V1 tracks the wider audit; specific credential-source evidence is required in EVIDENCE-01. |
| Removed sandbox configuration keys requesting a boundary fail closed at startup before provider or tool execution | GUARANTEED | When `NABD_BASH_SANDBOX=on`, `NABD_BASH_NETWORK=deny`, or `NABD_BASH_RESOURCES=limit` is present in environment or configuration files, startup fails closed with an explicit error instructing the user to remove the setting; no provider request is made and no subprocess is executed across headless and interactive entry points. Neutral values (`auto`, `off`, `allow`, empty) emit a one-line warning on stderr that the setting is ignored. Evidence: `TestRemovedBashKeysRequestingBoundaryFailClosed`, `TestRemovedBashKeysNeutralValuesWarnOnly`, `TestRemovedBashKeysFailBeforeProviderOrTool`, `TestRemovedBashKeysFailClosedInteractiveStartup` |
| The router's Retry-After wait budget is bounded, opt-in, and pre-commit only | GUARANTEED | `NABD_ROUTER_RETRY_AFTER_WAIT` is parsed into whole seconds in `[0, 120]` and defaults to 0 (disabled); the wait runs at most once per `Stream`, strictly before the commit point, through the injected clock and cancellable by the parent context. Evidence: `TestParseRetryAfterWait`, `TestWithRetryAfterWaitClamps`, `TestShouldWaitOut` |
| The runtime status row never claims progress after a terminal failure | GUARANTEED | `RunError` and `Interrupted` retire the progress claims when the event is projected, instead of waiting for the runner goroutine to return; the send gate (`busy`) is left set so a failed run cannot be followed by a second concurrent run before `doneMsg`. Evidence: `TestRunErrorRetiresProgressStatus`, `TestRunErrorKeepsSendGate`, `TestInterruptedRetiresProgressStatus`, `TestDoneMsgClearsRunFailedStatus` |
| A real SIGINT to a headless run exits 130 and records the stopped status | GUARANTEED | A subprocess harness re-executes the binary as a child, blocks a turn, and sends SIGINT to the child only; the child exits 130 and the journal's `run_end` carries the stopped status rather than success or failure. Evidence: `TestHeadlessRealSIGINTSubprocess` |
| Newly visible route statuses disclose no more than the existing ones | GUARANTEED | `waiting` and `blocked` notices go through the same `display.SanitizeForDisplay` policy with redaction enabled, are single-line, never include `StreamID`, and fall back to fixed placeholders for blank fields. `attempted` and `exhausted` remain hidden. Evidence: `TestFormatRouteNoticeWaitingIsVisible`, `TestFormatRouteNoticeWaitingRedactsSecrets`, `TestFormatRouteNoticeBlockedIsVisible`, `TestFormatRouteNoticeStillHidesStructuralStatuses` |
| Status-row runtime metadata reports only committed routes and discloses no new fields | GUARANTEED | `StatusProjector.Meta` reports a provider/model only from a `selected` route trace, never from `attempted`, `failed`, `waiting`, or `blocked`; elapsed time freezes at the terminal event; provider and model pass through the same sanitizer as route notices; `StreamID`, credentials, and file paths are never included. Evidence: `TestMetaTracksTurnTokensAndCommittedRoute`, `TestMetaElapsedFreezesAtTerminalEvent`, `TestRuntimeMetaSanitizesRouteFields`, `TestRuntimeStatusRowStaysOneRow` |
| Rendered failure detail is redacted, bounded, and adds no new source of data | GUARANTEED | `FormatRunError` reads only fields already in the `run_error` event (`Err`, `ErrorCode`, `Code`), passes every line through `cleanField` (sanitizer with redaction enabled), emits single logical lines only, caps detail lines at `maxRunErrorDetails`, and discloses that the remainder stayed in the journal. Hints are fixed literals keyed by `ErrorCode`, never provider text. Evidence: `TestFormatRunErrorKeepsCodeAndRouteDetail`, `TestFormatRunErrorRedactsSecrets`, `TestFormatRunErrorCapsDetails`, `TestRenderRunErrorRedactsSecrets`, `TestRenderRunErrorRespectsWidth` |
| The router's retry-after reaches the feed as a field and is stated at every width, on both surfaces | GUARANTEED | `agent.RunErrorEvent` lifts the shortest positive `RouterExhaustedError.RetryAfter` onto the pre-existing `Event.RetryAfter` field; `presentation.ErrorCardFromEvent` carries it to `ErrorCard.WaitSeconds` and `FormatRunError` to `RunErrorView.WaitSeconds`. The feed card renders it on its own line before the actionable lines — so the width ladder, which hides the card's `Message` below 40 columns and truncates it above that, can drop prose but never this number — and only a positive value is lifted, so no card claims a wait the router never reported. Evidence: `TestFeedErrorCardShowsWaitAtEveryWidth`, `TestFeedErrorCardStatesNoWaitWhenNoneReported`, `TestErrorCardCarriesWaitSeconds`, `TestRenderRunErrorShowsWaitAtNarrowWidth` |
| Tool-output truncation is recorded as data and disclosed on the row that reports the result | GUARANTEED | `Event.ForStore()` records the number of discarded bytes in `ToolCall.TruncatedBytes` alongside the existing in-output marker; the field is additive (`omitempty`, legacy journals decode to `0`), the live in-memory event is never mutated, and the renderer states the loss on the `tool_end` row within the terminal width. Evidence: `TestForStoreRecordsTruncatedBytes`, `TestForStoreLeavesSmallOutputAlone`, `TestForStoreDoesNotMutateLiveEvent`, `TestTruncatedBytesIsAdditive`, `TestToolEndStatesTheLoss`, `TestToolEndSaysNothingWhenNothingWasCut`, `TestTruncatedToolRowRespectsWidth` |
| Feed item retention is bounded and disclosed without mutating journal history | GUARANTEED | When the merged projected feed exceeds `maxVisibleFeedItems`, `visibleFeedItems` inserts one synthetic notice that states the exact number of older items omitted from the in-memory feed and identifies the session journal as the full-history source. The notice occupies one slot, the newest items remain visible, and neither projector items nor journal events are modified. Evidence: `TestVisibleItemCapStatesHiddenHistory`, `TestLineCacheNeverExceedsVisibleItemCap` |
| Plan mode is strict read-only and cannot be overridden by a session grant or YOLO | GUARANTEED | `perm.Policy` carries a `Mode`; when it is `ModePlan`, `Check` returns `Deny` for every `Mutating`/`Executing` tool and short-circuits before the YOLO and standing-grant branches, so neither a per-session "allow for this session" nor `SetYOLO(true)` can write a byte or run a command. Reads (`ReadOnly`) still pass, so a plan-mode run can inspect the tree but never change it. The mode is applied to both the interactive gate (`gate{pol}` in `doChat`/`doChatWithFeed`) and the headless gate, so the same rule holds with and without a TTY. Evidence: `TestModeTable`, `TestModePlanOverridesGrants`, `TestModePlanAllowsReads` |
| Shadow blobs are SHA-256 addressed and verified on read; publication never silently replaces an existing blob | GUARANTEED† | `internal/snap` checksum and rename capability tests (EVIDENCE-01). |
| Undo refuses when current bytes no longer match the recorded hash | GUARANTEED† | undo and persisted-undo tests (EVIDENCE-01). |
| Prompt injection through ReadOnly output | REDUCED | nonce fencing plus approval for sensitive actions; model behavior is not guaranteed |
| Compaction summary preserves the original untrusted-content fence | NOT PROVIDED | Supplied review describes the summary entering model context as user-role text prefixed `Session summary of what came before:` without the original tool-output fence. General permission checks remain, but are not a summary-label preservation mechanism. Adding a fence is proposed in V17, not implemented here. |
| Agent notices forwarded to the model context are restricted to an enumerated allowlist that defaults to deny | GUARANTEED | Only notices that change world-state (such as /undo results) or restrict required capabilities (such as loop limits) reach the provider message projection; monitoring, calibration, and display notices never enter the model conversation. Forwarded notices are explicitly tagged («notice») as agent/system events rather than raw user text. The tag is a semantic signal that does not technically prevent model compliance with allowed notices; the guarantee is narrowing injectable content, not immunizing the model against permitted notices. Evidence: `TestCalibrationNoticeNeverReachesTheModel`, `TestBlockedNoticeCategoriesDroppedWhileToolCallsPending`, `TestAllowedNoticeCategoriesFlushedAfterToolCallsPending` |
| The text of a notice that reaches the model is structured, bounded, and rendered by one choke point | GUARANTEED | `renderNotice` in `messages.go` is the only translation from a notice event to the model-facing line: a structured `NoticeData` payload is authoritative and the human `Text` is never consulted beside it, a payload that does not match its `NoticeCategory` is dropped rather than repaired, and text-only events (journals written before the payload existed) are sanitized and bounded so replay is unchanged. Every line passes `provider.SanitizeBody` for credential redaction, has every control rune collapsed so one notice is exactly one logical line, cannot open with the `«notice»` frame marker, and is capped at `maxNoticeBytes`. The emitters are guarded structurally: `TestAllowedNoticeEmitsCarryStructuredPayload` fails if an allowed category is ever constructed with raw `Text` and no payload, or if any allowed category has zero production emit sites; `TestAllowedNoticeGuardDetectsCategoryWithoutEmitter` proves that the AST guard detects orphan categories without emitters. Evidence: `TestNoticePayloadIsAuthoritativeOverHumanText`, `TestNoticePayloadCategoryMismatchIsDropped`, `TestNoticeLineIsBoundedSingleLineAndControlFree`, `TestLegacyNoticeTextIsSanitizedAndStillFlushed`, `TestNoticeTextCannotSpoofTheFrame`, `TestStructuredNoticeRendersPerCategory`, `TestNoticeProjectionUsesTheRenderer`, `TestAllowedNoticeEmitsCarryStructuredPayload`, `TestAllowedNoticeGuardDetectsCategoryWithoutEmitter` |
| Tool-call repair changes what runs relative to what the model asked for (NBD-420) | REDUCED | `internal/tools/repair.go` corrects malformed calls before the registry lookup, so the gate classifies and prompts on the corrected call. It is a new surface between the model and the gate, bounded by design: inference is one-directional (an unrecognised name resolves only to a ReadOnly tool — `read_file`, `glob`, `grep` — and `write_file`/`edit_file`/`bash` require a literal match); the name map is explicit, never edit distance or string similarity, and every target is verified against `known`; path resolution is untouched, because `Root.Resolve` alone decides inside/outside the root and the layer never expands `~`, absolutises or relativises anything; and a call needing more than three corrections is returned unchanged. An argument key the tool does not declare is dropped only where it is inert — where it cannot change the action the user approves — and never where it may be a live field. For `bash` the whole action is the `cmd` string, which the prompt displays verbatim, so any other key changes nothing that runs; for `write_file` and `edit_file`, `old`, `new`, `all` and `content` are live fields, so dropping an undeclared one — `old` from a `write_file` call, say — would turn a local edit into a whole-file replacement, which is a different action rather than a narrower one. Every drop is reported as a fix and carried on the `tool_start` event as `DroppedArgs`, so the executed arguments being shorter than the model's is stated rather than implied, and the prompt is built from the repaired call, so the arguments on screen are the arguments that run. A name still unknown after the map returns the existing unknown-tool error, which the model reads and recovers from. Proof: `TestRepairInferenceIsReadOnlyOnly`, `TestRepairNameMapTargetsAreDeclared`, `TestRepairPathResolutionIsUntouched`, `TestRepairCapReturnsUnchanged`, `TestRepairDropsUndeclaredKeysOnlyWhereSafe`, `TestBashPromptShowsTheRepairedCall`, `TestRepairRulesSaveARound` |
| File reads open through descriptor-relative operations and refuse symlinks and non-regular files | GUARANTEED | On unix, `internal/safefs.OpenRead` walks the relative path from a root descriptor with `O_NOFOLLOW` and validates the target from the opened descriptor with `Fstat`. Evidence: `TestOpenReadRefusesFinalSymlink`, `TestOpenReadRefusesIntermediateSymlink`, `TestOpenReadRefusesFIFO`, `TestOpenReadRefusesDirectory`, `TestCaptureFromRootRejectsFinalSymlink`, `TestCaptureFromRootRejectsIntermediateSymlink` |
| File mutations publish relative to the parent descriptor and re-prove the target at the syscall | GUARANTEED | `write_file`, `edit_file`, and `/undo` derive one relative path and never open, stat, or rename a project file by absolute path on unix; the absolute path is reporting metadata only. The pre-mutation content hash is re-verified immediately before publishing a write to abort loudly if the file changed since capture. Evidence: `TestWritePathFromRootRejectsTraversal`, `TestWriteFileAtomicRefusesIntermediateSymlink`, `TestWriteFileAtomicReplacesFinalSymlinkWithoutFollowing`, `TestWriteFromRootDoesNotUseAbsoluteMetadataAsAuthority`, `TestCommitRejectsSymlinkWithoutCapturingOutsideContent`, `TestReadSourceFromRootUsesRelativeAuthority`, `TestRemoveFromRootUsesRelativeAuthority`, `TestRemoveFileRemovesFinalSymlinkNotReferent`, `TestUndoRefusesModifiedAfterAgentWrite`, `TestVerifyUnchanged`. Supplementary structural guards (AST-based; they pin the delegation shape, not the path property): `TestWriteCommitDelegatesToAdapters`, `TestWriteCommitUnixAdapterIsDescriptorOnly`, and `TestWriteCommitOtherIsCompatibilityPath` |
| Traversal tools never surface symlinked entries and skip the shadow store | GUARANTEED | `glob` and `grep` list and search regular files only, and `skipDir` excludes `.ag`, so the content-addressed shadow history is never read back. Evidence: `TestGrepNeverSurfacesSymlinkedEntry`, `TestGrepSingleFileRefusesSymlinkEscape`, `TestGlobOmitsSymlinkedEntries`, `TestTraversalToolsNeverSurfaceShadowStore` |
| Shadow-store ignore-file creation reduces accidental Git inclusion without replacing an existing ignore file | REDUCED | Every store write-open asserts `<root>/.ag/.gitignore` as the single rule `*` with `os.O_CREATE` and `os.O_EXCL` and mode 0600 inside the 0700 store directory — not only when the directory is first created, so a store that predates the shield, or lost its file, is shielded by the next edit; exclusive creation makes the "never replace an existing file" promise atomic, an existing `.gitignore` (user content, or an older shield's `/shadow/` rule) is left byte-for-byte alone, and a failed shield write is deliberately ignored so it can never be the reason an edit fails. Residual: an existing conflicting ignore file, an ignored creation failure, already tracked content or forced addition can defeat Git invisibility. Ignore rules are not confidentiality controls. Evidence: `TestShieldGitignoreKeepsProjectStoreOutOfGit`, `TestShieldGitignoreNeverReplacesExistingFile`, `TestShieldGitignoreExistingContentStaysByteForByte`, `TestShadowGitignoreAddedToExistingStoreOnEdit`, `TestShadowGitignoreRecreatedOnNextWrite`, `TestGitignoreWriteFailureDoesNotBlockTheEdit`, `TestUndoStillWorksWithShieldedGitignore` |
| Release SBOM coverage and threat-model evidence citations are mechanically checked before merge | GUARANTEED | `TestReleasePipelineContracts` and `scripts/check-threat-model-tests.sh` |
| Path opening after `Resolve` outside the descriptor layer | REDUCED | The `!unix` compatibility adapter in `internal/tools/write_commit_other.go` (`TestWriteCommitOtherIsCompatibilityPath`) carries no descriptor guarantee; `bash` is not contained; `snap.Restore` / `snap.RestoreAt` are a path-based publish path, now test-only (`docs/TECH_DEBT.md`); configuration reading still traverses parent-directory symlinks. Residual: a same-uid attacker through those paths |
| Journal redaction is enabled by default | REDUCED | recognized credential patterns (Anthropic, OpenRouter, OpenAI `sk-proj-`/long legacy `sk-`/`sk-svcacct-`/`sk-admin-`/`sk_`, xAI `xai-`, Google `AIza`, Hugging Face `hf_`, Groq, NVIDIA, AWS access-key IDs, PEM private-key blocks, bare JWTs, GitHub, GitLab, Slack, `Bearer`/`authorization`) and exact provider keys from configured providers (Router and standalone, min length 8) are removed from new events before `Event.ForStore()` and output truncation. Pattern matching is best effort: streamed `text_delta` chunks are joined through `redact.Stream` before they reach a sink, so a credential or PEM block split across chunks is redacted as one value; only the listed formats and configured exact keys are recognized and hold-back is capped, so an unrecognized, unterminated, or over-cap secret can still survive. `NABD_REDACT_JOURNAL=0` is an explicit diagnostic opt-out; Config v1 in `~/.ag/config` takes precedence over the environment. Copy-on-write leaves the live in-memory event untouched. Unrecognized sensitive content, structural fields (paths, tool names, call IDs, hashes, blob addresses, error codes), and the shadow store are unchanged. `--json` applies the same policy so it cannot diverge. Evidence: `TestRedactExtendedPatterns`, `TestRedactIdempotent`, `TestStreamMatchesWholeEverySplit`, `TestJournalRedactsSecretSplitAcrossDeltas`, `TestHeadlessJSONRedactsSplitSecret`, `TestStreamFlushedOnInterrupt`, `TestJournalRedactionEnabledByDefaultAndDisabledOnlyByExplicitZero`, `TestJournalRedactionReadsV1Config`, `TestOpenSessionJournalUsesEnabledRedaction`, `TestRouterSecretKeysAggregatesRoutes`, `TestExactKeysHelper` |
| Stream redactor hold-back state transitions preserve PEM boundary markers | REDUCED | Over-long token runs being swallowed across chunks terminate before any PEM block boundary, and PEM blocks are evaluated after swallow transitions so boundary markers remain intact. Evidence: `TestStreamSwallowPreservesPEMBoundary`, `TestStreamSwallowHoldsAcrossDashBoundary` |
| Open PEM block hold-back in stream redaction is bounded | REDUCED | Unterminated or open PEM blocks and incomplete markers are bounded by `StreamPEMHoldCap` (8KB); exceeding the cap fails closed by emitting a redaction token and swallowing the remainder of the block until the matching end marker or flush. Evidence: `TestStreamPEMHoldCapOpenBlock`, `TestStreamPEMValidBlockMatchesRedact`, `TestStreamPEMSwallowFindsSplitEnd` |
| Stream redactor never splits a complete match across emitted and held chunks | REDUCED | Cut points that straddle a recognized secret pattern or configured exact key are clamped back to the match boundary, ensuring complete credentials are not split into an unredacted emitted prefix and held suffix. Buffer growth stays bounded because the clamp runs only after the pre-clamp over-cap guard: a token run longer than `StreamHoldCap` is emitted as one Token with its remainder swallowed, so a match that spans the whole buffer can never hold it without limit. Evidence: `TestStreamCutNeverSplitsCompleteMatch`, `TestStreamCutNeverSplitsExactKey`, `TestStreamPendingBoundedOnLongBearerChain`, `TestStreamLongBearerTokenStaysCapped` |
| Journal continuation handles a torn final line without appending across it | GUARANTEED | On append-open, a malformed final line is copied to a private `*.recovery-*` artifact outside the active `*.jsonl` session namespace, and the active journal is truncated to its valid prefix before continuation. A malformed middle line refuses the append without modifying the source; a valid final JSON line without a newline is separated before the next event. Appending refuses symlink targets up front and opens append and recovery targets with `O_NOFOLLOW`. Evidence: `TestNewJSONLRepairsTruncatedFinalLine`, `TestNewJSONLSeparatesValidFinalLineWithoutNewline`, `TestNewJSONLRefusesCorruptMiddleLine`, `TestAppendRefusesSymlink` |
| Unresolved mutation intents are classified on continuation without automatic replay | GUARANTEED | When the active branch contains an `edit_intent` without a matching `edit_record` or `edit_abort`, `--continue` compares the current descriptor-relative file state with the recorded before/after hashes, emits a display-only recovery notice with counts for `not_published`, `published`, `missing`, `conflict`, and read errors, and never re-executes the mutation. Abandoned branches are ignored. A subprocess crash harness proves the intent survives a killed process and each post-crash filesystem state is classified without changing the target. Evidence: `TestUnresolvedMutationIntentRequiresExplicitRecovery`, `TestCommittedOrAbortedMutationIsNotUnresolved`, `TestUnresolvedMutationIgnoresAbandonedBranch`, `TestReconcileMutationClassifiesWorkingTreeWithoutWriting`, `TestReconcileMutationRejectsUnsafePath`, `TestMutationCrashRecoveryMatrix` |
| Session purge is bounded and opt-in | GUARANTEED | nabd purge is a dry run unless --yes is supplied. [purgeCandidates](https://github.com/amiraq1/Nabd/blob/51859a06b5e4ba7d35babad700a4376acaa84a7d/cmd/ag/purge.go#L85) examines only direct entries of the selected directory: a candidate must be a regular file according to os.Lstat, and its name must end in `.jsonl` or contain `.archived-` (not necessarily a well-formed archive suffix). With a cutoff, its modification time must be strictly before that instant. No recursive walk or symlink follow is performed. Same-uid concurrent mutation is not made race-free. Evidence: `TestPurgeIsDryRunUnlessConfirmed`, `TestPurgeDeletesOnlyEligibleRegularJournals` (includes old.jsonl.archived-1700000000000000000), `TestPurgeRejectsInvalidCutoff`. V20 records a stale comment, not an unknown eligibility rule. |
| Redacted export leaves the source intact | GUARANTEED | `--export --redact` decodes and re-encodes to stdout; the source is opened read-only and never written. Raw `--export` copies source bytes verbatim. Evidence: `TestExportLeavesSourceUntouched`, `TestExportRawIsByteIdentical` |
| Pointer input can select and expand cards, but never answers permissions or executes tools | GUARANTEED | Evidence: `TestPointerNeverAnswersPermission`, `TestPointerNeverExecutesATool` |
| Permission modal ignores typeahead keystrokes during arm delay, resets delay on any non-decision key, and requires intentional confirmation | GUARANTEED | Evidence: `TestPermissionModalIgnoresTypeaheadDecisionKeys`, `TestPermissionModalAcceptsDecisionAfterArmDelay`, `TestPermissionModalArmDelayShowsHint`, `TestNavigationYNoLongerCopiesOrApproves`, `TestPermissionModalEnterNeverAllows`, `TestPermissionModalCtrlCDuringArmDelay`, `TestPermissionModalRapidTypingYesPleaseNoDecision`, `TestPermissionModalUppercaseDecisions`, `TestPermissionModalHintLineDimensions` |
| Git status header inspects only the granted root and never traverses to a parent repository | GUARANTEED | `isGitRepo` checks `.git` strictly at the root; parent repos are out of bounds. Evidence: `TestIsGitRepoDetection` |
| OSC 52 clipboard copy operates on canonical projected fields only, after credential redaction and display sanitization | GUARANTEED | Human-language cards bypass BiDi visual clusters when copied, so reordered Arabic and mirrored delimiters cannot replace logical source text. Tool cards retain their established projected/redacted summary and output contract. Raw journal bytes remain unavailable to the clipboard path. Evidence: `TestCopyRedactsRecognizedCredentials`, `TestCopyNeverUsesRawJournalContent`, `TestCopyRejectsRawErrorBodies`, `TestCopyIsBlockedByPermissionModal`, `TestCopyNeverExecutesACommand`, `TestCopyUsesLogicalSourceWhenDisplayIsReordered` |
| A bare `nabd` runs the feed UI, and `--feed=false` is the rollback that removes the `@` index | GUARANTEED | The `-feed` flag defaults to `true`, so the default interactive path is `doChatWithFeed` — the same path that calls `SetPickerRoot(root.Dir())` and therefore indexes the session root for `@`. `--feed=false` selects `doChat`, which wires no picker and performs no scan; the two entry points are mutually exclusive within one invocation, and the rollback needs no rebuild or reinstall. Evidence: `TestFeedIsTheDefaultInteractiveUI` |
| Provider API keys never enter child environments or error output | GUARANTEED | `internal/provider/` constructor routes load keys from parsed config only, never through `os.Setenv`; `loadEnv` does not push credentials into the process environment that `bash` children inherit. Error bodies pass through `redact.Redact` before logging. Evidence: `TestConstructorsCarryKey`, `TestRouterRedactsBearerAuthorization`, `TestRouterFallsBackOnCredentialFailureWithRedactedLog`, `TestParseRoutesErrorMessagesContainNoSecrets` |
| Recognized credential patterns are redacted from logs, journal, and display | REDUCED | `internal/redact/` matches Anthropic, OpenRouter, OpenAI (`sk-proj-`, long legacy `sk-`, `sk-svcacct-`, `sk-admin-`, `sk_`), xAI (`xai-`), Google (`AIza`), Hugging Face (`hf_`), Groq, NVIDIA, AWS access-key IDs (`AKIA`/`ASIA`), whole PEM private-key blocks (including an unterminated `BEGIN` line, redacted to end of input), bare JWTs, GitHub, GitLab, Slack, and `Bearer`/`authorization` patterns, replacing them with `[REDACTED]`; `SanitizeBody` runs redaction before truncation. Configured exact provider keys (min length 8) are also redacted across journal, stream, and export sinks. Pattern matching is best effort: only the listed formats are recognized and streamed hold-back is capped, so an unrecognized or over-cap secret can still survive; streamed chunks are joined before redaction. Evidence: `TestRedactRecognizedCredentials`, `TestRedactExactKeys`, `TestRedactIsIdempotent`, `TestRedactExtendedPatterns`, `TestRedactNewProviderPatterns`, `TestRedactExactKeysMinimumLength`, `TestStreamMatchesWholeEverySplit`, `TestStreamUnterminatedPEMRedactedOnFlush`, `TestSanitizeBodyRedactsBeforeTruncation` |
| Interactive assistant text passes through streaming redaction before rendering | REDUCED | `doChatWithFeed` and `doChat` wrap the sink Fanout in `newStreamRedactSink`. Residual: recognized-pattern and stream hold-back limits still apply; this does not guarantee removal of every secret. Inherited evidence: `TestInteractiveFeedRedactsStreamedSecret`. |
| Plain headless stdout is redacted | NOT PROVIDED | The requested plain headless answer stays verbatim by design. Journal, JSON, copy and redacted export use their own redaction paths. Inherited evidence: `TestHeadlessPlainStdoutNotRedactedByDesign`, `TestHeadlessJSONRedactsSplitSecret`, `TestCopyRedactsRecognizedCredentials`, `TestExportLeavesSourceUntouched`. |
| Session journal files and directories are created private and legacy files hardened | GUARANTEED | `internal/store/` creates journal files with mode `0600` and parent directories with `0700` via `ensurePrivateParent`; existing files wider than `0600` are hardened with `Fchmod` before any data is written. Evidence: `TestNewJSONL_NewFileIsPrivate`, `TestNewJSONL_LegacyFileIsHardened`, `TestNewJSONL_CreatedAncestorsArePrivate`, `TestNewJSONL_MissingParentDirIsPrivate` |
| Release artifacts are signed | GUARANTEED | `.goreleaser.yaml` signs the `checksums.txt` manifest with `cosign sign-blob` and publishes `checksums.txt.sig` (the signature) and `checksums.txt.pem` (the certificate) as release assets, so verification needs only those three files; the build is pinned to a known Go toolchain. `release.yml` pins the cosign major via `cosign-release`, because `--output-signature`/`--output-certificate` are the legacy cosign v2 interface and cosign v3 ignores both in favour of `--bundle`, which leaves the manifest unsigned and aborts the release at the signing step. The release notes come from the matching `CHANGELOG.md` section: `release.yml` and the `release-dryrun` job in `ci.yml` extract them with `scripts/release-notes.sh`, write them to `RUNNER_TEMP` outside the working tree, assert the tree is clean (`git status --porcelain`), and pass the result to GoReleaser through `--release-notes`; extraction fails closed when the `## <tag>` section is missing or empty. Evidence: `TestReleasePipelineContracts`, `TestReleaseNotesExtractedFromChangelog`, `TestReleaseNotesWrittenOutsideWorkspace`, `TestReleaseAssertsCleanTreeBeforeGoReleaser`, `TestReleaseDryrunExtractsNotes` |
| Security gate scripts are mechanically checked | GUARANTEED | `scripts/check-pr-security-checklist.sh` enforces Phase 1 of the PR template and Phase 2 regression-test diff cross-checks with narrow N/A explanations for non-executable changes; Phase 1 resolves each item either by a full tick or, when it genuinely does not apply, by `- [x] N/A: <reason>` on the item's own line with a reason of at least 10 characters, while the three items that state an invariant applying to every pull request (classification, credential hygiene, action pinning) can never be N/A and an unchecked item always fails; the regression-test cross-check runs for the resolved item however it is written, so marking that item N/A cannot disable it; its `behaviour_free_diff` helper drops only the anchored diff headers (`^--- (a/\|/dev/null)`, `^\+\+\+ (b/\|/dev/null)`), so a removed `.go` line whose text begins with `--` or an added line beginning with `++` stays in the hunk and is inspected as behaviour instead of being mistaken for a header; `scripts/check-threat-model-freshness.sh` monitors every security boundary and all non-test files executing processes (`internal/ui/copy.go` and `internal/ui/git_header.go` alongside `internal/tools/`), kept in lockstep with the checklist gate and self-enforced by `scripts/check-security-invariants.sh` (which fails closed when `rg` is absent — never a vacuous green — proven by `scripts/test-security-invariants-rg.sh`, with CI installing `rg` and verifying its availability before the audit — the Verify step's first-run failure proved the audit had been vacuously green on CI itself) and `scripts/test-threat-model-freshness.sh`; `scripts/check-threat-model-tests.sh` verifies every backtick-quoted test citation in this document and in `docs/TECH_DEBT.md` resolves to a real `Test*` function (`NOTES.md` is deferred pending a historical-annotation convention; see `TECH_DEBT.md §NOTES_CITATION_GUARD_DEFERRED`); a dedicated automated path for Dependabot PRs replaces the human checklist only when both `PR_AUTHOR_LOGIN` and `GITHUB_ACTOR` are exactly `"dependabot[bot]"` for the current event (verified against workflow-injected values from GitHub Actions context, not the branch name; an event triggered by a human does not benefit from the automated path even if the PR author is Dependabot, but this check does not inspect git history or prove the absence of prior human commits on the branch) and every changed file falls within the strictly bounded dependency path scope (`go.mod`, `go.sum`, `third_party/*/go.mod`, `third_party/*/go.sum`, and `.github/workflows/*.{yml,yaml}` directly; this validates file path names and directory boundaries only, and does not parse diff contents or prove workflow changes are confined to action version bumps); any out-of-scope file, spoofed branch prefix, or human author/actor falls through to the full human checklist. Evidence: `TestPRChecklistGateScopesThreatModelClaim`, `TestPRChecklistGateSecurityPathsAreRealBoundaries`, `TestPRChecklistGateNoTestExemption` (its `Negative_RemovedDoubleDashLineInGoDiff_IsBehaviour` and `Negative_AddedTriplePlusLineInGoDiff_IsBehaviour` cases), `TestPRChecklistGateExplicitNA`, `TestPRChecklistGateNATestItemStillRequiresATest`, `TestPRChecklistGateDependabotPath`, `TestPRChecklistGateDependabotAllowedPrefixesDocumented`, `TestCitationGuardPassesOnCleanRepository`, `TestCitationGuardTechDebtIncluded` |
| The `@` picker index enforces its documented traversal, exclusion and result-limit rules | GUARANTEED | The `@` picker is reachable by default through `Feed.SetPickerRoot`; its traversal, session root `.gitignore` awareness, static exclusions, limits, and partial-index disclosure are documented and tested. Evidence: `TestDefaultTimeoutDoesNotBindBeforeTheCandidateLimit`, `TestGitignoreMaintainsPerformanceMarginOnWideTree`, `TestPickerExplicitSessionRootOverridesGitDir`, `TestScanRefusesSymlinkedEntries`, `TestScanGitignoreExcludesMatchingFiles` |
| Session `.gitignore` exclusions, as matched by the documented subset of `.gitignore` syntax, are enforced at the permission layer | GUARANTEED | Patterns from the session root `.gitignore` are parsed by `internal/ignorefile` (the same matcher `pathindex.Scan` uses for the `@` picker, so the picker, reader, and mutators cannot disagree) and installed on `perm.Policy` at startup via `wirePathRule`, which is also the registry's path gate. `read_file` and `edit_file` refuse directly named excluded paths before any byte is read or inspected; `grep` skips excluded paths, appending an `N files excluded by the session .gitignore` disclosure instead of silently searching less. `write_file` permits writes to excluded paths (preserving legitimate generation of build artifacts in `dist/`, `build/`) while strictly separating model-facing disclosure from mutation tracking: diff generation (`Patch`) is suppressed so excluded content is never disclosed to the model or journal, whereas mutation tracking retains blobs in `.ag/shadow` and consumes read credit so user `/undo` reversibility remains guaranteed. Named trust boundary: non-disclosure to the model and `@` exclusion are guaranteed by the gate and patch suppression; rollback retention in `.ag/shadow` is deliberately preserved so agent mutations can be undone by the user; isolation of the local `.ag/shadow` directory from unauthorized local access remains the host environment's responsibility. This retention includes cleartext excluded-file content and is not redacted. PR #270 proposals must not be treated as merged fixes; V16 tracks capture ordering and older-reader undo compatibility. Scope decision: the refusal for reads and edits is default in `ask`, `deny`, and `plan` modes; `--permission-mode allow-reads` is the override for `read_file` and `grep` only (`edit_file` remains denied in all modes as editing cannot be performed without inspecting content). `bash` and `glob` are out of scope: glob sees names only, and an approved shell command already runs with the user's authority. A zero-value or missing ignore file leaves the rule inert. Matcher scope: the subset documented under "Pathindex traversal surface and candidate picker limits" (declared limits: nested `.gitignore` files, global `core.excludesFile`, negation patterns, `**` recursive globbing, and `.git/info/exclude` are out of scope); it is not full Git semantics. Evidence: `TestInteractiveSessionWiresPathRule`, `TestHeadlessSessionWiresPathRule`, `TestReadFileRefusesIgnoredPath`, `TestReadFileRefusalIsNotFromPathindex`, `TestReadFileWithoutGateUnchanged`, `TestReadFileAllowReadsOverrideReadsIgnoredPath`, `TestEditFileRefusesIgnoredPathAndDoesNotLeakContent`, `TestShadowRetainsBlobsForUndoWhileSuppressingDiff`, `TestWriteFileToExcludedPathSucceedsWithUndoTracking`, `TestUndoRevertsWriteToGitignoredFile`, `TestExcludedWriteConsumesReadCredit`, `TestGrepSkipsIgnoredFilesWithDisclosure`, `TestGrepFromExcludedDirCannotEscape`, `TestCheckReadRefusesInAskAndDenyAndPlan`, `TestCheckReadAllowReadsOverride`, `TestCheckEditRefusesInAllModes`, `TestIsPathExcluded`, `TestPrefixDirectoryInheritance` |
| A bash cleanup signal cannot target a recycled process-group ID after the leader has been reaped | GUARANTEED | `killGroup` runs only on timeout/cancellation before `Wait` returns; clean completion does not perform a post-reap group sweep. Evidence: `TestBashCleanExitDoesNotKillProcessGroup` |
| Read-credit hashes and renders one bounded snapshot, and uses one normalized relative key for read/write matching | GUARANTEED | `TestReadCreditCompositeKeyStructure`, `TestReadFileCreditPathForAbsoluteInside`, `TestReadFileUsesDescriptorStat`, `TestReadSinglePassSourceShape`, `TestReadCreditHashMatchesModelVisibleSource`, and `TestReadCreditValidMatchingCycle`; files over the bounded snapshot limit are refused; the descriptor-stat guard is AST-based (call sites, not text) |
| Semantic loop detection bounds repeating identical tool executions | GUARANTEED | Tool calls are fingerprinted by `(tool, hash(input), hash(output-error))` across turns in a run; identical calls trigger a conversational notice at 3 repeats and a hard cut with `ErrToolLoop` at 5 repeats, preventing token and turn exhaustion from looping models. Evidence: `TestToolLoopNoticeAtThreeRepeats`, `TestToolLoopHardCutAtFiveRepeats`, `TestToolLoopCanonicalJSONKeyOrdering`, `TestToolLoopResetAcrossRuns` |
| Provider registry schema rejects undeclared dialects, unknown fields, and literal credential keys | GUARANTEED | `TestRegistryRejectsUnknownDialect`, `TestRegistryRejectsUnknownFields`, `TestRegistryRejectsLiteralKeyInProvidersFile`, `TestModelIDAliasIsSentToProvider` |
| Provider registry auth file enforces private 0600 permissions, owner check, atomic writes, and redaction in errors | GUARANTEED | `registry.WriteAuthFile` performs an atomic write via a temporary file with `O_CREAT` and `O_EXCL`, verifies it is a regular file via descriptor `fstat`, tightens permissions to `0600` on the open descriptor, fsyncs the temp file before atomic rename, and fsyncs the parent directory. Symlink targets fail closed. Concurrent modifications in `providercmd.Connect` are serialized using a platform sidecar file lock (`~/.ag/auth.json.lock`). Evidence: `TestAuthFileRejectsOpenPermissions`, `TestAuthKeysAreRedactedInErrors`, `TestWriteAuthFileLeavesNoTemp`, `TestLockAuthFileSerializes` |
| Provider catalog and credentials merge according to documented precedence with legacy builtin compatibility and safe migration backup | GUARANTEED | `TestBuiltinCatalogCoversLegacyProviders`, `TestRegistryFileOverridesBuiltin`, `TestRegistryPrecedenceIsDocumented`, `TestMigrateFromV1ConfigPreservesBackup` |
| Route construction resolves provider identity, dialect, endpoint, model alias, and read ceiling through the registry, and failures name the file to fix | GUARANTEED | `BuildRouteProviderWithRegistry` takes the dialect (`openai`/`anthropic`), `baseURL`, model alias, and `readCap` from the registry; an unconfigured provider error names the requested ID, every configured provider, and the `providers.json` path, while a missing key names the `auth.json` path and the legacy variable. Neither error includes a key value. Evidence: `TestRouteResolvesRegistryProvider`, `TestUnknownProviderErrorNamesConfigFile`, `TestLegacyEnvConfigStillBuildsRoutes` |
| Every provider, including the four legacy builtins, is built from the registry; no per-provider constructor remains | GUARANTEED | The per-provider constructors that named a provider and read its key variable directly are deleted. The non-router path builds through `BuildStandaloneProviderWithRegistry`, which resolves dialect, endpoint, default model, and read ceiling from the registry exactly as the router path does, and the router path is unchanged. A legacy env-only configuration with no `providers.json` still builds every route with the same endpoint, default model, and key as before. Evidence: `TestLegacyConstructorsAreGoneAndRoutesStillBuild` |
| The read ceiling is a property of the provider's registry entry, never of its name | GUARANTEED | The cap comes from `ProviderConfig`: an explicit `readCap` is used as written, and its absence means `DefaultReadCapBytes`. The builtin groq entry states `GroqReadCapBytes` in the catalog, so the metered cap is a declared value rather than a name match; a custom provider that happens to be called `groq` without `readCap` gets the default. The route and standalone paths both take the cap from the same entry. Evidence: `TestReadCapComesFromRegistryNotName` |
| The provider list is open; the only parse-time rule is the identifier's shape | GUARANTEED | `ParseRoutes` accepts any provider matching `[a-z0-9-_]` and ≤32 bytes and no longer consults a catalog, so adding a provider is a JSON edit rather than a code change; membership is decided at route construction. Evidence: `TestParseRoutesDefersProviderCatalogCheck` |
| Provider credentials are enrolled only through a hidden prompt and never accepted on the command line | GUARANTEED | `nabd connect <provider>` takes exactly one provider identifier; a key-looking token or any second argument is refused with `ErrKeyAsArgument` before any read. The key is read with echo disabled (`term.ReadPassword`, which fails on a non-terminal stdin) and written with `registry.WriteAuthFile` at mode 0600 after the owner check. The confirmation line never contains the key. Evidence: `TestConnectWritesAuthFileWithTightPermissions`, `TestConnectRefusesKeyAsArgument` |
| Interactive /connect credential handling refuses argument keys, conceals input, excludes secret bytes from session journals and model context, and persists at mode 0600 | GUARANTEED | In interactive TUIs (Chat and Feed), `/connect <provider>` accepts only a provider identifier and rejects key-like tokens or extra arguments via `ErrKeyAsArgument` before reading input. The credential is concealed by a different mechanism on each surface. CLI (`nabd connect`): read with echo disabled through `term.ReadPassword`, which fails on a non-terminal stdin. TUI (Chat and Feed, `/connect`): the terminal is in raw mode under Bubble Tea, so echo is not the control — `routeKey` routes every keystroke to `secretPromptKey` before the composer, so the bytes never reach `composerEdit` and are never drawn into the composer; the composer slot renders a fixed `API key (input hidden):` line that does not depend on the transient status row. Both surfaces then write through `registry.WriteAuthFile` at mode 0600. Secret bytes never enter the session JSONL journal, event stream, model prompt context, or screen rendering. Non-guarantee: protecting `auth.json` against local processes running under the user's authority remains the responsibility of the host. Evidence: `TestConnectSecretNeverEntersTheJournal`, `TestFeedSecretPromptSurvivesStatusRanking`, `TestFeedSecretPromptIgnoresPointerGestures` |
| The models probe reads the endpoint's own catalog and classifies failures with the existing provider codes | GUARANTEED | `nabd models <provider>` sends `GET <baseURL>/models` with the key in a header (never a query parameter or path), parses only `data[].id`, and maps a non-200 through `provider.ClassifyHTTPStatus` while a transport failure is `temporary`, so a network failure surfaces as the same `provider_temporary`/`provider_auth` vocabulary the runtime uses. A 200 is catalog success only, never evidence that the key is valid: some endpoints answer `/models` without authenticating at all, so a bogus key still gets 200. The command states that limit, and the first inference request is the real check; model rejection errors direct the user back to `nabd models <provider>` for the live catalog. Evidence: `TestModelsCommandParsesCatalogResponse`, `TestModelsSuccessDoesNotImplyValidKey` |
| The provider listing discloses sources, not secrets | GUARANTEED | `nabd provider` reports each provider's definition source (`builtin`/`providers.json`), key source (`auth.json`/`env`/`none`), and dialect, and names `~/.ag/auth.json` for a missing key; it prints no key value. Evidence: `TestProviderCommandReportsMissingKeys` |
| Provider `providers.json` baseURL and `NABD_BASE_URL` override are validated at load time: plaintext http and non-public addresses are refused by default | GUARANTEED | `endpoint.CheckBaseURL` runs during `ParseProvidersData` and `BuildStandaloneProviderWithRegistry`, rejecting any declared or overridden `baseURL` that uses a non-https scheme or resolves to a loopback, RFC 1918, link-local, CGNAT, ULA, 6to4, cloud-metadata literal IP, or `.internal`/`.local`/`localhost`-suffixed hostname unless explicitly permitted by `NABD_ENDPOINT_ALLOW`. The active policy is read from `NABD_ENDPOINT_POLICY` (default `strict`); `loopback` allows plaintext HTTP strictly for loopback and private LAN addresses with HTTPS while continuing to refuse cloud metadata; `open` disables both layers. Evidence: `TestStrictRefusesPlaintextHTTP`, `TestStrictRefusesCloudMetadataLiteral`, `TestStrictRefusesCGNATLiteral`, `TestStrictRefusesIPv6ULA`, `TestStrictRefuses6to4Literal`, `TestLoopbackStillRefusesHTTP`, `TestOpenLevelDisablesBothLayers`, `TestAllowListStrictAcceptsAllowedHTTP` |
| Provider dial-time endpoint guard evaluates the socket address via Dialer.Control immediately before connect, eliminating the TOCTOU rebinding window | GUARANTEED | `Policy.Dialer` configures `net.Dialer.Control` to inspect the literal socket IP address right before `connect(2)`. No secondary DNS lookup is performed, closing the DNS-rebinding window with zero TOCTOU: a hostname resolving to a private, loopback, link-local, CGNAT, ULA, 6to4, or metadata IP is aborted before any packet is transmitted unless permitted by `NABD_ENDPOINT_ALLOW`. Under `PolicyLoopback`, dial-time checks continue to refuse cloud metadata, link-local, CGNAT, and 6to4 addresses. When refused, `endpoint.ErrEndpointRefused` is wrapped and classified as `provider.ErrorKindEndpointRefused`, surfaced as `agent.ErrCodeEndpointRefused` with responsive remediation guidance. Evidence: `TestControlRefusesRebindingViaResolver`, `TestControlRefusesRebindingToPrivateAddress`, `TestControlRefusesLoopbackAddress`, `TestControlRefusesCloudMetadata`, `TestControlRefusesCGNATAddress`, `TestControlRefusesIPv6ULAAddress`, `TestControlRefuses6to4Address`, `TestControlPassesThroughPublicAddress`, `TestControlPermitsAllowedAddr`, `TestControlUnderLoopback`, `TestErrorKindEndpointRefused`, `TestCrossPackageEndpointRefusedRemedyFlow`, `TestFeedProviderErrorShowsEndpointRefusedRemedy` |
| No provider can be constructed with an unguarded HTTP client | GUARANTEED | Every provider constructor (`NewOpenAIDialect`, `NewAnthropicDialect`) defaults to `endpoint.Client(0)` governed by the active endpoint policy; omitting explicit client injection is fail-closed, and direct or route constructors cannot produce an unguarded client. When an environment proxy (`HTTPS_PROXY` / `HTTP_PROXY`) is configured, the proxy URL is validated against the endpoint policy at request time. Evidence: `TestConstructorDefaultsToGuardedClient`, `scripts/check-security-invariants.sh`, `TestTransportProxyValidation`, `TestCustomEndpointIsAcceptedByDesign` |
| Retryability of the provider's model-unavailable answer is decided by error type, never by message wording | GUARANTEED | Both the 404/410 status branch and the OpenAI-compatible body heuristic raise `*modelUnavailableError`; `transient` classifies the type with `errors.As`, so re-wording, re-accenting, or wrapping the message cannot change whether it is retried. The model, status, suffix, and optional complete rendering are data on the error. The standalone OpenAI-compatible path is exercised against a counting HTTP server and performs exactly one request for a 400 body reporting `model not found`. Every pre-existing user-visible shape remains byte-identical. Evidence: `TestModelUnavailableIsTypedAndNotRetried`, `TestModelUnavailableHeuristicIsTypedAndNotRetried`, `TestStandaloneOpenAICompatDoesNotRetryModelNotFoundBody`, `TestModelUnavailableRetryDecisionIgnoresWording`, `TestModelUnavailableRendersBothSitesByteIdentical`, `TestModelUnavailableSurvivesWrapping`, `TestHTTPErrorRetryLadderUnchanged` |
| Local runtimes permit loopback HTTP under PolicyLoopback and explicit allowlisting via NABD_ENDPOINT_ALLOW under PolicyStrict | GUARANTEED | `endpoint.CheckBaseURL` permits plaintext http strictly for loopback destinations (`127.0.0.1`, `::1`, `localhost`) under `NABD_ENDPOINT_POLICY=loopback` to support local runtimes like Ollama without TLS, while remote destinations still require https and cloud metadata is refused at load and dial time. Alternatively, `NABD_ENDPOINT_ALLOW` permits designated local or private endpoints under `PolicyStrict` while keeping full connect-time protection active for all other providers. Evidence: `TestLoopbackStillRefusesHTTP`, `TestLoopbackAllowsLoopbackAndPrivate`, `TestLoopbackAllowsLoopbackHTTP`, `TestLoopbackRefusesCloudMetadata`, `TestAllowListStrictAcceptsAllowedHTTP`, `TestAllowListStrictAcceptsAllowedPrivate` |
| `NABD_ENDPOINT_POLICY=open` disables both the load-time URL check and the connect-time dial guard | REDUCED | When an operator sets `NABD_ENDPOINT_POLICY=open`, `CheckBaseURL` returns nil for any URL and `Dialer` returns the dialer unchanged without registering a `Control` hook. This is the override for air-gapped or development environments that cannot use HTTPS. Whoever can set this variable can direct all connections to an arbitrary endpoint including plaintext and private addresses. The remaining controls are the 0600 permission and owner check on `providers.json` and `auth.json`. Evidence: `TestOpenLevelDisablesBothLayers`, `TestControlIsNoOpUnderOpenPolicy` |
| OpenAI wire encoding preserves pre-compat baseline structure by default | GUARANTEED | `OpenAICompat.encode` pins the baseline request structure: standard `max_tokens`, `stream_options.include_usage=true`, and canonical `system` → `tool` → `user` message ordering; zero values of future endpoint compat flags preserve this body. Evidence: `TestOpenAIEncodingBaseline` |
| Skill bodies entering model projection are explicitly classified, user-role, fenced, and fail-closed | REDUCED | `TestSkillBodyEntersTheModelAsUntrustedProjectContent` proves an explicitly classified body is labeled as untrusted project content inside the existing tool fence (`UNTRUSTED_DATA NOT_INSTRUCTIONS`), and that a body whose class is not the explicit untrusted value is dropped rather than projected. The class is fixed by the guarded producer and is never inferred from the body text, the skill name, or its path, so a definition cannot label itself trusted by what it says. Session wiring is active: the user scope loads unconditionally, and the project scope only when `NABD_SKILLS_PROJECT=1` is supplied through user-scoped configuration or environment. This narrows and labels injectable content but does not immunize the model from permitted content. |
| Skills are progressive-disclosure, project scope is off unless an operator opts in, and every body is re-hashed before it is served | REDUCED | Only name, description and scope reach the prompt; the body is read on invocation. Containment is who may write into each scope plus the recorded SHA-256, not what the body says. The complete binary tool vocabulary is independent of the session's active registry, so continuation can fence historical `skill` calls while an empty skill index leaves the tool inactive. Evidence: `TestProjectSkillsAreAbsentWhenOptInIsOff`, `TestProjectSkillSymlinkEscapeIsRefused`, `TestJournalRecordsHashMatchesLoadedBytes`, `TestSkillToolRefusesBodyChangedSinceLoad`, `TestSkillRegistrationFollowsLoadedIndex`, `TestFenceRejectsUnknownName` |
| Bash filesystem reach after approval | NOT PROVIDED | On Termux, approved bash runs with the full authority of the Termux app user; there is no filesystem sandbox. |
| Selected zero-width format controls are stripped at the display boundary | GUARANTEED | `display.SanitizeForDisplay` strips U+200B (ZWSP), U+200C (ZWNJ), and U+FEFF (BOM); ZWJ (U+200D) is preserved for emoji sequences and LRM/RLM (U+200E/U+200F) for legitimate RTL text. Product decision 2026-10-09: ZWNJ stays stripped despite the Persian/Urdu fidelity cost — invisible format controls are a bidi-spoofing vector. U+2028/U+2029 and other bidi/format controls require separate review (V3). Evidence: `TestSanitizeStripsZeroWidthFormatControls` |
| The headless empty-response decision reads the loop's structural notices, never their wording | GUARANTEED | The decision (`emptyResponse` in `cmd/ag/headless.go`) requires a `NoticeCategoryProviderEmpty` or `NoticeCategoryLengthLimit` notice on the live branch and reads no notice text; the text-blank and tool-activity exclusions are unchanged. `internal/agent/loop.go` keeps length-cut notices under `NoticeCategoryLengthLimit` and marks both non-length empty-response arms with `NoticeCategoryProviderEmpty`; the notice texts and their emission conditions are byte-identical. Rewording or translating a notice cannot change the exit code, and an unrelated notice carrying the old phrase cannot create one. It is not a detector of every cause of silence. Evidence: `TestEmptyResponseReadsCategoryNotWording`, `TestHeadlessEmptyResponse`, `TestHeadlessEmptyResponseWithOtherStopReasonKeepsExitCode`, `TestHeadlessEmptyResponseKeepsRunEndEnded`, `TestEmptyTurnEmitsNotice`, `TestJournalNoticeCategoryRoundTripDoesNotReachModel` |

## Residual-risk register

One line per residual, pointing back to the claim that carries it. Each claim
row stays authoritative.

| Area | Status in claims | Residual |
|---|---|---|
| `bash` filesystem reach after approval | NOT PROVIDED | Approved `bash` runs with the full authority of the Termux app user; there is no filesystem sandbox |
| Deferred execution via file writes | NOT PROVIDED | Ordinary write consent does not separately authorize execution by later consumers (V15) |
| Tool argument duplicate/unknown-field handling | REDUCED | The decoder covers duplicates at any depth, case-varied spellings, leading whitespace and the depth ceiling; the repair layer's declared-key drop for ReadOnly/Executing tools, and the untraced repair/gate/display path, remain (V14) |
| decodeStrict decoder end-of-input rejection | GUARANTEED | A second Decode must return `io.EOF`; the statement covers the tools that decode through decodeStrict, not `glob` |
| Compaction label preservation | NOT PROVIDED | Summary loses the original untrusted fence (V17) |
| Android shared/emulated storage | Prerequisite-dependent | Validate mode and filename semantics before relying on storage-dependent claims (V8) |
| Same-uid processes | Out of scope (Adversary 4) | Modes 0600/0700 limit cross-user reads only |
| Prompt injection through ReadOnly output | REDUCED | Nonce fencing is a semantic signal; sensitive actions rely on approval |
| Tool-call repair | REDUCED | The corrected call is what is prompted and run; inference is one-directional and bounded |
| Journal, stream and display redaction | REDUCED | Pattern matching is best effort; unrecognized secrets, structural fields and the shadow store are not redacted |
| Plain headless stdout | NOT PROVIDED | Not redacted, by design |
| Config opening; path opening after `Resolve` | REDUCED | Parent-directory symlinks are traversed in config reading; `!unix` adapter and `snap.Restore` are path-based |
| `NABD_ENDPOINT_POLICY=open` | REDUCED | Disables both the load-time check and the dial-time guard |
| Custom endpoints | See "Custom endpoints: policy-controlled" | The chosen endpoint receives prompts and the key; induced tool calls are still gated; redirect containment is not claimed |
| Skills | REDUCED | Project skills, when enabled, are instructions authored by whoever wrote the repository |
| Git status header: user-installed filters and future command paths | NOT PROVIDED | Separate from the repository-controlled-config guarantee; see split claim rows |
| MCP STDIO servers | NOT PROVIDED | No OS containment on Termux; servers run as the user |
| Separate per-run MCP opt-in | NOT PROVIDED | An enabled server in user-scoped `~/.ag/mcp.json` needs no extra per-run opt-in flag; `--no-mcp` disables MCP |
| Excluded-file shadow retention | Local confidentiality risk | Cleartext rollback copies are outside journal redaction; this is not a privacy guarantee |
| Missing specific evidence citations | Evidence debt | Five claim rows are marked GUARANTEED† and listed individually in EVIDENCE-01 |
| MCP and local non-disclosure | NOT PROVIDED | `.gitignore` non-disclosure, read-credit and `readCap` do not extend to MCP servers |
| MCP approval scope | NOT PROVIDED | One server approval opens all its ReadOnly tools until a fingerprint change; no revocation UI |
| MCP output injection | REDUCED | Permission state is unaffected; output remains untrusted content |
| Independent security review | NOT PROVIDED | No independent review is established by the supplied evidence; author/owner identity alone does not prove its absence (V19) |

## Path layer

`internal/tools/path.go` retains `Resolve` for display and compatibility paths.
Unix file tools normalize input into a root-relative path, and
`internal/safefs` walks that relative path from a root descriptor with
`O_NOFOLLOW`; the descriptor operation is the filesystem authority. A
component replaced after normalization therefore fails instead of redirecting
the operation, so the resolve-then-open race is closed for `read_file`,
`glob`, `grep`, `write_file`, `edit_file`, and `/undo`. The absolute path is
reporting metadata; read-credit matching uses the same normalized relative key
as mutation.

Reads refuse symlinks, directories, FIFOs, sockets, and devices. Mutations
publish atomically — a temporary file in the parent descriptor, a file fsync, a
rename, then a directory fsync — and share one target rule. Traversal tools
refuse symlinked entries outright and skip `.ag`, so the shadow store's
cleartext history is never searched.

`bash` deliberately does not use this layer. Its cleanup signals are issued
only on timeout or cancellation before the leader is reaped; clean completion
performs no post-reap process-group sweep, avoiding a recycled group-ID signal.
The `!unix` compatibility adapter in `internal/tools/write_commit_other.go` carries
no descriptor guarantee. `snap.Restore` and `snap.RestoreAt` remain a second,
path-based publish path and are test-only
(see `docs/TECH_DEBT.md`). Configuration reading still traverses
parent-directory symlinks.

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
- Custom `base_url` is recognized but rejected by the minimal schema. Config v2
  is the locked-down schema for an operator who wants no custom endpoint; see
  "Custom endpoints: policy-controlled".

The inherited assertion that the config package never writes configuration
files is GUARANTEED† evidence debt, not a newly verified guarantee; open item
EVIDENCE-01 requires a specific test or mechanical guard, or a downgrade. Bash
child construction starts from an empty environment and copies only its
allowlist, independently of config loading.

### Config security invariants

- Config v1 uses `NABD_CONFIG` or `~/.ag/config`.
- Config v2 uses `NABD_CONFIG_V2` or `~/.ag/config.v2.json`.
- Explicitly selecting both versions, or finding both default files, is a fatal startup error.
- Config and credential files are opened with `O_RDONLY`, `O_CLOEXEC`, `O_NOFOLLOW`, and `O_NONBLOCK`, then validated through the opened descriptor.
- Files must be regular, owned by the current Unix user, inaccessible to group and others, and within their configured size limits.
- Config v2 rejects unknown fields, trailing JSON, command credential sources, and implicit environment fallback.
- Config v2 credential files must contain exactly one non-empty line.
- Loaded credentials remain in package memory and are never copied into the environment of `bash` children.

### Ownership refusals and shadow refusals: typed errors, language at the boundary

- A config or registry file owned by another uid is refused through a typed error
  (`config.OwnerMismatchError`, `registry.OwnerMismatchError`) carrying the path,
  the owning uid, and the current-user description. Those packages hold no
  user-facing Arabic: they carry ASCII baselines, and the Arabic sentence is
  rendered once at the CLI boundary in `cmd/ag/errors.go` (ADR-0002, decision 5).
- The refusal keeps the original error reachable through `Unwrap`, so a caller
  that inspects the cause with `errors.Is`/`errors.As` still finds it; the
  refusal decision itself is unchanged.
- The shadow capture walk refuses a directory with `ErrShadowNotRegular` and a
  symlink with `ErrShadowSymlink`, both ASCII sentinels. These are filesystem
  diagnostics rather than user-facing prose (ADR-0002, decision 3); what the walk
  refuses, and when, is unchanged.
- Evidence: `TestCheckOwnerPlatform` pins the typed refusal and its facts,
  `TestCaptureRefusesDirsAndLinks` pins both sentinels, and
  `TestSourceArabicLiteralRatchet` pins that no non-catalog source file gained
  Arabic text.

### Router timing keys

`NABD_ROUTER_PRESTREAM_TIMEOUT` and `NABD_ROUTER_RETRY_AFTER_WAIT` are
non-credential timing values. Both are parsed into bounded whole seconds before
reaching the router, and neither can widen the router's authority: they change
only how long the router waits before failing over or before reporting
exhaustion. `NABD_ROUTER_RETRY_AFTER_WAIT` accepts `0..120` and defaults to `0`,
which disables the wait entirely and preserves the historical fail-fast
behavior. An out-of-range or non-numeric value is a startup error rather than a
silently reinterpreted default.

The wait itself is a bounded pre-commit pause: it happens only after every route
has failed, only when a provider-supplied `Retry-After` is positive and fits
inside the configured budget, at most once per request, and always through the
router's injected clock with a `select` on the parent context, so cancellation
and deadlines remain authoritative. It can therefore never interleave with
already-delivered output and cannot extend the worst-case latency beyond one
additional route cycle plus that single wait.

## Custom endpoints: policy-controlled

> Editorial note (2026-10-15). The previous text of this section (v10–v13) said
> that none of the endpoint admission conditions is implemented and that every
> `baseURL` is accepted. That conflicts with the claims-table rows on load-time
> and dial-time endpoint policy. This rewrite follows those rows because they
> cite tests. Maintainers must confirm it against the code before merging
> (open item V1).

`~/.ag/providers.json` accepts `options.baseURL`, and `NABD_BASE_URL` can
override it. The decision behind the open door is unchanged: adding a provider
or a model is a JSON edit, with no code change, no rebuild, and no dialect
plugin. That is what makes a local runtime at `http://127.0.0.1:11434/v1` or a
private enterprise gateway usable at all.

**What the policy does.** `NABD_ENDPOINT_POLICY` (default `strict`) selects how
endpoints are admitted. `NABD_ENDPOINT_ALLOW` permits named exceptions under
`strict`. The check runs twice: when the URL is loaded, and again on the
resolved socket address immediately before connect. The exact address classes,
the `loopback` and `open` behaviour, and the evidence are in the claims-table
rows beginning "Provider `providers.json` baseURL", "Provider dial-time
endpoint guard", "No provider can be constructed with an unguarded HTTP
client", "Local runtimes permit loopback HTTP", and "`NABD_ENDPOINT_POLICY=open`".

**What it does not provide, stated plainly:**

- Under `open`, both layers are off. Whoever can set that variable can direct
  every connection anywhere, including plaintext and private addresses.
- The policy limits *where* a connection may go. It does not make the endpoint
  that `providers.json` (or the override) names trustworthy. That endpoint
  receives every prompt, file excerpt and tool result, and the provider key in
  the request header.
- That endpoint controls the response stream, so it can emit `tool_calls` the
  model never proposed. Those calls still pass the permission gate, which is
  the boundary that remains.
- Cross-host redirect containment, embedded-credential rejection, and
  query/fragment rejection were withdrawn as requirements in v10 and no claim
  covers them. Treat redirects as unprotected until a claim says otherwise
  (open item V5).
- When an environment proxy is configured, the claims table says the proxy URL
  is validated against the policy at request time. No claim covers how the
  proxy itself resolves the destination (open item V5).

**What still holds.** Both registry files must be regular, owned by the current
user, and mode 0600, verified from the opened descriptor. Only two dialects
exist, and neither is loaded from a package at request time. Keys are never
written into a `bash` child environment and pass through `internal/redact`
before any log or display. Every tool call the endpoint induces is still
classified and prompted by the permission layer.

Config v2 rejects `base_url` outright, and that asymmetry is deliberate: v2 is
the locked-down schema for an operator who wants no custom endpoint at all,
while the registry is the open door for one who does.
Evidence: `TestCustomEndpointIsAcceptedByDesign`,
`TestV2CredentialFileAndClosedEndpointPolicy`.

## Skills

A skill is a Markdown file with a small frontmatter header. Session wiring loads
the index at startup: its name, description and scope are listed in the system
prompt, and its body is read only when the model calls the `skill` tool with that
name. The binary knows the `skill` vocabulary and fence even when a session has
no loaded index; the tool is then absent from the active registry. Twenty
installed skills therefore cost tens of prompt lines rather than twenty file
bodies — the body is not part of the prompt at all until it is asked for.

**Two scopes, one trust boundary.** `~/.config/nabd/skills` is the user scope
and is loaded unconditionally: a file there was put in place by the operator,
who is the principal this document is written for. `<root>/.nabd/skills` is the
project scope and is **disabled by default**. In v1, `NABD_SKILLS_PROJECT=1` may come from the user v1 file or the v1 environment fallback. In v2, only user-configured `skills.project=true` enables it; the `NABD_SKILLS_PROJECT` environment variable is ignored and there is no implicit environment fallback. In both versions project files are never consulted, so a repository cannot enable its own instructions. Evidence: `TestProjectSkillsStayDisabledWhenOptInLivesInTheProjectRoot`, `TestProjectSkillsLoadWhenOptInIsUserScoped`, `TestProjectSkillsOptInConfigV2`.

**What is recorded.** At session start one `skills` event lists
`{name, rel, hash, scope}` for every definition that reached the prompt. The
hash is SHA-256 over the body bytes as loaded, so a replay shows both which
instructions the session ran with and whether they came from the trusted or the
project scope. The `skill` tool re-opens the body through `internal/safefs`,
re-verifies that hash, and refuses on mismatch: a file edited after load — by the
model, by a checkout, by anything — is instructions the session never approved,
and serving it would make the journal a false record of the run.

**Guarded projection.** A skill body never travels as ordinary tool output.
Plain `skillTool.Run` is refused, and `Registry.Run`/`RunDetailed` cannot extract
a body; only the single `GuardedOutcome` producer emits the structured body
event. The producer fixes `SkillContentClassUntrusted` internally, so no caller
can promote the class. On successful guarded projection, `ToolEnd.Output`
remains empty and the loop passes an empty result string to loop detection, so detection is based on the
tool name and input, never on the body or producer text. A guarded producer
error is deliberately returned as an ordinary failed tool result so the error
remains visible to the model; this is a named product trust boundary, not a
claim that arbitrary product error text is body-free. The product must not
include skill-body bytes in that error. A guarded name without a producer is
refused rather than falling back to plain execution. Evidence:
`TestSkillPlainExecutionIsRefused`, `TestGuardedOutcomeProjectsBodyAndSkipsPlainExecution`,
`TestLoopInputForGuardedCallIsEmpty`, `TestGuardedOutcomeTextNeverLeavesTheGuardedPath`, `TestGuardedNameWithoutGuardFailsClosed`,
`TestGuardedProductErrorIsNotMasked`, `TestRegistryGuardedForTracksSkillInstallation`.

**Bounds.** Names are `[a-z0-9-]{1,64}` with no leading, trailing or doubled
hyphen; a description is required and at most 1024 bytes; a body is at most
64 KiB and is read through `io.LimitReader` before it exists in memory; one
scope contributes at most 128 skills and the prompt contribution is capped at
16 KiB, with anything dropped reported as a diagnostic rather than silently
omitted. Discovery is bounded in depth and entry count, honors
`internal/ignorefile`, and never follows a symlink — a symlinked entry is
reported and refused, so a project skill cannot borrow a path outside its tree.
A name defined twice keeps the first and reports the loser with the path that
won.

**Residual risk (REDUCED, not GUARANTEED).** An operator who enables project
skills is accepting instructions authored by whoever wrote the repository. The
controls above bound the blast radius and make the choice visible and recorded;
they do not make repository instructions trustworthy. That is the same stance
this document takes on any tool output: the fence is a semantic signal, and the
permission layer — not the prompt — is what stands between a hostile instruction
and a sensitive action. A user-scope skill has the same reach and is trusted for
the same reason a config file is: the principal wrote it.

## Journal, shadow, and history concurrency

Session events are append-only. Compaction and rewind are serialized against
history mutations; compact boundary staleness has dedicated regression
coverage. The shadow store keeps complete file bytes for undo and verifies
content digests.

By default recognized credential patterns are removed from each event before
`Event.ForStore()` and before output truncation; the operation is copy-on-write,
so the live event in the in-memory history is unchanged. Set
`NABD_REDACT_JOURNAL=0` only for an explicit diagnostic run. The shadow store is
**not** redacted, and unrecognized sensitive content remains cleartext.

### Journal export

`--export` writes a journal to stdout as JSONL and exits. It is independent of
`NABD_REDACT_JOURNAL`; only `--redact` selects the output policy.

| Mode | Behavior | Residual |
|---|---|---|
| `--export FILE` (raw) | source bytes copied verbatim: unknown fields, blank lines, and a truncated final line survive; a warning is written to stderr | output may contain unredacted credentials |
| `--export FILE --redact` | decoded via `store.Read`, recognized patterns redacted, re-encoded through the same path as the journal and `--json`; unknown fields are dropped and a truncated final line is ignored | unrecognized sensitive content and structural fields remain |

The source file is opened read-only in both modes and is never modified.
`--redact` requires `--export`, and `--export` is rejected together with any
run mode. Diagnostics go to stderr; stdout is JSONL only.

## MCP integration

Implementation status: Phases 0–6 implemented, merged 2026-10-11 in PR #264.

The following rows describe the MCP posture per ADR-0003. All phases are
implemented and tested; GUARANTEED rows name the regression test that proves
the claim. Rows marked NOT PROVIDED are explicit residual risks or deferred
work, not design commitments awaiting implementation.

| Claim | Status | Evidence or residual |
|---|---|---|
| MCP settings load only from `~/.ag/mcp.json`; any MCP config inside the project root is refused regardless of filename | GUARANTEED | `TestLoadPinnedToDefaultPath`: Load reads only DefaultPath() (a valid config elsewhere is ignored); `TestMCPSec03RepoTreeRejected` and `TestMCPSec03SymlinkToRepoRejected`: path and root are symlink-resolved before comparison, so a symlinked dir cannot smuggle a config in. |
| MCP config secrets are `${VAR}` references only; explicit secret values are rejected | GUARANTEED | `TestMCPSec02SecretRefs`: plaintext, embedded, empty, and malformed references are refused; values must be exactly `${VAR}`. |
| MCP config env names cannot shadow spawner-controlled or loader-subverting variables | GUARANTEED | `TestMCPEnvReservedNames`: PATH, HOME, LD_*, DYLD_* are refused so config cannot bypass sanitizePath or the isolated HOME. |
| MCP config documents respect size and count caps | GUARANTEED | `TestMCPSec16_20SizeLimits`: 256KiB file cap, 16 servers, 256 tools per server, 64 env entries, 64 args, 4096-byte arg cap. |
| MCP client pins the negotiated protocol version | GUARANTEED | `TestMCPSec21ProtocolPin`: 2024-11-05/2025-11-25/2026-07-28 are accepted and returned as negotiated; newer versions (2027-01-01, 2026-07-29), unverified older versions (2025-06-18, 2025-03-26), and malformed versions are refused. `TestClientSendsRequestedVersion`: the initialize request carries protocolVersion 2026-07-28 (ADR-0003 v6). |
| MCP client refuses server-initiated methods it does not implement | GUARANTEED | `TestMCPSec22CapabilityRefusal` and `TestMCPSec22CapabilityRefusalSampling`: server requests for roots/list and sampling/createMessage get JSON-RPC -32601 and no action. `TestClientPingReply`: ping is answered with an empty result. |
| MCP server child processes run with a minimal env allowlist, never inheriting the full environment | GUARANTEED | `TestMCPLifecycleEnvIsolation`: the spawned server sees only PATH (sanitized), TERM, LANG, LC_*, NABD=1, an isolated 0700 HOME (never the real one, removed on Close), and declared `${VAR}` refs; an undeclared secret in the parent env does not reach the child. |
| Repository-cloned MCP configs never start servers | GUARANTEED | Config loads from `~/.ag/mcp.json` only; in-tree configs are refused at load (`TestMCPSec03RepoTreeRejected`). Session wiring (Phase 5) never consults the repo. |
| Tool permission classification comes from local user config only, never from server hints | GUARANTEED | `TestMCPSec05ReadOnlyHintIgnored`: a write-classified tool still prompts per call even when the server advertises readOnlyHint; the adapter's `MCPToolDesc` carries only the local classification and never reads server hints. |
| Tools named `capture_*`/`observe_*` are never ReadOnly | GUARANTEED | `effectiveClassification` raises such tools to at least Mutating ("write") even when the local config says read-only, because driving a browser or observing processes has external effects (`TestEffectiveClassification`). The server cannot opt out: the rule applies after the local classification is read. |
| Tool fingerprints (SHA-256 over JCS-canonical fields) invalidate approval on any change | GUARANTEED | `TestFingerprintToolChangeDetection`: description/schema/hint/server-id changes all change the fingerprint; `TestMCPSec06_07FingerprintInvalidation`: a changed fingerprint forces re-approval. Approval also binds the executable identity (path, argv, env names, binary SHA-256) — `TestMCPApprovalInvalidation`. |
| MCP server processes are killed on timeout, session close, or disable, with no orphans | GUARANTEED | `TestMCPS1NoOrphans`: 3 rounds, server + `sleep` grandchild spawned, group confirmed alive, `Close` → whole group gone. `TestMCPS2OversizeKillsServer`: a >64MiB message fails the call closed and the watchdog kills the server. killGroup always runs before Wait (bash.go no-sweep-after-Wait rule). |
| Journal records server/tool IDs, decisions, and sizes — never raw args or outputs by default | GUARANTEED | `TestMCPSec13JournalPrivacy`: HMAC-SHA256 binds args/outputs without storing them; the journal file contains no secret values. |
| Core packages (`tools`, `perm`, `safefs`, `redact`, `toolvocab`, `agent`) carry no MCP SDK or network dependencies | GUARANTEED | `TestMCPDependencyIsolation` runs `go list -deps` per core package and fails on any non-allowlisted module (ADR-0003 §12). |
| A single flag disables all of MCP | GUARANTEED | `TestMCPSec14NoMCPFlag` / `TestMCPSec14NoConfigNoRegistration`: `--no-mcp` loads no config, creates no manager, and never registers `mcp_tool`; with no config or no enabled servers nothing is registered either (lazy startup, `TestMCPSec15LazyStartup`). |
| Separate per-run CLI opt-in for a server enabled in `~/.ag/mcp.json` | NOT PROVIDED | An enabled server in user-scoped configuration activates MCP without an additional per-run CLI opt-in; `--no-mcp` disables it. With no configuration or no enabled server, nothing is registered (`TestMCPSec15LazyStartup`). This is not comparable to untrusted project-scope skills: user-scope skills also load without project opt-in. Any default-off redesign needs a separate decision (V19). |
| STDIO server processes are contained by the OS | NOT PROVIDED | No OS-level containment on Termux (Landlock unavailable to the app process). B7 preliminary decision (ADR-0003 §4.2): path 2 — run as user with explicit warning. Residual risk accepted pending named owner. |
| MCP server file reads bypass local .gitignore non-disclosure | NOT PROVIDED | The .gitignore non-disclosure guarantee covers local tools only. An MCP server running as the user can read files the local tools would refuse. Designed: user must understand this before first approval (ADR-0003 §6.1). |
| MCP tool outputs grant no read-credit and bypass readCap | NOT PROVIDED | Read-credit (NBD-034) and readCap accounting apply to local tools only. MCP outputs are untrusted content, not measured reads. Designed: stated explicitly so credit is never granted by accident (ADR-0003 P7). |
| MCP server outputs cannot reverse a Deny or reclassify a tool | GUARANTEED | `TestMCPSec09bPoisonedDeny`: a Deny stays denied deterministically even against poisoned output. `TestMCPSec09cReclassNoEffect`: reclassification requests in output are ignored — classification comes from local config only. |
| MCP server outputs cannot grant permissions through injection | REDUCED | `TestMCPSec09OutputInjection`: permission-granting injections stay UNTRUSTED and create no permission state. Residual: outputs remain untrusted content and may influence model behavior; this does not guarantee immunity to prompt injection or restrict an approved server process running as the user. |
| Interrupted or failed MCP writes are never re-executed | GUARANTEED | `TestMCPSec10NoRetryOnWrite`: `tools/call` is invoked exactly once; failures and cancellations propagate without retry (effect-duplication hazard). |
| YOLO never auto-approves MCP tools | GUARANTEED | Structural: `MCPCheckCall` never returns Allow for write/execute — the verdict matrix asks on every such call regardless of YOLO (`TestMCPCheckCallYOLOExempt`); `perm.Policy.SetYOLOExempt("mcp_tool")` is the policy-level backstop (`TestYOLODoesNotApproveMCPWrite`). No session grants in v1 (`TestMCPNoSessionGrant`); headless denies via the silent asker (`TestMCPHeadless`). |
| One server approval opens all its ReadOnly tools indefinitely | NOT PROVIDED | Unlike `bash` (which never gets even a session grant), a single MCP server approval opens every ReadOnly tool until fingerprint change. This is a deliberate but significant trust difference, stated here as a residual risk (ADR-0003 §8.2). |
| Saved approval scope, revocation, and new-tool discovery | NOT PROVIDED | Approval scope is implemented and bound: server-id + tool + JCS fingerprint (over the raw advertised object, all fields — `TestFingerprintUnknownField`) + executable identity (path, argv, env names, binary SHA-256), stored 0600 under `~/.ag/mcp/` with symlink/owner/permission hardening and atomic writes (`TestApprovalStoreSymlinkRefused`). New tools arriving via `notifications/tools_list_changed` are re-listed and start unclassified = refused (`TestMCPSec23ToolListChange`). Still missing: a revocation UI. Write/Execute always require per-execution approval, even under YOLO, and are refused headless. |
| Unclassified MCP tools are refused before any prompt | GUARANTEED | `TestMCPSec04UnclassifiedDenied`: `Describe` fails for unknown/unclassified tools, the adapter refuses without prompting and never reaches the server. |
| The reserved adapter name `mcp_tool` cannot address a server tool | GUARANTEED | `TestMCPSec08NameShadowing`: a server tool named `mcp_tool` is refused at the adapter boundary and by the manager's `Describe` (defense in depth). |
| A hung MCP call times out, kills the server, and re-handshakes on next use | GUARANTEED | `TestMCPSec11_12TimeoutOrphan`: a call exceeding the timeout is cancelled, the server is killed after a 5s grace (no orphan — the old PID is gone), and the next call re-spawns with a full initialize + tools/list + fingerprint check. `tools/call` is never retried. |
| Parallel MCP calls are capped per server; excess is refused immediately | GUARANTEED | `TestMCPSec24ParallelCap`: beyond `max_parallel_calls` the call is refused at once, never queued (no freeze). |
| MCP approvals travel the normal permission path | GUARANTEED | The loop's `CallAwareGate` consults `MCPCheckCall` per call, so every approval emits the standard `PermAsk` (modal) / `PermReply` (journal) events; the first-approval content is built locally with no server free text. The adapter's `GuardedResult` is verify-only and fails closed without a recorded approval (`TestMCPAdapterUnapprovedRefused`). |
| MCP server stderr is sanitized before diagnostics | GUARANTEED | `TestStderrSanitized`: ANSI escapes and control characters are stripped from `StderrTail()` — server stderr is untrusted and never reaches the UI or logs raw. |
| MCP approval/journal files resist symlink and permission attacks | GUARANTEED | `TestApprovalStoreSymlinkRefused` / `TestJournalSymlinkRefused`: O_NOFOLLOW opens, owner and 0600-or-stricter checks, atomic writes; a symlinked store is refused on read and safely replaced on write. |
| Independent security review and release acceptance | NOT PROVIDED | The supplied text records single-operator risk acceptance in ADR-0003. This revision does not verify reviewer identity, current acceptance, release settings or mechanical enforcement. Verify those separately before asserting that no independent review exists or that a release gate is absent (V19). |



## Operator guidance


1. Prefer secure credential files over shell startup exports.
2. Run nabd as a dedicated OS user on shared systems.
3. Use a disposable clone rather than a tree containing production secrets.
4. Treat every `bash` approval as authority equivalent to the current user.
5. Assume `session.jsonl` and `.ag/shadow` contain sensitive cleartext.
   Default journal redaction reduces recognized credentials but does not make
   the journal safe: it does not redact the shadow store, structural fields, or
   unrecognized sensitive content. Avoid `NABD_REDACT_JOURNAL=0` except for a
   deliberate diagnostic run. `nabd purge` handles eligible journal files,
   not `<root>/.ag/shadow`. It considers direct regular files ending in
   `.jsonl` or containing `.archived-`; it is a dry run until `--yes`.
   V20 tracks the contradictory rotation comment, not unknown eligibility.
6. Do not attach raw journals to public issues.
7. Redact before sharing: `nabd --export <file.jsonl> --redact`. Raw
   `--export` is a deliberate, byte-for-byte copy and warns on stderr.
8. Keep `~/.ag` and project roots in the Termux private home. File-mode bits
   are not enforced on Android shared storage, so the 0600/0700 claims do not
   hold there.
9. Scope provider keys: one key per project, with a spend limit, and rotate
   after any session that ran untrusted code. An approved `bash` command can
   read `~/.ag/auth.json`.
10. If `termux-api` is installed, an approved `bash` command can call it with
    whatever Android permissions you granted it. Grant only what you need.
11. Review agent changes to `.git/`, `Makefile`, `package.json` and `.envrc`
    before running programs that consume them, even outside nabd. Ordinary
    write permission is not a separate deferred-execution consent boundary.
12. If MCP is not needed, use `--no-mcp`. Protect `~/.ag/mcp.json`; an enabled
    server there needs no separate per-run CLI opt-in. This user-scoped choice
    differs from enabling repository-authored project skills.
13. Treat `<root>/.ag/shadow` as sensitive cleartext rollback storage, including
    excluded-file copies; avoid sharing it. Removing it can prevent undo.
14. Use `--permission-mode plan` to block classified writes and execution, not
    to keep readable content out of the provider context. `allow-reads` lifts
    session exclusions for `read_file` and `grep`; it is not secret protection.

### Inherited directory permissions

Write tools create missing parent directories with the permission bits of the nearest existing ancestor. This prevents a private `0700` project subtree from silently gaining `0755` descendants; `0755` is retained only as a documented root-level fallback when no usable ancestor exists.

### Aggregate diff memory budget

Each tool registry shares one cancellable LCS cell budget across concurrent mutations. A diff reserves its `n*m` work before allocating the matrix and releases the reservation on every return path, preventing parallel writes from multiplying the per-diff memory ceiling.

### UI text-width contract gate

The standalone UI width-contract gate checks text measurement, truncation bounds, composed frame width, Unicode segmentation, Arabic combining marks, CJK width, emoji sequences, and cursor alignment. The gate runs on every pull request, including dependency-only changes, so text-width dependency upgrades cannot bypass the UI regression suite.

Textarea vertical cursor navigation uses grapheme-cluster boundaries rather than raw rune-width accumulation. Arabic combining marks must remain zero-width during vertical movement, and the visible target column must be preserved across ASCII and Arabic lines. `TestTripwire_TextareaColumnMappingChanged` detects dependency or local-fork behavior changes, while `TestCorrectness_ComposerNavigationColumnAlignment` enforces the intended cursor-alignment contract.

### Pointer input boundaries

Pointer input can select and expand cards. It can never answer a permission
prompt: handleMouse returns before any hit-test while modalVisible or
decisionPending is set, so no pointer path reaches answerModal. Expansion
repaints already-projected output and never executes a tool.
Tests: `TestPointerNeverAnswersPermission`, `TestPointerNeverExecutesATool`

### Status-row truthfulness

The runtime status row is a security-relevant signal, not decoration: it is the
only place the user learns whether the agent is still acting on their behalf. A
row that claims `Generating…` after the run is dead invites the user to wait
instead of inspecting or re-approving, and it hides the failure that the
journal already recorded.

`RunError` and `Interrupted` are terminal. Both retire the progress claims
(`running`, `runningTool`) at projection time and replace them with an explicit
failure line, rather than waiting for the runner goroutine to return. The send
gate (`busy`) is deliberately not cleared there: only `doneMsg` proves the
runner has actually returned, so a failed run can never be overlapped by a
second concurrent run. Evidence: `TestRunErrorRetiresProgressStatus`,
`TestRunErrorKeepsSendGate`, `TestInterruptedRetiresProgressStatus`,
`TestDoneMsgClearsRunFailedStatus`.

### Route-trace visibility

The router documents six trace statuses. Two of them describe intervals in
which nothing appears to happen: `waiting` (the bounded, opt-in Retry-After
pause) and `blocked` (a route skipped while its breaker is cooling down).
Hiding them is not a safety property; it is an observability gap that turns a
deliberate pause into an apparent hang and pushes the user to kill the process
or re-run, which costs another rate-limit budget.

Both are now visible under the existing disclosure contract, not beside it:
the text is produced only by `presentation.FormatRouteNotice`, sanitized by
`display.SanitizeForDisplay` with redaction enabled, forced to a single line
with no terminal control sequences, given fixed placeholders for blank fields,
and never carries `StreamID`. `attempted` and `exhausted` stay hidden — the
first is noise, the second is already reported as a run error. Evidence:
`TestFormatRouteNoticeWaitingIsVisible`,
`TestFormatRouteNoticeWaitingRedactsSecrets`,
`TestFormatRouteNoticeBlockedIsVisible`,
`TestFormatRouteNoticeStillHidesStructuralStatuses`.

### Status-row runtime metadata

The status row now also answers "which turn, how expensive, how long, and who
is actually serving this" (`turn 3 · 6.6k tok · 12s · nvidia/…`). Three rules
keep that from becoming either a false claim or a new disclosure channel.

**Only committed routes are named.** `StatusProjector.Meta` adopts a provider
and model exclusively from a `selected` route trace. `attempted`, `failed`,
`waiting`, and `blocked` describe routes that did not serve the request, so
naming them would tell the user their prompt went somewhere it did not.

**Elapsed time is not a liveness claim.** The clock is injected rather than read
inside the projector, and elapsed freezes at `RunEnd`, `RunError`, or
`Interrupted`. A dead run cannot appear to keep working, which is the same
property as status-row truthfulness above.

**No new fields are disclosed.** Provider and model pass through the same
`cleanField` sanitizer as route notices (redaction on, single line, no control
sequences). Token counts are the provider's own aggregate numbers, already
present in `provider_usage` journal events. `StreamID`, credentials, file
paths, tool arguments, and error bodies are never part of the metadata. The row
remains exactly one row at every width, and when the metadata does not fit it is
dropped variant by variant rather than truncated, so the phase text — the part
that carries the safety signal — is never cut. Evidence:
`TestMetaTracksTurnTokensAndCommittedRoute`,
`TestMetaElapsedFreezesAtTerminalEvent`,
`TestRuntimeMetaSanitizesRouteFields`,
`TestRuntimeStatusRowCarriesMeta`,
`TestRuntimeStatusRowDegradesInsteadOfTruncating`,
`TestRuntimeStatusRowStaysOneRow`.

### Failure-detail disclosure

A terminal failure used to render as the raw `Err` string alone: the journaled
`ErrorCode` was dropped, the per-route causes the router had already written
into the error body were collapsed, and the reader was left with "all 2
route(s) exhausted" and no next step. That is a safety problem as much as a
usability one — a user who cannot tell an auth failure from a rate-limit
failure retries blindly, spending budget or leaving a bad key in place.

`presentation.FormatRunError` now composes the visible failure, and three
properties bound it.

**No new data source.** It reads only `Err`, `ErrorCode`, and `Code` from the
event the loop already journaled. It never reads the transcript, the shadow
store, tool arguments, or the environment, so nothing becomes visible that the
journal did not already contain.

**Provider text is redacted, hints are literals.** Error bodies are
provider-controlled input and can echo a request header, so every line passes
through `cleanField` — `display.SanitizeForDisplay` with redaction enabled — and
is emitted as a single logical line with no terminal control sequences. The
actionable hint is a fixed string selected by `ErrorCode`; provider text is
never promoted into it. Codes with no user-side remedy (`canceled`, `unknown`)
get no hint at all, and `unknown` is not printed, because a label that says
nothing trains the reader to ignore the label.

**Detail is bounded.** At most `maxRunErrorDetails` route lines reach the feed,
followed by an explicit count of what stayed in the journal, so a long or
hostile error body cannot push the rest of the conversation off the screen.
The block also stays inside the terminal width at 20, 40, 66, and 80 columns.
Evidence: `TestFormatRunErrorKeepsCodeAndRouteDetail`,
`TestFormatRunErrorHidesUnknownCode`, `TestFormatRunErrorHintsPerCode`,
`TestFormatRunErrorNeverRendersEmpty`, `TestFormatRunErrorRedactsSecrets`,
`TestFormatRunErrorCapsDetails`, `TestRenderRunErrorShowsCodeAndHint`,
`TestRenderRunErrorKeepsRouteCauses`, `TestRenderRunErrorRespectsWidth`,
`TestRenderRunErrorRedactsSecrets`.

### Route-exhaustion wait disclosure

The route-exhaustion failure (`all N route(s) exhausted; shortest retry-after:
20s`) carried the one number the reader acts on inside free text, and both
surfaces then lost it. The feed card hides its `details` line below 40 columns
and truncates it to the terminal width above that, so at 39 columns the wait was
not on screen at all and at 66 it read `shortest retr…`. The reader saw what
failed and never how long to wait — the difference between waiting out a rate
limit and abandoning a working key.

The number now travels as a field. `agent.RunErrorEvent` lifts the router's
shortest positive retry-after onto `Event.RetryAfter` — the field the
rate-limit event already uses for the provider's declared wait — and both
presentation surfaces read it from there. Two properties hold.

**Mapped from typed values, never parsed.** The wait is recovered with
`errors.As` on `*provider.RouterExhaustedError`, not by matching the phrase the
router formatted, and only a positive value is lifted. A run that reported no
retry-after states no wait, so `0` keeps its meaning of "none reported" instead
of becoming "wait zero seconds".

**Stated at every width.** `renderErrorCard` emits `wait: Ns` on its own
unconditional line in every width mode, above the actionable lines; the fixed
label plus the rounded number fits the 20-column floor the width contracts use,
so it is not truncated. `RunErrorView.Lines()` states it on its own line for the
chat scrollback, so the number is not buried mid-sentence there either. The value
is rounded for display only. Evidence:
`TestFeedErrorCardShowsWaitAtEveryWidth`,
`TestFeedErrorCardStatesNoWaitWhenNoneReported`, `TestErrorCardCarriesWaitSeconds`,
`TestRenderRunErrorShowsWaitAtNarrowWidth`.

### Feed-retention disclosure

The interactive feed retains at most `maxVisibleFeedItems` rendered items so a
long session cannot grow its navigation, line cache, and repaint work without
bound. Previously, the three consumers of that policy sliced the merged item
list independently and silently: once the cap was crossed, older cards vanished
from the feed with no user-visible explanation even though their events remained
in the append-only session journal.

`visibleFeedItems` is now the single retention boundary used by rendering,
navigation, and expansion refreshes. When the cap is crossed, one retained slot
becomes a synthetic notice that states the exact number of older items omitted
and identifies the session journal as the complete history source. The remaining
slots hold the newest projected items. The helper allocates a new slice and does
not modify projector output, UI notices, journal events, or model context.

The disclosure is UI-only and adds no content source: its count is derived from
slice lengths and its text is fixed. Evidence:
`TestVisibleItemCapStatesHiddenHistory`,
`TestLineCacheNeverExceedsVisibleItemCap`.

### Output-truncation disclosure

Tool output persisted to the journal is capped at `MaxPersistedOutput`. Until
now the only trace of that cap was a marker appended inside the stored text
(`...[truncated N bytes]`), which means the fact that evidence was discarded was
readable only by a human scrolling the output, and was not queryable over a
journal at all. A reviewer auditing what the agent actually saw could not
distinguish a complete command output from a silently shortened one without
string-matching free text.

`Event.ForStore()` now also records the discarded byte count in
`ToolCall.TruncatedBytes`, and the renderer states it on the `tool_end` row that
reports the result. Three properties bound the change.

**No new data source.** The byte count was already computed by `ForStore` and
already written into the output text. Promoting it to a field exposes nothing
that the journal did not contain; it only makes an existing disclosure
machine-readable. File contents, tool arguments, paths, and credentials are
untouched, and the count is a length, not content.

**Additive and copy-on-write.** The field is `omitempty`, so an untruncated call
serializes exactly as before and journals written by older builds decode to
`0` — indistinguishable from "nothing was cut", which is the truthful reading.
`ForStore` continues to operate on a copy, so the live in-memory event keeps its
full output and the redaction path is unaffected. The in-output marker is kept
deliberately: the two statements are redundant on purpose, so neither a reader
of raw text nor a reader of fields is misled.

**Bounded rendering.** The loss is reported as one short segment appended to the
existing `tool_end` row, and that row is now wrapped to the available width
rather than emitted raw, so adding the segment cannot push the row past the
terminal edge at 20, 40, 66, or 80 columns. A row that cut nothing says nothing,
so the signal stays meaningful. Evidence:
`TestForStoreRecordsTruncatedBytes`, `TestForStoreLeavesSmallOutputAlone`,
`TestForStoreDoesNotMutateLiveEvent`, `TestTruncatedBytesIsAdditive`,
`TestToolEndStatesTheLoss`, `TestToolEndSaysNothingWhenNothingWasCut`,
`TestTruncatedToolRowRespectsWidth`.

### Failure attribution

A sink failure inside `runCalls` is the one failure mode where the agent may
have acted without recording it. The loop returned a bare error, so the
`run_error` event read `session event was not saved: no space left on device`
and nothing more. The reader could not tell whether the unrecorded event was a
`read_file` result — in which case the working tree is untouched — or a
`write_file` result, in which case a mutation exists on disk with no journal
entry and no shadow record of its outcome. Those are different situations and
they call for different recovery, so the distinction has to survive the return
statement.

`ToolCallError` carries it. Three properties keep it from becoming either a
behavior change or a new disclosure channel.

**The message does not change.** `Error()` returns the wrapped message
byte-identical. The rendered failure block, the exit path, and several existing
tests assert exact error text, and a prefix here would rewrite what the user
reads on every persistence failure while adding nothing actionable. Attribution
travels as fields, not as prose.

**Classification is unaffected.** `Unwrap` keeps `errors.Is` and `errors.As`
transparent, so `ErrorCodeOf` still returns `persist` for a wrapped
`*PersistError` and `JournalPathOf` still finds the path. Wrapping is also
idempotent and keeps the innermost attribution: the call that actually failed
is the one reported, never an outer frame.

**Only the identity travels.** `RunErrorEvent` reports the call through the
event's existing `Call` field — the same shape `tool_start`, `tool_end`,
`perm_ask`, and `perm_reply` already use, so every existing decoder reads it
without change — and copies only `ID` and `Name`. Output, arguments, exit
status, and duration are deliberately left empty, because a call whose result
could not be recorded has no result to report, and the arguments may name a
path the journal was not able to protect. An unattributable failure reports no
call at all rather than an empty one. Evidence:
`TestWrapToolCallErrorKeepsTheMessage`,
`TestWrapToolCallErrorStaysTransparent`,
`TestWrapToolCallErrorKeepsTheInnermostCall`,
`TestWrapToolCallErrorIsANoOpWhenThereIsNothingToSay`,
`TestRunErrorEventNamesTheFailingCall`,
`TestRunErrorEventReportsNoResultForTheFailingCall`,
`TestRunErrorEventStaysQuietWhenNoCallIsKnown`,
`TestSinkFailureNamesTheCallInFlight`.

### Silent-turn exit attribution

The headless empty-response decision depends on the structural notices the loop
emits, not on their wording, and it keeps the text-exclusion and tool-activity
conditions unchanged: the final assistant text must be blank, and a turn that
used a tool is never treated as silent. A run that ends on such a turn therefore
exits 1 rather than 0, so a scripted caller cannot mistake a silent provider for
a successful empty answer. It is not an independent detector of every cause of
silence — it requires one of those two notices on the live branch — so a silent
turn that emits neither is outside its scope.

The wording it used to match was `strings.Contains(e.Text, "empty response")` and
`strings.Contains(e.Text, "NABD_MAX_TOKENS")`. Wording is display text, subject
to rewording and translation, and it is not a contract: a reworded notice would
have made a silent empty turn exit 0, and an unrelated notice carrying the phrase
would have exited 1. Neither effect was a reported incident. The supplied review
reports regression coverage of the wording-dependent decisions before the fix
and the category-based decisions after it; this document-only revision did not
run those tests or re-check their historical coverage.

The decision now reads the notice category. `internal/agent/loop.go` keeps
length-cut notices under `NoticeCategoryLengthLimit` and marks both non-length
empty-response arms with `NoticeCategoryProviderEmpty`. `emptyResponse` accepts
those two categories. Notice texts and their emission conditions are
byte-identical; only the identity of the condition travels structurally, the
same rule the provider retry decision already follows.

The value is appended last in the `NoticeCategory` block on purpose: it is
persisted as an integer in the append-only journal (`notice_category`), so
appending is safe and renumbering an existing value is not. Readers were
reviewed for the new value: `internal/store/jsonl.go` unmarshals events without
validating the category, `internal/presentation/projector.go` renders a notice
from its `Text` and never reads the category, and the category has two consumers:
`event.NoticeAllowedForModel`, a default-deny map lookup, and `emptyResponse`
in `cmd/ag/headless.go`, which reads the two categories named above for the
headless exit decision. No reader switches
exhaustively, so a reader at this revision drops an unknown value from the model
context — the intended behaviour. The review covers the readers in this tree, not
historical binaries.

The category is deliberately absent from `noticeReachesModel`, so it cannot
enter the model's context. That is the existing default-deny allowlist rather
than a new mechanism, and it is asserted twice on purpose: statically, and
behaviourally on what `Messages` actually sends, with pending tool calls and
idle. Evidence: `TestEmptyResponseReadsCategoryNotWording`,
`TestHeadlessEmptyResponseWithOtherStopReasonKeepsExitCode`,
`TestHeadlessEmptyResponseKeepsRunEndEnded`, `TestEmptyTurnEmitsNotice`,
`TestHeadlessEmptyResponse`,
`TestBlockedNoticeCategoriesDroppedWhileToolCallsPending`,
`TestJournalNoticeCategoryRoundTripDoesNotReachModel`,
`TestNoticeCategoryProviderEmptyIsNotModelFacing`,
`TestNoticeCategoryValuesAreAppendOnly`.

### Plan mode

`--permission-mode plan` switches `perm.Policy` into a strict read-only
mode. The goal is a session that can inspect the working tree but is
guaranteed not to mutate it or run commands — useful for review, auditing,
or letting a model propose changes without applying them.

`Policy.Check` implements it as a high-priority branch: once the tool is
known to be `Mutating` or `Executing`, plan mode returns `Deny` before the
YOLO override and before the per-session standing grant are consulted. A
user who previously granted `write_file` for the session, or code that
calls `SetYOLO(true)`, cannot widen permission in plan mode. `ReadOnly`
tools (`read_file`, `glob`, `grep`) still return `Allow`, so inspection
works. Because `plan` is a value of the same `--permission-mode` flag that
already governs headless runs, the interactive gate (`gate{pol}` in
`doChat`/`doChatWithFeed`) and the headless gate both call the same
`Policy.Check`, and the rule is identical with or without a TTY.

The empty flag preserves each path's existing default (interactive `ask`,
headless `deny`), so adopting plan mode is opt-in and cannot silently
change current behaviour. Evidence: `TestModeTable`,
`TestModePlanOverridesGrants`, `TestModePlanAllowsReads`.

### Default interactive UI and the @ index surface

The `-feed` flag defaults to `true`: a bare `nabd` runs `doChatWithFeed`. That
is not only a presentation choice. The feed path is the one that calls
`feed.SetPickerRoot(root.Dir())`, so the `@` path index of the session root now
runs for operators who never asked for it, whereas `doChat` wires no picker and
reads nothing for completion. The default therefore widens the read surface of
the default invocation, and the widening is bounded by the picker's own limits
rather than by the flag: BFS traversal inside the resolved root, symlinks
refused at directory read, `.ag` and the shadow store excluded, and the three
reachable stop conditions of `pathindex.Scan` (see "Pathindex traversal surface
and candidate picker limits"). No candidate is read as content; the index holds
paths.

`--feed=false` is the rollback, and it is a rollback in the operational sense:
it selects the legacy chat entry point in the same binary, with no rebuild, no
reinstall, and no source-control operation. A `git revert` of the default is not
an equivalent, because it reaches the operator only through a new release. The
flag is therefore expected to remain for at least one full release after the
default flips, so that an operator who does not want the `@` index on by default
has a switch rather than a downgrade.

The two entry points stay mutually exclusive within one invocation: `main`
branches once on `*useFeed` and returns, so a single run is either a feed
session or a chat session, never both. Evidence:
`TestFeedIsTheDefaultInteractiveUI`; the picker wiring it guards is proven by
`TestPickerExplicitSessionRootOverridesGitDir`,
`TestPickerNoSessionRootFallsBackToGitDir`, and
`TestPickerUnreadableSessionRootReportsStatusWithoutCrash`.

### Git status header containment

The interactive feed displays git branch and status information in the header.
The header must not describe files outside the granted root (`root.Dir()`). If
the granted root is a subdirectory within a larger parent git repository, `git
status` in that directory would report branches and dirty file counts for paths
outside the granted containment boundary. Therefore, `isGitRepo` inspects only
the granted root directly (`filepath.Join(abs, ".git")`) without upward
traversal, treating a parent repository as out-of-bounds even though `git` itself
would answer. Evidence: `TestIsGitRepoDetection`.

**Repository-local configuration cannot execute commands during status.** Git status
in an untrusted directory honours repository-local `.git/config`, where
`core.fsmonitor` or `filter.<driver>.clean`/`.process` can configure arbitrary
commands to run on stat-dirty files. The header subprocess neutralises
`core.fsmonitor` by setting `-c core.fsmonitor=false` (command-line `-c` has
highest precedence) and adding `--no-optional-locks` and `--ignore-submodules=all`.
Because clean and process filters cannot be neutralised by name in advance, the
header reads the effective configuration first via
`git config --null --list --includes --show-scope` (which executes nothing).
Filter detection inspects repository-controlled scopes (local, worktree, included
files, and unknown scopes), trusting system and global scopes as user-installed.
If any repository-controlled scope defines a `filter.*.clean` or
`filter.*.process` driver, `git status` is skipped entirely, failing closed if
the config read fails or output is malformed. Residual: a user-installed system
filter (e.g. git-lfs) may still run if referenced by `.gitattributes`, which
executes user-installed software rather than repository-defined commands. Evidence:
`TestGitHeaderIgnoresRepoFsmonitor`, `TestGitHeaderSkipsRepoCleanFilter`,
`TestGitHeaderStillWorksOnPlainRepo`, `TestRepoConfigDefinesCommands`.

### Bash child environment and argument validation

`bash` children inherit nothing by default: `childEnv` starts from an empty
environment and copies only its allowlist, so the caller's `HOME`, credentials,
and proxy variables never reach an approved command. This revision tightens two
more properties of that boundary.

**Temporary directories are private to the invocation.** `TMPDIR`, `TMP`, and
`TEMP` were on the allowlist, so a caller-supplied value was inherited and every
invocation in the session shared it. They are no longer inherited: each
invocation gets its own `nabd-tmp-*` directory, created by `MkdirTemp` and
tightened to 0700, and all three variables point at it until the command
finishes and the directory is removed. If the directory cannot be created, the
child receives no temporary-directory variable at all and tooling falls back to
its own default — never to the caller's value. This bounds what one approved
command can read from, or leave behind in, a directory shared with the rest of
the session; it does not bound the filesystem, which `bash` still reaches with
the current user's authority and without `Root.Resolve` containment.

**Argument decoding is a limited mitigation, not a complete strict-JSON
boundary.** Direct inspection of decode.go at the pinned base shows top-level
exact-case duplicate checks
only for objects beginning immediately with `{`, plus unknown-field rejection
at the decoder. Leading whitespace and case variants bypass duplicate detection;
trailing values lack an EOF check. The production repair/gate path and action
shown to the user still require V14 verification. Repair may drop inert
undeclared bash fields before classification; the decoder cannot reject a field
already removed. No claim is made here that every duplicate fails or that the
approval UI exposes the fully decoded action. Trace the repaired payload,
decoded `cmd` and displayed action together (V2/V14). The existing command
permission gate remains separate; decoding grants no execution authority.
Inherited Evidence:
`TestBashUnknownFieldIsDroppedAndRuns`,
`TestBashRepairedCallRunsWhenPayloadIsClean`,
`TestBashStrictDecodeStillFailsThroughLoop`, `TestBashRejectsNonStrictArgs`,
`TestBashChildTempDirIsIsolatedAndRemoved`.

### Goal mode

`internal/goal` builds a bounded, model-facing execution contract from one
objective. It does **not** create a privileged execution path: `goal.Run`
delegates to `agent.Loop.Run`, so every tool call still passes through the
existing permission gate (`internal/agent/gate.go`). The objective and the
generated contract are model-facing input, not trusted repository
instructions.

**What Goal Mode does not grant:** shell, write, network, secret, or
production access. It cannot bypass the permission gate, YOLO override, or
plan-mode deny branch. A generated contract is handled like any other user
message — journaled, subject to compaction, and replayable.

**Input bounds (integrity, not sandbox):** objectives are capped at 8 KiB
and must be valid UTF-8 without control characters (except `\n`, `\t`).
These limits reject pathological input; they are not a sandbox and do not
replace tool-level permission checks.

Evidence: `internal/goal/contract_test.go` (UTF-8, limits, determinism),
`internal/goal/runner_test.go` (nil-runner, single-dispatch, error
propagation), `var _ Runner = (*agent.Loop)(nil)` compile-time assertion
in `internal/goal/runner.go`.

### OSC 52 clipboard boundaries

OSC 52 copy operates only on canonical projected card content after recognized
credential redaction and display sanitization. Human-language cards read their
logical projection fields instead of BiDi display clusters, so visual
reordering and mirrored punctuation cannot enter copied source. Tool cards
retain their projected/redacted summary and output contract. Raw journal bytes
and raw error bodies are not clipboard sources. Unrecognized sensitive text
remains a residual risk.
Evidence: `TestCopyRedactsRecognizedCredentials`, `TestCopyNeverUsesRawJournalContent`, `TestCopyRejectsRawErrorBodies`, `TestCopyIsBlockedByPermissionModal`, `TestCopyNeverExecutesACommand`, `TestCopyUsesLogicalSourceWhenDisplayIsReordered`.

### @ path picker root resolution

The `@` picker indexes exactly one root, and that root is chosen explicitly
rather than inferred. `doChatWithFeed` calls `feed.SetPickerRoot(root.Dir())`
with the resolved session root, so the picker walks the same tree the session was
started in. `gitDir` is only the fallback for a feed constructed without a
session root, and a feed with neither reports `no directory to index for @`
rather than guessing. Before this wiring the picker used `gitDir`
unconditionally, so outside a repository — or from a subdirectory — it indexed
and disclosed the wrong tree.

A root that cannot be resolved or read reports the `cannot index files for @: `
status instead of panicking, and is not re-scanned on later keystrokes
(`pickerScanned`), so one unusable root cannot turn into a scan per keypress.
Evidence: `TestPickerExplicitSessionRootOverridesGitDir`,
`TestPickerNoSessionRootFallsBackToGitDir`,
`TestPickerUnreadableSessionRootReportsStatusWithoutCrash`.

### Pathindex traversal surface and candidate picker limits

The `@` path candidate picker indexes repository files to provide interactive path completion. Because scanning touches the filesystem and feeds completion options into interactive input, its traversal surface is constrained by seven security properties.

**BFS traversal and deterministic candidate ordering.** `pathindex.Scan` traverses the directory hierarchy breadth-first starting from `"."` using a FIFO queue. Entries within each directory are sorted alphabetically (`sort.Slice` on `Name()`), ensuring candidate order is stable and deterministic across runs rather than dependent on filesystem directory iteration order. Only regular files (`e.Type().IsRegular()`) are admitted as completion candidates.

**Single resolution per directory.** Directory containment is verified with `root.Resolve` exactly once per visited directory, never per candidate file. Child entries inherit the containment guarantee established for their parent directory, avoiding redundant resolution syscalls while preserving containment (`TestScanResolvesOncePerDirectory`).

**Symlink rejection at directory read without secondary syscalls.** Symbolic links are rejected immediately upon inspecting `e.Type()&fs.ModeSymlink != 0` from `os.ReadDir`. Because `os.ReadDir` does not follow links, a symlink arrives with `fs.ModeSymlink` set and is discarded immediately without an additional `os.Stat` or `Lstat` call. This eliminates TOCTOU races where a path could be inspected as a regular file and subsequently traversed as a link, and prevents links targeting locations outside the root from entering the candidate set (`TestScanRefusesSymlinkedEntries`).

**Shadow store and build directory exclusions.** `DefaultExcluded` skips known build, cache, and metadata directory subtrees (`.git`, `node_modules`, `vendor`, `.venv`, `__pycache__`, `target`, `dist`, `build`, `.next`, `.cache`, `.idea`). In particular, `.ag` is strictly excluded to keep the raw content-addressed shadow history (`.ag/shadow`) completely isolated from candidate completion (`TestScanSkipsExcludedDirectories`, `TestScanPathIndexNeverOffersTheShadowStore`).

**Session root `.gitignore` awareness and declared limits.** In addition to `DefaultExcluded`, `pathindex.Scan` parses `.gitignore` at the session root (if present as a regular file) to prevent project-specific build artifacts, temporary files, and excluded subtrees from entering `@` candidate completion. The matcher supports plain lines, `#` comments, directory-only patterns (`/` suffix), root-anchored paths (`/` prefix or internal `/`), and simple `*` wildcards (`path.Match`). Matching occurs without extra resolution syscalls: directory exclusions prune traversal queues before directory entry, and file exclusions skip candidates without allocating descriptors. Declared limits: nested `.gitignore` files in subdirectories, global `core.excludesFile`, negation patterns (`!`), `**` recursive globbing, and `.git/info/exclude` are strictly out of scope. If `.gitignore` is absent or unreadable, `Scan` falls back silently to `DefaultExcluded` without error or regression (`TestScanGitignoreExcludesMatchingFiles`, `TestScanGitignorePrunesDirectories`, `TestScanGitignoreAbsenceCausesNoRegression`, `TestGitignoreMaintainsPerformanceMarginOnWideTree`).

**Bounded execution limits and reachable stop conditions.** Indexing is bounded by three explicit limits: `DefaultMaxEntries = 50000`, `DefaultMaxCandidates = 10000`, and `DefaultTimeout = 2 * time.Second`. Traversal stops when entry count reaches `MaxEntries` (`StopEntries`), candidates reach `MaxCandidates` (`StopCandidates`), or elapsed time exceeds `Timeout` (`StopTimeout`), returning a clean partial index instead of hanging the process or exhausting memory. The invariant `DefaultMaxCandidates < DefaultMaxEntries` ensures that the candidate limit is structurally reachable (`TestDefaultCandidateLimitIsReachable`, `TestScanEntryLimitStopsTheWalk`, `TestScanCandidateLimitStopsTheWalk`, `TestScanTimeoutStopsTheWalk`).

**Partial index disclosure.** When traversal trips any limit before full completion (`!idx.Complete()`), the picker UI explicitly discloses partial indexing in its header (`── Files (partial index) ` at normal width, `── Files (partial) ` at narrow width). Truncated search results are never presented to the user as the complete state of the repository (`TestPickerDisclosesAPartialIndex`).

Evidence: `TestDefaultCandidateLimitIsReachable`, `TestScanRefusesSymlinkedEntries`, `TestScanSkipsExcludedDirectories`, `TestScanResolvesOncePerDirectory`, `TestScanEntryLimitStopsTheWalk`, `TestScanCandidateLimitStopsTheWalk`, `TestScanTimeoutStopsTheWalk`, `TestPickerDisclosesAPartialIndex`, `TestScanPathIndexNeverOffersTheShadowStore`, `TestScanGitignoreExcludesMatchingFiles`, `TestScanGitignorePrunesDirectories`, `TestScanGitignoreAbsenceCausesNoRegression`, `TestGitignoreMaintainsPerformanceMarginOnWideTree`.

### Strict argument decoding in read_file

`read_file` is the primary ReadOnly tool for reading project files. Malformed tool arguments could otherwise bypass containment or smuggle unexpected parameters prior to path resolution or descriptor open.

**Shared decoder limits apply before file access.** `read_file` uses
`decodeStrict`, but that name is not a guarantee against every duplicate or
additional JSON value. The shared decodeStrict implementation has the same
leading-whitespace, case-variant, top-level-only and EOF-check limitations
stated in the decoder claim; the production repair/gate path remains V14.
Unknown fields can be rejected by the decoder or removed upstream by repair;
this must be traced through the production path. Filesystem containment still
relies on its separate path/descriptor mechanisms, not on duplicate-key checks.

Inherited Evidence: `TestReadFileStrictArgs`.

### Disclosed discarded arguments in permission modal

When the tool-call repair layer drops inert undeclared arguments from a call, the user must not be misled into believing the dropped arguments will be passed to the underlying tool.

**Carried on ToolCall.** Any arguments discarded by repair are preserved on `agent.ToolCall.DroppedArgs`.

**Explicit disclosure row in PermissionModal.** `PermissionModal` checks `hasDroppedArgs()` and displays an explicit `dropped:` row (`| dropped: <keys> |`) in both `permLevelFull` and `permLevelCompact` modes.

**Responsive horizontal truncation.** At standard widths (120 columns), the complete list of discarded keys is shown. At narrow terminal widths down to the 24-column boundary, the row is preserved and truncated with an ellipsis (`dropped: …`) rather than omitted, ensuring the user is alerted to dropped parameters even in constrained displays.

**Vertical degradation preserves safety floor.** Under constrained vertical terminal heights, `permModalShape` gracefully reduces reserved rows by dropping blank lines first, followed by the args row, and then the dropped row, descending to the fail-closed minimum 3-row floor without crashing or misaligning modal layout. When `DroppedArgs` is empty, no dropped row is allocated or rendered.

Evidence: `TestModalDroppedArgsDisclosed`, `TestPermModalRowsMatchLineCount`.

### Semantic loop detection and tool repetition limits

The agent loop turn ceiling (`max-turns = 40`) is a numerical guard, not a semantic circuit breaker. A confused model that repeatedly invokes the same tool with identical arguments and receives identical errors or outputs would otherwise consume all 40 turns, exhausting API token budgets and delaying operator feedback.

**Tool execution fingerprinting.** Every tool invocation is fingerprinted by a 3-tuple `(tool, hash(input), hash(output-error))` recorded after execution:
1. `tool`: Exact tool name.
2. `hash(input)`: SHA-256 digest of canonicalized JSON arguments (lexicographically sorted keys via Go `json.Marshal`), falling back to raw byte hashing for non-JSON payloads.
3. `hash(output-error)`: SHA-256 digest of execution outcome, capturing both `OK` boolean status and output/error text.

**Run-scoped tracking.** Fingerprints are maintained in in-memory turn state scoped to the active `Loop.Run` invocation and reset at the beginning of each user prompt. Operations across different user interactions are not conflated.

**Dual-threshold enforcement:**
1. **Notice at 3 repeats:** Upon recording 3 identical fingerprints, the loop emits a `Notice` event into the journal (`«notice» loop detected...`). In accordance with the wire protocol in `messages.go`, this is translated into a `provider.Message{Role: provider.User}` in the next turn's history, alerting the model to alter its arguments or strategy.
2. **Hard cut at 5 repeats:** Upon recording 5 identical fingerprints, the loop aborts immediately without further tool execution or provider queries. It emits an explicit `Notice` event and a `RunError` event with `ErrorCode: "loop_detected"`, returning `ErrToolLoop` to the caller.

Evidence: `TestToolLoopNoticeAtThreeRepeats`, `TestToolLoopHardCutAtFiveRepeats`, `TestToolLoopCanonicalJSONKeyOrdering`, `TestToolLoopResetAcrossRuns`.

### Baseline measurement harness

`scripts/capture-baseline.sh` is security-adjacent measurement tooling, not a
runtime containment boundary. It records build, test, race, static-analysis,
security-gate, and artifact evidence in `local`, `ci`, and `full-security`
modes.

The harness does not install tools, modify provider credentials, or treat an
unavailable required security tool as a successful check. Its output can
contain command diagnostics and must be reviewed for sensitive data before
being shared or committed.

Android/arm64 does not support Go's race detector. A Termux result therefore
records race as `UNAVAILABLE`; the repository's GitHub Actions Linux race run
remains the authoritative race evidence.

## Open verification items (not claims)

Items below combine document questions and supplied source-review observations.
The existing citation guard was run, but no Go tests or security reproductions
were run. Decoder/purge/shadow details were inspected as stated above; other
inherited code observations were not re-audited. Distinguish an
inherited observation from a verified runtime result or an unadopted design.
Resolve each item at an exact commit, recording the evidence and limitations
before updating the relevant claim. No P0 severity is assigned by this list.

| ID | Question | How to resolve |
|---|---|---|
| V1 | Does endpoint policy match the selected baseline? Separately, which non-credential environment overrides still apply under Config v2: `NABD_ENDPOINT_ALLOW`, `NABD_PROVIDERS_FILE`, `NABD_AUTH_FILE`, `NABD_EXPORTS_DIR`, `NABD_CLIPBOARD`, `NABD_PUBLIC_DNS`? | Trace config, registry, endpoint and downstream consumers. Distinguish explicit credential sources from path/policy/utility overrides. Check what `TestCustomEndpointIsAcceptedByDesign` actually pins; cross-reference EVIDENCE-01. |
| V2 | On the permission modal, is the full `cmd` always visible or scrollable before approval can be armed, at every terminal size? `permModalShape` can drop the args row under vertical constraint | Trace the modal path; table-driven test over widths and heights with long and multi-line commands |
| V3 | Which modal/display characters are stripped, escaped or preserved beyond the listed zero-width controls: U+2028/U+2029, bidi embedding/override/isolate controls, tag characters, variation selectors, ESC and C1 controls? | Trace the actual modal path and render at constrained sizes; preserve legitimate Unicode where possible rather than deleting all format controls. |
| V4 | How does the `.gitignore` matcher treat `**`, nested files, `!` and `.git/info/exclude` compared with Git; in which direction does each divergence err; what reaches the provider and the journal? | Pattern tests against Git's behaviour; trace read, redaction and truncation |
| V5 | Endpoint guard: behaviour under `HTTPS_PROXY`; redirect handling (origin = scheme + host + port, headers carried, every client-construction path); address coverage (IPv4-mapped IPv6, NAT64, `0.0.0.0/8`, multicast) | Read the transport and `endpoint` package; table-driven tests |
| V6 | MCP: does approval identity cover launcher-based servers (`npx`, `uvx`, `node`, `python`) at script or package level or only the interpreter binary; does MCP output pass through the nonce fence; should ReadOnly tools with network egress need a separate consent dimension; do HMAC-only journal records suffice to reconstruct an incident? | Read the approval-store and fingerprint code; design decision for the last two |
| V8 | Termux/Android: do owner checks behave correctly under proot's emulated root; what happens on shared storage; which capabilities does `termux-api` expose? | Run the config-open tests under proot; add a startup filesystem check if needed |
| V9 | Does the stream redactor equal whole-string redaction under random N-way splits within its hold-back limits, and fail closed beyond them? | Fuzz test bounded by `StreamHoldCap` and `StreamPEMHoldCap` |
| V10 | Can background children outlive a cleanly exited bash call? | Prototype cleanup separately on Android/Linux; process-group membership and leader lifetime matter. Do not claim containment of detached descendants. See V18 for capture races. |
| V11 | Release pipeline: is the signature verified after signing, is build provenance published, is a vulnerability scan a gate? | Read `release.yml` and `ci.yml` |
| V12 | Are line endings and table rendering correct in the repository itself, not only in an exported copy? | `git ls-files --eol -- docs/THREAT_MODEL.md`; `git check-attr text eol -- docs/THREAT_MODEL.md`; view the rendered page |

| EVIDENCE-01 | Five GUARANTEED† claim rows lack specific test/guard citations: opened-config validation; config package never writes; Config v2 credential fallback; shadow checksum/publication; undo hash refusal. | Name and validate evidence or downgrade each row. For the credential row also audit the six non-credential overrides in V1. Add a row-aware evidence-presence guard; separately enumerate dagger rows and require EVIDENCE-01 coverage. Existence of a citation is not coverage or test success. |
| V14 | Which decoder/repair limits remain, and what payload does the permission UI show? | Decoder half resolved 2026-10-18: `decodeStrict` refuses duplicate keys at any depth (case-insensitive, checked against the Go struct-field match that made `{"cmd":…,"Cmd":…}` ambiguous), nesting past `maxJSONDepth`, and any data after the first value, with unit and end-to-end tests (`TestDecodeStrictRejectsDuplicateKeysAtAnyDepthOrSpelling`, `TestDecodeStrictRejectsTrailingData`, `TestBashRejectsNonStrictArgs`, `TestReadFileDecoderBoundaryWithRepairOff`, `TestWriteValidationRejectsBeforeSideEffects`, `TestMCPArgumentsStrictBoundary`). Still open: `glob` decodes with `json.Unmarshal`; the repair layer's declared-key drop for ReadOnly and Executing tools; and what payload the permission prompt shows was not traced. |
| V15 | Proposed protected-path policy: reject internal `.git/` and `.ag/` access through file tools; require per-call consent for selected build/execution files without YOLO or session bypass. | Design decision first. Then production-path read/write/edit and policy tests, including symlinked parents, session grants, YOLO and ordinary controls. A helper-only classification test is insufficient. |
| V16 | PR #270 review reports that blob references are removed after commit capture, so disk content may remain. Compatibility hypothesis: an older reader may interpret empty BlobBefore on an existing-file write as creation and delete the file on undo. | Verify the exact PR head and both readers. Exercise a real excluded-file write, inspect before/after blob bytes on disk, and run undo with old/new binaries on disposable copies. Consider an incompatible schema version or proven fail-closed representation; do not assume an arbitrary non-empty sentinel is safe. This is not a reproduced deletion result. |
| V17 | Should compaction output be fenced and labeled as model-generated text derived from untrusted history? | Adopt a projection design, then test the real compaction-to-message path, nonce/delimiter handling and continued permission gating. Map fixtures to actual event and projection APIs; a placeholder test is not ready-to-compile evidence. |
| V18 | Supplied review reports possible concurrent headTail access after bashDrainGrace while a descendant holds a pipe. Is this related to the timeout-test flake? | Run the actual timeout/capture path under the race detector; inspect failing logs. Evaluate synchronized buffers or closing/draining safely; do not attribute the flake without evidence. |
| V19 | What independent review or renewed acceptance exists for MCP, and is any release gate mechanically enforced? Should a separate CLI opt-in be required? | Verify ADR-0003, reviewer/acceptance records, release workflows and settings. Distinguish user-scoped enabled-server configuration from project-skill opt-in. Default-off and release gating are design decisions, not established defects. |
| V20 | Source-comment contradiction: journal_rotation.go says non-jsonl archives are invisible to purge, but purgeCandidates includes regular names containing .archived-. | The predicate is established by [purgeCandidates](https://github.com/amiraq1/Nabd/blob/51859a06b5e4ba7d35babad700a4376acaa84a7d/cmd/ag/purge.go#L85) and the existing archived-file fixture in TestPurgeDeletesOnlyEligibleRegularJournals. Correct the stale maybeRotateOversizedJournal comment in a separate source-comment-only change; no uncertainty about the predicate or new runtime behavior is implied. |

## Revision log

2026-10-18, decoder hardening (claims-affecting documentation revision):

- Narrowed the decoding claims to the decoder that now exists, and moved them
  from REDUCED/NOT PROVIDED to GUARANTEED with the mechanism named in each row:
  duplicate keys are refused at any depth and compared case-insensitively
  because encoding/json matches struct fields that way; `decodeStrict` enforces
  a single-value contract with a second `Decode` that must return `io.EOF`; the
  depth ceiling `maxJSONDepth` is enforced by the walk rather than by a syntax
  or type error.
- Added end-to-end coverage through the production paths (bash, read_file,
  write_file, edit_file, mcp_tool) and through the repair-disabled boundary,
  since repair may drop an undeclared key for ReadOnly and Executing tools.
- Kept `glob` out of scope: it still decodes with `json.Unmarshal`, which
  rejects trailing data on its own.
- Left V14 open for what this revision did not trace: the payload the
  permission prompt shows, and the repair layer's declared-key drop.
- No claim, boundary row or residual was added beyond what a test in this
  revision proves; the skill-walk read-ahead closure is recorded in
  `docs/TECH_DEBT.md`, not here, because it is not a threat boundary.

2026-10-15, v14 draft (claims-affecting documentation revision):

- Built from the supplied editorial v13. The raw v13 base was recovered by
  reversing the supplied editorial diff. Before PR submission, the document
  was reconciled with master at 51859a06 and the unmerged PR #279 paragraph
  and its test citation were excluded. The existing citation guard was run
  against that pinned snapshot; this checks existence, not execution.
- Separated decoding mitigation from an unprovided end-of-input boundary,
  interactive redaction from verbatim stdout, and repository-controlled Git
  config protections from user-installed/future command paths.
- Added deferred-execution and MCP per-run-opt-in boundary rows. Recorded
  compaction label preservation as NOT PROVIDED rather than naming a missing
  mechanism as REDUCED. Permission gating remains a separate mitigation.
- Marked the five uncited GUARANTEED claim rows GUARANTEED†. The number is
  computed from the supplied tables, not copied from the proposed six-row count.
- Added assets, an endpoint/MCP adversary, storage prerequisites, operator
  guidance, EVIDENCE-01 and V14–V20 and explicit source/CI validation limitations.
- Aligned detailed bash/read_file decoding prose with the downgraded claim.
- Reduced the absolute shadow Git-invisibility claim to ignore-file mitigation
  with explicit failure/existing-file/forced-add residuals.
- Retitled the picker and selected-zero-width-control claims; supplied a
  config-opening residual. Qualified shadow git-shielding and purge eligibility.
- Recast independent-review assertions as unestablished evidence, not a
  conclusion inferred from CODEOWNERS or single-operator status.
- V16 older-reader deletion remains a compatibility hypothesis. No Go changes,
  runtime reproductions, successful test executions or current PR state are
  claimed by this draft. All inherited test citations still require validation.
- Resolved V7 in the YOLO claim itself: standing Mutating grants may survive
  ModeDeny/ModeAllowReads, but never ModePlan. Removed the resolved open item;
  source ordering was inspected, not runtime-tested in this revision.
- Follow-up to PR review: scoped the trailing-data claim to decodeStrict and
  distinguished glob; cited rejectDuplicateKeys and its exact leading-byte gate.
  End-to-end repair/gate behavior remains open. Established purge's filename and
  cutoff predicates and recast V20 as a stale-comment contradiction.
- Renamed the evidence item to EVIDENCE-01 to distinguish it from version v13;
  added the MCP injection residual and aligned config prose with evidence debt.
- Repaired shadow-asset grammar and separated flag names to avoid table/code
  pipe ambiguity. The actual Go shadow call uses os.O_CREATE and os.O_EXCL;
  O_CREAT is not substituted for the valid Go identifier.
- Retained the editorial revision below under explicit superseded provenance;
  its historical "unchanged" statement does not describe the v14 result.


### Superseded provenance — v13 editorial revision only

The following record describes only the earlier editorial step. Its "not
changed" statement does not describe this v14 result or its follow-up corrections.

2026-10-15, documentation only, applied to the earlier v13 text:

- Added: contents, "How to read this document", "Global assumptions", "Trust
  boundaries at a glance", "Residual-risk register", "Open verification items",
  this log, and three operator-guidance items (8–10).
- Fixed: a blank line that split the claims table before the "OpenAI wire
  encoding" row.
- Rewrote "Custom endpoints" to agree with the claims-table rows, and removed
  the stale reason given for rejecting `base_url` in Config v2 (open item V1).
- Reworded Adversary 3: the environment allowlist protects the child's
  environment, not files the user can read.
- Retitled the `.gitignore` claim to state its matcher scope; status unchanged.
- Split one MCP row that carried both GUARANTEED and REDUCED tests into one row
  per status, keeping the original wording.
- Corrected two statements in "Silent-turn exit attribution": the old "untested"
  wording, and "only category consumer" (the headless decision also reads it).
- Moved the MCP implementation-status text out of the heading.
- At that earlier editorial step only, every other claim row, status, test
  citation and section was unchanged. Later v14 changes supersede that scope.
