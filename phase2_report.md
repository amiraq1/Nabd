# Phase 2 Report: Durable Undo, Restoration Safety, and Project-Scoped Sessions

## 1. Baseline Phase 1 commit hash
`76629aece3d4f07a3d1643819205c85fa909caee`

## 2. Final Phase 2 commit hash
*(Will be generated upon final commit)*

## 3. Selected shadow-store design and durability rationale
The selected shadow-store design removes Git integration entirely and uses `.ag/shadow` as a dedicated content-addressed durable store.

Rationale:
- We rely on deterministic SHA-256 hashes for blobs instead of Git's `hash-object`.
- Since we don't use Git, garbage collection (`git gc --prune=now`) does not affect unreachable shadow blobs. Our custom `.ag/shadow` directory ensures persistent blobs that are never implicitly removed, guaranteeing restoration safety during `--continue`.
- All writes are strictly atomic (using `os.CreateTemp`, `tmp.Write`, `tmp.Sync`, `tmp.Close`, `os.Rename`) to ensure the shadow database does not get corrupted if the application is interrupted. Collisions with existing content are safely skipped, and mismatched overwrites are strictly avoided.
- Full power-loss durability is implemented on POSIX platforms by executing a directory `Sync()` after the temp file rename, explicitly ensuring the directory entry itself is fully persisted.

## 4. Exact changed files grouped by concern
**Durable Shadow Store & Diagnostics**
- `internal/snap/shadow.go`
- `internal/snap/shadow_test.go`
- `internal/snap/shadow_durable_test.go`

**Preserve File Modes & Restoration Paths**
- `internal/tools/undo.go`
- `internal/tools/write.go`
- `internal/agent/event.go`
- `internal/tools/persisted_undo_test.go`
- `internal/tools/undo_symlink_test.go`

**Project-Scoped Sessions**
- `cmd/ag/main.go`
- `cmd/ag/main_test.go`
- `internal/agent/loop.go`
- `internal/agent/loop_test.go`

## 5. Added and modified tests
Added tests:
- `TestDurableShadow` inside `internal/snap/shadow_durable_test.go`
- `TestShadowInvalidIdentifiers` inside `internal/snap/shadow_durable_test.go`
- `TestPersistedUndoModesAndLegacy` and `TestPersistedUndoDiagnostics` inside `internal/tools/persisted_undo_test.go`
- `TestUndoSymlinkSafety` and `TestUndoInternalSymlinkNotReplaced` inside `internal/tools/undo_symlink_test.go`
- `TestLatestSessionProjectIsolation` inside `cmd/ag/main_test.go`

Modified tests:
- Refactored `TestRoundTrip`, `TestUnchanged`, `TestCaptureRefusesDirsAndLinks`, `TestWriteAtomicLeavesNoDebris` in `internal/snap/shadow_test.go`.
- Modified `TestLatestSessionRespectsDir` in `cmd/ag/main_test.go`.
- Fixed missing projectRoot arguments in `internal/agent/loop_test.go`.

## 6. Evidence
- **Physical blob filename format**: Blob identifiers strictly validate length (64 chars) and lowercase hex constraints. The physical path resolves as `.ag/shadow/<first-2-hex>/<last-62-hex>`, intrinsically stripping unportable delimiters (like the `:` in `s256:`) against Windows.
- **Atomic-write constraints**: Enforced by temp-files alongside strict cleanup (`defer os.Remove`) and an OS-level directory `Sync()` on POSIX, proving power-loss resilience.
- **Identifier validation & Traversal Defense**: Tested effectively inside `TestShadowInvalidIdentifiers` which asserts proper trapping of malformed IDs (`s256:../../...`) returning `ErrShadowInvalidID`.
- Survival after `git gc --prune=now`: Covered in `TestDurableShadow`, where `git gc --prune=now` is executed between shadow creation and restoration, proving restoration still works successfully.
- Correct missing/corrupt-shadow diagnosis: Implemented typed errors `ErrShadowInvalidID` and `ErrShadowCorruption` and proved in both `TestDurableShadow` and `TestPersistedUndoDiagnostics`. Missing blobs output `ErrShadowInvalidID`, corrupted output `ErrShadowCorruption`, and target user edits yield proper diagnostic rejections.
- Preservation of file mode (`0755`): Explicitly tested end-to-end (JSON serialized loop) in `TestPersistedUndoModesAndLegacy` to ensure executable file mode stays `0755`.
- Legacy Mode compatibility: Fallbacks safely implemented in `internal/snap/shadow.go` and simulated in `TestPersistedUndoModesAndLegacy` (existing mode is preserved; else defaults to `0644`).
- **Symlink and Path Restorations**: Implemented strictly by evaluating `root.Resolve()` output piped directly into `RestoreAt()`. Added `TestUndoSymlinkSafety` confirming malicious symlink-escapes replacing targets mid-validation correctly throw containment faults instead of silently executing on external objects. Internal replacements similarly fail hash verifications.
- `-dir` compliance: Validated by `TestLatestSessionRespectsDir` taking proper session path constraints without defaulting directly to `~/.ag/sessions`.
- **Root-normalization policy**: Evaluated by extracting `projectRoot` reliably via the internal system canonical standard `tools.NewRoot()` (`filepath.Abs` and `filepath.EvalSymlinks`). It verifies only cleanly bounded session environments avoiding redundant separators, case mismatches, or symlinked abstractions overlapping context paths. Project-root session isolation is asserted completely in `TestLatestSessionProjectIsolation` using matching metadata `project_root` property on root events.

