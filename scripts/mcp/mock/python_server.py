#!/usr/bin/env python3
"""Zero-dependency MCP mock server (Python).
Performs a minimal handshake: initialize -> tools/list on stdin/stdout.
Exits non-zero if any required env var is missing."""
import json
import os
import sys

for v in ("PATH", "HOME", "TMPDIR"):
    if not os.environ.get(v):
        print(f"missing {v}", file=sys.stderr)
        sys.exit(1)

for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    try:
        msg = json.loads(line)
    except json.JSONDecodeError:
        continue
    if msg.get("method") == "initialize":
        print(json.dumps({"jsonrpc": "2.0", "id": msg.get("id"),
                           "result": {"protocolVersion": "2024-11-05",
                                     "serverInfo": {"name": "mock-python", "version": "0.0.1"}}}),
              flush=True)
    elif msg.get("method") == "tools/list":
        print(json.dumps({"jsonrpc": "2.0", "id": msg.get("id"),
                           "result": {"tools": [{"name": "echo", "inputSchema": {"type": "object"}}]}}),
              flush=True)
