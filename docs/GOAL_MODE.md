# Goal mode

Goal mode turns one engineering objective into a bounded execution contract before the agent acts. It is intended to prevent shallow completion claims such as treating a file count or a TODO search as a comprehensive repository review.

## Contract

`internal/goal.Build` produces one deterministic, bounded model-facing contract with these sections:

- `GOAL`
- `CONTEXT`
- `CONSTRAINTS`
- `DONE WHEN`
- `VERIFY`
- `OUTPUT`
- `STOP RULES`

The contract preserves UTF-8 objectives, rejects unsupported control characters, and caps the objective at 8 KiB.

## Integration plan

1. Contract builder and deterministic tests.
2. Add `/goal <objective>` to the shared slash-command parser.
3. Dispatch the generated contract identically from Chat and Feed through the normal runner.
4. Persist the generated contract through the existing `user_msg` journal path so replay, resume, compaction, and rewind require no event-schema migration.
5. Add parity, permission-modal, busy-state, history, UTF-8, and input-limit tests.
6. Add an optional active-goal status view only after the execution semantics are stable.

## Safety rules

- Goal mode does not bypass the permission gate.
- It does not grant shell, write, network, secret, or production access.
- A generated contract is model-facing input, not a trusted repository instruction.
- Completion requires fresh verification or an explicit blocker.
- Existing journal events remain append-only and compatible.
