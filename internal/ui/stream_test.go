package ui

import (
	"strings"
	"testing"

	"nabd/internal/agent"
)

func delta(s string) agent.Event { return agent.Event{Type: agent.TextDelta, Text: s} }

func TestRendererCoalescesDeltas(t *testing.T) {
	r := &Renderer{Width: 50}
	for _, s := range []string{"أقرأ ", "main.go", " أولًا."} {
		if out := r.Feed(delta(s)); out != "" {
			t.Fatalf("delta printed early: %q", out)
		}
	}
	if !r.Pending() {
		t.Fatal("expected pending text")
	}
	out := r.Feed(agent.Event{Type: agent.ToolStart, Call: &agent.ToolCall{Name: "read_file"}})
	lines := strings.Split(out, "\n")
	if len(lines) != 2 {
		t.Fatalf("want text block + tool line, got %d lines: %q", len(lines), out)
	}
	if !strings.Contains(lines[0], "أقرأ main.go أولًا.") {
		t.Errorf("text not joined: %q", lines[0])
	}
	if !strings.Contains(lines[1], "read_file") {
		t.Errorf("tool line missing: %q", lines[1])
	}
	if r.Pending() {
		t.Error("buffer not cleared after flush")
	}
}

func TestRendererFlushEmptyIsEmpty(t *testing.T) {
	r := &Renderer{}
	if r.Flush() != "" {
		t.Fatal("flush of nothing should be empty")
	}
	r.Feed(delta("   "))
	if r.Flush() != "" {
		t.Fatal("whitespace-only should flush to nothing")
	}
}

func TestRendererNonTextPassesThrough(t *testing.T) {
	r := &Renderer{Width: 50}
	got := r.Feed(agent.Event{Type: agent.UserMsg, Text: "hi"})
	want := RenderEvent(agent.Event{Type: agent.UserMsg, Text: "hi"}, 50)
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRendererPartialTail(t *testing.T) {
	r := &Renderer{Width: 20}
	r.Feed(delta(strings.Repeat("كلمة ", 40)))
	p := r.Partial(3)
	n := strings.Count(p, "\n") + 1
	if n != 3 {
		t.Fatalf("want 3 lines, got %d: %q", n, p)
	}
	if !strings.HasPrefix(p, "… ") {
		t.Errorf("truncated tail should be marked: %q", p)
	}
	if r.Partial(100) == "" || strings.HasPrefix(r.Partial(100), "…") {
		t.Error("untruncated partial should not carry the ellipsis")
	}
}
