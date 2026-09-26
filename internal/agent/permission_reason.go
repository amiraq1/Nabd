package agent

// PermissionReason is the stable, language-neutral reason carried by
// permission journal events. Text remains an English compatibility fallback;
// presentation owns the localized rendering.
type PermissionReason string

const (
	PermissionReasonToolNoName         PermissionReason = "tool_no_name"
	PermissionReasonUnknownTool        PermissionReason = "unknown_tool"
	PermissionReasonPlanReadOnly       PermissionReason = "plan_read_only"
	PermissionReasonSessionGrant       PermissionReason = "session_grant"
	PermissionReasonPolicyDenied       PermissionReason = "policy_denied"
	PermissionReasonRequired           PermissionReason = "permission_required"
	PermissionReasonUnknownOrForbidden PermissionReason = "unknown_or_forbidden_tool"
	PermissionReasonNoPrompt           PermissionReason = "no_prompt_interface"
)

var knownPermissionReasons = map[PermissionReason]struct{}{
	PermissionReasonToolNoName:         {},
	PermissionReasonUnknownTool:        {},
	PermissionReasonPlanReadOnly:       {},
	PermissionReasonSessionGrant:       {},
	PermissionReasonPolicyDenied:       {},
	PermissionReasonRequired:           {},
	PermissionReasonUnknownOrForbidden: {},
	PermissionReasonNoPrompt:           {},
}

// Valid reports whether r belongs to the closed permission-reason vocabulary.
// The empty value is intentionally not valid: it means a legacy event whose
// Text field remains authoritative.
func (r PermissionReason) Valid() bool {
	_, ok := knownPermissionReasons[r]
	return ok
}
