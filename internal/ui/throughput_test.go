package ui

import (
	"strings"
	"testing"
	"time"

	"nabd/internal/agent"
	"nabd/internal/presentation"
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
	m.reqStartedAt = time.Now().Add(-1500 * time.Millisecond)
	m.firstDeltaAt = time.Now().Add(-1000 * time.Millisecond)
	m.lastDeltaAt = time.Now().Add(-100 * time.Millisecond)
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
	m.reqStartedAt = time.Now().Add(-1500 * time.Millisecond)
	m.firstDeltaAt = time.Now().Add(-1000 * time.Millisecond)
	m.lastDeltaAt = time.Now().Add(-100 * time.Millisecond)
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
// Ladder:
//
//	"TTFT 1.24s · 42.1 tok/s · 312 tok"
//	"TTFT 1.24s · 42.1 tok/s"
//	"42.1 tok/s"
//	"" at 20 columns
func TestThroughputWidthLadder(t *testing.T) {
	t.Run("measured ladder", func(t *testing.T) {
		m := NewFeed()
		m.running = true
		m.busy = true
		start := time.Now()
		m.reqStartedAt = start
		m.firstDeltaAt = start.Add(1240 * time.Millisecond) // TTFT = 1.24s
		m.lastDeltaAt = start.Add(2240 * time.Millisecond)  // stream = 1.0s
		m.statusProj = presentation.NewStatusProjector()
		m.statusProj.Apply(agent.Event{
			Type: agent.EventProviderUsage,
			Usage: &agent.ProviderUsage{
				PromptTokens:     100,
				CompletionTokens: 312, // 311 tokens over 1.0s => 311.0 tok/s
			},
		})

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
		m.firstDeltaAt = start.Add(1240 * time.Millisecond) // TTFT = 1.24s
		m.lastDeltaAt = start.Add(2240 * time.Millisecond)  // stream = 1.0s
		m.streamedChars = 168                              // 42.0 est tok / 1.0s => 42.0 tok/s

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

// TestProgressRequiresLiveRun verifies that throughput disappears completely
// when there is no live run (Phase 1 contract).
func TestThroughputRequiresLiveRun(t *testing.T) {
	m := NewFeed()
	m.width = 80
	m.height = 24
	start := time.Now()
	m.reqStartedAt = start
	m.firstDeltaAt = start.Add(500 * time.Millisecond)
	m.lastDeltaAt = start.Add(1500 * time.Millisecond)
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
	m.firstDeltaAt = start.Add(1 * time.Second)
	m.lastDeltaAt = start.Add(2 * time.Second) // stream duration = exactly 1.0s

	m.statusProj = presentation.NewStatusProjector()
	m.statusProj.Apply(agent.Event{
		Type: agent.EventProviderUsage,
		Usage: &agent.ProviderUsage{
			CompletionTokens: 11,
		},
	})

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
	m.firstDeltaAt = start.Add(500 * time.Millisecond)
	m.lastDeltaAt = start.Add(1500 * time.Millisecond)
	m.streamedChars = 400

	// 1. Live estimate (no provider usage event yet)
	live := m.runtimeThroughputText(80)
	if !strings.Contains(live, "est ") {
		t.Fatalf("live rate must have 'est ' prefix, got: %q", live)
	}

	// 2. Final measured rate (provider usage event applied)
	m.statusProj = presentation.NewStatusProjector()
	m.statusProj.Apply(agent.Event{
		Type: agent.EventProviderUsage,
		Usage: &agent.ProviderUsage{
			CompletionTokens: 101,
		},
	})
	measured := m.runtimeThroughputText(80)
	if strings.Contains(measured, "est") {
		t.Fatalf("measured rate must not contain 'est', got: %q", measured)
	}
	if !strings.Contains(measured, "100.0 tok/s") {
		t.Fatalf("expected measured 100.0 tok/s, got: %q", measured)
	}
}
