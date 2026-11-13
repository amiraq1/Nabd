package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/amiraq1/nabd/internal/llm"
	"github.com/amiraq1/nabd/internal/tools"
	tea "github.com/charmbracelet/bubbletea"
)

type mockClient struct {
	mu         sync.Mutex
	responses  []llm.Response
	errs       []error
	requests   []llm.Request
	calls      int
	block      chan struct{}
	started    chan struct{}
	canceled   chan struct{}
	startOnce  sync.Once
	cancelOnce sync.Once
}

func (m *mockClient) Chat(ctx context.Context, req llm.Request) (*llm.Response, error) {
	m.mu.Lock()
	index := m.calls
	m.calls++
	m.requests = append(m.requests, req)
	m.mu.Unlock()

	if m.started != nil {
		m.startOnce.Do(func() { close(m.started) })
	}
	if m.block != nil {
		select {
		case <-ctx.Done():
			if m.canceled != nil {
				m.cancelOnce.Do(func() { close(m.canceled) })
			}
			return nil, ctx.Err()
		case <-m.block:
		}
	}

	if index < len(m.errs) && m.errs[index] != nil {
		return nil, m.errs[index]
	}
	if index < len(m.responses) {
		response := m.responses[index]
		return &response, nil
	}
	return &llm.Response{Message: llm.Message{Role: "assistant", Content: "done"}}, nil
}

func (m *mockClient) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func (m *mockClient) requestAt(index int) llm.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.requests[index]
}

func testModel(t *testing.T) (string, uiModel) {
	return testModelWithClient(t, &mockClient{})
}

func testModelWithClient(t *testing.T, client llm.Client) (string, uiModel) {
	t.Helper()
	dir := t.TempDir()
	reg, err := tools.NewRegistry(dir, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	t.Cleanup(func() { _ = reg.Close() })
	return dir, initialModel(reg, client)
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
	got := historyForLLM(history, defaultHistoryTokens)
	if len(got) != 3 {
		t.Fatalf("history length = %d, want 3: %+v", len(got), got)
	}
	for _, msg := range got {
		if msg.Role == "local" || msg.Role == "error" {
			t.Fatalf("filtered role leaked into LLM history: %q", msg.Role)
		}
	}
}

type funcClient func(context.Context, llm.Request) (*llm.Response, error)

func (f funcClient) Chat(ctx context.Context, req llm.Request) (*llm.Response, error) {
	return f(ctx, req)
}

func llmToolCall(id, name, args string) ToolCall {
	var tc ToolCall
	tc.ID = id
	tc.Type = "function"
	tc.Function.Name = name
	tc.Function.Arguments = args
	return tc
}

func runTurn(t *testing.T, m uiModel, input string, maxSteps int) uiModel {
	t.Helper()
	m.textInput.SetValue(input)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(uiModel)

	for step := 0; cmd != nil && step < maxSteps; step++ {
		msg := cmd()
		updated, cmd = m.Update(msg)
		m = updated.(uiModel)
	}
	if cmd != nil {
		t.Fatalf("turn did not settle after %d steps", maxSteps)
	}
	return m
}

func assertToolCallsAnswered(t *testing.T, messages []Message) {
	t.Helper()
	for i, msg := range messages {
		if msg.Role != "assistant" {
			continue
		}
		for _, tc := range msg.ToolCalls {
			found := false
			for _, next := range messages[i+1:] {
				if next.Role == "user" || (next.Role == "assistant" && len(next.ToolCalls) == 0) {
					break
				}
				if next.Role == "tool" && next.ToolCallID == tc.ID {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("tool call %s in message %d has no tool response", tc.ID, i)
			}
		}
	}
}

func TestAgentLoopExecutesTool(t *testing.T) {
	mc := &mockClient{responses: []llm.Response{
		{Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{
			llmToolCall("call_1", "read_file", `{"path":"a.txt"}`),
		}}},
		{Message: llm.Message{Role: "assistant", Content: "final answer"}},
	}}
	dir, m := testModelWithClient(t, mc)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	m = runTurn(t, m, "read a.txt", 20)
	if mc.callCount() != 2 {
		t.Fatalf("client calls = %d, want 2", mc.callCount())
	}
	if m.busy {
		t.Fatal("model is still busy after the final answer")
	}
	if len(mc.requests) < 2 {
		t.Fatalf("requests = %d", len(mc.requests))
	}

	second := mc.requestAt(1)
	foundTool := false
	for _, msg := range second.Messages {
		if msg.Role == "tool" && msg.ToolCallID == "call_1" && strings.Contains(msg.Content, "hello") {
			foundTool = true
		}
	}
	if !foundTool {
		t.Fatalf("second request has no tool result: %+v", second.Messages)
	}

	foundFinal := false
	for _, msg := range m.messages {
		if msg.Role == "assistant" && msg.Content == "final answer" {
			foundFinal = true
		}
	}
	if !foundFinal {
		t.Fatalf("final answer missing: %+v", m.messages)
	}
	assertToolCallsAnswered(t, m.messages)
}

func TestAgentCancelsRequestOnCtrlC(t *testing.T) {
	mc := &mockClient{
		block:    make(chan struct{}),
		started:  make(chan struct{}),
		canceled: make(chan struct{}),
	}
	_, m := testModelWithClient(t, mc)

	m.textInput.SetValue("hello")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(uiModel)
	if !m.busy || cmd == nil {
		t.Fatal("request did not start")
	}

	done := make(chan struct{})
	go func() {
		_ = cmd()
		close(done)
	}()

	select {
	case <-mc.started:
	case <-time.After(2 * time.Second):
		t.Fatal("client was not called")
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	_ = updated

	select {
	case <-mc.canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight request was not canceled")
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("blocked client did not return")
	}
}

func TestMaxIterationsSynthesizesToolErrors(t *testing.T) {
	calls := 0
	client := funcClient(func(context.Context, llm.Request) (*llm.Response, error) {
		calls++
		return &llm.Response{Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{
			llmToolCall(fmt.Sprintf("call_%d", calls), "read_file", `{"path":"a.txt"}`),
		}}}, nil
	})
	dir, m := testModelWithClient(t, client)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.maxIterations = 2

	m = runTurn(t, m, "loop", 20)
	if calls != 2 {
		t.Fatalf("client calls = %d, want 2", calls)
	}
	if m.busy {
		t.Fatal("model still busy at the iteration limit")
	}
	found := false
	for _, msg := range m.messages {
		if msg.Role == "tool" && strings.Contains(msg.Content, "iteration limit") {
			found = true
		}
	}
	if !found {
		t.Fatalf("synthetic iteration-limit tool error missing: %+v", m.messages)
	}
	assertToolCallsAnswered(t, m.messages)
}

func TestRepeatedCallsBlocked(t *testing.T) {
	calls := 0
	client := funcClient(func(context.Context, llm.Request) (*llm.Response, error) {
		calls++
		return &llm.Response{Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{
			llmToolCall(fmt.Sprintf("call_%d", calls), "read_file", `{"path":"a.txt"}`),
		}}}, nil
	})
	dir, m := testModelWithClient(t, client)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	m = runTurn(t, m, "loop", 30)
	if calls != 3 {
		t.Fatalf("client calls = %d, want 3", calls)
	}
	if m.busy {
		t.Fatal("model still busy after blocking a repeated call")
	}
	found := false
	for _, msg := range m.messages {
		if msg.Role == "tool" && strings.Contains(msg.Content, "repeated identical tool call") {
			found = true
		}
	}
	if !found {
		t.Fatalf("blocked-call message missing: %+v", m.messages)
	}
	assertToolCallsAnswered(t, m.messages)
}

