package ui

import (
	"strings"
	"testing"
	"time"

	"nabd/internal/agent"
)

// TestThroughputDoesNotWriteStatus proves the core architectural invariant:
// runtimeThroughputText never touches m.status or rankHint in any path.
// The status line belongs to phase text and security prompts.
func TestThroughputDoesNotWriteStatus(t *testing.T) {
	m := NewFeed()
	m.width = 80
	m.height = 24
	m.running = true
	m.busy = true
	start := time.Now()
	m.reqStartedAt = start.Add(-1500 * time.Millisecond)
	m.streamStartedAt = start.Add(-1200 * time.Millisecond)
	m.streamFirstDeltaAt = start.Add(-1000 * time.Millisecond)
	m.streamLastDeltaAt = start.Add(-100 * time.Millisecond)
	m.streamedChars = 400

	if m.status != "" || m.statusRank != 0 {
		t.Fatalf("precondition: status or rank set before call: status=%q rank=%d", m.status, m.statusRank)
	}

	got := m.runtimeThroughputText(80)
	if got == "" {
		t.Fatal("expected non-empty throughput text")
	}
	if m.status != "" || m.statusRank != 0 {
		t.Fatalf("runtimeThroughputText mutated m.status (%q) or rank (%d)", m.status, m.statusRank)
	}

	gotStatus := m.runtimeStatusText(80)
	if gotStatus != got {
		t.Fatalf("runtimeStatusText did not return throughput: got %q, want %q", gotStatus, got)
	}
	if m.status != "" || m.statusRank != 0 {
		t.Fatalf("runtimeStatusText mutated m.status (%q) or rank (%d)", m.status, m.statusRank)
	}
}

// TestThroughputDecisionPendingPriority proves precedence: decisionPending
// remains visible and throughput is suppressed, never the reverse.
func TestThroughputDecisionPendingPriority(t *testing.T) {
	m := NewFeed()
	m.width = 80
	m.height = 24
	m.running = true
	m.busy = true
	m.decisionPending = true
	start := time.Now()
	m.reqStartedAt = start.Add(-1500 * time.Millisecond)
	m.streamStartedAt = start.Add(-1200 * time.Millisecond)
	m.streamFirstDeltaAt = start.Add(-1000 * time.Millisecond)
	m.streamLastDeltaAt = start.Add(-100 * time.Millisecond)
	m.streamedChars = 400

	got := m.runtimeStatusText(80)
	if got != "Waiting for permission…" {
		t.Fatalf("expected permission prompt to take priority, got: %q", got)
	}
	if strings.Contains(got, "tok") || strings.Contains(got, "TTFT") {
		t.Fatalf("throughput leaked through pending decision: %q", got)
	}
}

