# Contributing to nabd

## Workflow

1. Open a focused pull request against `master`.
2. Complete every item in the required security checklist.
3. Run formatting, vet, tests, race tests, module tidiness, and vulnerability checks.
4. Never include credentials or raw session journals in issues, commits, fixtures, or logs.

## Event contract

`session.jsonl` is an append-only event journal and a compatibility contract.
Existing lines are never rewritten or deleted. Rewind and compaction append new
events that select a branch or boundary. Changes to event fields, reconstruction,
or persistence require regression coverage for live rendering, replay, resume,
and historical compatibility.

## Security-relevant changes

Changes to path handling, permissions, shell execution, configuration,
credentials, journal/shadow storage, or release artifacts must update
`docs/THREAT_MODEL.md` whenever a claim or residual risk changes.

## Language

Code comments, identifiers, commit messages, and contributor documentation are
English. User-facing Arabic strings belong in the command/UI error catalog
(`cmd/ag/errors.go`) rather than being scattered through implementation files.
The string-literal contract test enforces the corresponding source policy.
