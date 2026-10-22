package main

// Phase 5 ID -> test mapping (ADR-0003 P9):
//   MCP-SEC-14  TestMCPSec14NoMCPFlag

import (
	"testing"

	"nabd/internal/event"
	"nabd/internal/perm"
	"nabd/internal/tools"
)

// TestMCPSec14NoMCPFlag: --no-mcp disables MCP entirely — no config is
// loaded, no manager is created, mcp_tool is never registered, and the gate
// never takes the per-call path.
func TestMCPSec14NoMCPFlag(t *testing.T) {
	backend, mgr, err := wireMCP(true, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if backend != nil {
		t.Fatal("--no-mcp must yield no backend")
	}
	if mgr != nil {
		t.Fatal("--no-mcp must yield no manager")
	}

	// The gate with no backend never handles mcp_tool calls.
	g := &gate{p: perm.New(nil)}
	if g.HandlesCall(event.ToolCall{Name: "mcp_tool"}) {
		t.Fatal("gate without backend must not handle mcp_tool")
	}
	if tools.MCPHandlesCall(nil, event.ToolCall{Name: "mcp_tool"}) {
		t.Fatal("MCPHandlesCall(nil, ...) must be false")
	}
}

// TestMCPSec14NoConfigNoRegistration: without --no-mcp but with no config
// file and no enabled servers, nothing is registered either (lazy startup:
// no config → no servers → no processes).
func TestMCPSec14NoConfigNoRegistration(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // no ~/.ag/mcp.json here
	backend, mgr, err := wireMCP(false, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if backend != nil || mgr != nil {
		t.Fatal("no config must yield no backend and no manager")
	}

	// wireMCPForSession with no backend registers nothing.
	reg := tools.NewRegistry(nil, nil)
	pol := perm.New(reg)
	g := &gate{p: pol}
	got, err := wireMCPForSession(true, t.TempDir(), reg, pol, g)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatal("expected nil manager")
	}
	if _, ok := reg.Class("mcp_tool"); ok {
		t.Fatal("mcp_tool must not be registered when MCP is off")
	}
	if g.mcp != nil {
		t.Fatal("gate must have no backend when MCP is off")
	}
}
