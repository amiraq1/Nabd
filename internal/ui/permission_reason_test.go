package ui

import (
	"strings"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/presentation"
)

func TestRenderPermissionReasonUsesCatalog(t *testing.T) {
	event := agent.Event{
		Type:   agent.PermAsk,
		Call:   &agent.ToolCall{Name: "bash"},
		Text:   "permission required",
		Reason: agent.PermissionReasonRequired,
	}
	got := RenderEvent(event, DefaultWidth)
	want := presentation.PermissionReasonText(event)
	if want == "" || !strings.Contains(got, want) {
		t.Fatalf("rendered permission = %q, want catalog text %q", got, want)
	}
	if strings.Contains(got, event.Text) {
		t.Fatalf("English fallback overrode catalog: %q", got)
	}
}

func TestRenderLegacyPermissionTextUnchanged(t *testing.T) {
	const legacy = "مسموح قديم"
	got := RenderEvent(agent.Event{
		Type: agent.PermReply,
		Text: legacy,
	}, DefaultWidth)
	if !strings.Contains(got, legacy) {
		t.Fatalf("legacy permission text was not preserved: %q", got)
	}
}

func TestPermissionModalShowsLocalizedReason(t *testing.T) {
	m := newPermissionModal()
	m.open(&agent.ToolCall{Name: "bash"}, "يتطلب موافقة")
	got := m.view(80)
	if !strings.Contains(got, "يتطلب موافقة") {
		t.Fatalf("modal omitted permission reason: %q", got)
	}
}
