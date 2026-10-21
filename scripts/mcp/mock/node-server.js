#!/usr/bin/env node
// Zero-dependency MCP mock server (Node).
// Performs a minimal handshake: initialize -> tools/list on stdin/stdout.
// Exits non-zero if any required env var is missing.
const need = ["PATH", "HOME", "TMPDIR"];
for (const v of need) {
  if (!process.env[v]) {
    console.error(`missing ${v}`);
    process.exit(1);
  }
}
const readline = require("readline");
const rl = readline.createInterface({ input: process.stdin, output: process.stdout, terminal: false });
rl.on("line", (line) => {
  let msg;
  try { msg = JSON.parse(line); } catch { return; }
  if (msg.method === "initialize") {
    console.log(JSON.stringify({ jsonrpc: "2.0", id: msg.id, result: { protocolVersion: "2024-11-05", serverInfo: { name: "mock-node", version: "0.0.1" } } }));
  } else if (msg.method === "tools/list") {
    console.log(JSON.stringify({ jsonrpc: "2.0", id: msg.id, result: { tools: [{ name: "echo", inputSchema: { type: "object" } }] } }));
  }
});
