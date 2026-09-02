package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"nabd/internal/agent"
)

// allowedUISymbols is the strict whitelist of non-ASCII glyphs permitted in UI string literals.
// These are UI boundary, status, and decoration symbols, neither Arabic nor Latin.
var allowedUISymbols = map[rune]bool{
	'⚙': true, // U+2699 ToolStart icon
	'✓': true, // U+2713 Tool success / PermReply allow
	'✗': true, // U+2717 Tool failure / RunError
	'✂': true, // U+2702 Truncation cut icon
	'⚑': true, // U+2691 Notice icon
	'›': true, // U+203A User prompt prefix
	'─': true, // U+2500 RunStart separator bar
	'⊘': true, // U+2298 Interrupted icon
	'≡': true, // U+2261 Compact icon
	'✎': true, // U+270E Edit record icon
	'·': true, // U+00B7 Middle dot separator
	'…': true, // U+2026 Ellipsis
	'—': true, // U+2014 Em dash
	'▌': true, // U+258C Prompt cursor block
	'→': true, // U+2192 Arrow
}

// TestUIStringLiteralsEnforceASCIISymbolWhitelist scans all non-test Go source files
// in internal/ui and asserts that string literals contain no runes >= 128
// other than the explicit allowedUISymbols whitelist.
func TestUIStringLiteralsEnforceASCIISymbolWhitelist(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob *.go: %v", err)
	}

	fset := token.NewFileSet()
	for _, fpath := range files {
		if strings.HasSuffix(fpath, "_test.go") {
			continue
		}

		src, err := os.ReadFile(fpath)
		if err != nil {
			t.Fatalf("read %s: %v", fpath, err)
		}

		fileNode, err := parser.ParseFile(fset, fpath, src, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", fpath, err)
		}

		ast.Inspect(fileNode, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}

			val, err := strconv.Unquote(lit.Value)
			if err != nil {
				val = lit.Value
			}

			for _, r := range val {
				if r >= 128 && !allowedUISymbols[r] {
					pos := fset.Position(lit.Pos())
					t.Errorf("%s:%d: string literal %q contains unallowed rune %q (U+%04X)",
						pos.Filename, pos.Line, val, r, r)
					break
				}
			}
			return true
		})
	}
}

// TestUIVisibleStringsAssertEnglishReplacements directly verifies the runtime
// output of the two visible UI strings identified in the forensic session.
func TestUIVisibleStringsAssertEnglishReplacements(t *testing.T) {
	// 1. Truncated read render: "✂ <path> · partially read"
	ev := agent.Event{
		Type: agent.EventRead,
		Read: &agent.ReadRecord{
			Path:      "NOTES.md",
			Truncated: true,
		},
	}
	rendered := RenderEvent(ev, DefaultWidth)
	if strings.Contains(rendered, "مقروء") {
		t.Errorf("rendered event must not contain Arabic 'مقروء', got: %q", rendered)
	}
	if !strings.Contains(rendered, "partially read") {
		t.Errorf("rendered event must contain 'partially read', got: %q", rendered)
	}

	// 2. Chat status on doneMsg with error: must be "error" (or "error: ..."), not "خطأ"
	ch := make(chan agent.Event, 1)
	chatMdl := asChat(t, NewChat(runnerStub{}, ch))
	testErr := agent.ErrMaxTurns
	mdl, _ := chatMdl.Update(doneMsg{err: testErr})
	m := asChat(t, mdl)
	if strings.Contains(m.status, "خطأ") {
		t.Errorf("chat status on error must not contain 'خطأ', got: %q", m.status)
	}
	if !strings.Contains(m.status, "error") {
		t.Errorf("chat status on error must contain 'error', got: %q", m.status)
	}
}
