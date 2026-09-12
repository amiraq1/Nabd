# Configuration Guide

Nabd supports user-scoped configuration to manage providers, routing, models, operational limits, and API credentials securely.

Configuration files are located strictly in the user's home directory (`~/.ag/`) or specified via explicit user-scoped environment overrides. **Project working directories are never consulted**—a repository checked out by the user can never inject or override provider keys, endpoints, or models.

---

## 1. Architectural Decisions

### Format Selection: JSON vs. TOML

Config v2 uses standard JSON (`~/.ag/config.v2.json`) decoded via Go's standard library `encoding/json`. This differs from earlier design sketches that contemplated TOML.

**Rationale:**
1. **Zero External Dependencies:** Go has native, robust JSON decoding in its standard library (`encoding/json`), featuring `Decoder.DisallowUnknownFields()`. Supporting TOML in Go requires third-party libraries (e.g. `BurntSushi/toml` or `pelletier/go-toml`). Nabd enforces a strict zero-dependency policy for security-critical configuration parsers to eliminate supply-chain vulnerability attack surface.
2. **Strict Schema Validation:** `dec.DisallowUnknownFields()` and trailing stream detection (`dec.Decode(&struct{}{}) == io.EOF`) allow deterministic, fail-closed parsing. Any misspelled key, unsupported attribute, or trailing payload causes an immediate abort before any network or tool execution.

**Trade-offs and Mitigations:**
- **No Inline Comments:** Standard JSON does not support comments (`//` or `#`). Operators cannot annotate entries or temporarily comment out routes inline.
- **Mitigation:** Nabd provides precise parse error diagnostics identifying unexpected fields and syntax errors. Complete schema examples and documentation are provided below.

---

## 2. Version Coexistence and Mutual Exclusivity

Nabd defines two configuration formats:
- **v1**: Flat `KEY=VALUE` shell-style syntax (`~/.ag/config` or `NABD_CONFIG`).
- **v2**: Strict JSON document (`~/.ag/config.v2.json` or `NABD_CONFIG_V2`).

### The Fatal Coexistence Rule
Having both default configuration files present simultaneously on disk:
- `~/.ag/config` (v1) **AND**
- `~/.ag/config.v2.json` (v2)

is a **fatal error at startup**. Nabd refuses to start with:
```
config v1 and config v2 files cannot coexist
```
Similarly, specifying both `NABD_CONFIG` and `NABD_CONFIG_V2` environment variables simultaneously is fatal:
```
config v1 and config v2 cannot be selected together
```

**Why this rule exists:**
Ambiguity in which configuration file is active presents a serious security risk. For instance, an operator might edit `~/.ag/config.v2.json` to disable a route or tighten limits, while an unremoved `~/.ag/config` silently remains active (or vice versa). By failing closed at startup, Nabd ensures the operator is aware of the exact configuration file being evaluated.

**Migration:**
To migrate from v1 to v2, create `~/.ag/config.v2.json` and remove or archive `~/.ag/config`.

---

## 3. Security Invariants and Hardening

All configuration and credential files read by Nabd must satisfy strict OS-level security invariants before their contents are processed:

1. **Permissions (`0600`):**
   File permissions must not expose read or write bits to group or others (`mode & 0o077 != 0`). Files with loose permissions (such as `0644` or `0664`) are rejected with an actionable error:
   ```
   permissions 0644 are open to others; run chmod 600 <path>
   ```
2. **Regular File Only:**
   The file must be a regular file (`fi.Mode().IsRegular()`). Directories, named pipes (FIFOs), sockets, and character or block devices are rejected.
3. **Symlink Rejection:**
   Files are opened with `unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK`. If any component or the target file itself is a symlink, the open fails immediately at the kernel syscall boundary with `ELOOP` (`too many levels of symbolic links`).
4. **Ownership Discipline:**
   On Unix systems, the file must be owned by the user running the process (`stat.Uid == os.Getuid()`). A file owned by another user cannot be protected and is rejected.
   - **Containers and Root:**
     * If the process runs as `root` (UID 0), files owned by UID 0 are accepted.
     * If the process runs as an unprivileged user (e.g. UID 1000), files owned by root or other users are refused. The container or operator must ensure the mounted file is owned by the process UID (`chown $(id -u) <file>`).
   - On Windows, numeric UID checks are a documented no-op (`owner_other.go`), with security delegated to NTFS ACLs.
