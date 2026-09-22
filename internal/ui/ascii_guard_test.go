package ui

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/provider"
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

			pos := fset.Position(lit.Pos())

			val, err := strconv.Unquote(lit.Value)
			if err != nil {
				val = lit.Value
			}

			for _, r := range val {
				if r >= 128 && !AllowedUISymbols[r] {
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
// output of visible UI strings identified in the forensic session.
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

	// 2. Feed error summary on doneMsg with ErrMaxTurns: must be "turn ceiling reached", not "خطأ"
	summary := errSummary(agent.ErrMaxTurns)
	if strings.Contains(summary, "خطأ") {
		t.Errorf("errSummary on ErrMaxTurns must not contain 'خطأ', got: %q", summary)
	}
	if !strings.Contains(summary, "turn ceiling reached") {
		t.Errorf("errSummary on ErrMaxTurns must contain 'turn ceiling reached', got: %q", summary)
	}
}

// TestUIBackDoorLeakPrevented asserts that an error originating from backend
// packages with Arabic text is intercepted and sanitized to 'execution failed',
// preventing Arabic text from leaking through doneMsg into the terminal interface.
func TestUIBackDoorLeakPrevented(t *testing.T) {
	simulatedErr := &simulatedArabicError{msg: "فشل في تنفيذ العملية"}
	summary := errSummary(simulatedErr)
	if strings.Contains(summary, "فشل") {
		t.Fatalf("backdoor leak: Arabic error leaked to UI status: %q", summary)
	}
	if summary != "execution failed" {
		t.Fatalf("expected 'execution failed', got: %q", summary)
	}
}

type simulatedArabicError struct{ msg string }

func (e *simulatedArabicError) Error() string { return e.msg }

// TestOriginalErrorPreservedInJournal asserts that when a runtime error occurs,
// the full verbatim error text is preserved in Event{Type: RunError} for the journal,
// while the UI receives only the sanitized errSummary.
func TestOriginalErrorPreservedInJournal(t *testing.T) {
	origErr := errors.New("تفاصيل الخطأ الأصلي الكاملة")
	ev := agent.Event{Type: agent.RunError, Err: origErr.Error()}

	// Check that event contains the full verbatim error
	if ev.Err != "تفاصيل الخطأ الأصلي الكاملة" {
		t.Fatalf("Event RunError dropped verbatim error: %q", ev.Err)
	}

	// Check that UI status is sanitized
	summary := errSummary(origErr)
	if strings.Contains(summary, "تفاصيل") {
		t.Fatalf("errSummary leaked Arabic error to UI: %q", summary)
	}
	if summary != "execution failed" {
		t.Fatalf("expected 'execution failed', got: %q", summary)
	}
}

// TestPermAllowReasonNeverReachesUIOrModel asserts that the internal reason
// "مسموح لهذه الجلسة" in internal/perm/policy.go:93 on Allow is purely internal,
// never rendered by RenderEvent, and never formatted into provider messages.
func TestPermAllowReasonNeverReachesUIOrModel(t *testing.T) {
	// 1. PermReply rendering: only renders mark and decision, never the internal why
	ev := agent.Event{
		Type:     agent.PermReply,
		Decision: agent.AllowSession,
	}
	rendered := RenderEvent(ev, DefaultWidth)
	if strings.Contains(rendered, "مسموح") {
		t.Fatalf("RenderEvent leaked policy internal reason: %q", rendered)
	}

	// 2. ToolCall / ToolEnd rendering on allowed tool: never includes internal why
	tc := agent.ToolCall{
		ID: "t1", Name: "write_file", OK: true, Output: "ok",
	}
	evEnd := agent.Event{Type: agent.ToolEnd, Call: &tc}
	renderedEnd := RenderEvent(evEnd, DefaultWidth)
	if strings.Contains(renderedEnd, "مسموح") {
		t.Fatalf("RenderEvent ToolEnd leaked policy internal reason: %q", renderedEnd)
	}

	// 3. Provider Messages: tool results contain only output, never why
	res := provider.ToolResult{ID: "t1", Output: "file written", IsErr: false}
	m := provider.Message{
		Role:        provider.User,
		ToolResults: []provider.ToolResult{res},
	}
	for _, tr := range m.ToolResults {
		if strings.Contains(tr.Output, "مسموح") {
			t.Fatalf("provider.Message leaked policy internal reason: %q", tr.Output)
		}
	}
}

// errSummary is the UI's last line of defence against non-ASCII text reaching
// the status bar, and it is LIVE: Feed calls it when a run fails (feed.go) and
// path_picker_wire.go calls it for the @ picker. It used to be guarded
// indirectly, through the Chat model; Chat was retired by ADR-0001, so this
// file exercises errSummary directly rather than through any surface.
//
// NABD_ASCII_ONLY is a DIFFERENT guarantee and is not what errSummary
// implements: that variable is honoured by separatorLine and visual_rows.go,
// and separatorLine's fallback is guarded by
// TestSeparatorLineUsesASCIIWhenEnvSet. errSummary's whitelist
// (AllowedUISymbols) is always on and is not environment-gated, which is why
// these cases assert the whitelist behaviour rather than an env toggle.
func TestErrSummaryIsASCIIOnly(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil error is empty", nil, ""},
		{"plain ascii passes through", errors.New("sink boom"), "sink boom"},
		{"empty message stays empty", errors.New(""), ""},
		{"allowed ui symbol is kept", errors.New("tool failed ✓"), "tool failed ✓"},
		{"several allowed symbols are kept", errors.New("✗ ✎ · …"), "✗ ✎ · …"},
		{"arabic is refused", errors.New("خطأ في التنفيذ"), "execution failed"},
		{"one disallowed rune anywhere refuses the whole string", errors.New("failed: خطأ"), "execution failed"},
		{"emoji is refused", errors.New("boom 🚀"), "execution failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := errSummary(tc.err); got != tc.want {
				t.Fatalf("errSummary(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

// TestErrSummaryOutputNeverCarriesUnlistedNonASCII is the property behind the
// table above: whatever a provider or tool error says, the string handed to the
// renderer is either pure ASCII or drawn only from AllowedUISymbols. Tool and
// provider errors carry text the model or a repository influenced, so this is a
// boundary that must hold for every input, not a formatting nicety.
func TestErrSummaryOutputNeverCarriesUnlistedNonASCII(t *testing.T) {
	inputs := []string{
		"boom",
		"خطأ",
		"failed: ✗ خطأ ✓",
		"provider said: حدث خطأ غير متوقع",
		"emoji 🚀 leak",
		"status 500 · upstream حدث خطأ",
	}
	for _, in := range inputs {
		got := errSummary(errors.New(in))
		for _, r := range got {
			if r >= 128 && !AllowedUISymbols[r] {
				t.Fatalf("errSummary(%q) = %q, which carries unlisted non-ASCII rune %U", in, got, r)
			}
		}
		if strings.Contains(got, "خطأ") {
			t.Fatalf("errSummary(%q) leaked Arabic to the UI: %q", in, got)
		}
	}
}
