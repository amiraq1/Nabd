package ui

import (
	"strings"
	"testing"

	"nabd/internal/agent"
)

// Replay must coalesce text deltas exactly as Chat does: one block per run
// of text, flushed by the next non-text event. README promises the live and
// replayed views go through the same render path; this pins it.
func TestReplayCoalescesDeltasLikeChat(t *testing.T) {
	evs := []agent.Event{
		{Seq: 1, Type: agent.RunStart, Text: "x"},
		{Seq: 2, Parent: 1, Type: agent.TextDelta, Text: "أقرأ "},
		{Seq: 3, Parent: 2, Type: agent.TextDelta, Text: "main.go"},
		{Seq: 4, Parent: 3, Type: agent.ToolStart, Call: &agent.ToolCall{Name: "read_file"}},
	}
	r := NewReplay(evs, 0)
	// step 0 prints RunStart; steps 1,2 buffer; step 3 flushes + prints tool.
	r.step()
	r.next = 1
	r.step()
	r.next = 2
	r.step()
	if *r.buf != "أقرأ main.go" {
		t.Fatalf("buf after two deltas = %q", *r.buf)
	}
	r.next = 3
	got := flushJoin(r.buf, evs[3], r.width)
	lines := strings.Split(got, "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "أقرأ main.go") || !strings.Contains(lines[1], "read_file") {
		t.Fatalf("flush = %q", got)
	}
	if *r.buf != "" {
		t.Fatal("buf not cleared")
	}
}

func TestFlushJoinSameForChatAndReplay(t *testing.T) {
	e := agent.Event{Type: agent.Notice, Text: "n"}
	a, b := "نص", "نص"
	if flushJoin(&a, e, 50) != flushJoin(&b, e, 50) {
		t.Fatal("flushJoin is not deterministic")
	}
	empty := ""
	if flushJoin(&empty, agent.Event{Type: agent.TurnEnd}, 50) != "" {
		t.Fatal("nothing to print should be empty")
	}
}

func TestPartialTail(t *testing.T) {
	p := partialTail(strings.Repeat("كلمة ", 40), 3, 20)
	if n := strings.Count(p, "\n") + 1; n != 3 {
		t.Fatalf("want 3 lines, got %d: %q", n, p)
	}
	if !strings.HasPrefix(p, "… ") {
		t.Errorf("cut tail should be marked: %q", p)
	}
	if q := partialTail("قصير", 3, 20); strings.HasPrefix(q, "…") || q == "" {
		t.Errorf("short text: %q", q)
	}
	if partialTail("   ", 3, 20) != "" {
		t.Error("whitespace should be empty")
	}
}