5. **Size Bounds:**
   Configuration documents cannot exceed 256 KB (`MaxFileBytes`). Credential secret files cannot exceed 64 KB (`MaxValueBytes`).

---

## 4. Config v2 Schema Reference

A minimal valid `config.v2.json` file:

```json
{
  "version": 2,
  "provider": "groq",
  "model": "openai/gpt-oss-120b",
  "limits": {
    "context": 32000,
    "max_tokens": 4096,
    "max_tokens_per_run": 100000,
    "max_read": 131072
  },
  "credentials": {
    "groq": {
      "source": "env"
    }
  }
}
```

### Schema Fields

| Field | Type | Required | Description |
|---|---|---|---|
| `version` | `int` | Yes | Must be strictly `2`. |
| `provider` | `string` | Yes | One of `"anthropic"`, `"groq"`, `"openrouter"`, `"nvidia"`, or `"router"`. |
| `model` | `string` | Optional | Model identifier (e.g. `"claude-3-7-sonnet-20250219"`). Forbidden when `provider="router"`. |
| `base_url` | `string` | No | **Forbidden in minimal strict v2.** Specifying `base_url` causes a fatal validation error. |
| `routes` | `[]Route` | For router | Required when `provider="router"`. Maximum 32 routes. Each item: `{"provider": "...", "model": "..."}`. |
| `router_mode` | `string` | For router | Must be `"fallback"`. |
| `router_prestream_timeout` | `string` | Optional | Timeout before attempting the next route (e.g. `"15s"`). |
| `provider_turn_timeout` | `string` | Optional | Overall turn timeout (e.g. `"60s"`). |
| `limits` | `Limits` | Optional | Operational constraints (see below). |
| `credentials` | `map[string]Cred` | Yes | Declares credential source for each active provider. |

### Operational Limits (`limits`)

- `context` (int): Context window size in tokens (must be > 8000).
- `max_tokens` (int): Maximum output tokens per provider turn (in `[128, 8192]`).
- `max_tokens_per_run` (int): Budget limit per session run (> 0).
- `max_read` (int): Maximum file read limit in bytes (in `[1, 262144]`).

---

## 5. Credential Separation in v2

Config v2 introduces explicit credential declarations, completely disabling implicit environment variable leakage:

### Implicit Environment Fallback Disabled
In Config v1, any missing key fell back to `os.Getenv(key)`. In Config v2, **implicit fallback is disabled**:
- An environment variable like `GROQ_API_KEY` is only consulted if explicitly declared in `credentials.groq.source = "env"`.
- Any undeclared environment variable is ignored and will never reach the provider client.
- Every provider referenced by `provider` or `routes` must have a matching entry in `credentials`.

### Credential Sources

1. **Environment Source (`"source": "env"`):**
   ```json
   "credentials": {
     "anthropic": {
       "source": "env"
     }
   }
   ```
   Reads from the provider's standard environment variable (`ANTHROPIC_API_KEY`, `GROQ_API_KEY`, `OPENROUTER_API_KEY`, `NVIDIA_API_KEY`). The `path` attribute must be omitted.

2. **File Source (`"source": "file"`):**
   ```json
   "credentials": {
     "openrouter": {
       "source": "file",
       "path": "~/.ag/credentials/openrouter-main"
     }
   }
   ```
   - `path`: Accepts absolute paths (e.g. `/home/alice/.ag/credentials/openrouter-main`) or user-home relative paths starting with `~/` (e.g. `~/.ag/credentials/openrouter-main`). Relative paths without `~/` are rejected.
   - Target credential file must be a regular file, mode `0600`, owned by the user, non-symlink, and contain exactly one non-empty line with the secret key.
   - Whitespace is trimmed, but newline (`\n`) or carriage return (`\r`) inside the secret causes rejection.

3. **Command Source (`"source": "command"`):**
   Strictly forbidden in minimal v2. Execution of external commands to resolve credentials is not permitted.

