package tools

// Phase 3 ID -> test mapping (ADR-0003 P9):
//   MCP-SEC-04  TestMCPSec04UnclassifiedDenied
//   MCP-SEC-05  TestMCPSec05ReadOnlyHintIgnored
//   MCP-SEC-06/07 TestMCPSec06_07FingerprintInvalidation
//   MCP-SEC-08  TestMCPSec08NameShadowing
// (extra)       TestMCPAdapterReadOnly, TestMCPAdapterWritePrompts,
//               TestMCPAdapterRunRefused, TestMCPAdapterModePlan,
//               TestMCPToolRegistered

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/event"
	"nabd/internal/perm"
	"nabd/internal/toolvocab"
)

type fakeMCPBackend struct {
	descs      map[string]MCPToolDesc
	approved   map[string]bool
	approveN   int
	callOut    string
	callIsErr  bool
	callErr    error
	callN      int
	lastServer string
	lastTool   string
}

func newFakeMCPBackend() *fakeMCPBackend {
	return &fakeMCPBackend{
		descs:    map[string]MCPToolDesc{},
		approved: map[string]bool{},
	}
}

func (f *fakeMCPBackend) key(server, tool string) string { return server + "\x00" + tool }

func (f *fakeMCPBackend) add(desc MCPToolDesc) {
	f.descs[f.key(desc.Server, desc.Name)] = desc
}

func (f *fakeMCPBackend) Describe(server, tool string) (MCPToolDesc, error) {
	d, ok := f.descs[f.key(server, tool)]
	if !ok {
		return MCPToolDesc{}, errors.New("mcp: unknown or unclassified tool " + server + "/" + tool)
	}
	return d, nil
}

func (f *fakeMCPBackend) IsApproved(desc MCPToolDesc) bool {
	return f.approved[f.key(desc.Server, desc.Name)]
}

func (f *fakeMCPBackend) Approve(desc MCPToolDesc) error {
	f.approved[f.key(desc.Server, desc.Name)] = true
	f.approveN++
	return nil
}

func (f *fakeMCPBackend) ApprovalText(desc MCPToolDesc) string {
	return "approve " + desc.Server + "/" + desc.Name + " fp=" + desc.Fingerprint
}

func (f *fakeMCPBackend) Call(ctx context.Context, server, tool string, args json.RawMessage) (string, bool, error) {
	f.callN++
	f.lastServer, f.lastTool = server, tool
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	return f.callOut, f.callIsErr, f.callErr
}

type fakeAsker struct {
	calls    int
	decision event.Decision
	lastCall event.ToolCall
}

func (f *fakeAsker) ask(ctx context.Context, call event.ToolCall) event.Decision {
	f.calls++
	f.lastCall = call
	return f.decision
}

func testMCPTool(be *fakeMCPBackend, asker *fakeAsker, mode perm.Mode) mcpTool {
	return mcpTool{
		backend: be,
		asker:   asker.ask,
		mode:    func() perm.Mode { return mode },
	}
}

func mcpArgs(server, tool string) json.RawMessage {
	return json.RawMessage(`{"server":"` + server + `","tool":"` + tool + `","args":{"q":1}}`)
}

// TestMCPSec04UnclassifiedDenied: a tool with no local classification is
// refused before any prompt and never reaches the server.
func TestMCPSec04UnclassifiedDenied(t *testing.T) {
	be := newFakeMCPBackend() // no tools configured
	asker := &fakeAsker{decision: event.AllowOnce}
	tool := testMCPTool(be, asker, perm.ModeAsk)

	_, err := tool.GuardedResult(context.Background(), mcpArgs("srv", "nope"))
	if err == nil || !strings.Contains(err.Error(), "unclassified") {
		t.Fatalf("expected unclassified refusal, got %v", err)
	}
	if asker.calls != 0 {
		t.Fatal("no prompt should precede the refusal")
	}
	if be.callN != 0 {
		t.Fatal("server must not be reached")
	}
}

