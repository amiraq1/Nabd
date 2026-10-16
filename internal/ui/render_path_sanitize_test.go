package ui

import (
	"encoding/json"
	"strings"
	"testing"

	"nabd/internal/display"
	"nabd/internal/event"
	"nabd/internal/toolvocab"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// The edit and read rows print a path that originates in tool arguments, which
// the model controls. internal/tools rejects NUL, absolute, volume and ".."
// components but no control character, so a path is an injection vector. These
// tests hold both rows to singleLineDisplayPolicy — the same definition the
// permission prompt (callLine) applies — and prove the assertion can fail.

// Terminal-control vectors a path must never deliver to the screen.
const (
	osc52Clipboard = "\x1b]52;c;cGF3bmVk\x07" // OSC 52: sets the user's clipboard
	csiRed         = "\x1b[31m"               // CSI: colour/terminal state
	c1CSI          = "\u009b31m"              // C1 CSI: the 8-bit form of ESC [
	c1OSC          = "\u009d52;c;x\u009c"     // C1 OSC … C1 ST: the 8-bit OSC form
	// The full bidi embedding/override range (LRE, RLE, PDF, LRO, RLO) and the
	// full isolate range (LRI, RLI, FSI, PDI). assertPathRowIsInert asserts both
	// ranges in full, so the input must cover both ranges in full too.
	bidiControls   = "\u202a\u202b\u202c\u202d\u202e\u2066\u2067\u2068\u2069"
	zeroWidthSpace = "\u200b" // invisible format control
	rtlMark        = "\u200f" // RIGHT-TO-LEFT MARK: legitimate Arabic text
	ghToken        = "ghp_abcdefghijklmnop12345678"
)

// pinPlainRender forces the Ascii colour profile so lipgloss emits no styling
// escapes. Without this a bare 0x1b in the output could come from the style
// rather than from the path, and the ESC assertion below would be vacuous.
// The suite baseline is already Ascii (see agent_color_test.go); this makes it
// explicit and restores it.
func pinPlainRender(t *testing.T) {
	t.Helper()
	lipgloss.SetColorProfile(termenv.Ascii)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })
}

// assertPathRowIsInert asserts that no terminal-control vector survives into
// the rendered row. The caller must have pinned the Ascii profile.
//
// ZWNJ (U+200C) is deliberately not asserted: the current policy strips it,
// which costs Persian and Urdu filename fidelity. That is callLine's
// pre-existing behaviour, not this change's subject, and is recorded as debt
// rather than pinned here.
func assertPathRowIsInert(t *testing.T, label, got string) {
	t.Helper()
	if i := strings.IndexByte(got, 0x1b); i >= 0 {
		t.Errorf("%s: output contains ESC (0x1b) at byte %d: %q", label, i, got)
	}
	for _, r := range got {
		switch {
		case r == 0x07:
			t.Errorf("%s: output contains BEL (0x07): %q", label, got)
		case r < 0x20:
			t.Errorf("%s: output contains C0 control U+%04X: %q", label, r, got)
		case r == 0x7f:
			t.Errorf("%s: output contains DEL (0x7f): %q", label, got)
		case r >= 0x80 && r <= 0x9f:
			t.Errorf("%s: output contains C1 control U+%04X: %q", label, r, got)
		case r >= 0x202a && r <= 0x202e:
			t.Errorf("%s: output contains bidi override/embedding U+%04X: %q", label, r, got)
		case r >= 0x2066 && r <= 0x2069:
			t.Errorf("%s: output contains bidi isolate U+%04X: %q", label, r, got)
		case r == 0x200b || r == 0xfeff:
			t.Errorf("%s: output contains zero-width format control U+%04X: %q", label, r, got)
		}
	}
	if strings.Contains(got, "\n") {
		t.Errorf("%s: a path row must stay one logical line: %q", label, got)
	}
}

// hostilePath carries every vector at once, including both the 7-bit CSI/OSC
// forms and their 8-bit C1 forms, and both complete bidi ranges. Checking only
// the complete OSC text would miss a sanitizer that dropped the BEL terminator
// but left `ESC ]52;c;…` behind; checking only 7-bit escapes would leave the C1
// claim untested; checking one code point per bidi range would leave the rest of
// the range untested while the assertion still claimed to cover it.
func hostilePath() string {
	return "src/evil" + osc52Clipboard + csiRed + c1CSI + c1OSC +
		bidiControls + "txt.gpj" + zeroWidthSpace
}

func editRow(path string, width int) string {
	return RenderEvent(event.Event{Type: event.EventEdit, Edit: &event.EditRecord{Path: path}}, width)
}

func truncatedReadRow(path string, width int) string {
	return RenderEvent(event.Event{Type: event.EventRead, Read: &event.ReadRecord{Path: path, Truncated: true}}, width)
}

