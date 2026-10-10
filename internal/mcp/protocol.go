package mcp

import (
	"encoding/json"
	"fmt"
)

const (
	// ProtocolVersionRequest is the protocol version this client asks for
	// in initialize (ADR-0003 v6).
	ProtocolVersionRequest = "2026-07-28"

	// MaxMessageBytes caps a single JSON-RPC message read from the server.
	// Anything larger fails the client closed (fail-closed).
	MaxMessageBytes = 64 * 1024 * 1024

	// MaxToolsListCount caps the number of tools accepted from tools/list.
	// A larger advertised list is refused outright (fail-closed).
	MaxToolsListCount = 1024
)

// KnownProtocolVersions are the only server protocol versions this client
// accepts (ADR-0003 v6). These are the versions actually verified in testing;
// any other version needs a new ADR before it can be accepted.
var KnownProtocolVersions = []string{"2024-11-05", "2025-11-25", "2026-07-28"}

// negotiateVersion implements the ADR-0003 v6 handshake rule (MCP-SEC-21):
// accept only a server version older-or-equal to the requested one AND
// present in the verified list. Anything else fails closed.
func negotiateVersion(server string) (string, error) {
	if server > ProtocolVersionRequest {
		return "", fmt.Errorf("mcp: server protocol version %q is newer than requested %q: refusing without a new ADR", server, ProtocolVersionRequest)
	}
	for _, known := range KnownProtocolVersions {
		if server == known {
			return server, nil
		}
	}
	return "", fmt.Errorf("mcp: server protocol version %q is not in the verified list %v: refusing", server, KnownProtocolVersions)
}

// Request is a JSON-RPC 2.0 request. A nil ID makes it a notification.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *uint64         `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// RPCError is a JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("mcp: json-rpc error %d: %s", e.Code, e.Message)
}

// Response is a JSON-RPC 2.0 response.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *uint64         `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// Notification is a server-to-client JSON-RPC notification.
type Notification struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// InitializeResult is the parsed result of an initialize response.
type InitializeResult struct {
	ProtocolVersion string          `json:"protocolVersion"`
	Capabilities    json.RawMessage `json:"capabilities,omitempty"`
	ServerInfo      json.RawMessage `json:"serverInfo,omitempty"`
}

// Tool is a single tool advertised by tools/list.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// ContentItem is one content block of a tools/call result.
type ContentItem struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// CallToolResult is the parsed result of a tools/call response.
type CallToolResult struct {
	Content []ContentItem `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}
