package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/provider"
	"nabd/internal/store"
)

// TestFeedCalibrationNoticeFilteredExact tests that routine calibration notices
// are filtered out from the Feed without leaving ghost lines, separators, or phantom unread counts.
func TestFeedCalibrationNoticeFilteredExact(t *testing.T) {
	f := NewFeed()
	f.width = 60
	f.height = 12

	evs := []agent.Event{
		{Seq: 1, Type: agent.UserMsg, Text: "first"},
	}
	_, _ = f.Update(agentEventBatchMsg{Events: evs})

	// Scroll up / browse older output to activate unseen tracking
	f.scrollTop = 0
	f.follow = false
	f.unseen = 0

	// Capture state immediately before hidden-only batch
	linesBefore := slices.Clone(f.lines)
	unseenBefore := f.unseen
	scrollBefore := f.scrollTop

	// Hidden-only batch
	evsHidden := []agent.Event{
		{Seq: 2, Type: agent.Notice, Calib: &agent.Calibration{PromptTokens: 100}, Text: "calibration: token ratio adopted 1.50"},
	}
	_, _ = f.Update(agentEventBatchMsg{Events: evsHidden})

	// Assert identical rendered lines and unchanged unseen/scroll position
	if !slices.Equal(linesBefore, f.lines) {
		t.Fatalf("hidden diagnostic modified rendered lines:\ngot:\n%s\nwant:\n%s", strings.Join(f.lines, "\n"), strings.Join(linesBefore, "\n"))
	}
	if f.unseen != unseenBefore {
		t.Fatalf("hidden diagnostic incremented unseen: got %d, want %d", f.unseen, unseenBefore)
	}
	if f.scrollTop != scrollBefore {
		t.Fatalf("hidden diagnostic changed scroll position: got %d, want %d", f.scrollTop, scrollBefore)
	}

	// Two-feed comparison to test spacing and formatting equivalence:
	// feedA receives only visible events.
	// feedB receives the identical visible events with a calibration notice inserted.
	visible1 := agent.Event{Seq: 1, Type: agent.UserMsg, Text: "first"}
	calibNotice := agent.Event{Seq: 2, Type: agent.Notice, Calib: &agent.Calibration{PromptTokens: 100}, Text: "calibration: token ratio adopted 1.50"}
	visible2 := agent.Event{Seq: 3, Type: agent.UserMsg, Text: "second"}
	fallbackNotice := agent.Event{Seq: 4, Type: agent.Notice, Text: "provider fallback to generic model"}
	wordNotice := agent.Event{Seq: 5, Type: agent.Notice, Text: "we need to rerun calibration for this sensor"}
	errNotice := agent.Event{Seq: 6, Type: agent.RunError, Err: "exhausted routes"}

	feedA := NewFeed()
	feedA.width = 60
	feedA.height = 12
	_, _ = feedA.Update(agentEventBatchMsg{Events: []agent.Event{visible1, visible2, fallbackNotice, wordNotice, errNotice}})

	feedB := NewFeed()
	feedB.width = 60
	feedB.height = 12
	_, _ = feedB.Update(agentEventBatchMsg{Events: []agent.Event{visible1, calibNotice, visible2, fallbackNotice, wordNotice, errNotice}})

	// Exact line-by-line comparison: the calibration notice must produce zero lines,
	// zero empty cards, and zero extra spacing separators.
	if !slices.Equal(feedA.lines, feedB.lines) {
		t.Fatalf("feed with calibration notice differed from feed without it:\nfeedB (with calib):\n%s\n\nfeedA (without calib):\n%s",
			strings.Join(feedB.lines, "\n"), strings.Join(feedA.lines, "\n"))
	}

	// Retain visibility checks on feedB:
	renderedB := strings.Join(feedB.lines, "\n")
	if strings.Contains(renderedB, "ratio adopted 1.50") {
		t.Fatalf("routine calibration diagnostic leaked into feed:\n%s", renderedB)
	}
	if !strings.Contains(renderedB, "provider fallback to generic model") {
		t.Fatalf("important fallback notice was suppressed:\n%s", renderedB)
	}
	if !strings.Contains(renderedB, "we need to rerun calibration for this sensor") {
		t.Fatalf("ordinary notice containing word 'calibration' was suppressed:\n%s", renderedB)
	}
	if !strings.Contains(renderedB, "exhausted routes") {
		t.Fatalf("error notice was suppressed:\n%s", renderedB)
	}

	// Verify that old Notice events without Calib (e.g. from historical session logs)
	// remain visible in Feed, proving no text-based filtering is applied.
	oldNotice := agent.Event{
		Seq:  7,
		Type: agent.Notice,
		Text: "calibration: token ratio adopted 1.45 (legacy)",
	}
	_, _ = feedB.Update(agentEventBatchMsg{Events: []agent.Event{oldNotice}})
	if !strings.Contains(strings.Join(feedB.lines, "\n"), "calibration: token ratio adopted 1.45 (legacy)") {
		t.Fatalf("legacy notice without Calib must remain visible in feed")
	}
}

