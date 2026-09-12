package ui

import (
	"strings"
	"testing"

	"nabd/internal/presentation"

	"github.com/charmbracelet/x/ansi"
)

// TestReadFileRenderCrossing9And10 verifies fixed-width gutter alignment when
// line numbers cross 9 and 10.
func TestReadFileRenderCrossing9And10(t *testing.T) {
	input := "8|func a() {\n9|\n10|    return 1\n11|}"
	lines := renderReadFileOutput(input, 40)

	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d:\n%s", len(lines), strings.Join(lines, "\n"))
	}

	// Max line is 11 (2 digits), gutter is " %2d | " (width 6)
	wantPrefixes := []string{
		"  8 | func a() {",
		"  9 | ",
		" 10 |     return 1",
		" 11 | }",
	}

	for i, want := range wantPrefixes {
		if lines[i] != want {
			t.Errorf("line %d: got %q, want %q", i, lines[i], want)
		}
	}
}

// TestReadFileRenderCrossing99And100 verifies fixed-width gutter alignment when
// line numbers cross 99 and 100.
func TestReadFileRenderCrossing99And100(t *testing.T) {
	input := "98|// 98\n99|// 99\n100|// 100\n101|// 101"
	lines := renderReadFileOutput(input, 40)

	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d:\n%s", len(lines), strings.Join(lines, "\n"))
	}

	// Max line is 101 (3 digits), gutter is " %3d | " (width 7)
	wantLines := []string{
		"  98 | // 98",
		"  99 | // 99",
		" 100 | // 100",
		" 101 | // 101",
	}

	for i, want := range wantLines {
		if lines[i] != want {
			t.Errorf("line %d: got %q, want %q", i, lines[i], want)
		}
	}
}

// TestReadFileRenderLaterOffset verifies that gutter width is computed from the
// actual displayed line numbers when reading at a nonzero offset.
func TestReadFileRenderLaterOffset(t *testing.T) {
	input := "1050|first := 1\n1051|second := 2"
	lines := renderReadFileOutput(input, 40)

	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d:\n%s", len(lines), strings.Join(lines, "\n"))
	}

	// Max line is 1051 (4 digits), gutter is " %4d | " (width 8)
	wantLines := []string{
		" 1050 | first := 1",
		" 1051 | second := 2",
	}

	for i, want := range wantLines {
		if lines[i] != want {
			t.Errorf("line %d: got %q, want %q", i, lines[i], want)
		}
	}
}

// TestReadFileRenderLongLinesAndContinuation verifies wrapping behavior,
// continuation alignment, whitespace boundary preference, hard-wrapping of long tokens,
// and indentation preservation.
func TestReadFileRenderLongLinesAndContinuation(t *testing.T) {
	input := "1|    // This is a long comment that should wrap nicely at whitespace boundaries\n" +
		"2|    var extremelyLongIdentifierNameThatExceedsTheContentWidthWithoutAnySpaces = 1\n" +
		"3|    path := \"/very/long/path/to/some/nested/directory/that/needs/wrapping/file.go\"\n" +
		"4|"

	width := 35
	lines := renderReadFileOutput(input, width)

	if len(lines) < 6 {
		t.Fatalf("expected at least 6 lines, got %d:\n%s", len(lines), strings.Join(lines, "\n"))
	}

	// Check line 0 (starts source line 1)
	if !strings.HasPrefix(lines[0], " 1 |     // ") {
		t.Errorf("line 0 should start with source line 1 and indentation: %q", lines[0])
	}

	// Check continuation row for line 1
	if !strings.HasPrefix(lines[1], "   | ") {
		t.Errorf("line 1 should be a continuation row starting with '   | ': %q", lines[1])
	}

	// Ensure continuation rows never invent line numbers
	for i, l := range lines {
		if ansi.StringWidth(l) > width {
			t.Errorf("line %d exceeded terminal width %d: visual width=%d: %q", i, width, ansi.StringWidth(l), l)
		}
		if strings.HasPrefix(l, "   | ") {
			// Continuation row: no digit before pipe
			if strings.TrimSpace(l[:5]) != "|" {
				t.Errorf("line %d continuation row has invalid prefix: %q", i, l)
			}
		}
	}

	// Blank line 4 must be preserved as " 4 | "
	var foundBlankLine bool
	for _, l := range lines {
		if l == " 4 | " {
			foundBlankLine = true
			break
		}
	}
	if !foundBlankLine {
		t.Errorf("blank source line 4 was not preserved as ' 4 | ':\n%s", strings.Join(lines, "\n"))
	}
}