// TestThroughputWidthLadder pins the exact degradation ladder across
// the six documented widths: 20, 39, 40, 79, 80, 120.
func TestThroughputWidthLadder(t *testing.T) {
	t.Run("measured ladder", func(t *testing.T) {
		m := NewFeed()
		m.running = true
		m.busy = true
		start := time.Now()
		m.reqStartedAt = start
		m.streamStartedAt = start
		m.streamFirstDeltaAt = start.Add(1240 * time.Millisecond) // TTFT = 1.24s
		m.streamLastDeltaAt = start.Add(2240 * time.Millisecond)  // stream = 1.0s
		m.turnCompletionTokens = 312                              // 311 tokens over 1.0s => 311.0 tok/s

		want := map[int]string{
			20:  "",
			39:  "TTFT 1.24s · 311.0 tok/s · 312 tok",
			40:  "TTFT 1.24s · 311.0 tok/s · 312 tok",
			79:  "TTFT 1.24s · 311.0 tok/s · 312 tok",
			80:  "TTFT 1.24s · 311.0 tok/s · 312 tok",
			120: "TTFT 1.24s · 311.0 tok/s · 312 tok",
		}
		for _, w := range []int{20, 39, 40, 79, 80, 120} {
			got := m.runtimeThroughputText(w)
			if got != want[w] {
				t.Errorf("width=%d:\n  got:  %q\n  want: %q", w, got, want[w])
			}
		}

		// Narrow intermediate width (24): full Candidate 1 doesn't fit (34 > 24),
		// Candidate 2 (24 <= 24) fits exactly.
		if got := m.runtimeThroughputText(24); got != "TTFT 1.24s · 311.0 tok/s" {
			t.Errorf("width=24: got %q, want %q", got, "TTFT 1.24s · 311.0 tok/s")
		}

		// Narrower intermediate width (22): Candidate 2 doesn't fit (24 > 22),
		// Candidate 3 (11 <= 22) fits.
		if got := m.runtimeThroughputText(22); got != "311.0 tok/s" {
			t.Errorf("width=22: got %q, want %q", got, "311.0 tok/s")
		}
	})

	t.Run("estimated ladder", func(t *testing.T) {
		m := NewFeed()
		m.running = true
		m.busy = true
		start := time.Now()
		m.reqStartedAt = start
		m.streamStartedAt = start
		m.streamFirstDeltaAt = start.Add(1240 * time.Millisecond) // TTFT = 1.24s
		m.streamLastDeltaAt = start.Add(2240 * time.Millisecond)  // stream = 1.0s
		m.streamedChars = 168                                    // 42.0 est tok / 1.0s => 42.0 tok/s

		want := map[int]string{
			20:  "",
			39:  "TTFT 1.24s · est 42.0 tok/s",
			40:  "TTFT 1.24s · est 42.0 tok/s · est 42 tok",
			79:  "TTFT 1.24s · est 42.0 tok/s · est 42 tok",
			80:  "TTFT 1.24s · est 42.0 tok/s · est 42 tok",
			120: "TTFT 1.24s · est 42.0 tok/s · est 42 tok",
		}
		for _, w := range []int{20, 39, 40, 79, 80, 120} {
			got := m.runtimeThroughputText(w)
			if got != want[w] {
				t.Errorf("width=%d:\n  got:  %q\n  want: %q", w, got, want[w])
			}
		}

		// At width 24, Candidate 2 (27 cols) does not fit, Candidate 3 (14 cols) fits.
		if got := m.runtimeThroughputText(24); got != "est 42.0 tok/s" {
			t.Errorf("width=24: got %q, want %q", got, "est 42.0 tok/s")
		}
	})
}

// TestThroughputRequiresLiveRun verifies that throughput disappears completely
// when there is no live run (Phase 1 contract).
func TestThroughputRequiresLiveRun(t *testing.T) {
	m := NewFeed()
	m.width = 80
	m.height = 24
	start := time.Now()
	m.reqStartedAt = start
	m.streamStartedAt = start
	m.streamFirstDeltaAt = start.Add(500 * time.Millisecond)
	m.streamLastDeltaAt = start.Add(1500 * time.Millisecond)
	m.streamedChars = 200

	// When idle, runtimeThroughputText and runtimeStatusText must return empty string.
	m.running = false
	m.busy = false

	if got := m.runtimeThroughputText(80); got != "" {
		t.Fatalf("expected empty throughput on idle feed, got %q", got)
	}
	if got := m.runtimeStatusText(80); got != "" {
		t.Fatalf("expected empty status on idle feed, got %q", got)
	}
}

// TestThroughputFirstTokenExcluded verifies the mathematical contract:
// The final rate divides (tokens - 1) by streamDuration, excluding the
// first token from the numerator because it marks the start boundary.
// For short replies, including the first token inflates the rate.
func TestThroughputFirstTokenExcluded(t *testing.T) {
	m := NewFeed()
	m.running = true
	m.busy = true
	start := time.Now()
	m.reqStartedAt = start
	m.streamStartedAt = start
	m.streamFirstDeltaAt = start.Add(1 * time.Second)
	m.streamLastDeltaAt = start.Add(2 * time.Second) // stream duration = exactly 1.0s
	m.turnCompletionTokens = 11

	got := m.runtimeThroughputText(80)
	// With 11 completion tokens over 1.0 second:
	// Excluding first token: (11 - 1) / 1.0s = 10.0 tok/s.
	// If first token were included: 11 / 1.0s = 11.0 tok/s.
	if !strings.Contains(got, "10.0 tok/s") {
		t.Fatalf("expected '10.0 tok/s' (excluding first token), got: %q", got)
	}
	if strings.Contains(got, "11.0 tok/s") {
		t.Fatalf("first token was not excluded: got %q", got)
	}
}

