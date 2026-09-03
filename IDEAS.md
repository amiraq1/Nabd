# Ideas and Future Work

- ~~**Secure API Key Storage**~~ — done: `internal/config` reads `~/.ag/config`
  (or `NABD_CONFIG`) read-only, refuses files with mode wider than `0600`, and
  never writes a byte. Environment variables remain a fallback.
  Remaining follow-up: `scrubEnv` still filters the environment by suffix; once
  everyone has migrated, consider passing an *empty* credential set explicitly
  instead of filtering.
- **TOCTOU**: `openat2(RESOLVE_BENEATH)` on Linux for the path layer.
- **Multi-process undo**: the pending-edit log lives in process memory; two
  `ag` instances in one repo cannot see each other's edits.