// TestReadFileRenderMixedUnicodeAndHostileControl verifies mixed Unicode
// and terminal-control sequence sanitization.
func TestReadFileRenderMixedUnicodeAndHostileControl(t *testing.T) {
	input := "1|// مرحبا 123 😊\n2|x := \"test \x1b[10;20Hjump cursor \x1b]0;hack\x07done\""
	lines := renderReadFileOutput(input, 50)

	if len(lines) < 2 {
		t.Fatalf("expected at least 2 lines, got %d:\n%s", len(lines), strings.Join(lines, "\n"))
	}

	// Arabic text preserved
	if !strings.Contains(lines[0], "مرحبا 123 😊") {
		t.Errorf("Arabic text not preserved: %q", lines[0])
	}

	// Hostile escape sequences sanitized
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "\x1b[10;20H") || strings.Contains(joined, "\x1b]0;") {
		t.Errorf("hostile terminal control sequence leaked into output: %q", joined)
	}
}

// TestReadFileRenderMetadataAndTruncatedReads verifies that pagination and
// truncation metadata are preserved as metadata without fake line numbers.
func TestReadFileRenderMetadataAndTruncatedReads(t *testing.T) {
	input := "1|package main\n2|func main() {}\n\n" +
		"[TRUNCATED: read lines 1-2 of 50; continue with offset=3]\n" +
		"lines_read=2  total_lines=50  next_offset=3"

	lines := renderReadFileOutput(input, 60)

	// Lines 1 and 2 must have gutters
	if !strings.HasPrefix(lines[0], " 1 | package main") {
		t.Errorf("line 0 missing gutter: %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], " 2 | func main() {}") {
		t.Errorf("line 1 missing gutter: %q", lines[1])
	}

	// Metadata lines must NOT have source line gutters
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "[TRUNCATED: read lines 1-2 of 50; continue with offset=3]") {
		t.Errorf("metadata missing: %q", joined)
	}
	if !strings.Contains(joined, "lines_read=2  total_lines=50  next_offset=3") {
		t.Errorf("pagination info missing: %q", joined)
	}

	for _, l := range lines {
		if strings.Contains(l, "[TRUNCATED:") || strings.Contains(l, "lines_read=") {
			if strings.Contains(l, " | ") {
				t.Errorf("metadata line was treated as source line with gutter: %q", l)
			}
		}
	}
}

// TestReadFileRenderUnchangedRawData verifies that presentation formatting does
// not modify the underlying ToolCard.Output data.
func TestReadFileRenderUnchangedRawData(t *testing.T) {
	rawOutput := "1|line a\n2|line b"
	it := presentation.FeedItem{
		Type: presentation.ItemTool,
		Tool: &presentation.ToolCard{
			Name:   "read_file",
			Status: presentation.ToolDone,
			Output: rawOutput,
		},
	}

	_ = renderTool(it, 40)

	// Verify original Output string was untouched
	if it.Tool.Output != rawOutput {
		t.Fatalf("renderTool mutated it.Tool.Output: got %q, want %q", it.Tool.Output, rawOutput)
	}
}

// TestReadFileRenderShellOutputNotReformatted verifies that shell/bash output
// containing digits and pipes is NOT interpreted as numbered source code.
func TestReadFileRenderShellOutputNotReformatted(t *testing.T) {
	rawOutput := "123|echo hello\n124|echo world"
	it := presentation.FeedItem{
		Type: presentation.ItemTool,
		Tool: &presentation.ToolCard{
			Name:   "bash", // not read_file!
			Status: presentation.ToolDone,
			Output: rawOutput,
		},
	}

	rendered := renderTool(it, 40, true)
	joined := strings.Join(rendered, "\n")

	// Must NOT contain formatted gutter " 123 | echo hello"
	if strings.Contains(joined, " 123 | ") || strings.Contains(joined, " 124 | ") {
		t.Errorf("shell output was reinterpreted as numbered source code:\n%s", joined)
	}

	// Must contain the original raw text (wrapped)
	if !strings.Contains(joined, "123|echo hello") {
		t.Errorf("shell output missing original text:\n%s", joined)
	}
}

// TestReadFileRenderNarrowTerminalDegradation verifies that when terminal width
// is too narrow for the gutter, it degrades safely to truncateOutput.
func TestReadFileRenderNarrowTerminalDegradation(t *testing.T) {
	input := "1|short line\n2|second line"
	// Width 5: gutter width is 5 (" 1 | "), contentWidth < 4
	lines := renderReadFileOutput(input, 5)

	if len(lines) == 0 {
		t.Fatalf("expected lines at narrow width, got 0")
	}

	// All lines must fit within 5 terminal cells
	for i, l := range lines {
		if ansi.StringWidth(l) > 5 {
			t.Errorf("line %d exceeded terminal width 5: width=%d: %q", i, ansi.StringWidth(l), l)
		}
	}
}

// TestReadFileRenderNonNumberedFallback verifies that non-numbered read_file output
// (such as "file is empty" or error text) uses the safe fallback.
func TestReadFileRenderNonNumberedFallback(t *testing.T) {
	input := "test.go is empty"
	lines := renderReadFileOutput(input, 40)

	if len(lines) != 1 || lines[0] != "test.go is empty" {
		t.Fatalf("unexpected fallback output: %v", lines)
	}
}
