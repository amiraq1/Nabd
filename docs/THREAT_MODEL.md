# Threat model — v11

Last reviewed: 2026-09-23

This is the only place nabd states security claims. README points here.

## Assets

| Asset | Where it lives |
|---|---|
| Provider API keys | v1: `NABD_CONFIG` or `~/.ag/config`, with process environment fallback. v2: `NABD_CONFIG_V2` or `~/.ag/config.v2.json`, with explicit `env` or secure file credential sources only. OpenCode registry: `~/.ag/auth.json` (mode 0600, owner checked) with legacy environment fallback, and `~/.ag/providers.json` (mode 0600) with builtin catalog fallback. |
| Working tree | The resolved project directory used to construct `tools.Root`. |
| Session journal | `~/.ag/sessions/*.jsonl` by default. Append-only events can include file contents and command output in cleartext; directory mode 0700 and file mode 0600 limit cross-user reads. Journal redaction is enabled by default and removes recognized credential patterns from new events before write; `NABD_REDACT_JOURNAL=0` is an explicit diagnostic opt-out. |
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
| `bash` cannot receive a session-wide grant | GUARANTEED | `TestSessionGrantAppliesToWritesOnly`, `TestRawDecisionForBash` |
| Denied `bash` starts no subprocess | GUARANTEED | `TestBashDeniedRunsNoSubprocess` |
| Permission decisions are journal-first and effective decisions are returned to the loop | GUARANTEED | `TestDecideLogsEffectiveDecision`, `TestDecideRefusesWhenPermissionQuestionCannotBeJournaled`; failed permission events deny execution and do not record a session grant |
| Bash child environment is an allowlist that strips unsafe PATH entries, uses an isolated HOME, and points TMPDIR/TMP/TEMP at one private per-invocation directory | GUARANTEED | `childEnv` starts from an empty environment and copies only its allowlist. The temp-directory variables are no longer inherited: each invocation gets its own `nabd-tmp-*` directory, created 0700, never shared across invocations, and removed when the command finishes; if it cannot be created the child gets no temp variable at all rather than the caller's value. Evidence: `TestBashChildEnvAllowlistIntegration`, `TestBashChildEnvPathStripping`, `TestBashChildEnvHomePolicy`, `TestBashChildTempDirIsIsolatedAndRemoved` |
| Bash arguments are decoded strictly before any subprocess starts | GUARANTEED | `bashTool.RunDetailed` uses the shared `decodeStrict` decoder — duplicate keys rejected by tokenizing the raw object, undeclared fields by `DisallowUnknownFields` — instead of `json.Unmarshal`, which keeps the last duplicate key and ignores undeclared ones. The boundary is the net behind repair: with repair disabled, or for a call the layer declines, an undeclared or duplicate key fails as `invalid args` and runs nothing. Evidence: `TestBashRejectsNonStrictArgs`, `TestBashStrictDecodeStillFailsThroughLoop` |
| Mutation recovery intent is journaled and synced before a file publish; failed pre-publish attempts are marked separately, while a committed edit remains undoable after restart | GUARANTEED | `edit_intent` and `edit_abort` are journal-only recovery records; critical journal events use the durable sink path before the filesystem mutation proceeds. The failure matrix distinguishes file-fsync and rename failures before publication from a parent-fsync failure after publication, so recovery never guesses from an ambiguous error. Evidence: `TestMutationIntentFailureDoesNotPublish`, `TestMutationAbortEventsTrackPrePublishFailure`, `TestWriteFileAtomicFailureMatrix`, `TestWriteFileAtomicReportsPublishedWhenParentFsyncFails`, `TestCriticalEventsSyncDurableSink` |
| Git header subprocess inherits no parent environment: only PATH/TERM/LANG/LC_ALL are forwarded, secrets and `GIT_CONFIG_GLOBAL` are dropped, and Env is never nil | GUARANTEED | `TestGitChildEnvForwardsOnlyAllowlist` |
| Opened config must be regular, user-owned on Unix, and have no group/other permission bits | GUARANTEED | `internal/config` ParseFile and secure-open tests |
| Config v1 and Config v2 default files coexistence on disk is fatal at startup | GUARANTEED | `TestV2CoexistenceOnDiskIsFatal` |
| Config v2 rejects unknown fields, trailing JSON, and custom `base_url` | GUARANTEED | `TestV2RejectsUnknownFieldsAndTrailingJSON`, `TestV2CredentialFileAndClosedEndpointPolicy` |
| Config package never writes configuration or credentials to disk | GUARANTEED | package API and configuration tests |
| Config descriptor opening rejects symlinks and blocking special files | REDUCED | `O_NOFOLLOW` and `O_NONBLOCK`, followed by descriptor validation |
| Config v2 has no implicit environment credential fallback | GUARANTEED | credential sources must explicitly use `env` or a secure absolute `file` only |
| Removed sandbox configuration keys requesting a boundary fail closed at startup before provider or tool execution | GUARANTEED | When `NABD_BASH_SANDBOX=on`, `NABD_BASH_NETWORK=deny`, or `NABD_BASH_RESOURCES=limit` is present in environment or configuration files, startup fails closed with an explicit error instructing the user to remove the setting; no provider request is made and no subprocess is executed across headless and interactive entry points. Neutral values (`auto`, `off`, `allow`, empty) emit a one-line warning on stderr that the setting is ignored. Evidence: `TestRemovedBashKeysRequestingBoundaryFailClosed`, `TestRemovedBashKeysNeutralValuesWarnOnly`, `TestRemovedBashKeysFailBeforeProviderOrTool`, `TestRemovedBashKeysFailClosedInteractiveStartup` |
| The router's Retry-After wait budget is bounded, opt-in, and pre-commit only | GUARANTEED | `NABD_ROUTER_RETRY_AFTER_WAIT` is parsed into whole seconds in `[0, 120]` and defaults to 0 (disabled); the wait runs at most once per `Stream`, strictly before the commit point, through the injected clock and cancellable by the parent context. Evidence: `TestParseRetryAfterWait`, `TestWithRetryAfterWaitClamps`, `TestShouldWaitOut` |
| The runtime status row never claims progress after a terminal failure | GUARANTEED | `RunError` and `Interrupted` retire the progress claims when the event is projected, instead of waiting for the runner goroutine to return; the send gate (`busy`) is left set so a failed run cannot be followed by a second concurrent run before `doneMsg`. Evidence: `TestRunErrorRetiresProgressStatus`, `TestRunErrorKeepsSendGate`, `TestInterruptedRetiresProgressStatus`, `TestDoneMsgClearsRunFailedStatus` |
| Newly visible route statuses disclose no more than the existing ones | GUARANTEED | `waiting` and `blocked` notices go through the same `display.SanitizeForDisplay` policy with redaction enabled, are single-line, never include `StreamID`, and fall back to fixed placeholders for blank fields. `attempted` and `exhausted` remain hidden. Evidence: `TestFormatRouteNoticeWaitingIsVisible`, `TestFormatRouteNoticeWaitingRedactsSecrets`, `TestFormatRouteNoticeBlockedIsVisible`, `TestFormatRouteNoticeStillHidesStructuralStatuses` |
| Status-row runtime metadata reports only committed routes and discloses no new fields | GUARANTEED | `StatusProjector.Meta` reports a provider/model only from a `selected` route trace, never from `attempted`, `failed`, `waiting`, or `blocked`; elapsed time freezes at the terminal event; provider and model pass through the same sanitizer as route notices; `StreamID`, credentials, and file paths are never included. Evidence: `TestMetaTracksTurnTokensAndCommittedRoute`, `TestMetaElapsedFreezesAtTerminalEvent`, `TestRuntimeMetaSanitizesRouteFields`, `TestRuntimeStatusRowStaysOneRow` |
| Rendered failure detail is redacted, bounded, and adds no new source of data | GUARANTEED | `FormatRunError` reads only fields already in the `run_error` event (`Err`, `ErrorCode`, `Code`), passes every line through `cleanField` (sanitizer with redaction enabled), emits single logical lines only, caps detail lines at `maxRunErrorDetails`, and discloses that the remainder stayed in the journal. Hints are fixed literals keyed by `ErrorCode`, never provider text. Evidence: `TestFormatRunErrorKeepsCodeAndRouteDetail`, `TestFormatRunErrorRedactsSecrets`, `TestFormatRunErrorCapsDetails`, `TestRenderRunErrorRedactsSecrets`, `TestRenderRunErrorRespectsWidth` |
| The router's retry-after reaches the feed as a field and is stated at every width, on both surfaces | GUARANTEED | `agent.RunErrorEvent` lifts the shortest positive `RouterExhaustedError.RetryAfter` onto the pre-existing `Event.RetryAfter` field; `presentation.ErrorCardFromEvent` carries it to `ErrorCard.WaitSeconds` and `FormatRunError` to `RunErrorView.WaitSeconds`. The feed card renders it on its own line before the actionable lines — so the width ladder, which hides the card's `Message` below 40 columns and truncates it above that, can drop prose but never this number — and only a positive value is lifted, so no card claims a wait the router never reported. Evidence: `TestFeedErrorCardShowsWaitAtEveryWidth`, `TestFeedErrorCardStatesNoWaitWhenNoneReported`, `TestErrorCardCarriesWaitSeconds`, `TestRenderRunErrorShowsWaitAtNarrowWidth` |
| Tool-output truncation is recorded as data and disclosed on the row that reports the result | GUARANTEED | `Event.ForStore()` records the number of discarded bytes in `ToolCall.TruncatedBytes` alongside the existing in-output marker; the field is additive (`omitempty`, legacy journals decode to `0`), the live in-memory event is never mutated, and the renderer states the loss on the `tool_end` row within the terminal width. Evidence: `TestForStoreRecordsTruncatedBytes`, `TestForStoreLeavesSmallOutputAlone`, `TestForStoreDoesNotMutateLiveEvent`, `TestTruncatedBytesIsAdditive`, `TestToolEndStatesTheLoss`, `TestToolEndSaysNothingWhenNothingWasCut`, `TestTruncatedToolRowRespectsWidth` |
| Feed item retention is bounded and disclosed without mutating journal history | GUARANTEED | When the merged projected feed exceeds `maxVisibleFeedItems`, `visibleFeedItems` inserts one synthetic notice that states the exact number of older items omitted from the in-memory feed and identifies the session journal as the full-history source. The notice occupies one slot, the newest items remain visible, and neither projector items nor journal events are modified. Evidence: `TestVisibleItemCapStatesHiddenHistory`, `TestLineCacheNeverExceedsVisibleItemCap` |
| Plan mode is strict read-only and cannot be overridden by a session grant or YOLO | GUARANTEED | `perm.Policy` carries a `Mode`; when it is `ModePlan`, `Check` returns `Deny` for every `Mutating`/`Executing` tool and short-circuits before the YOLO and standing-grant branches, so neither a per-session "allow for this session" nor `SetYOLO(true)` can write a byte or run a command. Reads (`ReadOnly`) still pass, so a plan-mode run can inspect the tree but never change it. The mode is applied to both the interactive gate (`gate{pol}` in `doChat`/`doChatWithFeed`) and the headless gate, so the same rule holds with and without a TTY. Evidence: `TestModeTable`, `TestModePlanOverridesGrants`, `TestModePlanAllowsReads` |
| Shadow blobs are SHA-256 addressed and verified on read; publication never silently replaces an existing blob | GUARANTEED | `internal/snap` checksum and rename capability tests |
| Undo refuses when current bytes no longer match the recorded hash | GUARANTEED | undo and persisted-undo tests |
| Prompt injection through ReadOnly output | REDUCED | nonce fencing plus approval for sensitive actions; model behavior is not guaranteed |
| Agent notices forwarded to the model context are restricted to an enumerated allowlist that defaults to deny | GUARANTEED | Only notices that change world-state (such as /undo results) or restrict required capabilities (such as loop limits) reach the provider message projection; monitoring, calibration, and display notices never enter the model conversation. Forwarded notices are explicitly tagged («notice») as agent/system events rather than raw user text. The tag is a semantic signal that does not technically prevent model compliance with allowed notices; the guarantee is narrowing injectable content, not immunizing the model against permitted notices. Evidence: `TestCalibrationNoticeNeverReachesTheModel`, `TestBlockedNoticeCategoriesDroppedWhileToolCallsPending`, `TestAllowedNoticeCategoriesFlushedAfterToolCallsPending` |
| The text of a notice that reaches the model is structured, bounded, and rendered by one choke point | GUARANTEED | `renderNotice` in `messages.go` is the only translation from a notice event to the model-facing line: a structured `NoticeData` payload is authoritative and the human `Text` is never consulted beside it, a payload that does not match its `NoticeCategory` is dropped rather than repaired, and text-only events (journals written before the payload existed) are sanitized and bounded so replay is unchanged. Every line passes `provider.SanitizeBody` for credential redaction, has every control rune collapsed so one notice is exactly one logical line, cannot open with the `«notice»` frame marker, and is capped at `maxNoticeBytes`. The emitters are guarded structurally: `TestAllowedNoticeEmitsCarryStructuredPayload` fails if an allowed category is ever constructed with raw `Text` and no payload, or if any allowed category has zero production emit sites; `TestAllowedNoticeGuardDetectsCategoryWithoutEmitter` proves that the AST guard detects orphan categories without emitters. Evidence: `TestNoticePayloadIsAuthoritativeOverHumanText`, `TestNoticePayloadCategoryMismatchIsDropped`, `TestNoticeLineIsBoundedSingleLineAndControlFree`, `TestLegacyNoticeTextIsSanitizedAndStillFlushed`, `TestNoticeTextCannotSpoofTheFrame`, `TestStructuredNoticeRendersPerCategory`, `TestNoticeProjectionUsesTheRenderer`, `TestAllowedNoticeEmitsCarryStructuredPayload`, `TestAllowedNoticeGuardDetectsCategoryWithoutEmitter` |
| Tool-call repair changes what runs relative to what the model asked for (NBD-420) | REDUCED | `internal/tools/repair.go` corrects malformed calls before the registry lookup, so the gate classifies and prompts on the corrected call. It is a new surface between the model and the gate, bounded by design: inference is one-directional (an unrecognised name resolves only to a ReadOnly tool — `read_file`, `glob`, `grep` — and `write_file`/`edit_file`/`bash` require a literal match); the name map is explicit, never edit distance or string similarity, and every target is verified against `known`; path resolution is untouched, because `Root.Resolve` alone decides inside/outside the root and the layer never expands `~`, absolutises or relativises anything; and a call needing more than three corrections is returned unchanged. An argument key the tool does not declare is dropped only where it is inert — where it cannot change the action the user approves — and never where it may be a live field. For `bash` the whole action is the `cmd` string, which the prompt displays verbatim, so any other key changes nothing that runs; for `write_file` and `edit_file`, `old`, `new`, `all` and `content` are live fields, so dropping an undeclared one — `old` from a `write_file` call, say — would turn a local edit into a whole-file replacement, which is a different action rather than a narrower one. Every drop is reported as a fix and carried on the `tool_start` event as `DroppedArgs`, so the executed arguments being shorter than the model's is stated rather than implied, and the prompt is built from the repaired call, so the arguments on screen are the arguments that run. A name still unknown after the map returns the existing unknown-tool error, which the model reads and recovers from. Proof: `TestRepairInferenceIsReadOnlyOnly`, `TestRepairNameMapTargetsAreDeclared`, `TestRepairPathResolutionIsUntouched`, `TestRepairCapReturnsUnchanged`, `TestRepairDropsUndeclaredKeysOnlyWhereSafe`, `TestBashPromptShowsTheRepairedCall`, `TestRepairRulesSaveARound` |
| File reads open through descriptor-relative operations and refuse symlinks and non-regular files | GUARANTEED | On unix, `internal/safefs.OpenRead` walks the relative path from a root descriptor with `O_NOFOLLOW` and validates the target from the opened descriptor with `Fstat`. Evidence: `TestOpenReadRefusesFinalSymlink`, `TestOpenReadRefusesIntermediateSymlink`, `TestOpenReadRefusesFIFO`, `TestOpenReadRefusesDirectory`, `TestCaptureFromRootRejectsFinalSymlink`, `TestCaptureFromRootRejectsIntermediateSymlink` |
| File mutations publish relative to the parent descriptor and re-prove the target at the syscall | GUARANTEED | `write_file`, `edit_file`, and `/undo` derive one relative path and never open, stat, or rename a project file by absolute path on unix; the absolute path is reporting metadata only. Evidence: `TestWritePathFromRootRejectsTraversal`, `TestWriteFileAtomicRefusesIntermediateSymlink`, `TestWriteFileAtomicReplacesFinalSymlinkWithoutFollowing`, `TestWriteFromRootDoesNotUseAbsoluteMetadataAsAuthority`, `TestCommitRejectsSymlinkWithoutCapturingOutsideContent`, `TestReadSourceFromRootUsesRelativeAuthority`, `TestRemoveFromRootUsesRelativeAuthority`, `TestRemoveFileRemovesFinalSymlinkNotReferent`, `TestUndoRefusesModifiedAfterAgentWrite`. Supplementary structural guards (AST-based; they pin the delegation shape, not the path property): `TestWriteCommitDelegatesToAdapters`, `TestWriteCommitUnixAdapterIsDescriptorOnly`, and `TestWriteCommitOtherIsCompatibilityPath` |
| Traversal tools never surface symlinked entries and skip the shadow store | GUARANTEED | `glob` and `grep` list and search regular files only, and `skipDir` excludes `.ag`, so the content-addressed shadow history is never read back. Evidence: `TestGrepNeverSurfacesSymlinkedEntry`, `TestGrepSingleFileRefusesSymlinkEscape`, `TestGlobOmitsSymlinkedEntries`, `TestTraversalToolsNeverSurfaceShadowStore` |
| Release SBOM coverage and threat-model evidence citations are mechanically checked before merge | GUARANTEED | `TestReleasePipelineContracts` and `scripts/check-threat-model-tests.sh` |
| Path opening after `Resolve` outside the descriptor layer | REDUCED | The `!unix` compatibility adapter in `internal/tools/write_commit_other.go` (`TestWriteCommitOtherIsCompatibilityPath`) carries no descriptor guarantee; `bash` is not contained; `snap.Restore` / `snap.RestoreAt` are a path-based publish path, now test-only (`docs/TECH_DEBT.md`); configuration reading still traverses parent-directory symlinks. Residual: a same-uid attacker through those paths |
| Journal redaction is enabled by default | REDUCED | recognized credential patterns (Anthropic, OpenRouter, OpenAI `sk-proj-`/long legacy `sk-`, Groq, NVIDIA, AWS access-key IDs, PEM private-key blocks, bare JWTs, GitHub, GitLab, Slack, `Bearer`/`authorization`) are removed from new events before `Event.ForStore()` and output truncation. Pattern matching is best effort: redaction runs per event, so a secret split across two streamed chunks (each provider text chunk is its own `text_delta` event) is not reassembled and can survive. `NABD_REDACT_JOURNAL=0` is an explicit diagnostic opt-out; Config v1 in `~/.ag/config` takes precedence over the environment. Copy-on-write leaves the live in-memory event untouched. Unrecognized sensitive content, structural fields (paths, tool names, call IDs, hashes, blob addresses, error codes), and the shadow store are unchanged. `--json` applies the same policy so it cannot diverge. Evidence: `TestRedactExtendedPatterns`, `TestRedactIdempotent`, `TestJournalRedactionEnabledByDefaultAndDisabledOnlyByExplicitZero`, `TestJournalRedactionReadsV1Config`, `TestOpenSessionJournalUsesEnabledRedaction` |
| Journal continuation handles a torn final line without appending across it | GUARANTEED | On append-open, a malformed final line is copied to a private `*.recovery-*` artifact outside the active `*.jsonl` session namespace, and the active journal is truncated to its valid prefix before continuation. A malformed middle line refuses the append without modifying the source; a valid final JSON line without a newline is separated before the next event. Evidence: `TestNewJSONLRepairsTruncatedFinalLine`, `TestNewJSONLSeparatesValidFinalLineWithoutNewline`, `TestNewJSONLRefusesCorruptMiddleLine` |
| Unresolved mutation intents are classified on continuation without automatic replay | GUARANTEED | When the active branch contains an `edit_intent` without a matching `edit_record` or `edit_abort`, `--continue` compares the current descriptor-relative file state with the recorded before/after hashes, emits a display-only recovery notice with counts for `not_published`, `published`, `missing`, `conflict`, and read errors, and never re-executes the mutation. Abandoned branches are ignored. A subprocess crash harness proves the intent survives a killed process and each post-crash filesystem state is classified without changing the target. Evidence: `TestUnresolvedMutationIntentRequiresExplicitRecovery`, `TestCommittedOrAbortedMutationIsNotUnresolved`, `TestUnresolvedMutationIgnoresAbandonedBranch`, `TestReconcileMutationClassifiesWorkingTreeWithoutWriting`, `TestReconcileMutationRejectsUnsafePath`, `TestMutationCrashRecoveryMatrix` |
| Session purge is bounded and opt-in | GUARANTEED | `nabd purge` is a dry run unless `--yes` is supplied, considers only regular `*.jsonl` files directly inside the selected directory, and supports an RFC3339 modification-time cutoff. It does not traverse subdirectories or follow symlinks. This does not provide race-free deletion against a same-user concurrent attacker; stop active nabd processes before cleanup. Evidence: `TestPurgeIsDryRunUnlessConfirmed`, `TestPurgeDeletesOnlyEligibleRegularJournals`, `TestPurgeRejectsInvalidCutoff` |
| Redacted export leaves the source intact | GUARANTEED | `--export --redact` decodes and re-encodes to stdout; the source is opened read-only and never written. Raw `--export` copies source bytes verbatim. Evidence: `TestExportLeavesSourceUntouched`, `TestExportRawIsByteIdentical` |
| Pointer input can select and expand cards, but never answers permissions or executes tools | GUARANTEED | Evidence: `TestPointerNeverAnswersPermission`, `TestPointerNeverExecutesATool` |
| Git status header inspects only the granted root and never traverses to a parent repository | GUARANTEED | `isGitRepo` checks `.git` strictly at the root; parent repos are out of bounds. Evidence: `TestIsGitRepoDetection` |
| OSC 52 clipboard copy operates on projected cards only, after credential redaction and display sanitization | GUARANTEED | Evidence: `TestCopyRedactsRecognizedCredentials`, `TestCopyNeverUsesRawJournalContent`, `TestCopyRejectsRawErrorBodies`, `TestCopyIsBlockedByPermissionModal`, `TestCopyNeverExecutesACommand` |
| A bare `nabd` runs the feed UI, and `--feed=false` is the rollback that removes the `@` index | GUARANTEED | The `-feed` flag defaults to `true`, so the default interactive path is `doChatWithFeed` — the same path that calls `SetPickerRoot(root.Dir())` and therefore indexes the session root for `@`. `--feed=false` selects `doChat`, which wires no picker and performs no scan; the two entry points are mutually exclusive within one invocation, and the rollback needs no rebuild or reinstall. Evidence: `TestFeedIsTheDefaultInteractiveUI` |
| Provider API keys never enter child environments or error output | GUARANTEED | `internal/provider/` constructor routes load keys from parsed config only, never through `os.Setenv`; `loadEnv` does not push credentials into the process environment that `bash` children inherit. Error bodies pass through `redact.Redact` before logging. Evidence: `TestConstructorsCarryKey`, `TestRouterRedactsBearerAuthorization`, `TestRouterFallsBackOnCredentialFailureWithRedactedLog`, `TestParseRoutesErrorMessagesContainNoSecrets` |
| Recognized credential patterns are redacted from logs, journal, and display | REDUCED | `internal/redact/` matches Anthropic, OpenRouter, OpenAI (`sk-proj-` and long legacy `sk-`), Groq, NVIDIA, AWS access-key IDs (`AKIA`/`ASIA`), whole PEM private-key blocks (including an unterminated `BEGIN` line, redacted to end of input), bare JWTs, GitHub, GitLab, Slack, and `Bearer`/`authorization` patterns, replacing them with `[REDACTED]`; `SanitizeBody` runs redaction before truncation. Pattern matching is best effort: unrecognized secrets, and secrets split across streamed events, are not removed. Evidence: `TestRedactRecognizedCredentials`, `TestRedactExactKeys`, `TestRedactIsIdempotent`, `TestRedactExtendedPatterns`, `TestRedactIdempotent`, `TestSanitizeBodyRedactsBeforeTruncation` |
| Session journal files and directories are created private and legacy files hardened | GUARANTEED | `internal/store/` creates journal files with mode `0600` and parent directories with `0700` via `ensurePrivateParent`; existing files wider than `0600` are hardened with `Fchmod` before any data is written. Evidence: `TestNewJSONL_NewFileIsPrivate`, `TestNewJSONL_LegacyFileIsHardened`, `TestNewJSONL_CreatedAncestorsArePrivate`, `TestNewJSONL_MissingParentDirIsPrivate` |
| Release artifacts are signed | GUARANTEED | `.goreleaser.yaml` signs the `checksums.txt` manifest with `cosign sign-blob` and publishes `checksums.txt.sig` (the signature) and `checksums.txt.pem` (the certificate) as release assets, so verification needs only those three files; the build is pinned to a known Go toolchain. `release.yml` pins the cosign major via `cosign-release`, because `--output-signature`/`--output-certificate` are the legacy cosign v2 interface and cosign v3 ignores both in favour of `--bundle`, which leaves the manifest unsigned and aborts the release at the signing step. Evidence: `TestReleasePipelineContracts` |
| Security gate scripts are mechanically checked | GUARANTEED | `scripts/check-pr-security-checklist.sh` enforces Phase 1 of the PR template; `scripts/check-threat-model-tests.sh` verifies every backtick-quoted test citation in this document resolves to a real `Test*` function. Evidence: `TestPRChecklistGateScopesThreatModelClaim`, `TestPRChecklistGateSecurityPathsAreRealBoundaries` |
| Pathindex is a security surface in the default UI | GUARANTEED | The `@` picker is reachable by default through `Feed.SetPickerRoot`; its traversal, session root `.gitignore` awareness, static exclusions, limits, and partial-index disclosure are documented and tested. Evidence: `TestDefaultTimeoutDoesNotBindBeforeTheCandidateLimit`, `TestGitignoreMaintainsPerformanceMarginOnWideTree`, `TestPickerExplicitSessionRootOverridesGitDir`, `TestScanRefusesSymlinkedEntries`, `TestScanGitignoreExcludesMatchingFiles` |
| Session `.gitignore` exclusions are enforced at the permission layer | GUARANTEED | Patterns from the session root `.gitignore` are parsed by `internal/ignorefile` (the same matcher `pathindex.Scan` uses for the `@` picker, so the picker, reader, and mutators cannot disagree) and installed on `perm.Policy` at startup via `wirePathRule`, which is also the registry's path gate. `read_file` and `edit_file` refuse directly named excluded paths before any byte is read or inspected; `grep` skips excluded paths, appending an `N files excluded by the session .gitignore` disclosure instead of silently searching less. `write_file` permits writes to excluded paths (preserving legitimate generation of build artifacts in `dist/`, `build/`) while strictly separating model-facing disclosure from mutation tracking: diff generation (`Patch`) is suppressed so excluded content is never disclosed to the model or journal, whereas mutation tracking retains blobs in `.ag/shadow` and consumes read credit so user `/undo` reversibility remains guaranteed. Named trust boundary: non-disclosure to the model and `@` exclusion are guaranteed by the gate and patch suppression; rollback retention in `.ag/shadow` is deliberately preserved so agent mutations can be undone by the user; isolation of the local `.ag/shadow` directory from unauthorized local access remains the host environment's responsibility. Scope decision: the refusal for reads and edits is default in `ask`, `deny`, and `plan` modes; `--permission-mode allow-reads` is the override for `read_file` and `grep` only (`edit_file` remains denied in all modes as editing cannot be performed without inspecting content). `bash` and `glob` are out of scope: glob sees names only, and an approved shell command already runs with the user's authority. A zero-value or missing ignore file leaves the rule inert. Evidence: `TestInteractiveSessionWiresPathRule`, `TestHeadlessSessionWiresPathRule`, `TestReadFileRefusesIgnoredPath`, `TestReadFileRefusalIsNotFromPathindex`, `TestReadFileWithoutGateUnchanged`, `TestReadFileAllowReadsOverrideReadsIgnoredPath`, `TestEditFileRefusesIgnoredPathAndDoesNotLeakContent`, `TestShadowRetainsBlobsForUndoWhileSuppressingDiff`, `TestWriteFileToExcludedPathSucceedsWithUndoTracking`, `TestUndoRevertsWriteToGitignoredFile`, `TestExcludedWriteConsumesReadCredit`, `TestGrepSkipsIgnoredFilesWithDisclosure`, `TestGrepFromExcludedDirCannotEscape`, `TestCheckReadRefusesInAskAndDenyAndPlan`, `TestCheckReadAllowReadsOverride`, `TestCheckEditRefusesInAllModes`, `TestIsPathExcluded`, `TestPrefixDirectoryInheritance` |
| A bash cleanup signal cannot target a recycled process-group ID after the leader has been reaped | GUARANTEED | `killGroup` runs only on timeout/cancellation before `Wait` returns; clean completion does not perform a post-reap group sweep. Evidence: `TestBashCleanExitDoesNotKillProcessGroup` |
| Read-credit hashes and renders one bounded snapshot, and uses one normalized relative key for read/write matching | GUARANTEED | `TestReadCreditCompositeKeyStructure`, `TestReadFileCreditPathForAbsoluteInside`, `TestReadFileUsesDescriptorStat`, `TestReadSinglePassSourceShape`, `TestReadCreditHashMatchesModelVisibleSource`, and `TestReadCreditValidMatchingCycle`; files over the bounded snapshot limit are refused; the descriptor-stat guard is AST-based (call sites, not text) |
| Semantic loop detection bounds repeating identical tool executions | GUARANTEED | Tool calls are fingerprinted by `(tool, hash(input), hash(output-error))` across turns in a run; identical calls trigger a conversational notice at 3 repeats and a hard cut with `ErrToolLoop` at 5 repeats, preventing token and turn exhaustion from looping models. Evidence: `TestToolLoopNoticeAtThreeRepeats`, `TestToolLoopHardCutAtFiveRepeats`, `TestToolLoopCanonicalJSONKeyOrdering`, `TestToolLoopResetAcrossRuns` |
| Provider registry schema rejects undeclared dialects, unknown fields, and literal credential keys | GUARANTEED | `TestRegistryRejectsUnknownDialect`, `TestRegistryRejectsUnknownFields`, `TestRegistryRejectsLiteralKeyInProvidersFile`, `TestModelIDAliasIsSentToProvider` |
| Provider registry auth file enforces private 0600 permissions, owner check, and redaction in errors | GUARANTEED | `TestAuthFileRejectsOpenPermissions`, `TestAuthKeysAreRedactedInErrors` |
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
| Provider dial-time endpoint guard evaluates the socket address via Dialer.Control immediately before connect, eliminating the TOCTOU rebinding window | GUARANTEED | `Policy.Dialer` configures `net.Dialer.Control` to inspect the literal socket IP address right before `connect(2)`. No secondary DNS lookup is performed, closing the DNS-rebinding window with zero TOCTOU: a hostname resolving to a private, loopback, link-local, CGNAT, ULA, 6to4, or metadata IP is aborted before any packet is transmitted unless permitted by `NABD_ENDPOINT_ALLOW`. Under `PolicyLoopback`, dial-time checks continue to refuse cloud metadata, link-local, CGNAT, and 6to4 addresses. Evidence: `TestControlRefusesRebindingViaResolver`, `TestControlRefusesRebindingToPrivateAddress`, `TestControlRefusesLoopbackAddress`, `TestControlRefusesCloudMetadata`, `TestControlRefusesCGNATAddress`, `TestControlRefusesIPv6ULAAddress`, `TestControlRefuses6to4Address`, `TestControlPassesThroughPublicAddress`, `TestControlPermitsAllowedAddr`, `TestControlUnderLoopback` |
| No provider can be constructed with an unguarded HTTP client | GUARANTEED | Every provider constructor (`NewOpenAIDialect`, `NewAnthropicDialect`) defaults to `endpoint.Client(0)` governed by the active endpoint policy; omitting explicit client injection is fail-closed, and direct or route constructors cannot produce an unguarded client. When an environment proxy (`HTTPS_PROXY` / `HTTP_PROXY`) is configured, the proxy URL is validated against the endpoint policy at request time. Evidence: `TestConstructorDefaultsToGuardedClient`, `scripts/check-security-invariants.sh`, `TestTransportProxyValidation`, `TestCustomEndpointIsAcceptedByDesign` |
| Local runtimes permit loopback HTTP under PolicyLoopback and explicit allowlisting via NABD_ENDPOINT_ALLOW under PolicyStrict | GUARANTEED | `endpoint.CheckBaseURL` permits plaintext http strictly for loopback destinations (`127.0.0.1`, `::1`, `localhost`) under `NABD_ENDPOINT_POLICY=loopback` to support local runtimes like Ollama without TLS, while remote destinations still require https and cloud metadata is refused at load and dial time. Alternatively, `NABD_ENDPOINT_ALLOW` permits designated local or private endpoints under `PolicyStrict` while keeping full connect-time protection active for all other providers. Evidence: `TestLoopbackStillRefusesHTTP`, `TestLoopbackAllowsLoopbackAndPrivate`, `TestLoopbackAllowsLoopbackHTTP`, `TestLoopbackRefusesCloudMetadata`, `TestAllowListStrictAcceptsAllowedHTTP`, `TestAllowListStrictAcceptsAllowedPrivate` |
| `NABD_ENDPOINT_POLICY=open` disables both the load-time URL check and the connect-time dial guard | REDUCED | When an operator sets `NABD_ENDPOINT_POLICY=open`, `CheckBaseURL` returns nil for any URL and `Dialer` returns the dialer unchanged without registering a `Control` hook. This is the override for air-gapped or development environments that cannot use HTTPS. Whoever can set this variable can direct all connections to an arbitrary endpoint including plaintext and private addresses. The remaining controls are the 0600 permission and owner check on `providers.json` and `auth.json`. Evidence: `TestOpenLevelDisablesBothLayers`, `TestControlIsNoOpUnderOpenPolicy` |

