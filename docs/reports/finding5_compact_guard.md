# Finding 5 — `/compact` history-mutation interlock

## Scope

The existing stale-boundary validation remains defense at the append boundary. This finding closes the structural command overlap that made the stale state reachable: manual compaction can spend a provider request while a concurrent rewind changes the live branch, and duplicate manual compactions can run simultaneously.

## Contract

- `Loop.Compact` acquires the history-mutation interlock before reading a boundary and holds it through final append or failure.
- `Loop.Rewind` acquires the same interlock through boundary selection and rewind append.
- The interlock is non-blocking: a conflicting operation returns `ErrHistoryMutationInProgress` immediately.
- Ordinary turn/event appends remain permitted during summarisation; the existing fresh-boundary and raw-pairing validation still decides whether the final compact projection is safe.
- Every return path releases the interlock.
- A rejected operation appends no history event and makes no provider request.

## Evidence

`TestCompactInterlockRejectsConcurrentHistoryMutation` blocks the real compact summarisation path deterministically, then proves that both a concurrent rewind and duplicate compact are rejected without changing history. After release, the original compact succeeds and rewind works, proving cleanup.

`TestCompactInterlockReleasesAfterEarlyFailure` proves that a compact rejected before provider work does not leave the session permanently locked.

The pre-existing `TestCompactBoundaryValidationRace` remains as a lower-level safety net for stale boundaries and synthetic raw-pairing violations.
