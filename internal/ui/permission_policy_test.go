package ui

import (
	"strings"
	"testing"

	"nabd/internal/agent"
)

func TestPermissionModalHidesUnsupportedSessionGrant(t *testing.T) {
	m := newPermissionModal()
	m.open(&agent.ToolCall{
		ID:                  "c1",
		Name:                "bash",
		SessionGrantKnown:   true,
		SessionGrantAllowed: false,
	})
	if len(m.choices()) != 2 {
		t.Fatalf("choices = %+v, want once and deny only", m.choices())
	}
	for _, choice := range m.choices() {
		if choice.Decision == agent.AllowSession {
			t.Fatal("unsupported AllowSession choice was exposed")
		}
	}
	view := m.view(80)
	if strings.Contains(view, "Allow Session") || strings.Contains(view, "a session") {
		t.Fatalf("view exposes unsupported session grant: %q", view)
	}
	if !strings.Contains(view, "scope: this request only") {
		t.Fatalf("view omits explicit request scope: %q", view)
	}
}

func TestPermissionModalShowsSupportedSessionGrantScope(t *testing.T) {
	m := newPermissionModal()
	m.open(&agent.ToolCall{
		ID:                  "c1",
		Name:                "write_file",
		SessionGrantKnown:   true,
		SessionGrantAllowed: true,
	})
	if len(m.choices()) != 3 {
		t.Fatalf("choices = %+v, want once/session/deny", m.choices())
	}
	view := m.view(100)
	if !strings.Contains(view, "Allow Session") || !strings.Contains(view, "scope: this tool") {
		t.Fatalf("view omits supported session scope: %q", view)
	}
}
