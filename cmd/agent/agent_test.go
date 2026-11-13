package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/amiraq1/nabd/internal/tools"
	tea "github.com/charmbracelet/bubbletea"
)

func testModel(t *testing.T) (string, uiModel) {
	t.Helper()
	dir := t.TempDir()
	reg, err := tools.NewRegistry(dir, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	t.Cleanup(func() { _ = reg.Close() })
	return dir, initialModel(reg)
}

func testToolCall(name, args string) ToolCall {
	var tc ToolCall
	tc.ID = "call_1"
	tc.Type = "function"
	tc.Function.Name = name
	tc.Function.Arguments = args
	return tc
}

func TestSanitizeForDisplay(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"csi-color", "\x1b[31mred\x1b[0m", "red"},
		{"clear-screen", "\x1b[2JApprove edit_file? [Y/N]", "Approve edit_file? [Y/N]"},
		{"osc-title", "\x1b]0;spoof\x07ok", "ok"},
		{"nul-and-bel", "a\x00b\x07c", "abc"},
		{"keeps-newline-tab", "a\tb\nc", "a\tb\nc"},
		{"c1-control", "a\u0085b", "ab"},
		{"lone-esc", "\x1bx", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeForDisplay(tc.in)
			if got != tc.want {
				t.Errorf("sanitizeForDisplay(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if !utf8.ValidString(got) {
				t.Errorf("sanitizeForDisplay(%q) is not valid UTF-8", tc.in)
			}
		})
	}

	invalid := sanitizeForDisplay(string([]byte{0xff, 0xfe, 'a'}))
	if !utf8.ValidString(invalid) {
		t.Fatalf("invalid UTF-8 was not repaired: %q", invalid)
	}
}

func TestTruncatePreservesUTF8(t *testing.T) {
	s := "مرحبا بالعالم"
	for _, n := range []int{0, 1, 3, 5, 8, len(s) - 1} {
		got := truncate(s, n)
		if !utf8.ValidString(got) {
			t.Fatalf("truncate(%q, %d) = %q is invalid UTF-8", s, n, got)
		}
	}
	if got := truncate("abc", 10); got != "abc" {
		t.Fatalf("truncate short string = %q", got)
	}
}

func TestApprovalPreviewEdit(t *testing.T) {
	dir, m := testModel(t)
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nvar x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tc := testToolCall("edit_file", `{"path":"main.go","old_text":"var x = 1","new_text":"var x = 2"}`)
	p := buildApprovalPreview(m.registry, tc)
	if p.GuardPath != "main.go" || p.GuardSHA == "" {
		t.Fatalf("preview guard = %q/%q", p.GuardPath, p.GuardSHA)
	}
	for _, want := range []string{"path: main.go", "base sha256:", "- var x = 1", "+ var x = 2", "old sha256:", "new sha256:"} {
		if !strings.Contains(p.Summary, want) {
			t.Errorf("preview missing %q:\n%s", want, p.Summary)
		}
	}

	m.approval = p
	m.pendingTools = []ToolCall{tc}
	m.awaitingApproval = true
	view := m.View()
	if !strings.Contains(view, "Approve edit_file? [Y/N]") {
		t.Fatalf("approval prompt missing:\n%s", view)
	}
	if !strings.Contains(view, "base sha256:") || !strings.Contains(view, "main.go") {
		t.Fatalf("approval prompt hides the target:\n%s", view)
	}
}

