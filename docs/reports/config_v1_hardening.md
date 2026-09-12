# Config v1 hardening

## User-scoped invariant

Provider selection, credentials, model names, routes and base URLs come only from the user-selected `NABD_CONFIG` absolute path or the default `~/.ag/config`. Nabd never discovers or merges project-local provider configuration.

## Fail-closed parsing

- A present but unreadable, insecure or malformed file is fatal.
- Duplicate keys are fatal.
- Unknown v1 keys produce name-only warnings; values are never echoed.
- The parser bounds file size, line length, key count, key length and value length.
- A broken file cannot silently fall back to an environment credential.

## File opening

On Unix, the final path component is opened with `O_NOFOLLOW|O_CLOEXEC`, then regular-file type, ownership, mode and size are checked from the opened descriptor. This closes the old `Lstat`/`Open` final-component swap. Parent-directory symlink resolution remains a documented same-uid residual, including on Termux.

Windows keeps the existing documented ownership limitation and verifies that pre-open and post-open metadata identify the same file.

## Operator commands

- `nabd config path`
- `nabd config validate`
- `nabd config show --redacted`

`show` refuses to run without `--redacted`. Credential-like key names are always rendered as `<redacted>`.
