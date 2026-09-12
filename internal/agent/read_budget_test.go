package agent

import (
	"strings"
	"testing"

	"nabd/internal/provider"
)

// TestReadBudgetDerivation: readBudget computes the per-call read ceiling
// from the live-calibrated baseline (3072) scaled by context pressure.
// Empty context (fraction 1.0) reads at 1.5× baseline (more bytes/call,
// fewer round trips). Near-full context reads down to 60% of baseline,
// never below the 512 floor.
func TestReadBudgetDerivation(t *testing.T) {
	l := &Loop{Budget: NewBudget()}

	// Empty context: fraction 1.0 → scale 1.5 → 3072 * 1.5 = 4608.
	empty := l.readBudget(nil)
	if empty != 4608 {
		t.Fatalf("empty-context budget=%d, want 4608", empty)
	}
	t.Logf("empty-context read budget: %d bytes", empty)

	// Near-full context: fraction clamped to 0.1 → scale 0.6 → 3072*0.6=1843.
	l2 := &Loop{Budget: &Budget{Limit: 10000, Reserve: 0, ratio: 1}}
	l2.Budget.SetTokenizer(&fixedTokenizer{n: 9500})
	full := l2.readBudget([]provider.Message{{Role: provider.User, Text: "x"}})
	if full < 512 {
		t.Fatalf("near-full budget=%d, want >= 512 (floor)", full)
	}
	if full >= empty {
		t.Fatalf("near-full budget (%d) should be < empty-context budget (%d)", full, empty)
	}
	t.Logf("near-full read budget: %d bytes (empty was %d)", full, empty)
}

// TestReadBudgetFloor: the floor (512) binds even when the derivation
// wants less — a context so full that fraction*perReq would undershoot.
func TestReadBudgetFloor(t *testing.T) {
	l := &Loop{Budget: &Budget{Limit: 1000, Reserve: 0, ratio: 1}}
	l.Budget.SetTokenizer(&fixedTokenizer{n: 999})
	got := l.readBudget([]provider.Message{{Role: provider.User, Text: "x"}})
	if got < 512 {
		t.Fatalf("budget=%d, want >= 512 (floor)", got)
	}
}

// TestReadBudgetNoOscillation: the fraction clamp to [0.1, 1.0] plus the
// floor/ceiling means the budget moves in one direction as context fills
// and does not jitter ±1 byte per turn. A monotonic fill produces a
// monotonic (non-increasing) sequence of ceilings.
func TestReadBudgetNoOscillation(t *testing.T) {
	l := &Loop{Budget: &Budget{Limit: 100000, Reserve: 0, ratio: 1}}
	prev := l.readBudget(nil) // empty context → max
	for _, n := range []int{1000, 5000, 20000, 50000, 80000, 95000} {
		l.Budget.SetTokenizer(&fixedTokenizer{n: n})
		cur := l.readBudget([]provider.Message{{Role: provider.User, Text: "x"}})
		if cur > prev {
			t.Fatalf("budget increased as context filled: %d → %d (estimate=%d)", prev, cur, n)
		}
		prev = cur
	}
}

// TestReadLimitOverride: a fixed NABD_MAX_READ disables adaptation —
// initReadLimit installs a constant function regardless of context.
func TestReadLimitOverride(t *testing.T) {
	l := &Loop{Budget: NewBudget()}
	l.initReadLimit(2048) // fixed override
	if l.readLimitFn == nil {
		t.Fatal("override did not install a limit function")
	}
	// Same value whether context is empty or "full".
	empty := l.readLimitFn(nil)
	full := l.readLimitFn([]provider.Message{{Role: provider.User, Text: strings.Repeat("x", 1000)}})
	if empty != 2048 || full != 2048 {
		t.Fatalf("override: empty=%d full=%d, want 2048 2048", empty, full)
	}
}

// TestUpdateReadLimitNotice: updateReadLimit emits exactly one Notice per
// change (not per turn). We count Notices across several turns with a
// shrinking context and assert the count matches the number of distinct
// ceiling values minus the initial.
func TestUpdateReadLimitNotice(t *testing.T) {
	l := &Loop{Budget: &Budget{Limit: 100000, Reserve: 0, ratio: 1}}
	l.readLimitFn = l.readBudget
	l.readLimitInit = true

	var notices int
	l.Sink = sinkFunc(func(e Event) error {
		if e.Type == Notice && len(e.Text) >= 9 && e.Text[0:9] == "read budg" {
			notices++
		}
		return nil
	})

	// Drive the budget through a sequence of distinct values.
	estimates := []int{0, 10000, 10000, 50000, 50000, 90000}
	seen := map[int]bool{}
	var ceilings []int
	for _, n := range estimates {
		l.Budget.SetTokenizer(&fixedTokenizer{n: n})
		ms := []provider.Message{{Role: provider.User, Text: "x"}}
		l.updateReadLimit(ms)
		l.mu.Lock()
		c := l.readLimitCur
		l.mu.Unlock()
		if !seen[c] {
			ceilings = append(ceilings, c)
			seen[c] = true
		}
	}
	// One Notice per change after the initial value: len(ceilings)-1.
	want := len(ceilings) - 1
	if want < 0 {
		want = 0
	}
	if notices != want {
		t.Fatalf("notices=%d, want %d (distinct ceilings %v)", notices, want, ceilings)
	}
}

// fixedTokenizer returns a fixed Count regardless of input.
type fixedTokenizer struct{ n int }

func (f fixedTokenizer) Count(text string) int { return f.n }
