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
5. **Size Bounds:**
   Configuration documents cannot exceed 256 KB (`MaxFileBytes`). Credential secret files cannot exceed 64 KB (`MaxValueBytes`).

---

## 4. Project skills

Project skills are disabled by default and project files are never consulted to enable them.

Config v1 accepts `NABD_SKILLS_PROJECT=1` in `~/.ag/config` (or `NABD_CONFIG`), and may use the process environment when the v1 file omits it. Other values do not enable the feature. The default is disabled.

Config v2 does not use the `NABD_SKILLS_PROJECT` environment variable. Enable project skills only with the structured boolean `skills.project` field:

```json
{
  "version": 2,
  "provider": "groq",
  "skills": { "project": true },
  "credentials": { "groq": { "source": "env" } }
}
```

The schema field `skills.project` has type `boolean` and default `false`. It is a user setting, not a project setting; merely having `.nabd/skills` in a repository never enables it. Config v2 does not restore implicit environment fallback, so `NABD_SKILLS_PROJECT` is ignored under v2 and cannot override `false` or an absent field.

### Config v1 keys

`NABD_SKILLS_PROJECT` is a known v1 key. Its only enabling value is `1`.

## 5. Config v2 Schema Reference

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
| `skills.project` | `boolean` | Optional | Enable project skills; defaults to `false`. |
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

`NABD_REDACT_JOURNAL` is a user-scoped Config v1 key in `~/.ag/config` (or the
absolute path selected by `NABD_CONFIG`). The environment variable remains
supported as a compatibility override when Config v1 is selected. Config v2
does not currently expose this setting; its default is the safe redacted mode.

### Default and opt-out

Redaction is enabled by default. To explicitly opt out for a diagnostic run,
set the exact literal value `0`:

```sh
NABD_REDACT_JOURNAL=0 nabd
```

Every other value, including empty, `1`, `true`, `yes`, `on`, and `" 1 "`,
keeps redaction enabled. Config v1 takes precedence over the environment,
consistent with the other user-scoped settings.

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

## 8. Provider registry (`~/.ag/providers.json`)

User-defined providers live in `~/.ag/providers.json`; the path can be
overridden with `NABD_PROVIDERS_FILE`. API keys are not stored here — they live
in `~/.ag/auth.json` (override `NABD_AUTH_FILE`) and are enrolled with
`nabd connect` or `nabd provider add`. The file is optional, and its document is
a single object keyed by `provider`, whose keys are your provider IDs:

```json
{
  "provider": {
    "acme": {
      "api": "openai",
      "name": "Acme Cloud",
      "options": { "baseURL": "https://api.acme.example.com/v1" },
      "readCap": 65536,
      "models": {
        "acme-large": { "name": "Acme Large", "id": "acme-large-2026-01" },
        "acme-small": { "name": "Acme Small" }
      },
      "defaultModel": "acme-large"
    }
  }
}
```

An entry whose ID matches a builtin provider (`anthropic`, `groq`,
`openrouter`, `nvidia`) replaces that builtin definition entirely.

| Field | Type | Required | Meaning |
|---|---|---|---|
| `api` | `string` | Yes | Wire dialect: `"openai"` or `"anthropic"`. Any other value is rejected when the file is parsed. |
| `name` | `string` | No | Human-readable provider label. |
| `options.baseURL` | `string` | No | Endpoint for every request to this provider. Checked at load time and again at connect time under the endpoint policy (see below). |
| `readCap` | `int` | No | Per-provider read ceiling in bytes. Omission or a non-positive value means the built-in default, `DefaultReadCapBytes` (16384). An explicit `NABD_MAX_READ` still outranks it at runtime. |
| `models` | `object` | No | Declared model names. Router routes resolve their `model` through this map, and the optional `id` is the wire model ID actually sent (defaults to the map key). The standalone path (`NABD_PROVIDER` with `NABD_MODEL`/`defaultModel`) sends its model name as given. |
| `defaultModel` | `string` | No | The model used when nothing else names one. On the standalone path an explicit `NABD_MODEL` wins over it; when neither is set the run fails with an error naming this key and the file to edit. Router routes always name their own model, so `defaultModel` is not consulted there. |

Rules enforced when the file is loaded:

- strict JSON: unknown fields and trailing documents are rejected;
- provider IDs match `[a-z0-9-_]` and are at most 32 bytes; model keys are
  1-256 bytes;
- literal API keys are rejected — keys belong in `~/.ag/auth.json`;
- the file must be a regular file, owned by the user on Unix, with no group or
  other permission bits (`0600`), and at most 256 KB;
- every `options.baseURL` must satisfy the endpoint policy: HTTPS by default,
  with loopback, private, link-local, CGNAT, ULA, 6to4, cloud-metadata, and
  `.internal`/`.local`/`localhost` targets refused unless
  `NABD_ENDPOINT_POLICY=loopback` permits a local runtime or
  `NABD_ENDPOINT_ALLOW` names the endpoint. See
  [THREAT_MODEL.md](THREAT_MODEL.md) for the exact guarantee.

Precedence: provider definitions come from `providers.json` before the builtin
catalog, and credentials from `auth.json` before legacy environment variables
and v1 config (`PrecedenceDocumentation` in `internal/registry/registry.go`).

---

## 9. Network and DNS Configuration (Environment Only)

### `NABD_PUBLIC_DNS`

On Termux (`android/arm64`), Go's standard library does not use system `libc` resolver functions (like `getaddrinfo`) because Android's bionic libc is not linked without cgo. Instead, the pure-Go resolver reads `$PREFIX/etc/resolv.conf`.

- **Environment-only variable:** `NABD_PUBLIC_DNS` is read strictly from the process environment (`os.Getenv`) during network initialization. It is **not** a configuration file key and cannot be set in `~/.ag/config` or `~/.ag/config.v2.json`. Setting it in a config file will trigger an unknown key warning (in v1) or validation error (in v2).
- **Purpose:** If `$PREFIX/etc/resolv.conf` is missing or contains no nameservers, Nabd does not silently fallback to public DNS. Setting `NABD_PUBLIC_DNS=1` (or `true`) explicitly permits Nabd to use fallback public DNS resolvers (`1.1.1.1:53`, `8.8.8.8:53`).
- **Privacy notice:** Resolving DNS via `$PREFIX/etc/resolv.conf` (or public DNS) bypasses Android system Private DNS (DNS-over-TLS) and VPN-directed DNS. To route DNS queries to your preferred nameservers, configure `$PREFIX/etc/resolv.conf`.

---

## 10. Display Configuration (Environment Only)

### `NABD_ASCII_ONLY`

- **Environment-only variable:** `NABD_ASCII_ONLY` is read strictly from the process environment (`os.Getenv`). It is **not** a configuration file key and cannot be set in `~/.ag/config` or `~/.ag/config.v2.json`. Setting it in a config file triggers an unknown key warning (in v1) or validation error (in v2).
- **Scope:** Controls decorative UI glyphs only. When set to any non-empty value (e.g. `NABD_ASCII_ONLY=1`), Nabd replaces Unicode decorative symbols with plain ASCII equivalents:
  - Error indicator glyph: `✗ ` becomes `x ` (or `! ` depending on width).
  - Truncation tail: `…` becomes `...`.
  - Border and separator lines: Unicode box-drawing character `─` becomes ASCII hyphen `-`.
- **Preserves Arabic and content text:** `NABD_ASCII_ONLY` affects only decorative framing glyphs and punctuation markers. It does **not** strip or alter Arabic text, user input, assistant responses, or error message bodies.



