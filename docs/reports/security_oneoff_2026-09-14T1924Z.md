# One-off security scan — master (2026-09-14T1924Z)

Scope: single-pass SAST + secret detection + dependency vulnerability scan on
the tip of master (`8ab0264`, UI/p8 responsive chrome (#98)).

## 1. Dependency / known-CVE scan (govulncheck ./...)
- Result: **No vulnerabilities found.**
- Tool: golang.org/x/vuln/cmd/govulncheck (matches CI step).

## 2. Secret detection
- Method: regex scan for common credential shapes across *.go/*.md/*.yml/*.yaml/*.sh,
  excluding third_party and test fixtures.
- Result: **No live secrets.** All hits were synthetic test fixtures
  (e.g. `ghp_12345678901234567890`, `glpat-...`, `xoxb-...`) or a documentation
  example under .commandcode/taste. No real tokens, private keys, or AWS/GCP keys found.

## 3. Static analysis (SAST) — staticcheck ./...
- Result: **clean** (no SA/ST/U-level findings on non-test code paths).
- Note: staticcheck is the same linter gated in CI; this pass mirrors it manually.

## Conclusion
No blocking issues. Master is clean for a one-off review. This file is a dated
snapshot; it is not a substitute for the gated CI pipeline (govulncheck,
check-exec-env, check-threat-model-tests, CodeQL) which runs on every push.
