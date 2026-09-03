package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

// Composer height policy: the composer starts at one row and grows as the
// user adds logical lines, up to maxComposerHeight rows. Beyond that the
// textarea scrolls its own content internally.
const (
	minComposerHeight = 1
	maxComposerHeight = 8
)

// composer wraps a bubbles textarea with the Phase 3A input policy. Send,
// newline, history Up/Down, Ctrl-C/Ctrl-D and modal routing are handled by
// the feed's input router before keys reach this editing surface.
type composer struct {
	ta textarea.Model

	// height is the textarea height currently configured, so the feed can
	// recompute the viewport whenever it changes.
	height int

	// width is the terminal width the textarea was last sized to.
	width int
}

func newComposer() *composer {
	ta := textarea.New()
	ta.Prompt = "› "
	ta.Placeholder = "type a message… (Enter to send)"
	ta.ShowLineNumbers = false
	ta.CharLimit = 0 // the feed enforces maxInputRunes atomically, never cutting silently
	ta.MaxHeight = maxComposerHeight
	c := &composer{ta: ta, height: minComposerHeight}
	c.ta.Focus()
	c.ta.SetHeight(minComposerHeight)
	c.setWidth(DefaultWidth)
	return c
}

// focus gives the composer keyboard focus. The textarea is focused by
// construction; this restores focus after the permission modal closes.
func (c *composer) focus() {
	c.ta.Focus()
}

// blur removes keyboard focus (used while the permission modal is visible).
func (c *composer) blur() {
	c.ta.Blur()
}

// focused reports whether the composer owns keyboard input.
func (c *composer) focused() bool {
	return c.ta.Focused()
}

// value returns the composer text.
func (c *composer) value() string {
	return c.ta.Value()
}

// valueLen returns the composer text length in runes.
func (c *composer) valueLen() int {
	return len([]rune(c.ta.Value()))
}

// isEmpty reports whether the composer holds only whitespace.
func (c *composer) isEmpty() bool {
	return strings.TrimSpace(c.ta.Value()) == ""
}

// cursorLogicalLine returns the 0-based index of the logical line the
// cursor is on (lines separated by \n, not visual wraps).
func (c *composer) cursorLogicalLine() int {
	return c.ta.Line()
}

// logicalLineCount returns the total number of logical lines.
func (c *composer) logicalLineCount() int {
	return c.ta.LineCount()
}

// setValue replaces the whole composer text and moves the cursor to the
// end. Used by history recall, draft restore and limit rollback. The
// height follows the content.
func (c *composer) setValue(s string) {
	c.ta.SetValue(s)
	c.ta.CursorEnd()
	c.growToContent(maxComposerHeight)
}

// clear empties the composer and resets the cursor to the start, and the
// height back to a single row.
func (c *composer) clear() {
	c.ta.Reset()
	c.growToContent(maxComposerHeight)
}

// setWidth sizes the textarea to the terminal width.
func (c *composer) setWidth(width int) {
	if width < minViewportWidth {
		width = minViewportWidth
	}
	c.ta.SetWidth(width)
	c.width = width
}

// update passes a message to the textarea's own editing rules. The cursor
// blink command the textarea returns is deliberately dropped: the feed
// re-renders after every Update and manages the cursor via its own focus
// contract, so scheduling blink ticks from here adds nothing but noise.
func (c *composer) update(msg tea.Msg) tea.Cmd {
	ta, _ := c.ta.Update(msg)
	c.ta = ta
	return nil
}

// resize recomputes the textarea width and clamps its height to the number
// of logical lines it currently holds (bounded by maxRows, itself bounded
// by maxComposerHeight). Returns the new height. Text and cursor position
// are preserved.
func (c *composer) resize(width, maxRows int) int {
	c.setWidth(width)
	if maxRows < minComposerHeight {
		maxRows = minComposerHeight
	}
	if maxRows > maxComposerHeight {
		maxRows = maxComposerHeight
	}
	lines := c.ta.LineCount()
	if lines < minComposerHeight {
		lines = minComposerHeight
	}
	if lines > maxRows {
		lines = maxRows
	}
	c.ta.SetHeight(lines)
	c.height = lines
	return lines
}

// growToContent sets the height to the current logical line count clamped
// to [1, maxRows] and returns the new height.
func (c *composer) growToContent(maxRows int) int {
	return c.resize(c.width, maxRows)
}

// view renders the composer (a fixed number of rows: the feed reserves
// c.height rows in its layout, and the textarea scrolls internally when the
// content is taller).
func (c *composer) view() string {
	return c.ta.View()
}

// deleteForward deletes the rune under the cursor (the Ctrl+D / Delete
// binding of the textarea, which is rune-safe: multi-byte UTF-8 is never
// split). At the very end of the text it is a no-op.
func (c *composer) deleteForward() tea.Cmd {
	if c.ta.Value() == "" {
		return nil
	}
	return c.update(tea.KeyMsg{Type: tea.KeyDelete})
}
