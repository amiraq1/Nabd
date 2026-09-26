package ui

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"nabd/internal/agent"

	"github.com/charmbracelet/x/ansi"
)

// ModalArmDelay defines the typeahead guard interval.
const ModalArmDelay = 400 * time.Millisecond

var modalClock = time.Now

func setModalClock(fn func() time.Time) {
	if fn == nil {
		modalClock = time.Now
	} else {
		modalClock = fn
	}
}

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
	reason          string
	selected        int
	decisionPending bool
	armedAt         time.Time
}

// choiceIndex returns the index of the given decision in choices, or -1 if
// the decision is absent. This is the single source of truth for locating a
// decision's position without assuming ordering.
func choiceIndex(choices []PermissionChoice, decision agent.Decision) int {
	for i, c := range choices {
		if c.Decision == decision {
			return i
		}
	}
	return -1
}

// denyIndex returns the index of the Deny choice in choices(), or -1 if Deny
// is somehow missing. newPermissionModal/open/selectedChoice rely on this for
// a fail-closed default: selected=-1 combined with currentDecision()'s -1 guard
// yields Deny both in logic and in display.
func denyIndex() int {
	return choiceIndex((&PermissionModal{}).choices(), agent.Deny)
}

// fallbackChoice returns an explicit Deny choice for display when the Deny
// option is absent from choices(). This keeps rendering fail-closed without
// picking an arbitrary element.
func fallbackChoice() PermissionChoice {
	return PermissionChoice{Decision: agent.Deny, Label: "Deny", KeyHint: "n / esc"}
}

func newPermissionModal() *PermissionModal {
	return &PermissionModal{
		selected: denyIndex(),
	}
}

func (m *PermissionModal) open(call *agent.ToolCall, reasons ...string) {
	m.visible = true
	m.call = call
	m.reason = ""
	if len(reasons) > 0 {
		m.reason = SanitizeForDisplay(reasons[0], DisplayPolicy{Redact: true})
	}
	m.selected = denyIndex()
	m.decisionPending = false
	m.armedAt = modalClock()
}

func (m *PermissionModal) close() {
	m.visible = false
	m.call = nil
	m.reason = ""
	m.selected = denyIndex()
	m.decisionPending = false
	m.armedAt = time.Time{}
}

// isArmed reports whether the arm delay has elapsed.
func (m *PermissionModal) isArmed() bool {
	if m.armedAt.IsZero() {
		return true
	}
	return modalClock().Sub(m.armedAt) >= ModalArmDelay
}

// Rearm resets the arm delay window to the current clock time.
func (m *PermissionModal) Rearm() {
	m.armedAt = modalClock()
}

func (m *PermissionModal) toolName() string {
	if m.call == nil {
		return ""
	}
	return m.call.Name
}

func (m *PermissionModal) choices() []PermissionChoice {
	choices := []PermissionChoice{
		{Decision: agent.AllowOnce, Label: "Allow Once", KeyHint: "y"},
		{Decision: agent.Deny, Label: "Deny", KeyHint: "n / esc"},
	}
	if m.call == nil || !m.call.SessionGrantKnown || m.call.SessionGrantAllowed {
		choices = append(choices[:1], append([]PermissionChoice{{Decision: agent.AllowSession, Label: "Allow Session", KeyHint: "a"}}, choices[1:]...)...)
	}
	return choices
}

func (m *PermissionModal) keyHintText() string {
	if m.call != nil && m.call.SessionGrantKnown && !m.call.SessionGrantAllowed {
		return "y allow · n deny · Esc cancel"
	}
	return "y allow · a session · n deny · Esc cancel"
}

func (m *PermissionModal) scopeText() string {
	if m.call != nil && m.call.SessionGrantKnown && m.call.SessionGrantAllowed {
		return "scope: this tool for this session"
	}
	return "scope: this request only"
}

func (m *PermissionModal) toolScopeLine(tool string) string {
	line := fmt.Sprintf("Tool: %s (not executed yet)", tool)
	if m.reason != "" {
		line += " · " + m.reason
	}
	return line + " · " + m.scopeText()
}

