package presentation

import (
	"testing"

	"nabd/internal/event"
)

func TestPermissionReasonCatalogCoversClosedVocabulary(t *testing.T) {
	reasons := []event.PermissionReason{
		event.PermissionReasonToolNoName,
		event.PermissionReasonUnknownTool,
		event.PermissionReasonPlanReadOnly,
		event.PermissionReasonSessionGrant,
		event.PermissionReasonPolicyDenied,
		event.PermissionReasonRequired,
		event.PermissionReasonUnknownOrForbidden,
		event.PermissionReasonNoPrompt,
	}
	for _, reason := range reasons {
		got := PermissionReasonText(event.Event{Reason: reason, Text: "english fallback"})
		if got == "" || got == "english fallback" {
			t.Fatalf("reason %q has no localized catalog entry: %q", reason, got)
		}
	}
}

func TestPermissionReasonTextLegacyAndUnknownBehavior(t *testing.T) {
	const legacy = "مسموح قديم"
	if got := PermissionReasonText(event.Event{Text: legacy}); got != legacy {
		t.Fatalf("legacy text = %q, want %q", got, legacy)
	}
	if got := PermissionReasonText(event.Event{
		Reason: event.PermissionReason("future_unreviewed_reason"),
		Text:   "must not become authoritative",
	}); got != "" {
		t.Fatalf("unknown reason rendered %q, want fail-closed empty text", got)
	}
}

func TestProjectorCarriesLocalizedPermissionReason(t *testing.T) {
	p := NewProjector()
	items, err := p.Build([]event.Event{{
		Seq:    1,
		Type:   event.PermAsk,
		Call:   &event.ToolCall{ID: "c1", Name: "bash"},
		Text:   "permission required",
		Reason: event.PermissionReasonRequired,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Perm == nil {
		t.Fatalf("items = %#v", items)
	}
	if items[0].Perm.Reason != permissionReasonArabic[event.PermissionReasonRequired] {
		t.Fatalf("card reason = %q", items[0].Perm.Reason)
	}
}
