package ui

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"nabd/internal/event"

	tea "github.com/charmbracelet/bubbletea"
)

// R3 (audit 2026-10-06, re-verified 2026-10-09 at 1797602): the chat
// streaming render boundary must sanitize like the feed path. sanitizeStreamText
// is applied at the render points (streaming view, scrollback flush); the
// accumulation buffer m.buf stays raw.
func TestChatStreamingSanitizesBidi(t *testing.T) {
	t.Setenv("NABD_RTL", "off")
	hostile := "Amount: 100\u202E USD"

	out := partialTail(sanitizeStreamText(hostile), 6, 80)
	if strings.Contains(out, "\u202E") {
		t.Errorf("RLO survived chat streaming render: %q", out)
	}
	if !strings.Contains(out, "Amount: 100 USD") {
		t.Errorf("readable text lost in chat streaming render: %q", out)
	}
}

func TestChatStreamingSanitizesANSI(t *testing.T) {
	t.Setenv("NABD_RTL", "off")
	hostile := "hello\x1b[31mRED\x1b[0m world"

	out := partialTail(sanitizeStreamText(hostile), 6, 80)
	if strings.Contains(out, "\x1b") {
		t.Errorf("ANSI survived chat streaming render: %q", out)
	}
	if !strings.Contains(out, "helloRED world") {
		t.Errorf("readable text lost in chat streaming render: %q", out)
	}
}

func TestChatStreamingPreservesArabic(t *testing.T) {
	t.Setenv("NABD_RTL", "off")
	arabic := "مرحبا بالعالم"

	out := partialTail(sanitizeStreamText(arabic), 6, 80)
	if !strings.Contains(out, arabic) {
		t.Errorf("Arabic text altered by chat streaming render: %q", out)
	}
}

func TestFlushJoinSanitizesStreamBuffer(t *testing.T) {
	t.Setenv("NABD_RTL", "off")
	buf := "Amount: 100\u202E USD\nhello\x1b[31mRED\x1b[0m world\nمرحبا"

	out := flushJoin(&buf, event.Event{Type: event.TextDelta}, 80)
	if strings.Contains(out, "\u202E") {
		t.Errorf("RLO survived flushJoin: %q", out)
	}
	if strings.Contains(out, "\x1b") {
		t.Errorf("ANSI survived flushJoin: %q", out)
	}
	for _, want := range []string{"Amount: 100 USD", "helloRED world", "مرحبا"} {
		if !strings.Contains(out, want) {
			t.Errorf("flushJoin lost readable text %q in: %q", want, out)
		}
	}
	if buf != "" {
		t.Errorf("flushJoin did not drain the buffer, left %q", buf)
	}
}

// TestReplayViewSanitizesStreamBuffer drives the real Replay model
// (NewReplay -> step -> View) with hostile deltas. It fails if Replay.View
// renders the raw buffer.
func TestReplayViewSanitizesStreamBuffer(t *testing.T) {
	t.Setenv("NABD_RTL", "off")
	hostile := "Amount: 100\u202E USD\nhello\x1b[31mRED\x1b[0m world"
	evs := []event.Event{
		{Seq: 1, Type: event.TextDelta, Text: hostile},
	}
	r := NewReplay(evs, 0)
	r.step() // buffers the delta, as in a live replay
	if *r.buf == "" {
		t.Fatal("step did not buffer the delta")
	}
	out := r.View()
	if strings.Contains(out, "\u202E") {
		t.Errorf("RLO survived Replay View: %q", out)
	}
	if strings.Contains(out, "\x1b[") {
		t.Errorf("ANSI survived Replay View: %q", out)
	}
	if !strings.Contains(out, "Amount: 100 USD") || !strings.Contains(out, "helloRED world") {
		t.Errorf("Replay View lost readable text: %q", out)
	}
}