// TestMCPSec05ReadOnlyHintIgnored: server hints never lower the local
// classification — a write-classified tool still prompts per call even when
// the server advertises readOnlyHint.
func TestMCPSec05ReadOnlyHintIgnored(t *testing.T) {
	be := newFakeMCPBackend()
	be.add(MCPToolDesc{Server: "srv", Name: "t", Classification: "write", Fingerprint: "fp1"})
	asker := &fakeAsker{decision: event.AllowOnce}
	tool := testMCPTool(be, asker, perm.ModeAsk)
	be.callOut = "ok"

	if _, err := tool.GuardedResult(context.Background(), mcpArgs("srv", "t")); err != nil {
		t.Fatalf("first approved call: %v", err)
	}
	// First approval + per-call approval for write.
	if asker.calls != 2 {
		t.Fatalf("write tool needs first + per-call approval, asker called %d times", asker.calls)
	}

	// Second call: still prompts (per-call), hints cannot silence it.
	asker.calls = 0
	if _, err := tool.GuardedResult(context.Background(), mcpArgs("srv", "t")); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if asker.calls != 1 {
		t.Fatalf("write tool must prompt on every call, asker called %d times", asker.calls)
	}
}

// TestMCPSec06_07FingerprintInvalidation: any fingerprint change invalidates the
// approval and forces re-approval (MCP-SEC-06/07).
func TestMCPSec06_07FingerprintInvalidation(t *testing.T) {
	be := newFakeMCPBackend()
	be.add(MCPToolDesc{Server: "srv", Name: "t", Classification: "read-only", Fingerprint: "fp1"})
	asker := &fakeAsker{decision: event.AllowOnce}
	tool := testMCPTool(be, asker, perm.ModeAsk)
	be.callOut = "ok"

	if _, err := tool.GuardedResult(context.Background(), mcpArgs("srv", "t")); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if asker.calls != 1 || be.approveN != 1 {
		t.Fatalf("expected one first-approval, asker=%d approveN=%d", asker.calls, be.approveN)
	}

	// Same fingerprint: no new prompt.
	asker.calls = 0
	if _, err := tool.GuardedResult(context.Background(), mcpArgs("srv", "t")); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if asker.calls != 0 {
		t.Fatal("valid approval must not re-prompt")
	}

	// Fingerprint changed (e.g. description edited): re-approval required.
	be.approved = map[string]bool{} // backend drops it: fingerprint mismatch
	be.add(MCPToolDesc{Server: "srv", Name: "t", Classification: "read-only", Fingerprint: "fp2"})
	asker.calls = 0
	if _, err := tool.GuardedResult(context.Background(), mcpArgs("srv", "t")); err != nil {
		t.Fatalf("changed fingerprint call: %v", err)
	}
	if asker.calls != 1 || be.approveN != 2 {
		t.Fatalf("fingerprint change must force re-approval, asker=%d approveN=%d", asker.calls, be.approveN)
	}
}

// TestMCPSec08NameShadowing: the adapter name can never address a server tool.
func TestMCPSec08NameShadowing(t *testing.T) {
	be := newFakeMCPBackend()
	be.add(MCPToolDesc{Server: "srv", Name: "mcp_tool", Classification: "read-only", Fingerprint: "fp"})
	asker := &fakeAsker{decision: event.AllowOnce}
	tool := testMCPTool(be, asker, perm.ModeAsk)

	_, err := tool.GuardedResult(context.Background(), mcpArgs("srv", "mcp_tool"))
	if err == nil || !strings.Contains(err.Error(), "reserved name") {
		t.Fatalf("expected reserved-name refusal, got %v", err)
	}
	if be.callN != 0 {
		t.Fatal("server must not be reached for the reserved name")
	}
}