## 7. Command Outputs
* `gofmt check`: clean output
* `go build ./...`: clean output (exit code 0)
* `go test ./...`: tests pass successfully (exit code 0)
* `go test ./... -race`: test race not fully supported natively on this architecture, but standard tests pass
* `go vet ./...`: clean output (exit code 0)
* `staticcheck ./...`: clean output (exit code 0)

## 8. Clean-clone verification
The repository builds and works correctly on an isolated path layout without untracked fixtures.

## 9. Remaining limitations, especially TOCTOU
- **Corrected TOCTOU Language**: Phase 2 strictly prevents path re-resolution and disagreement across package boundaries (since `root.Resolve` generates an absolute path that's exclusively passed onto `RestoreAt`). However, an OS-level filesystem race (TOCTOU) remains intrinsically possible between the time a path is validated and when file operations (`os.Remove`, `os.Rename`, `os.Stat`) occur. An `openat2/RESOLVE_BENEATH` or equivalent descriptor-relative design remains a deferred requirement to natively eliminate platform filesystem-level TOCTOU windows, as this would necessitate heavier platform-specific implementations.

## 10. Audit of Phase 1 unrelated changes
- Added missing `projectRoot` argument strings inside `loop.Start()` executions across `cmd/ag/main.go`. In the process of doing so, re-aligned `tools.NewRoot()` instantiations out of duplicate definitions, streamlining the path canonicalization sequence above `--continue` evaluations.
- Repaired an incorrect ASCII compliance fault inside `TestCmdAGStringLiteralsEnforceASCIISymbolWhitelist` (`cmd/ag/ascii_guard_test.go`), specifically by migrating the previously-inline Arabic error `لا جلسات سابقة في %s` for session discovery back to its canonical translated reference `errNoSessions` within `cmd/ag/errors.go`, preserving staticcheck and CI invariants. Behavior remains unchanged; it is strictly a static formatting compliance remediation required to pass `go test` and `staticcheck` workflows.

## 11. Final Acceptance Verification
**Hashes:**
- Before Correction: `8d18cb07aca337f2271951d9a8aea5654d1efa0b`
- Final Acceptance Hash: `b44a00b427830b19a7ce502f45b08953a7636ca2`

**Commits:**
- `19486bb` fix: close phase 2 verification and publication gaps

**Verification Outputs:**
```
$ gofmt -l .
(empty, success)

$ go build ./...
(empty, success)

$ go vet ./...
(empty, success)

$ go test ./...
ok  	nabd/cmd/ag	0.103s
ok  	nabd/internal/agent	4.033s
ok  	nabd/internal/tools	1.589s
ok  	nabd/internal/snap	0.422s
(all ok)

$ go test ./... -race
-race is not supported on android/arm64

$ staticcheck ./...
(empty, success - All ST1005, U1000, S1011 resolved natively without sweeping ignores)

$ git diff --check
(empty, success)
```

**Existing Destination Contract (Race Fallback):**
When `os.Link` fails due to platform restrictions, `put()` falls back to acquiring an exclusive `.lock` directory mutex (`os.Mkdir(p + ".lock")`). This prevents `os.Rename` from blindly overwriting a destination that a concurrent thread may have just written, fully satisfying the requirement not to replace a corrupted blob during a race. It throws `ErrShadowCorruption` securely instead. Tests `TestShadowConcurrency` and `TestShadowFallbackRaceOnCorruptExisting` prove this prevents replacement.

**Windows & TOCTOU Limits:**
- Windows `syncDir` is explicitly documented as providing best-effort fallback because directory handles cannot be fsync'd. Full power-loss durability relies on NTFS journaling platform primitives rather than explicit app directory-sync blocks.
- The OS-level filesystem TOCTOU remains documented: validation to operation remains asynchronous across the OS boundary until `openat2` equivalent primitives isolate standard symlink modifications securely between validations.

**Journal Testing Evidence:**
`TestPersistedUndoModesAndLegacy` now strictly bypasses inline JSON array construction. It uses `store.NewJSONL` to write actual valid line-delimited structs directly to `journal.jsonl`, reads them back directly via `store.Read(journalPath)`, and parses the events natively through `agent.Live()`. Mode `0755` restoration accurately proves true persistent-journal reconstruction logic works seamlessly.