// TestReplayAdvanceSanitizesFinalFlush drives the real Replay.advance() for
// the end-of-session flush and inspects the string it hands to tea.Println.
// It fails if advance() renders the raw buffer.
func TestReplayAdvanceSanitizesFinalFlush(t *testing.T) {
	t.Setenv("NABD_RTL", "off")
	hostile := "Amount: 100\u202E USD\nhello\x1b[31mRED\x1b[0m world"
	evs := []event.Event{
		{Seq: 1, Type: event.TextDelta, Text: hostile},
	}
	r := NewReplay(evs, 0)
	r.step() // buffers the delta
	r.next = len(r.events)

	out := printedByAdvance(t, r)
	if strings.Contains(out, "\u202E") {
		t.Errorf("RLO survived Replay final flush: %q", out)
	}
	if strings.Contains(out, "\x1b[") {
		t.Errorf("ANSI survived Replay final flush: %q", out)
	}
	if !strings.Contains(out, "Amount: 100 USD") || !strings.Contains(out, "helloRED world") {
		t.Errorf("Replay final flush lost readable text: %q", out)
	}
}

// printedByAdvance runs Replay.advance() and extracts the string it hands to
// tea.Println. tea.Sequence wraps commands in an unexported sequenceMsg and
// tea.Println wraps the text in an unexported printLineMessage, so both are
// unwrapped with reflection. FRAGILE: depends on bubbletea unexported types;
// a bubbletea upgrade may break this helper (not the production code).
// printedByAdvance runs Replay.advance() and extracts the string it hands to
// tea.Println. tea.Sequence wraps commands in an unexported sequenceMsg and
// tea.Println wraps the text in an unexported printLineMessage, so both are
// unwrapped with reflection. FRAGILE: depends on bubbletea unexported types;
// a bubbletea upgrade may break this helper (not the production code).
func printedByAdvance(t *testing.T, r Replay) string {
	t.Helper()
	_, cmd := r.advance()
	if cmd == nil {
		t.Fatal("advance returned nil cmd for non-empty buffer")
	}

	seqMsg := cmd()
	seq := reflect.ValueOf(seqMsg)
	if !seq.IsValid() || seq.Kind() != reflect.Slice || seq.Len() == 0 {
		t.Fatalf("expected non-empty command sequence, got %T", seqMsg)
	}

	first := seq.Index(0)
	if !first.CanInterface() {
		t.Fatal("first sequence item cannot be inspected")
	}

	printlnCmd, ok := first.Interface().(tea.Cmd)
	if !ok {
		t.Fatalf("first sequence item is %T, want tea.Cmd", first.Interface())
	}

	printedMsg := printlnCmd()
	bodyVal := reflect.ValueOf(printedMsg)
	if !bodyVal.IsValid() || bodyVal.Kind() != reflect.Struct {
		t.Fatalf("println message is %T, want struct (Bubble Tea may have changed)", printedMsg)
	}

	body := bodyVal.FieldByName("messageBody")
	if !body.IsValid() {
		t.Fatalf("println message has no messageBody: %T", printedMsg)
	}
	if body.Kind() != reflect.String {
		t.Fatalf("println messageBody has kind %s, want string", body.Kind())
	}

	return body.String()
}