| OpenAI wire encoding preserves pre-compat baseline structure by default | GUARANTEED | `OpenAICompat.encode` pins the baseline request structure: standard `max_tokens`, `stream_options.include_usage=true`, and canonical `system` → `tool` → `user` message ordering; zero values of future endpoint compat flags preserve this body. Evidence: `TestOpenAIEncodingBaseline` |
| Skill bodies entering model projection are explicitly classified, user-role, fenced, and fail-closed | REDUCED | `TestSkillBodyEntersTheModelAsUntrustedProjectContent` proves an explicitly classified body is labeled as untrusted project content inside the existing tool fence (`UNTRUSTED_DATA NOT_INSTRUCTIONS`), and that a body whose class is not the explicit untrusted value is dropped rather than projected. The class is fixed by the guarded producer and is never inferred from the body text, the skill name, or its path, so a definition cannot label itself trusted by what it says. Session wiring is active: the user scope loads unconditionally, and the project scope only when `NABD_SKILLS_PROJECT=1` is supplied through user-scoped configuration or environment. This narrows and labels injectable content but does not immunize the model from permitted content. |
| Skills are progressive-disclosure, project scope is off unless an operator opts in, and every body is re-hashed before it is served | REDUCED | Only name, description and scope reach the prompt; the body is read on invocation. Containment is who may write into each scope plus the recorded SHA-256, not what the body says. The complete binary tool vocabulary is independent of the session's active registry, so continuation can fence historical `skill` calls while an empty skill index leaves the tool inactive. Evidence: `TestProjectSkillsAreAbsentWhenOptInIsOff`, `TestProjectSkillSymlinkEscapeIsRefused`, `TestJournalRecordsHashMatchesLoadedBytes`, `TestSkillToolRefusesBodyChangedSinceLoad`, `TestSkillRegistrationFollowsLoadedIndex`, `TestFenceRejectsUnknownName` |
| Bash filesystem reach after approval | NOT PROVIDED | On Termux, approved bash runs with the full authority of the Termux app user; there is no filesystem sandbox. |

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