func (m *PermissionModal) currentDecision() agent.Decision {
	ch := m.choices()
	if m.selected < 0 || m.selected >= len(ch) {
		return agent.Deny
	}
	return ch[m.selected].Decision
}

// hasArgs reports whether the pending call carries arguments worth showing.
func (m *PermissionModal) hasArgs() bool {
	return m.call != nil && len(m.call.Args) > 0 &&
		string(m.call.Args) != "{}" && string(m.call.Args) != `""`
}

// hasDroppedArgs reports whether the pending call carries dropped argument keys worth showing.
func (m *PermissionModal) hasDroppedArgs() bool {
	return m.call != nil && len(m.call.DroppedArgs) > 0
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
	// permLevelFull: top, tool, [args], [dropped], [blank], choices, [blank], hint, bottom.
	permLevelFull permModalLevel = iota
	// permLevelCompact: top, tool, [args], [dropped], selected choice, hint, bottom.
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
	level          permModalLevel
	rows           int
	includeArgs    bool
	includeDropped bool
	includeBlanks  bool
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
	hasDropped := m.hasDroppedArgs()
	choiceRows := len(m.choices())
	if m.decisionPending {
		// A pending decision collapses the three choices into one status row.
		choiceRows = 1
	}
	// top + tool + 2 blanks + choices + hint + bottom, plus optional rows.
	full := 6 + choiceRows
	if hasArgs {
		full++
	}
	if hasDropped {
		full++
	}

	limit := 0
	if len(maxRows) > 0 {
		limit = maxRows[0]
	}
	if limit <= 0 || limit >= full {
		return permModalShape{
			level:          permLevelFull,
			rows:           full,
			includeArgs:    hasArgs,
			includeDropped: hasDropped,
			includeBlanks:  true,
		}
	}

	// The degradation ladder, as the set of heights that can be filled exactly:
	// 1. drop the blank rows
	// 2. drop the args row
	// 3. drop the dropped row
	// 4. show only the selected choice
	// 5. drop the tool row (tool name moves into the title)
	candidates := []permModalShape{
		{level: permLevelFull, rows: full - 2, includeArgs: hasArgs, includeDropped: hasDropped},
	}
	if hasArgs && hasDropped {
		candidates = append(candidates,
			permModalShape{level: permLevelFull, rows: full - 3, includeArgs: false, includeDropped: true},
			permModalShape{level: permLevelCompact, rows: 7, includeArgs: true, includeDropped: true},
			permModalShape{level: permLevelCompact, rows: 6, includeArgs: false, includeDropped: true},
		)
	} else if hasArgs {
		candidates = append(candidates,
			permModalShape{level: permLevelFull, rows: full - 3, includeArgs: false, includeDropped: false},
			permModalShape{level: permLevelCompact, rows: 6, includeArgs: true, includeDropped: false},
		)
	} else if hasDropped {
		candidates = append(candidates,
			permModalShape{level: permLevelCompact, rows: 6, includeArgs: false, includeDropped: true},
		)
	}
	candidates = append(candidates,
		permModalShape{level: permLevelCompact, rows: 5, includeArgs: false, includeDropped: false},
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
		hint := "+-- " + m.keyHintText() + " "
		if !m.isArmed() {
			hint = "+-- wait · " + m.keyHintText() + " "
		}
		if ansi.StringWidth(hint) > cardW-2 {
			hint = ansi.Truncate(hint, max(1, cardW-2), "…")
		}
		hintDashes := max(0, cardW-ansi.StringWidth(hint)-1)
		return dim.Render(hint + strings.Repeat("-", hintDashes) + "+")
	}

	plainBorder := func() string {
		return dim.Render("+" + strings.Repeat("-", cardW-2) + "+")
	}

	// selectedChoice returns the choice to show when only one row is available.
	// Fail-closed: any invalid selection resolves to Deny, matching
	// currentDecision() and the default selection set by newPermissionModal/open.
	selectedChoice := func() PermissionChoice {
		ch := m.choices()
		idx := m.selected
		if idx < 0 || idx >= len(ch) {
			// selected is invalid: try Deny, then render an explicit fallback.
			idx = choiceIndex(ch, agent.Deny)
			if idx < 0 {
				return fallbackChoice()
			}
		}
		return ch[idx]
	}

	argsRow := func() string {
		return formatRow(fmt.Sprintf("Args: %s", safeArgs(string(m.call.Args), cardW-14)))
	}

	droppedRow := func() string {
		return formatRow(fmt.Sprintf("dropped: %s", strings.Join(m.call.DroppedArgs, ", ")))
	}

	switch sh.level {
	case permLevelMinimum:
		// 3 rows: title carries the tool name, one choice, hint in the border.
		title := fmt.Sprintf("+-- Permission: %s ", tool)
		if ansi.StringWidth(title) > cardW-2 {
			title = "+-- Perm: " + ansi.Truncate(tool, max(4, cardW-13), "…") + " "
		}
		sel := selectedChoice()
		if m.decisionPending {
			sel.Label = "submitting"
			sel.KeyHint = "…"
		}
		choiceLine := formatRow(fmt.Sprintf("%s (%s)", sel.Label, sel.KeyHint))
		return strings.Join([]string{titleBorder(title), choiceLine, hintBorder()}, "\n")

	case permLevelToolRow:
		// 4 rows: title, tool, one choice, hint in the border.
		sel := selectedChoice()
		if m.decisionPending {
			sel.Label = "submitting"
			sel.KeyHint = "…"
		}
		return strings.Join([]string{
			standardTitle(),
			formatRow(fmt.Sprintf("Tool: %s", tool)),
			formatRow(fmt.Sprintf("  %s (%s)", sel.Label, sel.KeyHint)),
			hintBorder(),
		}, "\n")

	case permLevelCompact:
		// 5, 6, or 7 rows: title, tool, [args], [dropped], one choice, hint row, border.
		lines := []string{
			standardTitle(),
			formatRow(m.toolScopeLine(tool)),
		}
		if sh.includeArgs {
			lines = append(lines, argsRow())
		}
		if sh.includeDropped {
			lines = append(lines, droppedRow())
		}
		sel := selectedChoice()
		if m.decisionPending {
			sel.Label = "submitting decision…"
			sel.KeyHint = ""
		}
		hintLine := m.keyHintText()
		if !m.isArmed() {
			hintLine = "wait · " + m.keyHintText()
		}
		lines = append(lines,
			formatRow(fmt.Sprintf("  %s (%s)", sel.Label, sel.KeyHint)),
			formatRow(hintLine),
			plainBorder(),
		)
		return strings.Join(lines, "\n")
	}

	// permLevelFull: title, tool, [args], [dropped], [blank], choices, [blank], hint, border.
	lines := []string{
		standardTitle(),
		formatRow(m.toolScopeLine(tool)),
	}
	if sh.includeArgs {
		lines = append(lines, argsRow())
	}
	if sh.includeDropped {
		lines = append(lines, droppedRow())
	}
	if sh.includeBlanks {
		lines = append(lines, formatRow(""))
	}
	if m.decisionPending {
		lines = append(lines, formatRow("· submitting decision…"))
	} else {
		for _, c := range m.choices() {
			lines = append(lines, formatRow(fmt.Sprintf("  %s (%s)", c.Label, c.KeyHint)))
		}
	}
	if sh.includeBlanks {
		lines = append(lines, formatRow(""))
	}
	switch {
	case m.decisionPending:
		lines = append(lines, formatRow("waiting for decision to apply…"))
	case !m.isArmed():
		lines = append(lines, formatRow("wait · "+m.keyHintText()))
	default:
		lines = append(lines, formatRow(m.keyHintText()))
	}
	lines = append(lines, plainBorder())
	return strings.Join(lines, "\n")
}
