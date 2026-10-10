package ui

import (
	"strings"
	"testing"

	"nabd/internal/event"
	"nabd/internal/presentation"
)

func TestRenderPermissionReasonUsesCatalog(t *testing.T) {
	t.Setenv("NABD_RTL", "off")
	resetRTLModeCache()
	ev := event.Event{
		Type:   event.PermAsk,
		Call:   &event.ToolCall{Name: "bash"},
		Text:   "permission required",
		Reason: event.PermissionReasonRequired,
	}
	got := RenderEvent(ev, DefaultWidth)
	want := presentation.PermissionReasonText(ev)
	if want == "" || !strings.Contains(got, want) {
		t.Fatalf("rendered permission = %q, want catalog text %q", got, want)
	}
	if strings.Contains(got, ev.Text) {
		t.Fatalf("English fallback overrode catalog: %q", got)
	}
}

func TestRenderLegacyPermissionTextUnchanged(t *testing.T) {
	const legacy = "مسموح قديم"
	got := RenderEvent(event.Event{
		Type: event.PermReply,
		Text: legacy,
	}, DefaultWidth)
	if !strings.Contains(got, legacy) {
		t.Fatalf("legacy permission text was not preserved: %q", got)
	}
}

func TestPermissionModalShowsLocalizedReason(t *testing.T) {
	t.Setenv("NABD_RTL", "off")
	resetRTLModeCache()
	m := newPermissionModal()
	m.open(&event.ToolCall{Name: "bash"}, "يتطلب موافقة")
	got := m.view(80)
	if !strings.Contains(got, "يتطلب موافقة") {
		t.Fatalf("modal omitted permission reason: %q", got)
	}
}