func TestChatStreamingArabicControlsPolicy(t *testing.T) {
	t.Setenv("NABD_RTL", "off")
	// Conscious policy (must match internal/display/sanitize.go):
	// - Preserved: U+061C (Arabic Letter Mark), U+200D (ZWJ), U+200E (LRM), U+200F (RLM)
	// - Stripped: U+202A-U+202E (overrides), U+2066-U+2069 (isolates) — spoofing vectors
	// - Stripped: U+200C (ZWNJ) — documented tradeoff, pinned by 4c54a31
	//   (breaks Persian/Urdu joining; kept for the security boundary)
	cases := []struct {
		name      string
		input     string
		preserved bool
	}{
		{"ALM U+061C", "نص\u061C", true},
		{"ZWJ U+200D", "نص\u200D", true},
		{"LRM U+200E", "نص\u200E", true},
		{"RLM U+200F", "نص\u200F", true},
		{"LRE U+202A", "نص\u202A", false},
		{"RLE U+202B", "نص\u202B", false},
		{"PDF U+202C", "نص\u202C", false},
		{"LRO U+202D", "نص\u202D", false},
		{"RLO U+202E", "نص\u202E", false},
		{"LRI U+2066", "نص\u2066", false},
		{"RLI U+2067", "نص\u2067", false},
		{"FSI U+2068", "نص\u2068", false},
		{"PDI U+2069", "نص\u2069", false},
		{"ZWNJ U+200C", "نص\u200C", false},
	}
	for _, tc := range cases {
		out := sanitizeStreamText(tc.input)
		ctrl := string([]rune(tc.input)[2:])
		got := strings.Contains(out, ctrl)
		if got != tc.preserved {
			t.Errorf("%s: preserved=%v, got %q from %q", tc.name, tc.preserved, out, tc.input)
		}
		// Readable text must survive regardless
		if !strings.Contains(out, "نص") {
			t.Errorf("%s: readable text lost in %q", tc.name, out)
		}
	}
}

func TestChatViewSanitizesLiveStream(t *testing.T) {
	t.Setenv("NABD_RTL", "off")
	ch := make(chan event.Event, 1)
	m := asChat(t, NewChat(runnerStub{}, ch))
	m.running = true

	hostile := "Amount: 100\u202E USD hello\x1b[31mRED\x1b[0m"
	updated, _ := m.Update(evMsg(event.Event{
		Type: event.TextDelta,
		Text: hostile,
	}))
	m = asChat(t, updated)

	out := m.View()

	if strings.Contains(out, "\u202E") {
		t.Errorf("RLO survived live Chat.View: %q", out)
	}
	if strings.Contains(out, "\x1b[31m") {
		t.Errorf("hostile ANSI color survived live Chat.View: %q", out)
	}
	for _, want := range []string{
		"Amount: 100 USD",
		"helloRED",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Chat.View lost readable text %q: %q", want, out)
		}
	}

	// Sanitization belongs at the display boundary; the buffer stays raw.
	if m.buf != hostile {
		t.Errorf("stream buffer changed: got %q, want original %q", m.buf, hostile)
	}
}

func TestToolEndSanitizesOutput(t *testing.T) {
	t.Setenv("NABD_RTL", "off")
	hostile := "result: ok\nAmount: 100\u202E USD\nhello\x1b[31mRED\x1b[0m world\nمرحبا بالعالم"
	call := &event.ToolCall{
		ID:     "call_1",
		Name:   "bash",
		Output: hostile,
		OK:     true,
	}

	out := toolEnd(call, 80)
	if strings.Contains(out, "\u202E") {
		t.Errorf("RLO survived toolEnd: %q", out)
	}
	if strings.Contains(out, "\x1b[") {
		t.Errorf("ANSI survived toolEnd: %q", out)
	}
	for _, want := range []string{"result: ok", "Amount: 100 USD", "helloRED world", "مرحبا بالعالم"} {
		if !strings.Contains(out, want) {
			t.Errorf("toolEnd lost readable text %q in: %q", want, out)
		}
	}
}

func TestToolEndPreservesTailTruncation(t *testing.T) {
	t.Setenv("NABD_RTL", "off")
	// 20 lines; tail() keeps only the last maxTailLines
	var lines []string
	for i := 1; i <= 20; i++ {
		lines = append(lines, fmt.Sprintf("line%02d", i))
	}
	call := &event.ToolCall{
		ID:     "call_1",
		Name:   "bash",
		Output: strings.Join(lines, "\n"),
		OK:     true,
	}

	out := toolEnd(call, 80)
	// Tail truncation must still work (last lines present, first lines cut)
	if strings.Contains(out, "line01\n") {
		t.Errorf("toolEnd did not truncate tail: %q", out)
	}
	if !strings.Contains(out, "line20") {
		t.Errorf("toolEnd lost tail content: %q", out)
	}
}

