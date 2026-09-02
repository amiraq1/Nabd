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

// TestUIStringLiteralsEnforceASCIISymbolWhitelist scans all non-test Go source files
// in internal/ui and asserts that string literals contain no runes >= 128
// other than the explicit AllowedUISymbols whitelist.
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
				if r >= 128 && !AllowedUISymbols[r] {
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
	if !strings.Contains(m.status, "error: turn ceiling reached") {
		t.Errorf("chat status on error must contain 'error: turn ceiling reached', got: %q", m.status)
	}
}

// TestUIBackDoorLeakPrevented asserts that an error originating from backend
// packages with Arabic text is intercepted and sanitized to 'error: execution failed',
// preventing Arabic text from leaking through doneMsg into the terminal interface.
func TestUIBackDoorLeakPrevented(t *testing.T) {
	ch := make(chan agent.Event, 1)
	chatMdl := asChat(t, NewChat(runnerStub{}, ch))
	arabicErr := os.ErrInvalid
	_ = arabicErr
	// Simulate an error containing Arabic text
	simulatedErr := &simulatedArabicError{msg: "فشل في تنفيذ العملية"}
	mdl, _ := chatMdl.Update(doneMsg{err: simulatedErr})
	m := asChat(t, mdl)
	if strings.Contains(m.status, "فشل") {
		t.Fatalf("backdoor leak: Arabic error leaked to UI status: %q", m.status)
	}
	if m.status != "error: execution failed" {
		t.Fatalf("expected 'error: execution failed', got: %q", m.status)
	}
}

type simulatedArabicError struct{ msg string }

func (e *simulatedArabicError) Error() string { return e.msg }