func TestMalformedToolArgumentsReported(t *testing.T) {
	mc := &mockClient{responses: []llm.Response{
		{Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{
			llmToolCall("call_1", "read_file", `{"path":`),
		}}},
		{Message: llm.Message{Role: "assistant", Content: "done"}},
	}}
	_, m := testModelWithClient(t, mc)

	m = runTurn(t, m, "bad args", 20)
	found := false
	for _, msg := range m.messages {
		if msg.Role == "tool" && strings.HasPrefix(msg.Content, "Error:") {
			found = true
		}
	}
	if !found {
		t.Fatalf("malformed tool arguments were not reported: %+v", m.messages)
	}
	if mc.callCount() != 2 {
		t.Fatalf("client calls = %d, want 2", mc.callCount())
	}
}

func TestLLMErrorSurfaces(t *testing.T) {
	mc := &mockClient{errs: []error{fmt.Errorf("connection refused")}}
	_, m := testModelWithClient(t, mc)

	m = runTurn(t, m, "hello", 5)
	if m.busy {
		t.Fatal("model still busy after an LLM error")
	}
	found := false
	for _, msg := range m.messages {
		if msg.Role == "error" && strings.Contains(msg.Content, "connection refused") {
			found = true
		}
	}
	if !found {
		t.Fatalf("LLM error missing: %+v", m.messages)
	}
}

func TestTrimHistoryKeepsSystemAndLastUnit(t *testing.T) {
	messages := []Message{{Role: "system", Content: "sys"}}
	for i := 0; i < 20; i++ {
		messages = append(messages, Message{Role: "user", Content: strings.Repeat("x", 100)})
		messages = append(messages, Message{Role: "assistant", Content: strings.Repeat("y", 100)})
	}

	got := trimHistory(messages, 150)
	if len(got) >= len(messages) {
		t.Fatalf("history was not trimmed: %d messages", len(got))
	}
	if got[0].Role != "system" {
		t.Fatalf("system message lost: %+v", got)
	}
	if got[len(got)-1].Content != messages[len(messages)-1].Content {
		t.Fatalf("newest message lost")
	}
}

func TestTrimHistoryKeepsToolPairs(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "old"},
		{Role: "assistant", ToolCalls: []ToolCall{llmToolCall("call_1", "read_file", `{"path":"a.txt"}`)}},
		{Role: "tool", ToolCallID: "call_1", Content: "hello"},
		{Role: "user", Content: "new"},
	}
	budget := estimateMessages(messages[:1]) + estimateMessages(messages[2:4]) + estimateMessages(messages[4:])

	got := trimHistory(messages, budget)
	if len(got) != 4 {
		t.Fatalf("trimmed length = %d, want 4: %+v", len(got), got)
	}
	if got[1].Role != "assistant" || got[2].Role != "tool" || got[2].ToolCallID != "call_1" {
		t.Fatalf("tool pair was split: %+v", got)
	}
}

func TestTrimHistoryStartsAtUser(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "old"},
		{Role: "assistant", Content: strings.Repeat("a", 400)},
		{Role: "user", Content: "new"},
	}
	budget := estimateMessages(messages[:1]) + estimateMessages(messages[2:3]) + estimateMessages(messages[3:])

	got := trimHistory(messages, budget)
	if len(got) != 2 || got[1].Role != "user" || got[1].Content != "new" {
		t.Fatalf("trimmed history does not start at a user turn: %+v", got)
	}
}
