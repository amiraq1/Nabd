# Minimal strict Config v2

Config v2 is an opt-in, user-scoped JSON document. Nabd reads it from the absolute path in `NABD_CONFIG_V2`, or from `~/.ag/config.v2.json` when present. The file must satisfy the same regular-file, ownership, `0600`, no-symlink and size checks as Config v1.

## Selection

- Config v1 and Config v2 cannot be selected together.
- `~/.ag/config` and `~/.ag/config.v2.json` cannot coexist.
- A selected but missing, unreadable, insecure or malformed v2 file is fatal.
- Project-local configuration discovery remains forbidden.

## Strict schema

- `version` must equal `2`.
- Unknown JSON fields are fatal at every schema level.
- Supported providers are `anthropic`, `groq`, `openrouter`, `nvidia`, and `router`.
- Router precedence is the order of the `routes` array. There is no `priority` field.
- The minimal schema deliberately rejects `base_url`. Custom endpoints stay closed until redirect and post-DNS-resolution address controls can be enforced by the HTTP transport.

## Credentials

Each used provider must declare a credential source:

- `{ "source": "env" }` reads only that provider's canonical API-key variable.
- `{ "source": "file", "path": "/absolute/user/path" }` reads one non-empty line from a protected file.
- `source=command`, relative paths, inline secrets and undeclared environment fallback are forbidden.

Credential values are never included in validation errors or diagnostics.

## Router example

```json
{
  "version": 2,
  "provider": "router",
  "router_mode": "fallback",
  "routes": [
    { "provider": "groq", "model": "openai/gpt-oss-120b" },
    { "provider": "nvidia", "model": "deepseek-ai/deepseek-v4-pro-0813" }
  ],
  "credentials": {
    "groq": { "source": "env" },
    "nvidia": { "source": "env" }
  }
}
```

Store the document at `~/.ag/config.v2.json`, run `chmod 600 ~/.ag/config.v2.json`, remove or move the v1 file, then verify with `nabd config validate` and inspect only through `nabd config show --redacted`.
