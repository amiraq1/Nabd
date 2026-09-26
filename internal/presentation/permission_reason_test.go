package presentation

import (
	"testing"

	"nabd/internal/agent"
)

func TestPermissionReasonCatalogCoversClosedVocabulary(t *testing.T) {
	reasons := []agent.PermissionReason{
		agent.PermissionReasonToolNoName,
		agent.PermissionReasonUnknownTool,
		agent.PermissionReasonPlanReadOnly,
		agent.PermissionReasonSessionGrant,
		agent.PermissionReasonPolicyDenied,
		agent.PermissionReasonRequired,
		agent.PermissionReasonUnknownOrForbidden,
		agent.PermissionReasonNoPrompt,
	}
	for _, reason := range reasons {
		got := PermissionReasonText(agent.Event{Reason: reason, Text: "english fallback"})
		if got == "" || got == "english fallback" {
			t.Fatalf("reason %q has no localized catalog entry: %q", reason, got)
		}
	}
}

func TestPermissionReasonTextLegacyAndUnknownBehavior(t *testing.T) {
	const legacy = "مسموح قديم"
	if got := PermissionReasonText(agent.Event{Text: legacy}); got != legacy {
		t.Fatalf("legacy text = %q, want %q", got, legacy)
	}
	if got := PermissionReasonText(agent.Event{
		Reason: agent.PermissionReason("future_unreviewed_reason"),
		Text:   "must not become authoritative",
	}); got != "" {
		t.Fatalf("unknown reason rendered %q, want fail-closed empty text", got)
	}
}

func TestProjectorCarriesLocalizedPermissionReason(t *testing.T) {
	p := NewProjector()
	items, err := p.Build([]agent.Event{{
		Seq:    1,
		Type:   agent.PermAsk,
		Call:   &agent.ToolCall{ID: "c1", Name: "bash"},
		Text:   "permission required",
		Reason: agent.PermissionReasonRequired,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Perm == nil {
		t.Fatalf("items = %#v", items)
	}
	if items[0].Perm.Reason != permissionReasonArabic[agent.PermissionReasonRequired] {
		t.Fatalf("card reason = %q", items[0].Perm.Reason)
	}
}
