package perm

import "testing"

// TestYOLODoesNotApproveMCPWrite: the structural YOLO exemption in the policy
// itself (ADR-0003 §7). Even if an MCP-backed tool ever reports a
// YOLO-approvable class, YOLO must not auto-approve it.
func TestYOLODoesNotApproveMCPWrite(t *testing.T) {
	cls := fakeClassifier{"mcp_tool": Mutating, "write_file": Mutating}
	p := New(cls)
	p.SetYOLO(true)
	p.SetYOLOExempt("mcp_tool")

	if v, _ := p.Check("mcp_tool"); v != Ask {
		t.Fatalf("YOLO must not approve mcp_tool, got verdict %v", v)
	}
	// Non-exempt tools keep YOLO behavior.
	if v, _ := p.Check("write_file"); v != Allow {
		t.Fatalf("YOLO should still approve write_file, got verdict %v", v)
	}
	// Without YOLO the exemption is inert (still Ask, not Allow).
	p2 := New(cls)
	p2.SetYOLOExempt("mcp_tool")
	if v, _ := p2.Check("mcp_tool"); v != Ask {
		t.Fatalf("expected Ask without YOLO, got verdict %v", v)
	}
	// Exemption survives Reset (it is structural, not a grant).
	p.Reset()
	p.SetYOLO(true)
	if v, _ := p.Check("mcp_tool"); v != Ask {
		t.Fatalf("exemption must survive Reset, got verdict %v", v)
	}
}
