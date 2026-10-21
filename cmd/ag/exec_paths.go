package main

import (
	"encoding/json"

	"nabd/internal/agent"
	"nabd/internal/event"
	"nabd/internal/perm"
)

// protectedWriteVerdict maps a file-tool call to the verdict its target
// demands. The loop consults it per call, before the name-based ladder, so a
// standing grant or a YOLO flag cannot stand in for the approval a protected
// path requires. It reports ok=false for every call it does not govern, which
// keeps the name-based path and the MCP path untouched.
func protectedWriteVerdict(c event.ToolCall) (agent.Verdict, event.PermissionReason, string, bool) {
	rel, ok := writeTargetOf(c)
	if !ok {
		return agent.VerdictAsk, "", "", false
	}
	tier, reason := perm.ClassifyWritePath(rel)
	switch tier {
	case perm.WritePathInternal:
		// Refused rather than asked about: a session has no business writing
		// repository-internal state, and a prompt would only invite a yes.
		return agent.VerdictDeny, event.PermissionReasonPolicyDenied, reason, true
	case perm.WritePathExecutionSensitive:
		// Asked every time: such a write may be legitimate, but it changes
		// what runs later, so the human decides call by call.
		return agent.VerdictAsk, event.PermissionReasonRequired, reason, true
	}
	return agent.VerdictAsk, "", "", false
}

// writeTargetOf extracts the root-relative target of a mutating file call. A
// call this cannot parse is deliberately not governed here: the tool's own
// strict decoder rejects it, and the name-based ladder still asks for every
// ungranted write.
func writeTargetOf(c event.ToolCall) (string, bool) {
	if c.Name != "write_file" && c.Name != "edit_file" {
		return "", false
	}
	var m struct {
		Path *string `json:"path"`
	}
	if err := json.Unmarshal(c.Args, &m); err != nil || m.Path == nil {
		return "", false
	}
	return *m.Path, true
}
