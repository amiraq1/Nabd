package ui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"nabd/internal/agent"

	"github.com/charmbracelet/x/ansi"
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
// Enforces: Sanitize -> Redact -> Truncate.
func safeArgs(args string, maxRunes int) string {
	if args == "" || maxRunes <= 0 {
		return ""
	}
	clean := SanitizeForDisplay(args, DisplayPolicy{
		AllowNewline: false,
		AllowTab:     false,
		Redact:       true,
	})
	clean = strings.TrimSpace(clean)

	if utf8.RuneCountInString(clean) <= maxRunes {
		return clean
	}
	runes := []rune(clean)
	if maxRunes <= 1 {
		return "…"
	}
	return string(runes[:maxRunes-1]) + "…"
}

// view renders the modal card with ASCII-safe formatting adhering to the symbol whitelist.
// Supports internal degradation when maxRows is specified to preserve visibility
// on small terminals (title + selected choice + confirm hint minimum).
func (m *PermissionModal) view(width int, maxRows ...int) string {
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
	// Uses ansi.StringWidth for accurate cell-width measurement.
	formatRow := func(s string) string {
		avail := cardW - 4
		if avail < 0 {
			avail = 0
		}
		sw := ansi.StringWidth(s)
		if sw > avail {
			s = ansi.Truncate(s, avail, "…")
			sw = ansi.StringWidth(s)
		}
		pad := max(0, avail-sw)
		return "| " + s + strings.Repeat(" ", pad) + " |"
	}

	tool := m.toolName()
	if tool == "" {
		tool = "unknown"
	}

	targetRows := m.lineCount(maxRows...)

	// Level 4: Modal minimum (3 rows: title+tool, selected choice, bottom with hint)
	if targetRows <= 3 {
		title := fmt.Sprintf("+-- Permission: %s ", tool)
		if ansi.StringWidth(title) > cardW-2 {
			title = "+-- Perm: " + ansi.Truncate(tool, max(4, cardW-13), "…") + " "
		}
		dashCount := max(0, cardW-ansi.StringWidth(title)-1)
		topBorder := warn.Render(title + strings.Repeat("-", dashCount) + "+")

		ch := m.choices()
		selIdx := m.selected
		if selIdx < 0 || selIdx >= len(ch) {
			selIdx = 0
		}
		selChoice := ch[selIdx]
		mark := "[*]"
		if m.decisionPending {
			mark = "[·]"
			selChoice.Label = "submitting"
			selChoice.KeyHint = "…"
		}
		choiceLine := formatRow(fmt.Sprintf("%s %s (%s)", mark, selChoice.Label, selChoice.KeyHint))

		hint := "+-- Enter confirm · y/a/n "
		if ansi.StringWidth(hint) > cardW-2 {
			hint = "+-- Enter · y/a/n "
		}
		hintDashes := max(0, cardW-ansi.StringWidth(hint)-1)
		bottomBorder := dim.Render(hint + strings.Repeat("-", hintDashes) + "+")

		return topBorder + "\n" + choiceLine + "\n" + bottomBorder
	}

	// Level 3: 4 rows (top, tool, selected choice, bottom with hint)
	if targetRows == 4 {
		title := "+-- Permission Required "
		if ansi.StringWidth(title) > cardW-2 {
			title = "+-- Permission "
		}
		dashCount := max(0, cardW-ansi.StringWidth(title)-1)
		topBorder := warn.Render(title + strings.Repeat("-", dashCount) + "+")
		toolLine := formatRow(fmt.Sprintf("Tool: %s", tool))

		ch := m.choices()
		selIdx := m.selected
		if selIdx < 0 || selIdx >= len(ch) {
			selIdx = 0
		}
		selChoice := ch[selIdx]
		mark := "[*]"
		if m.decisionPending {
			mark = "[·]"
			selChoice.Label = "submitting"
			selChoice.KeyHint = "…"
		}
		choiceLine := formatRow(fmt.Sprintf("  %s %s (%s)", mark, selChoice.Label, selChoice.KeyHint))

		hint := "+-- Enter confirm · y/a/n "
		if ansi.StringWidth(hint) > cardW-2 {
			hint = "+-- Enter · y/a/n "
		}
		hintDashes := max(0, cardW-ansi.StringWidth(hint)-1)
		bottomBorder := dim.Render(hint + strings.Repeat("-", hintDashes) + "+")

		return topBorder + "\n" + toolLine + "\n" + choiceLine + "\n" + bottomBorder
	}

	// Level 2: 5 or 6 rows (top, tool, [args], selected choice, hint, bottom)
	if targetRows <= 6 {
		var lines []string
		title := "+-- Permission Required "
		if ansi.StringWidth(title) > cardW-2 {
			title = "+-- Permission "
		}
		dashCount := max(0, cardW-ansi.StringWidth(title)-1)
		lines = append(lines, warn.Render(title+strings.Repeat("-", dashCount)+"+"))
		lines = append(lines, formatRow(fmt.Sprintf("Tool: %s (not executed yet)", tool)))

		hasArgs := m.call != nil && len(m.call.Args) > 0 && string(m.call.Args) != "{}" && string(m.call.Args) != `""`
		if targetRows == 6 && hasArgs {
			argDisplay := safeArgs(string(m.call.Args), cardW-14)
			lines = append(lines, formatRow(fmt.Sprintf("Args: %s", argDisplay)))
		}

		ch := m.choices()
		selIdx := m.selected
		if selIdx < 0 || selIdx >= len(ch) {
			selIdx = 0
		}
		selChoice := ch[selIdx]
		mark := "[*]"
		if m.decisionPending {
			mark = "[·]"
			selChoice.Label = "submitting decision…"
			selChoice.KeyHint = ""
		}
		lines = append(lines, formatRow(fmt.Sprintf("  %s %s (%s)", mark, selChoice.Label, selChoice.KeyHint)))
		lines = append(lines, formatRow("Enter confirm · Up/Down select · y/a/n direct"))
		lines = append(lines, dim.Render("+"+strings.Repeat("-", cardW-2)+"+"))
		return strings.Join(lines, "\n")
	}

	// Level 1 or 0: 7+ rows
	var lines []string
	title := "+-- Permission Required "
	if ansi.StringWidth(title) > cardW-2 {
		title = "+-- Permission "
	}
	dashCount := max(0, cardW-ansi.StringWidth(title)-1)
	lines = append(lines, warn.Render(title+strings.Repeat("-", dashCount)+"+"))
	lines = append(lines, formatRow(fmt.Sprintf("Tool: %s (not executed yet)", tool)))

	hasArgs := m.call != nil && len(m.call.Args) > 0 && string(m.call.Args) != "{}" && string(m.call.Args) != `""`
	fullWithoutCompression := m.lineCount()
	includeArgs := hasArgs && targetRows >= fullWithoutCompression-2
	if includeArgs {
		argDisplay := safeArgs(string(m.call.Args), cardW-14)
		lines = append(lines, formatRow(fmt.Sprintf("Args: %s", argDisplay)))
	}

	includeBlanks := targetRows == fullWithoutCompression
	if includeBlanks {
		lines = append(lines, formatRow(""))
	}

	if m.decisionPending {
		lines = append(lines, formatRow("· submitting decision…"))
	} else {
		for i, c := range m.choices() {
			mark := "[ ]"
			if i == m.selected {
				mark = "[*]"
			}
			lines = append(lines, formatRow(fmt.Sprintf("  %s %s (%s)", mark, c.Label, c.KeyHint)))
		}
	}

	if includeBlanks {
		lines = append(lines, formatRow(""))
	}

	if m.decisionPending {
		lines = append(lines, formatRow("waiting for decision to apply…"))
	} else if m.selected >= 0 {
		lines = append(lines, formatRow("Enter confirm · Up/Down select · y/a/n direct"))
	} else {
		lines = append(lines, formatRow("y/a/n direct · Up/Down select · Enter confirm"))
	}

	lines = append(lines, dim.Render("+"+strings.Repeat("-", cardW-2)+"+"))
	return strings.Join(lines, "\n")
}

// lineCount returns the number of rendered lines in view().
// When maxRows is provided and positive, it applies the internal degradation ladder:
// 1. drop blank rows
// 2. drop arguments row
// 3. show only selected choice
// 4. modal minimum (3 rows: title+tool, selected choice, confirm hint+border)
func (m *PermissionModal) lineCount(maxRows ...int) int {
	if !m.visible && !m.decisionPending {
		return 0
	}
	hasArgs := m.call != nil && len(m.call.Args) > 0 && string(m.call.Args) != "{}" && string(m.call.Args) != `""`
	full := 7 // top, tool, blank, choices, blank, hint, bottom
	if !m.decisionPending {
		full += 2 // 3 choices instead of 1
	}
	if hasArgs {
		full++
	}

	if len(maxRows) == 0 || maxRows[0] <= 0 || maxRows[0] >= full {
		return full
	}

	limit := maxRows[0]
	if limit <= 3 {
		return 3
	}
	if limit == 4 {
		return 4
	}
	if limit == 5 {
		return 5
	}
	noBlanks := full - 2
	if limit < noBlanks {
		if hasArgs && limit == noBlanks-1 {
			return limit
		}
		if limit < 6 {
			return 5
		}
		return 6
	}
	return limit
}
