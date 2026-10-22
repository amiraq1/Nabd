package tools

// Phase 3 ID -> test mapping (ADR-0003 P9):
//   MCP-SEC-04  TestMCPSec04UnclassifiedDenied
//   MCP-SEC-05  TestMCPSec05ReadOnlyHintIgnored
//   MCP-SEC-06/07 TestMCPSec06_07FingerprintInvalidation
//   MCP-SEC-08  TestMCPSec08NameShadowing
// (extra)       TestMCPCheckCallMatrix, TestMCPCheckCallYOLOExempt,
//               TestMCPAdapterReadOnly, TestMCPAdapterUnapprovedRefused,
//               TestMCPAdapterRunRefused, TestMCPRecordCallIdempotent,
//               TestMCPRecordCallDenyNoApprove, TestMCPToolRegistered
//
// Permission is decided by MCPCheckCall through the loop's normal
// PermAsk/PermReply path (CallAwareGate); the adapter's GuardedResult is
// verify-only and fails closed without a recorded first-approval.

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

func testMCPTool(be *fakeMCPBackend) mcpTool {
	return mcpTool{backend: be}
}

// mcpCall builds the loop-level ToolCall the gate sees.
func mcpCall(server, tool string) event.ToolCall {
	return event.ToolCall{
		ID:   "call-1",
		Name: "mcp_tool",
		Args: json.RawMessage(`{"server":"` + server + `","tool":"` + tool + `","args":{"q":1}}`),
	}
}

func mcpArgs(server, tool string) json.RawMessage {
	return json.RawMessage(`{"server":"` + server + `","tool":"` + tool + `","args":{"q":1}}`)
}

func checkCall(t *testing.T, mode perm.Mode, be *fakeMCPBackend, server, tool string) (agent.Verdict, event.PermissionReason, string) {
	t.Helper()
	return MCPCheckCall(mode, be, mcpCall(server, tool))
}

// TestMCPSec04UnclassifiedDenied: a tool with no local classification is
// denied at the gate before any prompt and never reaches the server.
func TestMCPSec04UnclassifiedDenied(t *testing.T) {
	be := newFakeMCPBackend() // no tools configured

	v, _, _ := checkCall(t, perm.ModeAsk, be, "srv", "nope")
	if v != agent.VerdictDeny {
		t.Fatalf("unclassified tool must be denied at the gate, got %v", v)
	}
	if be.callN != 0 {
		t.Fatal("server must not be reached")
	}

	// And the adapter fails closed too, even if the gate were bypassed.
	tool := testMCPTool(be)
	if _, err := tool.GuardedResult(context.Background(), mcpArgs("srv", "nope")); err == nil {
		t.Fatal("adapter must refuse unclassified tool")
	}
}

// TestMCPSec05ReadOnlyHintIgnored: server hints never lower the local
// classification — a write-classified tool still asks per call, in every
// mode, even when the server advertises readOnlyHint.
func TestMCPSec05ReadOnlyHintIgnored(t *testing.T) {
	be := newFakeMCPBackend()
	be.add(MCPToolDesc{Server: "srv", Name: "t", Classification: "write", Fingerprint: "fp1"})
	// First approval recorded (as the loop would after an AllowOnce).
	MCPRecordCall(be, mcpCall("srv", "t"), event.AllowOnce)
	if be.approveN != 1 {
		t.Fatal("first approval must be recorded")
	}

	for _, mode := range []perm.Mode{perm.ModeAsk, perm.ModeDeny, perm.ModeAllowReads, perm.ModePlan} {
		v, _, _ := checkCall(t, mode, be, "srv", "t")
		if mode == perm.ModeAsk {
			if v != agent.VerdictAsk {
				t.Fatalf("mode %v: write tool must ask per call, got %v", mode, v)
			}
		} else if v != agent.VerdictDeny {
			t.Fatalf("mode %v: write tool must be denied, got %v", mode, v)
		}
	}
}

// TestMCPSec06_07FingerprintInvalidation: any fingerprint change invalidates
// the approval and forces re-approval (MCP-SEC-06/07).
func TestMCPSec06_07FingerprintInvalidation(t *testing.T) {
	be := newFakeMCPBackend()
	be.add(MCPToolDesc{Server: "srv", Name: "t", Classification: "read-only", Fingerprint: "fp1"})

	v, _, _ := checkCall(t, perm.ModeAsk, be, "srv", "t")
	if v != agent.VerdictAsk {
		t.Fatalf("unapproved tool must ask, got %v", v)
	}
	MCPRecordCall(be, mcpCall("srv", "t"), event.AllowOnce)
	if be.approveN != 1 {
		t.Fatal("approval must be recorded once")
	}

	// Same fingerprint: allowed silently.
	v, _, _ = checkCall(t, perm.ModeAsk, be, "srv", "t")
	if v != agent.VerdictAllow {
		t.Fatalf("approved read-only must be allowed, got %v", v)
	}

	// Fingerprint changed (e.g. description edited): re-approval required.
	be.approved = map[string]bool{} // backend drops it: fingerprint mismatch
	be.add(MCPToolDesc{Server: "srv", Name: "t", Classification: "read-only", Fingerprint: "fp2"})
	v, _, why := checkCall(t, perm.ModeAsk, be, "srv", "t")
	if v != agent.VerdictAsk {
		t.Fatalf("changed fingerprint must re-ask, got %v", v)
	}
	if !strings.Contains(why, "fp2") {
		t.Fatalf("approval prompt must carry the new fingerprint, got %q", why)
	}
	MCPRecordCall(be, mcpCall("srv", "t"), event.AllowOnce)
	if be.approveN != 2 {
		t.Fatalf("re-approval must be recorded, approveN=%d", be.approveN)
	}
}