## Custom endpoints are accepted by design

`~/.ag/providers.json` accepts any `options.baseURL`: `https` or `http`, public
host, loopback, RFC 1918, link-local, or a `.internal`/`.local` suffix. This
section previously listed five admission conditions that a future custom-endpoint
implementation was required to satisfy — HTTPS only, globally routable hosts only,
no embedded credentials, no query or fragment, and cross-host redirect
containment. **All five are withdrawn as of v10.** None of them was ever
implemented, and the provider registry already accepts arbitrary endpoints, so
keeping them here claimed a guard the binary does not have.

The decision behind the withdrawal is one door: adding a provider or a model is a
JSON edit, with no code change, no rebuild, and no dialect plugin. That is what
makes a local runtime at `http://127.0.0.1:11434/v1` and a private enterprise
gateway usable at all, and a rule that rejects plaintext loopback would reject the
most common legitimate case first.

What is given up, stated plainly:

- A hostile or mistaken `providers.json` redirects every prompt, file excerpt, and
  tool result to an endpoint of its choosing.
- That endpoint receives the provider key in the `authorization` header on every
  request, in cleartext if the URL is `http`.
- That endpoint controls the response stream, so it can emit `tool_calls` the
  model never proposed; those calls still pass the permission gate, which is the
  boundary that remains.