// TestThroughputVisualDistinction verifies that estimated rates use prefix
// 'est ' and never format like measured numbers, while measured rates use
// the exact format without 'est'.
func TestThroughputVisualDistinction(t *testing.T) {
	m := NewFeed()
	m.running = true
	m.busy = true
	start := time.Now()
	m.reqStartedAt = start
	m.streamStartedAt = start
	m.streamFirstDeltaAt = start.Add(500 * time.Millisecond)
	m.streamLastDeltaAt = start.Add(1500 * time.Millisecond)
	m.streamedChars = 400

	// 1. Live estimate (no provider usage event yet)
	live := m.runtimeThroughputText(80)
	if !strings.Contains(live, "est ") {
		t.Fatalf("live rate must have 'est ' prefix, got: %q", live)
	}

	// 2. Final measured rate (provider usage event applied)
	m.turnCompletionTokens = 101
	measured := m.runtimeThroughputText(80)
	if strings.Contains(measured, "est") {
		t.Fatalf("measured rate must not contain 'est', got: %q", measured)
	}
	if !strings.Contains(measured, "100.0 tok/s") {
		t.Fatalf("expected measured 100.0 tok/s, got: %q", measured)
	}
}

// TestThroughputBatchUsesEventTime verifies that when multiple deltas arrive
// in a single batch, the throughput calculation relies on Event.Time and NOT
// on the batch processing wall-clock loop time.
func TestThroughputBatchUsesEventTime(t *testing.T) {
	m := NewFeed()
	m.running = true
	m.busy = true

	t0 := time.Now().Add(-5 * time.Second)
	events := []agent.Event{
		{Seq: 1, Type: agent.RunStart, Time: t0},
		{Seq: 2, Type: agent.TurnStart, Time: t0.Add(100 * time.Millisecond)},
		{Seq: 3, Type: agent.TextDelta, Text: "hello world ", Time: t0.Add(500 * time.Millisecond)},
		{Seq: 4, Type: agent.TextDelta, Text: "streaming text ", Time: t0.Add(1500 * time.Millisecond)},
	}

	// Apply all events in one batch (executed in microseconds).
	_, _ = m.applyBatch(events)

	// streamFirstDeltaAt must be t0 + 500ms, streamLastDeltaAt must be t0 + 1500ms.
	wantFirst := t0.Add(500 * time.Millisecond)
	wantLast := t0.Add(1500 * time.Millisecond)
	if !m.streamFirstDeltaAt.Equal(wantFirst) {
		t.Errorf("streamFirstDeltaAt = %v, want %v", m.streamFirstDeltaAt, wantFirst)
	}
	if !m.streamLastDeltaAt.Equal(wantLast) {
		t.Errorf("streamLastDeltaAt = %v, want %v", m.streamLastDeltaAt, wantLast)
	}

	elapsed := m.streamLastDeltaAt.Sub(m.streamFirstDeltaAt)
	if elapsed != 1000*time.Millisecond {
		t.Errorf("measured elapsed = %v, want 1.0s (Event.Time delta)", elapsed)
	}
}