// TestRegressionUnseenProxyItemUpdate verifies that updating an earlier rendered item
// while keeping total rendered line count and the final line unchanged correctly increments
// the unseen counter. The old proxy (based on line count and last line) failed to detect this.
func TestRegressionUnseenProxyItemUpdate(t *testing.T) {
	runCase := func(t *testing.T, modal bool) {
		f := NewFeed()
		f.width = 60
		f.height = 12

		// ToolStart (running) renders 2 lines:
		// Line 0: "⚙ bash"
		// Line 1: "  ···"
		// UserMsg renders:
		// Line 2: "" (boundary separator)
		// Line 3: "You"
		// Line 4: "bottom message"
		initBatch := []agent.Event{
			{Seq: 1, Type: agent.ToolStart, Call: &agent.ToolCall{ID: "c1", Name: "bash"}},
			{Seq: 2, Type: agent.UserMsg, Text: "bottom message"},
		}
		_, _ = f.Update(agentEventBatchMsg{Events: initBatch})

		if modal {
			f.modalVisible = true
		} else {
			// Browse older output to activate unseen counting
			f.follow = false
			f.scrollTop = 0
		}
		f.unseen = 0

		beforeLines := slices.Clone(f.lines)
		beforeLen := len(beforeLines)
		if beforeLen == 0 {
			t.Fatal("expected non-empty rendered lines")
		}
		beforeLast := beforeLines[beforeLen-1]

		// ToolEnd arrives with Duration > 0 (e.g. 50ms) and no output:
		// Tool card updates in-place from "⚙ bash" / "  ···" to "✓ bash" / "  50ms".
		// Total lines remain exactly 5, and the last line remains "bottom message".
		updateBatch := []agent.Event{
			{Seq: 3, Type: agent.ToolEnd, Call: &agent.ToolCall{ID: "c1", Name: "bash", OK: true, MS: 50}},
		}
		_, _ = f.Update(agentEventBatchMsg{Events: updateBatch})

		afterLen := len(f.lines)
		afterLast := f.lines[afterLen-1]

		// Assert fixture conditions explicitly:
		if beforeLen != afterLen {
			t.Fatalf("fixture condition failed: line count changed from %d to %d", beforeLen, afterLen)
		}
		if beforeLast != afterLast {
			t.Fatalf("fixture condition failed: final line changed: got %q, want %q", afterLast, beforeLast)
		}
		if slices.Equal(beforeLines, f.lines) {
			t.Fatalf("fixture condition failed: rendered lines did not mutate")
		}

		// Verify unseen increments
		if f.unseen != 1 {
			t.Fatalf("modal=%v: expected unseen=1 after earlier item update, got %d", modal, f.unseen)
		}
	}

	t.Run("browsing_older_output", func(t *testing.T) {
		runCase(t, false)
	})

	t.Run("modal_paused", func(t *testing.T) {
		runCase(t, true)
	})
}

type dummyTools struct{}

func (dummyTools) Specs() []provider.ToolSpec { return nil }
func (dummyTools) Run(ctx context.Context, c provider.ToolCall) (string, bool, error) {
	return "", false, nil
}
func (dummyTools) Check(tool string) (agent.Verdict, string)              { return agent.VerdictDeny, "no" }
func (dummyTools) Record(tool string, d agent.Decision)                   {}
func (dummyTools) Effective(tool string, d agent.Decision) agent.Decision { return d }
func (dummyTools) Ask(ctx context.Context, c agent.ToolCall) agent.Decision {
	return agent.Deny
}

type fnSink func(agent.Event) error