func TestRenderEditPathStripsTerminalControlSequences(t *testing.T) {
	pinPlainRender(t)
	got := editRow(hostilePath(), DefaultWidth)
	if !strings.Contains(got, "✎") {
		t.Fatalf("RenderEvent = %q, want the ✎ edit marker", got)
	}
	assertPathRowIsInert(t, "edit row", got)
	if !strings.Contains(got, "src/evil") || !strings.Contains(got, "txt.gpj") {
		t.Errorf("RenderEvent = %q, want the benign path text preserved", got)
	}
}

func TestRenderReadPathStripsTerminalControlSequences(t *testing.T) {
	pinPlainRender(t)
	got := truncatedReadRow(hostilePath(), DefaultWidth)
	if !strings.Contains(got, "✂") || !strings.Contains(got, "partially read") {
		t.Fatalf("RenderEvent = %q, want the truncated-read warning", got)
	}
	assertPathRowIsInert(t, "read row", got)
	if !strings.Contains(got, "src/evil") || !strings.Contains(got, "txt.gpj") {
		t.Errorf("RenderEvent = %q, want the benign path text preserved", got)
	}
}

// An embedded newline or tab must not survive: singleLineDisplayPolicy sets
// AllowNewline and AllowTab to false, and the sanitizer replaces each with a
// single space. Without an input that carries them the "\n" assertion above
// could never fail.
func TestRenderPathCollapsesToSingleLine(t *testing.T) {
	pinPlainRender(t)
	got := editRow("src/\nملف\t.go", DefaultWidth)
	assertPathRowIsInert(t, "edit row (embedded newline/tab)", got)
	if strings.ContainsAny(got, "\n\t") {
		t.Errorf("edit row = %q, must not carry a newline or tab", got)
	}
	if !strings.Contains(got, "src/ ملف .go") {
		t.Errorf("edit row = %q, want the newline and tab replaced by spaces", got)
	}
	if !strings.Contains(got, "ملف") {
		t.Errorf("edit row = %q, want the Arabic name intact", got)
	}
}

// The policy redacts credentials, so a credential-shaped filename must not be
// printed. Uses a fixture of the same shape as internal/redact's own tests.
func TestRenderPathRedactsCredentialShapedNames(t *testing.T) {
	pinPlainRender(t)
	got := editRow("src/"+ghToken+".go", DefaultWidth)
	if strings.Contains(got, ghToken) {
		t.Errorf("edit row = %q, must not carry the credential", got)
	}
	if !strings.Contains(got, display.RedactedToken) {
		t.Errorf("edit row = %q, want the redaction token %q", got, display.RedactedToken)
	}
}

// The policy strips bidi overrides and isolates but keeps LRM/RLM for
// legitimate Arabic. Sanitizing must not damage an ordinary Arabic name.
func TestRenderPathPreservesLegitimateRTL(t *testing.T) {
	pinPlainRender(t)
	got := editRow("src/"+rtlMark+"ملف.go", DefaultWidth)
	assertPathRowIsInert(t, "edit row (Arabic + RLM)", got)
	if !strings.Contains(got, rtlMark) {
		t.Errorf("edit row = %q, want RLM preserved for legitimate Arabic text", got)
	}
	if !strings.Contains(got, "ملف.go") {
		t.Errorf("edit row = %q, want the Arabic name intact", got)
	}
}

// The recorded row must agree with the permission prompt, which is the point
// of the change. This compares against callLine's own output — not against a
// policy expression copied into the test — so a change to callLine alone is
// caught here.
func TestRenderEditPathAgreesWithPermissionPrompt(t *testing.T) {
	pinPlainRender(t)

	// The fixture must use a name the binary actually advertises. Without this
	// guard, a rename in toolvocab would fail the test for a reason unrelated to
	// the render boundary.
	const toolName = "edit_file"
	if !toolvocab.Has(toolName) {
		t.Fatalf("fixture tool %q is not in toolvocab.Names(); pick a real tool name", toolName)
	}

	raw := hostilePath()
	args, err := json.Marshal(map[string]string{"path": raw})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	prompt := callLine(&event.ToolCall{Name: toolName, Args: args})

	// callLine renders "<name> <sanitized argument>"; take the argument half.
	_, promptPath, ok := strings.Cut(prompt, " ")
	if !ok || promptPath == "" {
		t.Fatalf("callLine = %q, want %q followed by the sanitized argument", prompt, toolName)
	}

	row := editRow(raw, DefaultWidth)
	if !strings.Contains(row, promptPath) {
		t.Errorf("edit row = %q, want it to contain the prompt's form %q", row, promptPath)
	}
	if strings.Contains(row, raw) {
		t.Errorf("edit row = %q, must not contain the raw path", row)
	}
}

// Sanitization must hold at any width. Wrapping the row is a separate concern,
// recorded as EDIT_READ_ROW_NOT_WRAPPED in docs/TECH_DEBT.md and pinned by
// render_path_width_test.go.
func TestRenderPathSanitizedAtNarrowWidth(t *testing.T) {
	pinPlainRender(t)
	for _, w := range []int{20, 40, 80} {
		assertPathRowIsInert(t, "edit row narrow", editRow(hostilePath(), w))
		assertPathRowIsInert(t, "read row narrow", truncatedReadRow(hostilePath(), w))
	}
}