func TestRenderEventSanitizesNotice(t *testing.T) {
	t.Setenv("NABD_RTL", "off")
	hostile := "compact failed: \u202Eerr\x1b[31mRED\x1b[0m\nمرحبا"
	e := event.Event{Type: event.Notice, Text: hostile}

	out := RenderEvent(e, 80)
	if strings.Contains(out, "\u202E") {
		t.Errorf("RLO survived Notice render: %q", out)
	}
	if strings.Contains(out, "\x1b[") {
		t.Errorf("ANSI survived Notice render: %q", out)
	}
	for _, want := range []string{"compact failed:", "errRED", "مرحبا"} {
		if !strings.Contains(out, want) {
			t.Errorf("Notice render lost readable text %q in: %q", want, out)
		}
	}
}

func TestRenderEventSanitizesCompact(t *testing.T) {
	t.Setenv("NABD_RTL", "off")
	// Compact Text is a model-generated summary: untrusted.
	hostile := "Summary:\u202E hidden\x1b[31mRED\x1b[0m\nنص عربي"
	e := event.Event{Type: event.Compact, Text: hostile}

	out := RenderEvent(e, 80)
	if strings.Contains(out, "\u202E") {
		t.Errorf("RLO survived Compact render: %q", out)
	}
	if strings.Contains(out, "\x1b[") {
		t.Errorf("ANSI survived Compact render: %q", out)
	}
	for _, want := range []string{"Summary:", "hiddenRED", "نص عربي"} {
		if !strings.Contains(out, want) {
			t.Errorf("Compact render lost readable text %q in: %q", want, out)
		}
	}
}

func TestCallLineSanitizesNameAndArgs(t *testing.T) {
	t.Setenv("NABD_RTL", "off")
	// Model-controlled tool name and arguments: untrusted.
	call := &event.ToolCall{
		ID:   "call_1",
		Name: "bash\u202E",
		Args: json.RawMessage(`{"cmd": "echo \x1b[31mRED\x1b[0m"}`),
	}

	out := callLine(call)
	if strings.Contains(out, "\u202E") {
		t.Errorf("RLO survived callLine: %q", out)
	}
	if strings.Contains(out, "\x1b[") {
		t.Errorf("ANSI survived callLine: %q", out)
	}
	if !strings.Contains(out, "bash") {
		t.Errorf("callLine lost tool name: %q", out)
	}
}

func TestPermAskSanitizesCallLine(t *testing.T) {
	t.Setenv("NABD_RTL", "off")
	// The permission prompt is the security-critical path: the user decides
	// based on what they see.
	call := &event.ToolCall{
		ID:   "call_1",
		Name: "bash",
		Args: json.RawMessage(`{"cmd": "rm -rf /\u202E"}`),
	}
	e := event.Event{Type: event.PermAsk, Call: call}

	out := RenderEvent(e, 80)
	if strings.Contains(out, "\u202E") {
		t.Errorf("RLO survived PermAsk render: %q", out)
	}
	if !strings.Contains(out, "allow?") {
		t.Errorf("PermAsk render lost prompt: %q", out)
	}
}

func TestToolEndSanitizesName(t *testing.T) {
	t.Setenv("NABD_RTL", "off")
	rawName := "lookup\u202e\x1b[2JNAME"
	call := &event.ToolCall{
		Name: rawName,
		OK:   true,
	}

	got := toolEnd(call, 80)

	if strings.Contains(got, "\u202e") {
		t.Fatalf("bidi override survived ToolEnd: %q", got)
	}
	if strings.Contains(got, "\x1b[2J") {
		t.Fatalf("terminal clear-screen sequence survived ToolEnd: %q", got)
	}
	if !strings.Contains(got, "lookupNAME") {
		t.Fatalf("readable tool name was lost: %q", got)
	}
	if call.Name != rawName {
		t.Fatalf("original tool name was modified: got %q, want %q",
			call.Name, rawName)
	}
}