func (s fnSink) Emit(e agent.Event) error { return s(e) }

// TestCalibrationNoticeJournalAndFeedIntegration tests the end-to-end path with a synthetic provider:
// 1. Provider sends usage triggering real Budget.Calibrate ratchet rise.
// 2. Loop emits EventCalib, EventProviderUsage, and Notice with Calib.
// 3. Events route through Fanout to temporary store.JSONL and Feed UI.
// 4. Reading JSONL confirms Notice, Calib.PromptTokens, and neighboring events.
// 5. Feed UI confirms calibration notice is absent, but answer text is present.
func TestCalibrationNoticeJournalAndFeedIntegration(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(`data: {"choices":[{"delta":{"content":"synthetic answer"},"finish_reason":""}]}` + "\n\n"))
		w.Write([]byte(`data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":5}}` + "\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	journalPath := filepath.Join(t.TempDir(), "session.jsonl")
	journal, err := store.NewJSONL(journalPath)
	if err != nil {
		t.Fatalf("failed to create test journal: %v", err)
	}

	feed := NewFeed()
	feed.width = 60
	feed.height = 20

	feedSink := fnSink(func(e agent.Event) error {
		_, _ = feed.Update(agentEventBatchMsg{Events: []agent.Event{e}})
		return nil
	})

	prov := &provider.OpenAICompat{
		Key:     "synthetic-key",
		Model:   "synthetic-model",
		BaseURL: srv.URL,
		Client:  srv.Client(),
	}

	loop := &agent.Loop{
		Provider: prov,
		Tools:    dummyTools{},
		Budget:   agent.NewBudget(),
		Gate:     dummyTools{},
		Human:    dummyTools{},
		Sink:     agent.Fanout{journal, feedSink},
	}

	if err := loop.Run(context.Background(), "synthetic question"); err != nil {
		t.Fatalf("loop.Run failed: %v", err)
	}

	if err := journal.Close(); err != nil {
		t.Fatalf("journal.Close failed: %v", err)
	}

	// Read recorded events back from temporary journal
	recorded, err := store.Read(journalPath)
	if err != nil {
		t.Fatalf("store.Read failed: %v", err)
	}

	var foundNotice bool
	var foundEventCalib bool
	var foundProviderUsage bool
	var foundUserMsg bool
	var foundTurnEnd bool

	for _, ev := range recorded {
		switch ev.Type {
		case agent.UserMsg:
			if ev.Text == "synthetic question" {
				foundUserMsg = true
			}
		case agent.EventCalib:
			if ev.Calib != nil && ev.Calib.PromptTokens == 20 {
				foundEventCalib = true
			}
		case agent.EventProviderUsage:
			if ev.Usage != nil && ev.Usage.PromptTokens == 20 {
				foundProviderUsage = true
			}
		case agent.Notice:
			if strings.HasPrefix(ev.Text, "calibration: token ratio") {
				foundNotice = true
				if ev.Calib == nil {
					t.Fatalf("recorded Notice missing Calib struct: %+v", ev)
				}
				if ev.Calib.PromptTokens != 20 {
					t.Fatalf("recorded Notice Calib.PromptTokens = %d, want 20", ev.Calib.PromptTokens)
				}
			}
		case agent.TurnEnd:
			foundTurnEnd = true
		}
	}

	if !foundNotice {
		t.Fatalf("expected calibration Notice event in journal, not found in recorded events: %+v", recorded)
	}
	if !foundEventCalib {
		t.Fatalf("expected EventCalib in journal")
	}
	if !foundProviderUsage {
		t.Fatalf("expected EventProviderUsage in journal")
	}
	if !foundUserMsg {
		t.Fatalf("expected UserMsg in journal")
	}
	if !foundTurnEnd {
		t.Fatalf("expected TurnEnd in journal")
	}

	// Assert the routine notice is absent from Feed
	feedText := strings.Join(feed.lines, "\n")
	if strings.Contains(feedText, "calibration: token ratio") || strings.Contains(feedText, "conservative ratchet") {
		t.Fatalf("routine calibration notice leaked into Feed:\n%s", feedText)
	}

	// Assert assistant response text is rendered in Feed
	if !strings.Contains(feedText, "synthetic answer") {
		t.Fatalf("feed missing assistant answer:\n%s", feedText)
	}
}