// TestThroughputMultiTurnIsolation verifies that when a run contains multiple
// provider turns separated by tool execution:
//   1. Turn 1 streams text and completes with its own EventProviderUsage.
//   2. Turn 2 begins with TurnStart, resetting Turn 1's usage and delta timestamps.
//   3. Turn 2 streaming does NOT display Turn 1's measured rate or token count.
//   4. Turn 2 receives its own EventProviderUsage and shows Turn 2's measured rate.
func TestThroughputMultiTurnIsolation(t *testing.T) {
	m := NewFeed()
	m.running = true
	m.busy = true

	t0 := time.Now().Add(-10 * time.Second)

	// Turn 1:
	m.trackState(agent.Event{Seq: 1, Type: agent.RunStart, Time: t0})
	m.trackState(agent.Event{Seq: 2, Type: agent.TurnStart, Time: t0.Add(100 * time.Millisecond)})
	m.trackState(agent.Event{Seq: 3, Type: agent.TextDelta, Text: "1234567890123456", Time: t0.Add(600 * time.Millisecond)})
	m.trackState(agent.Event{Seq: 4, Type: agent.TextDelta, Text: "1234567890123456", Time: t0.Add(1600 * time.Millisecond)})
	// Turn 1 completes:
	m.trackState(agent.Event{
		Seq:  5,
		Type: agent.EventProviderUsage,
		Time: t0.Add(1700 * time.Millisecond),
		Usage: &agent.ProviderUsage{
			CompletionTokens: 51, // 50 tokens / 1.0s => 50.0 tok/s
		},
	})

	turn1Rate := m.runtimeThroughputText(80)
	if !strings.Contains(turn1Rate, "50.0 tok/s") || !strings.Contains(turn1Rate, "51 tok") {
		t.Fatalf("turn 1 want 50.0 tok/s and 51 tok, got: %q", turn1Rate)
	}

	// Tool execution between turns:
	m.trackState(agent.Event{Seq: 6, Type: agent.ToolStart, Time: t0.Add(2000 * time.Millisecond), Call: &agent.ToolCall{Name: "bash"}})
	m.trackState(agent.Event{Seq: 7, Type: agent.ToolEnd, Time: t0.Add(3000 * time.Millisecond)})

	// Turn 2 begins: TurnStart
	tTurn2 := t0.Add(3100 * time.Millisecond)
	m.trackState(agent.Event{Seq: 8, Type: agent.TurnStart, Time: tTurn2})

	// Turn 1 usage MUST be cleared! Turn 2 has not streamed deltas yet, so throughput is empty.
	if got := m.runtimeThroughputText(80); got != "" {
		t.Fatalf("turn 2 before deltas must have empty throughput, got: %q", got)
	}

	// Turn 2 streams deltas:
	m.trackState(agent.Event{Seq: 9, Type: agent.TextDelta, Text: "abcd", Time: tTurn2.Add(500 * time.Millisecond)})
	m.trackState(agent.Event{Seq: 10, Type: agent.TextDelta, Text: "efgh", Time: tTurn2.Add(1500 * time.Millisecond)})

	turn2Live := m.runtimeThroughputText(80)
	// Must be LIVE estimate ("est "), NOT Turn 1's measured 50.0 tok/s or 51 tok!
	if strings.Contains(turn2Live, "51 tok") || strings.Contains(turn2Live, "50.0 tok/s") {
		t.Fatalf("turn 2 leaked turn 1 measured rate: %q", turn2Live)
	}
	if !strings.Contains(turn2Live, "est ") {
		t.Fatalf("turn 2 while streaming must show live 'est ' rate, got: %q", turn2Live)
	}

	// Turn 2 provider usage arrives:
	m.trackState(agent.Event{
		Seq:  11,
		Type: agent.EventProviderUsage,
		Time: tTurn2.Add(1600 * time.Millisecond),
		Usage: &agent.ProviderUsage{
			CompletionTokens: 101, // 100 tokens / 1.0s => 100.0 tok/s
		},
	})

	turn2Final := m.runtimeThroughputText(80)
	if !strings.Contains(turn2Final, "100.0 tok/s") || !strings.Contains(turn2Final, "101 tok") {
		t.Fatalf("turn 2 final want 100.0 tok/s and 101 tok, got: %q", turn2Final)
	}
}