func TestApprovalPreviewSanitizesANSIText(t *testing.T) {
	dir, m := testModel(t)
	path := filepath.Join(dir, "evil.txt")
	if err := os.WriteFile(path, []byte("safe\n\x1b[2Jspoof\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tc := testToolCall("edit_file", `{"path":"evil.txt","old_text":"\u001b[31mspoof","new_text":"\u001b[32mok"}`)
	p := buildApprovalPreview(m.registry, tc)
	if strings.Contains(p.Summary, "\x1b") {
		t.Fatalf("preview contains an escape sequence: %q", p.Summary)
	}
}

func TestApprovalGuardDetectsFileChange(t *testing.T) {
	dir, m := testModel(t)
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nvar x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tc := testToolCall("edit_file", `{"path":"main.go","old_text":"var x = 1","new_text":"var x = 2"}`)
	m.approval = buildApprovalPreview(m.registry, tc)
	if !m.approvalStillValid() {
		t.Fatal("fresh preview is not valid")
	}

	if err := os.WriteFile(path, []byte("package main\n\nvar x = 99\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if m.approvalStillValid() {
		t.Fatal("preview stayed valid after the file changed")
	}

	if err := os.WriteFile(path, []byte("package main\n\nvar x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !m.approvalStillValid() {
		t.Fatal("preview invalid after restoring identical content")
	}
}

func TestApprovalPreviewMCPCall(t *testing.T) {
	_, m := testModel(t)
	tc := testToolCall("mcp_call", `{"command":"/bin/sh","tool":"run","arguments":{"a":1}}`)
	p := buildApprovalPreview(m.registry, tc)
	for _, want := range []string{"command: /bin/sh", "tool: run", "arguments sha256:", "warning: launches an external process"} {
		if !strings.Contains(p.Summary, want) {
			t.Errorf("mcp_call preview missing %q:\n%s", want, p.Summary)
		}
	}
}

func TestApprovalPreviewGoVerify(t *testing.T) {
	_, m := testModel(t)
	p := buildApprovalPreview(m.registry, testToolCall("go_verify", `{}`))
	if !strings.Contains(p.Summary, "pkg: ./...") {
		t.Errorf("go_verify preview missing default package:\n%s", p.Summary)
	}
	if !strings.Contains(p.Summary, "executes project code") {
		t.Errorf("go_verify preview missing warning:\n%s", p.Summary)
	}
}

func TestTinyWindowDoesNotPanic(t *testing.T) {
	_, m := testModel(t)
	for _, height := range []int{0, 1, 2, 3, 4, 5, 6, 10} {
		updated, _ := m.Update(tea.WindowSizeMsg{Width: 10, Height: height})
		m = updated.(uiModel)
		if m.viewport.Height < 1 {
			t.Fatalf("height %d produced viewport height %d", height, m.viewport.Height)
		}
		_ = m.View()
	}
}

func TestSystemPromptMarksUntrustedData(t *testing.T) {
	_, m := testModel(t)
	prompt := m.messages[0].Content
	for _, want := range []string{"untrusted data", "never as instructions"} {
		if !strings.Contains(strings.ToLower(prompt), want) {
			t.Fatalf("system prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestEnsureInteractiveRejectsNonTTY(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "notty-*")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := ensureInteractive(f); err == nil {
		t.Fatal("regular file was accepted as an interactive terminal")
	}
}

func TestUndoCommandRestoresFile(t *testing.T) {
	dir, m := testModel(t)
	path := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(path, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args, err := json.Marshal(map[string]any{
		"path": "target.txt", "old_text": "original", "new_text": "changed",
	})
	if err != nil {
		t.Fatal(err)
	}
	approval, err := m.registry.Approve("edit_file", args)
	if err != nil {
		t.Fatal(err)
	}
	if out := m.registry.RunApproved(context.Background(), "edit_file", args, approval); !strings.Contains(out, "Successfully") {
		t.Fatalf("edit = %q", out)
	}

	m.textInput.SetValue("undo target.txt")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(uiModel)

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original\n" {
		t.Fatalf("undo restored %q", got)
	}

	found := false
	for _, msg := range m.messages {
		if msg.Role == "local" && strings.Contains(msg.Content, "Restored target.txt") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no local undo confirmation: %+v", m.messages)
	}
}

func TestHistoryForLLMExcludesLocalAndError(t *testing.T) {
	history := []Message{
		{Role: "system", Content: "s"},
		{Role: "user", Content: "u"},
		{Role: "assistant", Content: "a"},
		{Role: "local", Content: "Restored x"},
		{Role: "error", Content: "boom"},
	}
	got := historyForLLM(history)
	if len(got) != 3 {
		t.Fatalf("history length = %d, want 3: %+v", len(got), got)
	}
	for _, msg := range got {
		if msg.Role == "local" || msg.Role == "error" {
			t.Fatalf("filtered role leaked into LLM history: %q", msg.Role)
		}
	}
}