- A host that resolves into private or link-local space (including a cloud
  metadata address) is reachable, so the binary can be used as an SSRF pivot by
  whoever writes that file.

What still holds: both registry files must be regular, owned by the current user,
and mode 0600, verified from the opened descriptor; only two dialects exist and
neither is loaded from a package at request time; keys are never written into a
`bash` child environment and pass through `internal/redact` before any log or
display; and every tool call the endpoint induces is still classified and
prompted by the permission layer.

Minimal strict config v2 continues to reject `base_url`, and that asymmetry is
deliberate: config v2 is the locked-down schema for an operator who wants no
custom endpoint at all, while the registry is the open door for one who does.
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

## Operator guidance

1. Prefer secure credential files over shell startup exports.
2. Run nabd as a dedicated OS user on shared systems.
3. Use a disposable clone rather than a tree containing production secrets.
4. Treat every `bash` approval as authority equivalent to the current user.
5. Assume `session.jsonl` and `.ag/shadow` contain sensitive cleartext.
   Default journal redaction reduces recognized credentials but does not make
   the journal safe: it does not redact the shadow store, structural fields, or
   unrecognized sensitive content. Avoid `NABD_REDACT_JOURNAL=0` except for a
   deliberate diagnostic run.
6. Do not attach raw journals to public issues.
7. Redact before sharing: `nabd --export <file.jsonl> --redact`. Raw
   `--export` is a deliberate, byte-for-byte copy and warns on stderr.

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

