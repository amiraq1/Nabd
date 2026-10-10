# MCP Phase S Measurement Report

**Status:** template — fill after running on Termux
**ADR:** docs/DECISIONS/0003-mcp-integration.md (v4)

## Environment

| Field | Value |
|---|---|
| Device | [e.g. Xiaomi 23078PND5G] |
| Termux version | [e.g. 0.119.0-beta.3] |
| Android version | [e.g. 16] |
| Go version | [`go version`] |
| Git HEAD | [`git rev-parse HEAD`] |
| Date | [YYYY-MM-DD] |

## S1: Process-group kill

| Test | Result | Notes |
|---|---|---|
| S1a: killGroup, no orphans | [PASS/FAIL] | remaining=N |
| S1b: pipe holder, no hang | [PASS/FAIL] | |

**Go/No-Go:** if S1a fails, B3 and the cancellation approach must be revised before Phase 4.

## S2: Pipes and timeouts under load

| Test | Result | Notes |
|---|---|---|
| S2a: 64 MiB discard | [PASS/FAIL] | heap delta=N bytes |
| S2b: timeout cleanup | [PASS/FAIL] | goroutines before=N after=M |
| S2c: write-no-read | [PASS/FAIL] | |

**Go/No-Go:** failures here revise size/timeout limits.

## S3: Env allowlist

Paste the table from `bash scripts/mcp/s3-env-allowlist.sh`:

| server | list A | list B | list C |
|---|---|---|---|
| node | | | |
| python | | | |

**Recommendation:** [which list, or "needs standalone decision"]
**Go/No-Go:** if the restricted list breaks Node/Python, reformulate as a standalone decision.

## S4: SDK vs stdlib

Paste from `bash scripts/mcp/s4-sdk-size.sh`:

- SDK deps count:
- SDK graph edges:
- Stdlib scaffold deps count:
- **Decision:** [SDK @ pinned tag | stdlib scaffold] with rationale

**Go/No-Go:** the choice is recorded in ADR §4.5.

## Raw output

[Paste the full test/script output below for the record]
