# Termux AI Agent

A local AI assistant for Android running inside Termux to automate CLI tasks.

## Features

- Natural-language to CLI task mapping
- Safe shell execution with allowlists and blocklists
- File operations (read, create, write, append, delete)
- Termux environment detection
- Dry-run mode for safe experimentation

## Installation

```bash
cd ~/termux-ai-agent
npm install
npm link
```

## Usage

```bash
# Run a natural-language task
termux-ai-agent run "list files in the current directory"

# Run a shell command safely
termux-ai-agent shell "ls -la"

# List available skills
termux-ai-agent list

# Show configuration
termux-ai-agent config
```

## Configuration

Copy `config/default.json` to `~/.termux-ai-agent.json` and customize:

```json
{
  "dryRun": false,
  "safeMode": true,
  "shell": {
    "allowedCommands": ["ls", "pwd", "cat"],
    "blockedPatterns": ["rm -rf /"],
    "maxTimeoutMs": 30000
  }
}
```

## Safety

This tool executes shell commands. It uses a command allowlist and a regex blocklist to prevent dangerous operations. Always review tasks before running with `dryRun: true`.

## Testing

```bash
npm test
```