**Arguments are decoded strictly before anything runs.** `bash` arguments are
decoded with `decodeStrict` — the decoder `write_file` already used — instead of
`json.Unmarshal`, which keeps the last value of a duplicate key and ignores
undeclared ones. Repair drops undeclared keys and records them before
classification; the strict decode rejects any undeclared or duplicate key before
a subprocess starts. The first keeps a stray key from costing a round, the second
keeps the guarantee from depending on the first. A key may be dropped only where
it is inert: for `bash` the entire action is the `cmd` string, displayed verbatim
in the permission prompt, so no other key can change what executes, whereas for
`write_file` and `edit_file` an undeclared key may be a live field of the other
tool — dropping `old` there turns a local edit into a whole-file replacement — so
those calls stay rejected rather than narrowed (NBD-010). Where the layer
declines — a duplicate key, or more corrections than it will make — the call still
fails as `invalid args`, starts nothing, and hands the model a reason. Decoding is
validation only: it grants no authority, and `bash` remains the one tool that
cannot receive a session-wide grant. Evidence:
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

OSC 52 copy operates only on projected card content after recognized
credential redaction and display sanitization. Raw journal bytes and raw
error bodies are not clipboard sources. Unrecognized sensitive text
remains a residual risk.
Evidence: `TestCopyRedactsRecognizedCredentials`, `TestCopyNeverUsesRawJournalContent`, `TestCopyRejectsRawErrorBodies`, `TestCopyIsBlockedByPermissionModal`, `TestCopyNeverExecutesACommand`.

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

**Strict JSON decoding before resolution or descriptor opening.** Arguments are unmarshaled via `decodeStrict`, which enforces two defenses before any filesystem operation:
1. Duplicate key rejection: `rejectDuplicateKeys` tokenizes the raw JSON input stream and compares the number of discovered keys against the unmarshaled key map. Any duplicate key causes an immediate rejection (`invalid args: duplicate key in request`), defeating JSON parameter smuggling attacks where decoders disagree on precedence.
2. Undeclared field rejection: `DisallowUnknownFields` forbids any JSON fields not declared on the target argument structure unless safely dropped into `DroppedArgs` by the read-only repair layer.

**Boundary behind repair.** The read-only repair layer allows dropping inert undeclared fields for ReadOnly tools to prevent wasted conversational rounds, recording discarded keys in `DroppedArgs`. However, duplicate keys are declined by the repair layer and always fail strictly through `decodeStrict`, preventing argument injection or smuggling prior to path resolution or opening descriptors.

Evidence: `TestReadFileStrictArgs`.

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
