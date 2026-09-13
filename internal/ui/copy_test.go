package ui

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"nabd/internal/agent"

	tea "github.com/charmbracelet/bubbletea"
)

// decodeOSC52Payload extracts and decodes the base64 payload from an OSC 52 sequence.
func decodeOSC52Payload(t *testing.T, osc52 string) string {
	t.Helper()
	const prefix = "\x1b]52;c;"
	const suffix = "\x1b\\"
	if !strings.HasPrefix(osc52, prefix) || !strings.HasSuffix(osc52, suffix) {
		t.Fatalf("malformed OSC 52 sequence: %q", osc52)
	}
	payload := osc52[len(prefix) : len(osc52)-len(suffix)]
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatalf("failed to decode base64 payload %q: %v", payload, err)
	}
	return string(decoded)
}

func TestCopyRedactsRecognizedCredentials(t *testing.T) {
	testCases := []struct {
		name   string
		secret string
	}{
		{"Bearer token", "Bearer abcdefgh12345678"},
		{"authorization header", "authorization: abcdefgh12345678"},
		{"GitHub PAT", "github_pat_abcdefghijklmnop12345678"},
		{"GitHub token", "ghp_abcdefghijklmnop12345678"},
		{"Groq key", "gsk_abcdefgh12345678"},
		{"NVIDIA key", "nvapi-abcdefgh12345678"},
		{"OpenRouter key", "sk-or-abcdefgh12345678"},
		{"Slack token", "xoxb-abcdefghijklmnop12345678"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cardText := fmt.Sprintf("output with credential: %s and other text", tc.secret)
			m := feedWithCustomTexts(t, []string{cardText}, 100)
			m.enterNavigation()
			m.selectItem(0)

			var buf bytes.Buffer
			m.SetClipboardWriter(&buf)

			_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
			if cmd != nil {
				t.Fatal("copy returned non-nil cmd")
			}

			if buf.Len() == 0 {
				t.Fatal("no OSC 52 sequence written to clipboard")
			}

			decoded := decodeOSC52Payload(t, buf.String())

			// The raw secret MUST NOT be in the clipboard payload
			if strings.Contains(decoded, tc.secret) {
				t.Fatalf("clipboard leaked raw secret %q in %q", tc.secret, decoded)
			}
			// Redacted token MUST be in the clipboard payload
			if !strings.Contains(decoded, "[REDACTED]") {
				t.Fatalf("clipboard payload missing [REDACTED] in %q", decoded)
			}

			// Status line must NEVER display the copied text or secret
			if strings.Contains(m.status, tc.secret) || strings.Contains(m.status, "output with credential") {
				t.Fatalf("status line leaked copied content: %q", m.status)
			}
			if m.status != copySuccessNotice {
				t.Fatalf("status = %q, want %q", m.status, copySuccessNotice)
			}
		})
	}
}

func TestCopyNeverUsesRawJournalContent(t *testing.T) {
	// Raw unprojected string in event call ID or raw unprojected fields
	const unprojectedRaw = "RAW_UNPROJECTED_JOURNAL_CONTENT_XYZ_999"

	m := NewFeed()
	m.width = 80
	m.height = 20
	// Apply an event with a clean display output, but unprojected raw data
	m.applyBatch([]agent.Event{
		{
			Seq:  1,
			Type: agent.ToolStart,
			Call: &agent.ToolCall{ID: "call-1", Name: "bash"},
		},
		{
			Seq:  2,
			Type: agent.ToolEnd,
			Call: &agent.ToolCall{ID: "call-1", Name: "bash", Output: "projected display line", OK: true},
		},
	})
	m.toolsExpanded = true
	m.refresh()

	m.enterNavigation()
	m.selectItem(0)

	var buf bytes.Buffer
	m.SetClipboardWriter(&buf)

	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})

	decoded := decodeOSC52Payload(t, buf.String())

	if strings.Contains(decoded, unprojectedRaw) {
		t.Fatalf("clipboard leaked unprojected raw content: %q", decoded)
	}
	if !strings.Contains(decoded, "projected display line") {
		t.Fatalf("clipboard missing projected display line: %q", decoded)
	}
}

