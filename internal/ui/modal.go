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

// hasArgs reports whether the pending call carries arguments worth showing.
func (m *PermissionModal) hasArgs() bool {
	return m.call != nil && len(m.call.Args) > 0 &&
		string(m.call.Args) != "{}" && string(m.call.Args) != `""`
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

// permModalLevel identifies which rendering path the modal takes. Each level
// emits a fixed, known number of rows for a given set of optional rows.
type permModalLevel int

const (
	// permLevelFull: top, tool, [args], [blank], choices, [blank], hint, bottom.
	permLevelFull permModalLevel = iota
	// permLevelCompact: top, tool, [args], selected choice, hint, bottom.
	permLevelCompact
	// permLevelToolRow: top, tool, selected choice, bottom (hint in the border).
	permLevelToolRow
	// permLevelMinimum: top (tool in the title), selected choice, bottom (hint
	// in the border). The floor: the modal must always say what is being asked
	// and how to answer it.
	permLevelMinimum
)

// permModalShape is the single source of truth for modal row accounting.
//
// shape() decides how many rows are rendered and which optional rows are
// included; lineCount() reports shape.rows, and view() renders exactly that
// shape. So the rows computeLayout reserves are the rows View emits by
// construction, not by two ladders happening to agree.
type permModalShape struct {
	level         permModalLevel
	rows          int
	includeArgs   bool
	includeBlanks bool
}

// shape resolves the rendering shape for the available height. When maxRows is
// absent or non-positive the modal renders at full height; otherwise the
// tallest shape that fits is chosen. Candidates only ever claim a height their
// rendering path actually fills, which is what makes the choice idempotent:
// shape(shape(limit).rows) == shape(limit).
func (m *PermissionModal) shape(maxRows ...int) permModalShape {
	if !m.visible && !m.decisionPending {
		return permModalShape{}
	}

	hasArgs := m.hasArgs()
	choiceRows := 3
	if m.decisionPending {
		// A pending decision collapses the three choices into one status row.
		choiceRows = 1
	}
	// top + tool + 2 blanks + choices + hint + bottom, plus the args row.
	full := 6 + choiceRows
	if hasArgs {
		full++
	}

	limit := 0
	if len(maxRows) > 0 {
		limit = maxRows[0]
	}
	if limit <= 0 || limit >= full {
		return permModalShape{
			level:         permLevelFull,
			rows:          full,
			includeArgs:   hasArgs,
			includeBlanks: true,
		}
	}

	// The degradation ladder, as the set of heights that can be filled exactly:
	// 1. drop the blank rows
	// 2. drop the args row
	// 3. show only the selected choice
	// 4. drop the tool row (tool name moves into the title)
	candidates := []permModalShape{
		{level: permLevelFull, rows: full - 2, includeArgs: hasArgs},
	}
	if hasArgs {
		candidates = append(candidates,
			permModalShape{level: permLevelFull, rows: full - 3},
			permModalShape{level: permLevelCompact, rows: 6, includeArgs: true},
		)
	}
	candidates = append(candidates,
		permModalShape{level: permLevelCompact, rows: 5},
		permModalShape{level: permLevelToolRow, rows: 4},
	)

	best := permModalShape{level: permLevelMinimum, rows: 3}
	for _, c := range candidates {
		if c.rows <= limit && c.rows > best.rows {
			best = c
		}
	}
	return best
}

// lineCount returns the number of rows view() renders for the same maxRows.
func (m *PermissionModal) lineCount(maxRows ...int) int {
	return m.shape(maxRows...).rows
}

// view renders the modal card with ASCII-safe formatting adhering to the symbol whitelist.
// It renders the shape resolved by shape(), so the row count always matches
// lineCount() for the same arguments.
func (m *PermissionModal) view(width int, maxRows ...int) string {
	sh := m.shape(maxRows...)
	if sh.rows == 0 {
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
	tool = SanitizeForDisplay(tool, DisplayPolicy{AllowNewline: false, Redact: true})

	// titleBorder renders the top border carrying the given title text.
	titleBorder := func(title string) string {
		dashCount := max(0, cardW-ansi.StringWidth(title)-1)
		return warn.Render(title + strings.Repeat("-", dashCount) + "+")
	}

	// standardTitle is the top border used by every level above the minimum.
	standardTitle := func() string {
		title := "+-- Permission Required "
		if ansi.StringWidth(title) > cardW-2 {
			title = "+-- Permission "
		}
		return titleBorder(title)
	}

	// hintBorder is the bottom border that also carries the key hints, used by
	// the two shortest levels where no separate hint row exists.
	hintBorder := func() string {
		hint := "+-- Enter confirm · y/a/n "
		if ansi.StringWidth(hint) > cardW-2 {
			hint = "+-- Enter · y/a/n "
		}
		hintDashes := max(0, cardW-ansi.StringWidth(hint)-1)
		return dim.Render(hint + strings.Repeat("-", hintDashes) + "+")
	}

	plainBorder := func() string {
		return dim.Render("+" + strings.Repeat("-", cardW-2) + "+")
	}

	// selectedChoice returns the choice to show when only one row is available.
	selectedChoice := func() PermissionChoice {
		ch := m.choices()
		idx := m.selected
		if idx < 0 || idx >= len(ch) {
			idx = 0
		}
		return ch[idx]
	}

	argsRow := func() string {
		return formatRow(fmt.Sprintf("Args: %s", safeArgs(string(m.call.Args), cardW-14)))
	}

	switch sh.level {
	case permLevelMinimum:
		// 3 rows: title carries the tool name, one choice, hint in the border.
		title := fmt.Sprintf("+-- Permission: %s ", tool)
		if ansi.StringWidth(title) > cardW-2 {
			title = "+-- Perm: " + ansi.Truncate(tool, max(4, cardW-13), "…") + " "
		}
		sel := selectedChoice()
		mark := "[*]"
		if m.decisionPending {
			mark = "[·]"
			sel.Label = "submitting"
			sel.KeyHint = "…"
		}
		choiceLine := formatRow(fmt.Sprintf("%s %s (%s)", mark, sel.Label, sel.KeyHint))
		return strings.Join([]string{titleBorder(title), choiceLine, hintBorder()}, "\n")

	case permLevelToolRow:
		// 4 rows: title, tool, one choice, hint in the border.
		sel := selectedChoice()
		mark := "[*]"
		if m.decisionPending {
			mark = "[·]"
			sel.Label = "submitting"
			sel.KeyHint = "…"
		}
		return strings.Join([]string{
			standardTitle(),
			formatRow(fmt.Sprintf("Tool: %s", tool)),
			formatRow(fmt.Sprintf("  %s %s (%s)", mark, sel.Label, sel.KeyHint)),
			hintBorder(),
		}, "\n")

	case permLevelCompact:
		// 5 or 6 rows: title, tool, [args], one choice, hint row, border.
		lines := []string{
			standardTitle(),
			formatRow(fmt.Sprintf("Tool: %s (not executed yet)", tool)),
		}
		if sh.includeArgs {
			lines = append(lines, argsRow())
		}
		sel := selectedChoice()
		mark := "[*]"
		if m.decisionPending {
			mark = "[·]"
			sel.Label = "submitting decision…"
			sel.KeyHint = ""
		}
		lines = append(lines,
			formatRow(fmt.Sprintf("  %s %s (%s)", mark, sel.Label, sel.KeyHint)),
			formatRow("Enter confirm · Up/Down select · y/a/n direct"),
			plainBorder(),
		)
		return strings.Join(lines, "\n")
	}

	// permLevelFull: title, tool, [args], [blank], choices, [blank], hint, border.
	lines := []string{
		standardTitle(),
		formatRow(fmt.Sprintf("Tool: %s (not executed yet)", tool)),
	}
	if sh.includeArgs {
		lines = append(lines, argsRow())
	}
	if sh.includeBlanks {
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
	if sh.includeBlanks {
		lines = append(lines, formatRow(""))
	}
	switch {
	case m.decisionPending:
		lines = append(lines, formatRow("waiting for decision to apply…"))
	case m.selected >= 0:
		lines = append(lines, formatRow("Enter confirm · Up/Down select · y/a/n direct"))
	default:
		lines = append(lines, formatRow("y/a/n direct · Up/Down select · Enter confirm"))
	}
	lines = append(lines, plainBorder())
	return strings.Join(lines, "\n")
}