// TestThroughputToolOnlyTurnDoesNotCorruptNextTurn verifies that when the first
// turn is tool-only (no TextDeltas, only tool call), Turn 2's TTFT is measured
// from Turn 2's TurnStart, NOT from the beginning of the entire run.
func TestThroughputToolOnlyTurnDoesNotCorruptNextTurn(t *testing.T) {
	m := NewFeed()
	m.running = true
	m.busy = true

	t0 := time.Now().Add(-10 * time.Second)

	// Run starts, Turn 1 starts:
	m.trackState(agent.Event{Seq: 1, Type: agent.RunStart, Time: t0})
	m.trackState(agent.Event{Seq: 2, Type: agent.TurnStart, Time: t0.Add(50 * time.Millisecond)})
	// Turn 1 produces tool call only:
	m.trackState(agent.Event{Seq: 3, Type: agent.ToolStart, Time: t0.Add(100 * time.Millisecond), Call: &agent.ToolCall{Name: "read_file"}})
	// Tool takes 5 seconds:
	m.trackState(agent.Event{Seq: 4, Type: agent.ToolEnd, Time: t0.Add(5100 * time.Millisecond)})

	// Turn 2 starts at t0 + 5200ms:
	tTurn2 := t0.Add(5200 * time.Millisecond)
	m.trackState(agent.Event{Seq: 5, Type: agent.TurnStart, Time: tTurn2})

	// First delta of Turn 2 arrives 800ms later:
	m.trackState(agent.Event{Seq: 6, Type: agent.TextDelta, Text: "Here is the content", Time: tTurn2.Add(800 * time.Millisecond)})

	got := m.runtimeThroughputText(80)
	// TTFT for this turn must be 0.80s, NOT 6.00s!
	if !strings.Contains(got, "TTFT 0.80s") {
		t.Fatalf("expected Provider TTFT 0.80s for turn 2, got: %q", got)
	}
	if strings.Contains(got, "6.00s") || strings.Contains(got, "5.") {
		t.Fatalf("tool-only turn 1 corrupted turn 2 TTFT: %q", got)
	}
}

// TestThroughputClearsOnCancellation verifies that when a run is cancelled
// via Interrupted or RunError, the throughput text is retired immediately.
func TestThroughputClearsOnCancellation(t *testing.T) {
	m := NewFeed()
	m.running = true
	m.busy = true
	t0 := time.Now().Add(-2 * time.Second)
	m.trackState(agent.Event{Seq: 1, Type: agent.RunStart, Time: t0})
	m.trackState(agent.Event{Seq: 2, Type: agent.TurnStart, Time: t0.Add(100 * time.Millisecond)})
	m.trackState(agent.Event{Seq: 3, Type: agent.TextDelta, Text: "streaming text", Time: t0.Add(500 * time.Millisecond)})

	if got := m.runtimeThroughputText(80); got == "" {
		t.Fatal("precondition: expected non-empty throughput while streaming")
	}

	// Interrupted arrives:
	m.trackState(agent.Event{Seq: 4, Type: agent.Interrupted, Time: t0.Add(600 * time.Millisecond)})

	if got := m.runtimeThroughputText(80); got != "" {
		t.Fatalf("expected empty throughput after Interrupted, got: %q", got)
	}
	if got := m.runtimeStatusText(80); got != runFailedStatus {
		t.Fatalf("expected status %q after Interrupted, got: %q", runFailedStatus, got)
	}
}

// TestThroughputEventTimeZeroFallback verifies that legacy events without
// a timestamp (Time.IsZero()) fall back safely to time.Now() without panicking
// or corrupting throughput.
func TestThroughputEventTimeZeroFallback(t *testing.T) {
	m := NewFeed()
	m.running = true
	m.busy = true

	// Send events with zero Time:
	m.trackState(agent.Event{Seq: 1, Type: agent.RunStart})
	m.trackState(agent.Event{Seq: 2, Type: agent.TurnStart})
	m.trackState(agent.Event{Seq: 3, Type: agent.TextDelta, Text: "hello world"})

	if m.streamFirstDeltaAt.IsZero() {
		t.Fatal("streamFirstDeltaAt should be populated even with zero-time event")
	}
	got := m.runtimeThroughputText(80)
	if !strings.Contains(got, "TTFT") {
		t.Fatalf("expected TTFT text with zero-time event fallback, got: %q", got)
	}
}
