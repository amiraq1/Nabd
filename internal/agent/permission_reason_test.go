package agent

import (
	"encoding/json"
	"testing"
)

func TestPermissionReasonClosedVocabulary(t *testing.T) {
	reasons := []PermissionReason{
		PermissionReasonToolNoName,
		PermissionReasonUnknownTool,
		PermissionReasonPlanReadOnly,
		PermissionReasonSessionGrant,
		PermissionReasonPolicyDenied,
		PermissionReasonRequired,
		PermissionReasonUnknownOrForbidden,
		PermissionReasonNoPrompt,
	}
	seen := map[PermissionReason]bool{}
	for _, reason := range reasons {
		if reason == "" || !reason.Valid() {
			t.Fatalf("declared permission reason %q is not valid", reason)
		}
		if seen[reason] {
			t.Fatalf("duplicate permission reason %q", reason)
		}
		seen[reason] = true
	}
	if PermissionReason("").Valid() {
		t.Fatal("empty reason is the legacy marker, not a vocabulary value")
	}
	if PermissionReason("future_unreviewed_reason").Valid() {
		t.Fatal("unknown reason was accepted")
	}
}

func TestLegacyPermissionEventKeepsTextAndEmptyReason(t *testing.T) {
	raw := []byte(`{"seq":7,"type":"perm_reply","text":"مسموح قديم","decision":"deny"}`)
	var event Event
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