---

## 6. Multi-Provider Router Example

```json
{
  "version": 2,
  "provider": "router",
  "router_mode": "fallback",
  "router_prestream_timeout": "10s",
  "routes": [
    {
      "provider": "groq",
      "model": "openai/gpt-oss-120b"
    },
    {
      "provider": "nvidia",
      "model": "deepseek-ai/deepseek-v4-pro-0813"
    }
  ],
  "credentials": {
    "groq": {
      "source": "file",
      "path": "~/.ag/credentials/groq-key"
    },
    "nvidia": {
      "source": "env"
    }
  }
}
```
In this example:
- The router prioritizes Groq with a 10s prestream timeout before falling back to NVIDIA.
- Groq's key is read securely from `~/.ag/credentials/groq-key` (mode 0600).
- NVIDIA's key is sourced from the `NVIDIA_API_KEY` environment variable.
- Any other environment variables are disregarded.

---

## 7. Journal Redaction and Export (operational)

`NABD_REDACT_JOURNAL` is a process-level **runtime environment variable**. It is
**not** a Config v1 key and **not** a Config v2 field: it is never read from
`~/.ag/config` or `~/.ag/config.v2.json`, and setting it in either file has no
effect.

### Activation

Redaction is enabled only by the exact literal value `1`:

```sh
NABD_REDACT_JOURNAL=1 nabd
```

Every other spelling leaves the default raw-journal behavior in place,
including `true`, `yes`, `on`, and `" 1 "` (with surrounding whitespace).

### What it does

- Redacts recognized credential patterns in **newly appended events** before
  they are written, ahead of `Event.ForStore()` and output truncation.
- Does **not** rewrite existing journal lines. With `--continue`, only events
  appended during the resumed session are redacted; historical lines keep their
  original bytes.
- Does **not** mutate the live event held in memory: redaction is copy-on-write,
  so the in-memory history, replay, and undo are unaffected.
- Applies to the JSONL emitted by `--json`, so stdout and the journal follow the
  same policy and cannot diverge.

### Redacted fields

Recognized patterns are removed from:
- conversation text and notices (`Event.Text`);
- errors and raw provider messages (`Event.Err`, `Event.RawMessage`,
  `Event.RawRetryAfter`);
- tool arguments (`Call.Args`);
- tool output (`Call.Output`);
- edit diffs (`Edit.Patch`);
- routing reasons (`Route.Reason`).

Structural fields required for replay, resume, and undo are **never** redacted:
project, session, read, and edit paths; tool names and call IDs; content hashes
and shadow blob addresses; error codes and provider stop states; and numeric
usage counters.

### Boundaries

Redaction is not a DLP system. It:
- only removes credential patterns it recognizes;
- does not find arbitrary high-entropy secrets that carry no known prefix;
- does not encrypt the journal;
- does not redact the shadow store;
- does not stop a same-uid user or process from reading unredacted data; and
- does not guarantee removal of personal information or sensitive file content.

### Exporting a journal

`--export` writes a journal to stdout as JSONL and exits. It is independent of
`NABD_REDACT_JOURNAL`; whether the output is redacted is controlled only by
`--redact`:

```sh
nabd --export SESSION.jsonl             # raw, byte-for-byte copy
nabd --export SESSION.jsonl --redact    # recognized credentials redacted
```

- **Raw (no `--redact`):** the source bytes are copied verbatim. Unknown JSON
  fields, blank lines, and a truncated final line are preserved. A warning is
  written to stderr because the output may be sensitive.
- **`--redact`:** the journal is decoded and re-encoded through the same
  redaction path as the live journal and `--json`. Unknown JSON fields are
  dropped and a truncated final line is ignored. The source file is opened
  read-only and is never modified.

`--redact` requires `--export`, and `--export` cannot be combined with any run
mode (`-p`, `--continue`, `--replay`, `--feed`, `--feed-touch`, `--json`,
`--dir`, `--version`, `--max-turns`, `--permission-mode`, `--speed`) or with
positional arguments. Diagnostics go to stderr; stdout is JSONL only.