// TestMCPSec08NameShadowing: the adapter name can never address a server tool.
func TestMCPSec08NameShadowing(t *testing.T) {
	be := newFakeMCPBackend()
	be.add(MCPToolDesc{Server: "srv", Name: "mcp_tool", Classification: "read-only", Fingerprint: "fp"})

	v, _, _ := checkCall(t, perm.ModeAsk, be, "srv", "mcp_tool")
	if v != agent.VerdictDeny {
		t.Fatalf("reserved name must be denied at the gate, got %v", v)
	}

	tool := testMCPTool(be)
	_, err := tool.GuardedResult(context.Background(), mcpArgs("srv", "mcp_tool"))
	if err == nil || !strings.Contains(err.Error(), "reserved name") {
		t.Fatalf("expected reserved-name refusal, got %v", err)
	}
	if be.callN != 0 {
		t.Fatal("server must not be reached for the reserved name")
	}
}

// TestMCPCheckCallMatrix: the full verdict matrix across modes and
// classifications.
func TestMCPCheckCallMatrix(t *testing.T) {
	newBackend := func() *fakeMCPBackend {
		be := newFakeMCPBackend()
		be.add(MCPToolDesc{Server: "srv", Name: "ro", Classification: "read-only", Fingerprint: "fp-ro"})
		be.add(MCPToolDesc{Server: "srv", Name: "wr", Classification: "write", Fingerprint: "fp-wr"})
		be.add(MCPToolDesc{Server: "srv", Name: "ex", Classification: "execute", Fingerprint: "fp-ex"})
		return be
	}
	cases := []struct {
		mode perm.Mode
		tool string
		want agent.Verdict
		note string
	}{
		{perm.ModeAsk, "ro", agent.VerdictAsk, "first approval asks"},
		{perm.ModeAsk, "wr", agent.VerdictAsk, "first approval asks"},
		{perm.ModeAsk, "ex", agent.VerdictAsk, "first approval asks"},
		{perm.ModePlan, "ro", agent.VerdictAsk, "plan: read-only first approval asks"},
		{perm.ModePlan, "wr", agent.VerdictDeny, "plan: write denied outright"},
		{perm.ModePlan, "ex", agent.VerdictDeny, "plan: execute denied outright"},
		{perm.ModeDeny, "ro", agent.VerdictAsk, "deny: read-only first approval asks"},
		{perm.ModeDeny, "wr", agent.VerdictDeny, "deny: write denied outright"},
		{perm.ModeAllowReads, "ro", agent.VerdictAsk, "allow-reads: read-only first approval asks"},
		{perm.ModeAllowReads, "ex", agent.VerdictDeny, "allow-reads: execute denied outright"},
	}
	for _, tc := range cases {
		be := newBackend()
		v, _, _ := checkCall(t, tc.mode, be, "srv", tc.tool)
		if v != tc.want {
			t.Errorf("mode=%v tool=%s: got %v, want %v (%s)", tc.mode, tc.tool, v, tc.want, tc.note)
		}
	}

	// Approved tools: read-only allowed silently, write/execute ask per call.
	be := newBackend()
	for _, tool := range []string{"ro", "wr", "ex"} {
		MCPRecordCall(be, mcpCall("srv", tool), event.AllowOnce)
	}
	if v, _, _ := checkCall(t, perm.ModeAsk, be, "srv", "ro"); v != agent.VerdictAllow {
		t.Errorf("approved read-only: got %v, want Allow", v)
	}
	if v, _, _ := checkCall(t, perm.ModeAsk, be, "srv", "wr"); v != agent.VerdictAsk {
		t.Errorf("approved write: got %v, want Ask (per-call)", v)
	}
	if v, _, _ := checkCall(t, perm.ModeAsk, be, "srv", "ex"); v != agent.VerdictAsk {
		t.Errorf("approved execute: got %v, want Ask (per-call)", v)
	}
	// Restrictive modes keep approved read-only silent.
	if v, _, _ := checkCall(t, perm.ModePlan, be, "srv", "ro"); v != agent.VerdictAllow {
		t.Errorf("plan approved read-only: got %v, want Allow", v)
	}
}

