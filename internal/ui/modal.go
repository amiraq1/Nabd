package ui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"nabd/internal/agent"
)

// PermissionChoice represents an selectable action in the permission modal.
type PermissionChoice struct {
	Decision agent.Decision
	Label    string
	KeyHint  string
}

// PermissionModal manages state and visual rendering of the permission modal.
type PermissionModal struct {
	visible         bool
	call            *agent.ToolCall
	selected        int
	decisionPending bool
}

func newPermissionModal() *PermissionModal {
	return &PermissionModal{
		selected: -1,
	}
}

func (m *PermissionModal) open(call *agent.ToolCall) {
	m.visible = true
	m.call = call
	m.selected = -1
	m.decisionPending = false
}

func (m *PermissionModal) close() {
	m.visible = false
	m.call = nil
	m.selected = -1
	m.decisionPending = false
}

func (m *PermissionModal) toolName() string {
	if m.call == nil {
		return ""
	}
	return m.call.Name
}

func (m *PermissionModal) supportsSession() bool {
	return m.toolName() != "bash"
}

func (m *PermissionModal) choices() []PermissionChoice {
	if m.supportsSession() {
		return []PermissionChoice{
			{Decision: agent.AllowOnce, Label: "Allow Once", KeyHint: "y"},
			{Decision: agent.AllowSession, Label: "Allow Session", KeyHint: "a"},
			{Decision: agent.Deny, Label: "Deny", KeyHint: "n / esc"},
		}
	}
	return []PermissionChoice{
		{Decision: agent.AllowOnce, Label: "Allow Once", KeyHint: "y"},
		{Decision: agent.Deny, Label: "Deny", KeyHint: "n / esc"},
	}
}

func (m *PermissionModal) currentDecision() agent.Decision {
	ch := m.choices()
	if m.selected < 0 || m.selected >= len(ch) {
		return agent.Deny
	}
	return ch[m.selected].Decision
}

func (m *PermissionModal) nextChoice() {
	ch := m.choices()
	if len(ch) == 0 {
		return
	}
	if m.selected < 0 {
		m.selected = 0
		return
	}
	m.selected = (m.selected + 1) % len(ch)
}

func (m *PermissionModal) prevChoice() {
	ch := m.choices()
	if len(ch) == 0 {
		return
	}
	if m.selected < 0 {
		m.selected = len(ch) - 1
		return
	}
	m.selected = (m.selected - 1 + len(ch)) % len(ch)
}

// safeArgs formats arguments for display without splitting runes or leaking secrets.
func safeArgs(args string, maxRunes int) string {
	if args == "" || maxRunes <= 0 {
		return ""
	}
	// Normalize multiline to single readable preview
	single := strings.ReplaceAll(args, "\r", "")
	single = strings.ReplaceAll(single, "\n", " ")
	single = strings.TrimSpace(single)

	if utf8.RuneCountInString(single) <= maxRunes {
		return single
	}
	runes := []rune(single)
	if maxRunes <= 1 {
		return "…"
	}
	return string(runes[:maxRunes-1]) + "…"
}

// view renders the modal card with ASCII-safe formatting adhering to the symbol whitelist.
func (m *PermissionModal) view(width int) string {
	if !m.visible && !m.decisionPending {
		return ""
	}

	w := width
	if w < 20 {
		w = 20
	}

	var b strings.Builder

	// Top banner
	b.WriteString(warn.Render("── Permission Required ────────────────────────"))
	b.WriteByte('\n')

	// Tool name and execution status
	tool := m.toolName()
	if tool == "" {
		tool = "unknown"
	}
	b.WriteString(fmt.Sprintf("Tool: %s (not executed yet)\n", bold.Render(tool)))

	// Arguments
	if m.call != nil && len(m.call.Args) > 0 {
		argDisplay := safeArgs(string(m.call.Args), max(10, w-10))
		b.WriteString(fmt.Sprintf("Args: %s\n", dim.Render(argDisplay)))
	}

	// Submission state or interactive choices
	if m.decisionPending {
		b.WriteString(dim.Render("· submitting decision…\n"))
	} else {
		ch := m.choices()
		var choiceStrings []string
		for i, c := range ch {
			if i == m.selected {
				choiceStrings = append(choiceStrings, good.Render(fmt.Sprintf("[*] %s", c.Label)))
			} else {
				choiceStrings = append(choiceStrings, dim.Render(fmt.Sprintf("[ ] %s", c.Label)))
			}
		}
		b.WriteString(strings.Join(choiceStrings, "   "))
		b.WriteByte('\n')
	}

	// Bottom border
	b.WriteString(dim.Render("───────────────────────────────────────────────"))
	return b.String()
}

// lineCount returns the number of rendered lines in view().
func (m *PermissionModal) lineCount() int {
	if !m.visible && !m.decisionPending {
		return 0
	}
	count := 4 // top border, tool line, choices line, bottom border
	if m.call != nil && len(m.call.Args) > 0 {
		count++ // args line
	}
	return count
}