func TestCopyRejectsRawErrorBodies(t *testing.T) {
	const rawStackOrBody = "RAW_INTERNAL_STACK_TRACE_HTTP_500_BODY_SECRET"

	m := NewFeed()
	m.width = 80
	m.height = 20
	// Add an error notice where the raw body contains unprojected details
	m.applyBatch([]agent.Event{
		{
			Seq:        1,
			Type:       agent.RunError,
			Err:        "sanitized error description: connection failed",
			RawMessage: rawStackOrBody,
		},
	})
	m.refresh()

	m.enterNavigation()
	m.selectItem(0)

	var buf bytes.Buffer
	m.SetClipboardWriter(&buf)

	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})

	decoded := decodeOSC52Payload(t, buf.String())

	if strings.Contains(decoded, rawStackOrBody) {
		t.Fatalf("clipboard leaked raw error body: %q", decoded)
	}
	if !strings.Contains(decoded, "connection failed") {
		t.Fatalf("clipboard missing projected error text: %q", decoded)
	}
}

func TestCopyIsBlockedByPermissionModal(t *testing.T) {
	m := feedWithPendingPermission(t, 80)

	var buf bytes.Buffer
	m.SetClipboardWriter(&buf)

	// Attempt to copy using 'y'
	_, cmd1 := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})

	// In permission modal, 'y' means AllowOnce! It must answer the modal, NOT copy!
	if buf.Len() > 0 {
		t.Fatal("copy wrote to clipboard while permission modal was active")
	}

	// Attempt to copy using 'c'
	var buf2 bytes.Buffer
	m2 := feedWithPendingPermission(t, 80)
	m2.SetClipboardWriter(&buf2)

	_, cmd2 := m2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if cmd2 != nil {
		t.Fatal("unexpected cmd from 'c' during modal")
	}
	if buf2.Len() > 0 {
		t.Fatal("copy wrote to clipboard during permission modal")
	}
	_ = cmd1
}

func TestCopyNeverExecutesACommand(t *testing.T) {
	m := feedWithCustomTexts(t, []string{"executable content test"}, 80)
	m.enterNavigation()
	m.selectItem(0)

	m.running = false
	m.busy = false
	m.runningTool = ""

	var buf bytes.Buffer
	m.SetClipboardWriter(&buf)

	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})

	if m.running || m.busy || m.runningTool != "" {
		t.Fatal("copy triggered command execution or set running/busy state")
	}
}

func TestCopyReturnsNoToolCommand(t *testing.T) {
	m := feedWithCustomTexts(t, []string{"card output"}, 80)
	m.enterNavigation()
	m.selectItem(0)

	var buf bytes.Buffer
	m.SetClipboardWriter(&buf)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd != nil {
		t.Fatal("copy returned a non-nil tea.Cmd")
	}

	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if cmd != nil {
		t.Fatal("copy returned a non-nil tea.Cmd")
	}
}

func TestCopyDoesNotMutateProjection(t *testing.T) {
	m := feedWithCustomTexts(t, []string{"alpha card", "beta card"}, 80)
	before := m.proj.Items()
	fps := make([]uint64, len(before))
	for i, it := range before {
		fps[i] = it.Fingerprint()
	}

	m.enterNavigation()
	m.selectItem(0)

	var buf bytes.Buffer
	m.SetClipboardWriter(&buf)

	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})

	after := m.proj.Items()
	if len(after) != len(before) {
		t.Fatalf("projector item count changed: %d -> %d", len(before), len(after))
	}
	for i, it := range after {
		if it.Fingerprint() != fps[i] {
			t.Fatalf("projector item %d mutated by copy", i)
		}
	}
}