// TestMCPCheckCallYOLOExempt: the exemption is structural — the matrix never
// returns Allow for write/execute, so YOLO can never auto-approve them.
// (The policy-level exemption is additionally covered by
// TestYOLODoesNotApproveMCPWrite in internal/perm.)
func TestMCPCheckCallYOLOExempt(t *testing.T) {
	be := newFakeMCPBackend()
	be.add(MCPToolDesc{Server: "srv", Name: "wr", Classification: "write", Fingerprint: "fp-wr"})
	be.add(MCPToolDesc{Server: "srv", Name: "ex", Classification: "execute", Fingerprint: "fp-ex"})
	MCPRecordCall(be, mcpCall("srv", "wr"), event.AllowOnce)
	MCPRecordCall(be, mcpCall("srv", "ex"), event.AllowOnce)

	for _, tool := range []string{"wr", "ex"} {
		v, _, _ := checkCall(t, perm.ModeAsk, be, "srv", tool)
		if v == agent.VerdictAllow {
			t.Fatalf("%s: YOLO-exempt tool must never be auto-allowed", tool)
		}
	}
}

// TestMCPAdapterReadOnly: approved read-only output travels as a fenced,
// UNTRUSTED event.
func TestMCPAdapterReadOnly(t *testing.T) {
	be := newFakeMCPBackend()
	be.add(MCPToolDesc{Server: "srv", Name: "t", Classification: "read-only", Fingerprint: "fp1"})
	MCPRecordCall(be, mcpCall("srv", "t"), event.AllowOnce)
	tool := testMCPTool(be)
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
}

// TestMCPAdapterUnapprovedRefused: without a recorded approval the adapter
// fails closed even if the gate were bypassed.
func TestMCPAdapterUnapprovedRefused(t *testing.T) {
	be := newFakeMCPBackend()
	be.add(MCPToolDesc{Server: "srv", Name: "t", Classification: "read-only", Fingerprint: "fp1"})
	tool := testMCPTool(be)
	be.callOut = "ok"

	if _, err := tool.GuardedResult(context.Background(), mcpArgs("srv", "t")); err == nil {
		t.Fatal("unapproved tool must be refused by the adapter")
	}
	if be.callN != 0 {
		t.Fatal("server must not be reached without approval")
	}
}

func TestMCPAdapterRunRefused(t *testing.T) {
	tool := testMCPTool(newFakeMCPBackend())
	_, _, err := tool.Run(context.Background(), mcpArgs("srv", "t"))
	if err == nil || !strings.Contains(err.Error(), "guarded execution") {
		t.Fatalf("plain Run must be refused, got %v", err)
	}
}

// TestMCPRecordCallIdempotent: per-call approvals never rewrite the stored
// first-approval.
func TestMCPRecordCallIdempotent(t *testing.T) {
	be := newFakeMCPBackend()
	be.add(MCPToolDesc{Server: "srv", Name: "wr", Classification: "write", Fingerprint: "fp1"})

	MCPRecordCall(be, mcpCall("srv", "wr"), event.AllowOnce)
	MCPRecordCall(be, mcpCall("srv", "wr"), event.AllowOnce)
	MCPRecordCall(be, mcpCall("srv", "wr"), event.AllowOnce)
	if be.approveN != 1 {
		t.Fatalf("first approval must be recorded exactly once, got %d", be.approveN)
	}
}

// TestMCPRecordCallDenyNoApprove: a denied call records nothing.
func TestMCPRecordCallDenyNoApprove(t *testing.T) {
	be := newFakeMCPBackend()
	be.add(MCPToolDesc{Server: "srv", Name: "t", Classification: "read-only", Fingerprint: "fp1"})

	MCPRecordCall(be, mcpCall("srv", "t"), event.Deny)
	MCPRecordCall(be, mcpCall("srv", "t"), event.AllowSession)
	if be.approveN != 0 {
		t.Fatal("denial must not record an approval")
	}
	if be.IsApproved(MCPToolDesc{Server: "srv", Name: "t"}) {
		t.Fatal("denied tool must not be approved")
	}
}

func TestMCPToolRegistered(t *testing.T) {
	be := newFakeMCPBackend()
	reg := &Registry{byName: map[string]Tool{}}
	reg.RegisterMCP(be)

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
}

// TestMCPArgumentsStrictBoundary pins the decoder for the one declared field
// that is forwarded unexamined: `args` reaches the server as raw JSON, so a key
// repeated inside it would be collapsed there with the last value winning. The
// repair layer does not descend into a declared field, so this is the only net.
func TestMCPArgumentsStrictBoundary(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"duplicate inside args", `{"server":"s","tool":"t","args":{"x":1,"x":2}}`, "duplicate key"},
		{"mixed-case duplicate top-level key", `{"server":"s","tool":"t","Server":"s2"}`, "duplicate key"},
		{"duplicate nested two levels down", `{"server":"s","tool":"t","args":{"a":{"b":1,"b":2}}}`, "duplicate key"},
		{"trailing data", `{"server":"s","tool":"t","args":{}}{}`, "unexpected trailing data"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseMCPArgs(json.RawMessage(tc.raw)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("parseMCPArgs(%q) = %v, want an error containing %q", tc.raw, err, tc.want)
			}
		})
	}
}
