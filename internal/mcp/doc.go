// Package mcp is Nabd's isolated Model Context Protocol integration layer
// (ADR-0003, stdio only in v1).
//
// Isolation contract — enforced by internal/build/mcp_dependency_isolation_test.go:
//   - This package MUST NOT be imported by internal/tools, internal/perm,
//     internal/safefs, internal/redact, internal/snap, internal/store,
//     internal/toolvocab, internal/ignorefile, or internal/agent.
//   - No MCP SDK dependency: the stdio JSON-RPC client is hand-rolled on the
//     standard library (S4 decision, ADR-0003). Secure file opens use
//     syscall.O_NOFOLLOW directly (stdlib on unix); the Windows stub refuses
//     clearly instead of running weaker.
//
// Trust boundaries (ADR-0003):
//   - The server, its tool descriptions, schemas, and outputs are UNTRUSTED
//     by default. Permission classification comes from local user config only.
//   - Server outputs never flow through a plain Tool.Run; they go through the
//     guarded adapter (see adapter.go, Phase 3) and the nonce fence.
//   - Configuration loads from ~/.ag/mcp.json only; any path resolving inside
//     the project tree is refused regardless of file name.
package mcp
