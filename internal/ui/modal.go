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

func (m *PermissionModal) choices() []PermissionChoice {
	return []PermissionChoice{
		{Decision: agent.AllowOnce, Label: "Allow Once", KeyHint: "y"},
		{Decision: agent.AllowSession, Label: "Allow Session", KeyHint: "a"},
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
	cardW := w
	if cardW > 60 {
		cardW = 60
	}

	// formatRow wraps text in vertical borders: | <content> |
	formatRow := func(s string) string {
		rCount := utf8.RuneCountInString(s)
		avail := cardW - 4
		if rCount > avail {
			s = truncate(s, avail)
			rCount = utf8.RuneCountInString(s)
		}
		pad := max(0, avail-rCount)
		return "| " + s + strings.Repeat(" ", pad) + " |"
	}

	var lines []string

	// Top border
	title := "+-- Permission Required "
	if utf8.RuneCountInString(title) > cardW-2 {
		title = "+-- Permission "
	}
	dashCount := max(0, cardW-utf8.RuneCountInString(title)-1)
	lines = append(lines, warn.Render(title+strings.Repeat("-", dashCount)+"+"))

	// Tool name
	tool := m.toolName()
	if tool == "" {
		tool = "unknown"
	}
	lines = append(lines, formatRow(fmt.Sprintf("Tool: %s (not executed yet)", tool)))

	// Arguments line
	hasArgs := m.call != nil && len(m.call.Args) > 0 && string(m.call.Args) != "{}" && string(m.call.Args) != `""`
	if hasArgs {
		argDisplay := safeArgs(string(m.call.Args), cardW-14)
		lines = append(lines, formatRow(fmt.Sprintf("Args: %s", argDisplay)))
	}

	// Blank row
	lines = append(lines, formatRow(""))

	// Choices
	if m.decisionPending {
		lines = append(lines, formatRow("· submitting decision…"))
	} else {
		for i, c := range m.choices() {
			mark := "[ ]"
			if i == m.selected {
				mark = "[*]"
			}
			rowText := fmt.Sprintf("  %s %s (%s)", mark, c.Label, c.KeyHint)
			lines = append(lines, formatRow(rowText))
		}
	}

	// Blank row
	lines = append(lines, formatRow(""))

	// Hint row
	if m.decisionPending {
		lines = append(lines, formatRow("waiting for decision to apply…"))
	} else if m.selected >= 0 {
		lines = append(lines, formatRow("Enter confirm · Up/Down select · y/a/n direct"))
	} else {
		lines = append(lines, formatRow("y/a/n direct · Up/Down select · Enter confirm"))
	}

	// Bottom border
	lines = append(lines, dim.Render("+"+strings.Repeat("-", cardW-2)+"+"))

	return strings.Join(lines, "\n")
}

// lineCount returns the number of rendered lines in view().
func (m *PermissionModal) lineCount() int {
	if !m.visible && !m.decisionPending {
		return 0
	}
	count := 7 // top, tool, blank, choices(1 or 3), blank, hint, bottom
	if !m.decisionPending {
		count += 2 // 3 choices instead of 1
	}
	hasArgs := m.call != nil && len(m.call.Args) > 0 && string(m.call.Args) != "{}" && string(m.call.Args) != `""`
	if hasArgs {
		count++
	}
	return count
}