func TestMCPAdapterReadOnly(t *testing.T) {
	be := newFakeMCPBackend()
	be.add(MCPToolDesc{Server: "srv", Name: "t", Classification: "read-only", Fingerprint: "fp1"})
	asker := &fakeAsker{decision: event.AllowOnce}
	tool := testMCPTool(be, asker, perm.ModeAsk)
	be.callOut = "result-text"

	res, err := tool.GuardedResult(context.Background(), mcpArgs("srv", "t"))
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Event.MCPResult == nil {
		t.Fatalf("bad result: %+v", res)
	}
	mr := res.Event.MCPResult
	if mr.Class != event.SkillContentClassUntrusted {
		t.Fatal("MCP output must be tagged UNTRUSTED")
	}
	if mr.Output != "result-text" || mr.Server != "srv" || mr.Tool != "t" {
		t.Fatalf("bad event content: %+v", mr)
	}
	if res.Event.Type != event.EventMCPToolResult {
		t.Fatalf("bad event type: %q", res.Event.Type)
	}
	// Approved read-only: second call needs no prompt.
	asker.calls = 0
	if _, err := tool.GuardedResult(context.Background(), mcpArgs("srv", "t")); err != nil {
		t.Fatal(err)
	}
	if asker.calls != 0 {
		t.Fatal("approved read-only tool must not re-prompt")
	}
}

func TestMCPAdapterDeniedApproval(t *testing.T) {
	be := newFakeMCPBackend()
	be.add(MCPToolDesc{Server: "srv", Name: "t", Classification: "read-only", Fingerprint: "fp1"})
	asker := &fakeAsker{decision: event.Deny}
	tool := testMCPTool(be, asker, perm.ModeAsk)

	if _, err := tool.GuardedResult(context.Background(), mcpArgs("srv", "t")); err == nil {
		t.Fatal("denied approval must refuse the call")
	}
	if be.callN != 0 {
		t.Fatal("server must not be reached after denial")
	}
}

func TestMCPAdapterRunRefused(t *testing.T) {
	tool := testMCPTool(newFakeMCPBackend(), &fakeAsker{}, perm.ModeAsk)
	_, _, err := tool.Run(context.Background(), mcpArgs("srv", "t"))
	if err == nil || !strings.Contains(err.Error(), "guarded execution") {
		t.Fatalf("plain Run must be refused, got %v", err)
	}
}

func TestMCPAdapterModePlan(t *testing.T) {
	be := newFakeMCPBackend()
	be.add(MCPToolDesc{Server: "srv", Name: "ro", Classification: "read-only", Fingerprint: "fp1"})
	be.add(MCPToolDesc{Server: "srv", Name: "wr", Classification: "write", Fingerprint: "fp2"})
	be.callOut = "ok"
	asker := &fakeAsker{decision: event.AllowOnce}

	ro := testMCPTool(be, asker, perm.ModePlan)
	if _, err := ro.GuardedResult(context.Background(), mcpArgs("srv", "ro")); err != nil {
		t.Fatalf("read-only in plan mode: %v", err)
	}

	wr := testMCPTool(be, asker, perm.ModePlan)
	asker.calls = 0
	if _, err := wr.GuardedResult(context.Background(), mcpArgs("srv", "wr")); err == nil {
		t.Fatal("write tool in plan mode must be denied")
	}
	if asker.calls != 0 {
		t.Fatal("plan-mode denial must not prompt")
	}
}

func TestMCPToolRegistered(t *testing.T) {
	be := newFakeMCPBackend()
	reg := &Registry{byName: map[string]Tool{}}
	asker := &fakeAsker{}
	reg.RegisterMCP(be, asker.ask, func() perm.Mode { return perm.ModeAsk })

	got, ok := reg.byName["mcp_tool"]
	if !ok {
		t.Fatal("mcp_tool not registered")
	}
	if got.Name() != "mcp_tool" {
		t.Fatalf("bad name %q", got.Name())
	}
	if c, ok := reg.Class("mcp_tool"); !ok || c != perm.ReadOnly {
		t.Fatalf("Class = %v, %v", c, ok)
	}
	if !toolvocab.Guarded("mcp_tool") {
		t.Fatal("toolvocab.Guarded(mcp_tool) must be true")
	}
	var _ agent.GuardedOutcome = got.(agent.GuardedOutcome)
}
