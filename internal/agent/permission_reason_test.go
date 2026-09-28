package agent

import (
	"encoding/json"
	"nabd/internal/event"
	"testing"
)

func TestPermissionReasonClosedVocabulary(t *testing.T) {
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
	seen := map[event.PermissionReason]bool{}
	for _, reason := range reasons {
		if reason == "" || !reason.Valid() {
			t.Fatalf("declared permission reason %q is not valid", reason)
		}
		if seen[reason] {
			t.Fatalf("duplicate permission reason %q", reason)
		}
		seen[reason] = true
	}
	if event.PermissionReason("").Valid() {
		t.Fatal("empty reason is the legacy marker, not a vocabulary value")
	}
	if event.PermissionReason("future_unreviewed_reason").Valid() {
		t.Fatal("unknown reason was accepted")
	}
}

func TestLegacyPermissionEventKeepsTextAndEmptyReason(t *testing.T) {
	raw := []byte(`{"seq":7,"type":"perm_reply","text":"مسموح قديم","decision":"deny"}`)
	var event event.Event
	if err := json.Unmarshal(raw, &event); err != nil {
		t.Fatal(err)
	}
	if event.Text != "مسموح قديم" {
		t.Fatalf("legacy text = %q", event.Text)
	}
	if event.Reason != "" {
		t.Fatalf("legacy reason = %q, want empty", event.Reason)
	}
}
