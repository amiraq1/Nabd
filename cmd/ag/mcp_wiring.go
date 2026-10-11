package main

// MCP session wiring (Phase 5). The manager lives for the session and is
// closed with it. When MCP is disabled (--no-mcp), when no config exists, or
// when no servers are enabled, no manager is created, mcp_tool is never
// registered, and no server process can start (MCP-SEC-14/15).

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"nabd/internal/agent"
	"nabd/internal/event"
	"nabd/internal/mcp"
	"nabd/internal/perm"
	"nabd/internal/tools"
)

// mcpBackendAdapter bridges *mcp.Manager to tools.MCPBackend. The two types
// stay distinct so internal/tools never imports internal/mcp (Phase 6
// isolation guard). Server free text never crosses the boundary: Description
// is dropped, so the approval content stays locally-built.
type mcpBackendAdapter struct {
	mgr *mcp.Manager
}

func (a mcpBackendAdapter) toInfo(desc tools.MCPToolDesc) mcp.ToolInfo {
	return mcp.ToolInfo{
		Server:         desc.Server,
		Name:           desc.Name,
		Classification: desc.Classification,
		Fingerprint:    desc.Fingerprint,
	}
}

func (a mcpBackendAdapter) Describe(server, tool string) (tools.MCPToolDesc, error) {
	info, err := a.mgr.Describe(server, tool)
	if err != nil {
		return tools.MCPToolDesc{}, err
	}
	return tools.MCPToolDesc{
		Server:         info.Server,
		Name:           info.Name,
		Classification: info.Classification,
		Fingerprint:    info.Fingerprint,
	}, nil
}

func (a mcpBackendAdapter) IsApproved(desc tools.MCPToolDesc) bool {
	return a.mgr.IsApproved(a.toInfo(desc))
}

func (a mcpBackendAdapter) Approve(desc tools.MCPToolDesc) error {
	return a.mgr.Approve(a.toInfo(desc))
}

func (a mcpBackendAdapter) ApprovalText(desc tools.MCPToolDesc) string {
	return a.mgr.ApprovalText(a.toInfo(desc))
}

func (a mcpBackendAdapter) Call(ctx context.Context, server, tool string, args json.RawMessage) (string, bool, error) {
	return a.mgr.Call(ctx, server, tool, args)
}

// wireMCP creates the MCP manager for a session. It returns (nil, nil) when
// MCP is unavailable, and the caller must then skip registration entirely.
func wireMCP(noMCP bool, projectRoot string) (tools.MCPBackend, *mcp.Manager, error) {
	if noMCP {
		return nil, nil, nil
	}
	cfg, err := mcp.Load(projectRoot)
	if err != nil {
		return nil, nil, err
	}
	if !hasEnabledServers(*cfg) {
		return nil, nil, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil, err
	}
	dir := filepath.Join(home, ".ag", "mcp")
	mgr, err := mcp.NewManager(*cfg,
		filepath.Join(dir, "approvals.json"),
		filepath.Join(dir, "journal.jsonl"),
		filepath.Join(dir, "journal.key"))
	if err != nil {
		return nil, nil, err
	}
	return mcpBackendAdapter{mgr: mgr}, mgr, nil
}

func hasEnabledServers(cfg mcp.Config) bool {
	for _, s := range cfg.Servers {
		if s.Enabled {
			return true
		}
	}
	return false
}

// The CallAwareGate methods live on gate (cmd/ag/main.go) so the loop's
// decide() consults the per-call MCP verdict through the normal
// PermAsk/PermReply path.

func (g gate) HandlesCall(c event.ToolCall) bool {
	return tools.MCPHandlesCall(g.mcp, c)
}

func (g gate) CheckCall(c event.ToolCall) (agent.Verdict, event.PermissionReason, string) {
	return tools.MCPCheckCall(g.p.Mode(), g.mcp, c)
}

func (g gate) RecordCall(c event.ToolCall, d event.Decision) {
	tools.MCPRecordCall(g.mcp, c, d)
}

// wireMCPForSession is the shared session/headless wiring: register mcp_tool,
// exempt it from YOLO, and hand the backend to the gate. It returns the
// manager for lifecycle management (nil when MCP is off).
func wireMCPForSession(noMCP bool, projectRoot string, reg *tools.Registry, pol *perm.Policy, g *gate) (*mcp.Manager, error) {
	backend, mgr, err := wireMCP(noMCP, projectRoot)
	if err != nil {
		return nil, err
	}
	if backend == nil {
		return nil, nil
	}
	reg.RegisterMCP(backend)
	pol.SetYOLOExempt("mcp_tool")
	g.mcp = backend
	return mgr, nil
}
