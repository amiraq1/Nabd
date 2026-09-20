package ui

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// ─────────────────────────────────────────────────────────────────────────────
// CONTRACT: Ctrl-C ladder (ADR-0001, rule 12 — "Ctrl-C semantics do not change")
//
// This file is a frozen contract, not a coverage exercise. The ladder below is
// the v1.5.0 behavior that users have muscle memory for. Any change to the
// order, to a branch's effect, or to the quit condition MUST be a deliberate
// edit to expectedCtrlCEffect() plus an ADR amendment — never a silent fix to
// make a failing test green.
//
//	1. secret prompt visible      -> cancel prompt only          (never quit)
//	2. modal/decision + run       -> cancel run, decision stays  (never approve)
//	3. modal/decision, no run     -> no-op (orphan ask)          (never quit)
//	4. run in flight              -> cancel run                  (never quit)
//	5. search active              -> exit search                 (never quit)
//	6. composer non-empty         -> clear + hint                (never quit)
//	7. idle + empty composer      -> quit
//
// Deliberate consequences that are part of the contract:
//   - Barrier 1 wins over an in-flight run: canceling a secret prompt does NOT
//     cancel the run behind it. One Ctrl-C, one effect.
//   - Ctrl-C is never an approval. A pending decision survives every branch.
//   - Quit requires the model to be fully idle AND the composer empty.
// ─────────────────────────────────────────────────────────────────────────────

type ctrlCEffect string

const (
	effectCancelSecret ctrlCEffect = "cancel-secret-prompt"
	effectCancelRun    ctrlCEffect = "cancel-run"
	effectOrphanModal  ctrlCEffect = "ignore-orphan-modal"
	effectCancelSearch ctrlCEffect = "cancel-search"
	effectClearInput   ctrlCEffect = "clear-composer"
	effectQuit         ctrlCEffect = "quit"
)

// ctrlCState is the complete set of knobs onCtrlC branches on. If a new branch
// is added to onCtrlC without a field here, TestCtrlCLadderIsExhaustive fails.
type ctrlCState struct {
	secretPrompt    bool
	modalVisible    bool
	decisionPending bool
	running         bool
	busy            bool
	searchActive    bool
	composer        string
}

// expectedCtrlCEffect is the contract, written independently of the
// implementation. It is the specification; onCtrlC is the thing under test.
func expectedCtrlCEffect(s ctrlCState) ctrlCEffect {
	switch {
	case s.secretPrompt:
		return effectCancelSecret
	case s.modalVisible || s.decisionPending:
		if s.running || s.busy {
			return effectCancelRun
		}
		return effectOrphanModal
	case s.running || s.busy:
		return effectCancelRun
	case s.searchActive:
		return effectCancelSearch
	case s.composer != "":
		return effectClearInput
	default:
		return effectQuit
	}
}

// ─── exhaustive matrix: 2^6 × 2 composer states = 128 combinations ───────────

func TestCtrlCLadderIsExhaustive(t *testing.T) {
	bools := []bool{false, true}
	composers := []string{"", "draft prompt"}

	for _, secret := range bools {
		for _, modal := range bools {
			for _, decision := range bools {
				for _, running := range bools {
					for _, busy := range bools {
						for _, search := range bools {
							for _, text := range composers {
								st := ctrlCState{
									secretPrompt:    secret,
									modalVisible:    modal,
									decisionPending: decision,
									running:         running,
									busy:            busy,
									searchActive:    search,
									composer:        text,
								}
								t.Run(ctrlCCaseName(st), func(t *testing.T) {
									assertCtrlC(t, st, expectedCtrlCEffect(st))
								})
							}
						}
					}
				}
			}
		}
	}
}

// ─── named scenarios: documentation-grade cases with the real helpers ────────

func TestCtrlCNamedBarriers(t *testing.T) {
	cases := []struct {
		name  string
		state ctrlCState
		want  ctrlCEffect
	}{
		{"secret prompt beats everything", ctrlCState{secretPrompt: true, running: true, composer: "x"}, effectCancelSecret},
		{"modal over a live run cancels the run", ctrlCState{modalVisible: true, running: true}, effectCancelRun},
		{"orphan ask is a safe no-op", ctrlCState{modalVisible: true}, effectOrphanModal},
		{"decision pending without modal still shields", ctrlCState{decisionPending: true}, effectOrphanModal},
		{"busy counts as in-flight", ctrlCState{busy: true}, effectCancelRun},
		{"run in flight never quits even with empty composer", ctrlCState{running: true}, effectCancelRun},
		{"search exits before composer is touched", ctrlCState{searchActive: true, composer: "keep me"}, effectCancelSearch},
		{"non-empty composer is cleared, not quit", ctrlCState{composer: "half typed"}, effectClearInput},
		{"whitespace-only composer is NOT empty", ctrlCState{composer: "   "}, effectClearInput},
		{"idle and empty quits", ctrlCState{}, effectQuit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertCtrlC(t, tc.state, tc.want)
		})
	}
}

// ─── sequences: the habits v1.5.0 users already have ─────────────────────────

func TestCtrlCTwicePathsToQuit(t *testing.T) {
	t.Run("clear then quit", func(t *testing.T) {
		f, _ := newCtrlCFeed(t, ctrlCState{composer: "half typed"})
		if _, cmd := f.onCtrlC(); isQuitCmd(cmd) {
			t.Fatal("first Ctrl-C quit; it must only clear the composer")
		}
		if got := statusTextOf(f); got != ctrlCClearHint {
			t.Fatalf("missing quit hint after clear: got %q want %q", got, ctrlCClearHint)
		}
		if _, cmd := f.onCtrlC(); !isQuitCmd(cmd) {
			t.Fatal("second Ctrl-C on an empty composer must quit")
		}
	})

	t.Run("cancel run then quit", func(t *testing.T) {
		f, probe := newCtrlCFeed(t, ctrlCState{running: true})
		if _, cmd := f.onCtrlC(); isQuitCmd(cmd) {
			t.Fatal("Ctrl-C quit mid-flight")
		}
		if !probe.runCanceled {
			t.Fatal("run context was not canceled")
		}
		settleRun(t, f) // run observes cancellation and clears running/busy
		if _, cmd := f.onCtrlC(); !isQuitCmd(cmd) {
			t.Fatal("Ctrl-C after cancellation must quit")
		}
	})
}

// Ctrl-C is never consent. This is the one that protects the permission gate.
func TestCtrlCNeverApprovesPendingDecision(t *testing.T) {
	for _, st := range []ctrlCState{
		{modalVisible: true, decisionPending: true},
		{modalVisible: true, decisionPending: true, running: true},
		{modalVisible: true, decisionPending: true, busy: true},
	} {
		t.Run(ctrlCCaseName(st), func(t *testing.T) {
			f, _ := newCtrlCFeed(t, st)
			f.onCtrlC()
			if !f.decisionPending {
				t.Fatal("pending decision was resolved by Ctrl-C")
			}
			assertNoDecisionDelivered(t, f)
		})
	}
}

// ─── harness ─────────────────────────────────────────────────────────────────

type ctrlCProbe struct{ runCanceled bool }

func statusTextOf(f *Feed) string   { return f.Status() }
func composerTextOf(f *Feed) string { return f.ComposerValue() }
func historyBrowsing(f *Feed) bool  { return f.HistoryBrowsing() }
func menuOpen(f *Feed) bool         { return f.menu.visible }
func pickerOpen(f *Feed) bool       { return f.pickerVisible() }

// newCtrlCFeed builds a Feed in the requested state and returns a probe that
// records whether the run context was canceled.
func newCtrlCFeed(t *testing.T, s ctrlCState) (*Feed, *ctrlCProbe) {
	t.Helper()

	f, r := feedWithBlockingRunner(t) // existing helper, input_router_test.go:27
	probe := &ctrlCProbe{}

	if s.running || s.busy {
		startBlockingRun(t, f, feedRunnerOf(t, f), "contract probe")
	}
	prev := f.cancel
	f.cancel = func() {
		probe.runCanceled = true
		if prev != nil {
			prev()
		}
	}

	t.Cleanup(func() {
		if prev != nil {
			prev()
		}
		r.mu.Lock()
		select {
		case <-r.release:
		default:
			close(r.release)
		}
		r.mu.Unlock()
	})

	f.running = s.running
	f.busy = s.busy
	f.secretPrompt = s.secretPrompt
	if s.modalVisible {
		openModal(f) // existing helper
	}
	f.modalVisible = s.modalVisible
	f.decisionPending = s.decisionPending
	f.search.active = s.searchActive
	if s.composer != "" {
		typeIntoFeed(t, f, s.composer)
	}
	f.clearStatus()
	return f, probe
}

func assertCtrlC(t *testing.T, s ctrlCState, want ctrlCEffect) {
	t.Helper()
	f, probe := newCtrlCFeed(t, s)
	before := snapshotFeed(f)

	_, cmd := f.onCtrlC()
	quit := isQuitCmd(cmd)

	if quit != (want == effectQuit) {
		t.Fatalf("quit=%v, want %v (effect %s, state %+v)", quit, want == effectQuit, want, s)
	}

	switch want {
	case effectCancelSecret:
		if f.secretPrompt {
			t.Error("secret prompt still visible")
		}
		if statusTextOf(f) != "connect canceled" {
			t.Errorf("status = %q, want %q", statusTextOf(f), "connect canceled")
		}
		if probe.runCanceled {
			t.Error("canceling the secret prompt must not cancel the run behind it")
		}
		if composerTextOf(f) != before.composer {
			t.Error("composer was modified")
		}

	case effectCancelRun:
		if !probe.runCanceled {
			t.Error("run context was not canceled")
		}
		if !strings.Contains(statusTextOf(f), "canceling") {
			t.Errorf("status = %q, want it to mention canceling", statusTextOf(f))
		}
		if composerTextOf(f) != before.composer {
			t.Error("composer was cleared while canceling a run")
		}

	case effectOrphanModal:
		if got := snapshotFeed(f); got != before {
			t.Errorf("orphan modal Ctrl-C mutated state:\n got %+v\nwant %+v", got, before)
		}
		if probe.runCanceled {
			t.Error("canceled a run that was not in flight")
		}
		if cmd != nil {
			t.Error("orphan modal Ctrl-C must return a nil cmd")
		}

	case effectCancelSearch:
		if f.search.active {
			t.Error("search still active")
		}
		if composerTextOf(f) != before.composer {
			t.Error("composer was cleared while exiting search")
		}

	case effectClearInput:
		if composerTextOf(f) != "" {
			t.Errorf("composer = %q, want empty", composerTextOf(f))
		}
		if historyBrowsing(f) {
			t.Error("history browsing was not reset")
		}
		if menuOpen(f) || pickerOpen(f) {
			t.Error("popups survived the clear")
		}
		if statusTextOf(f) != ctrlCClearHint {
			t.Errorf("status = %q, want the quit hint %q", statusTextOf(f), ctrlCClearHint)
		}

	case effectQuit:
		if probe.runCanceled {
			t.Error("quit path canceled a run that did not exist")
		}
	}
}

// feedSnapshot captures every field the ladder is allowed to touch, so the
// orphan-modal branch can be asserted as a true no-op.
type feedSnapshot struct {
	secretPrompt, modalVisible, decisionPending bool
	running, busy, searchActive                 bool
	composer, status                            string
}

func snapshotFeed(f *Feed) feedSnapshot {
	return feedSnapshot{
		secretPrompt:    f.secretPrompt,
		modalVisible:    f.modalVisible,
		decisionPending: f.decisionPending,
		running:         f.running,
		busy:            f.busy,
		searchActive:    f.search.active,
		composer:        composerTextOf(f),
		status:          statusTextOf(f),
	}
}

// isQuitCmd compares function pointers instead of invoking cmd, because
// invoking an arbitrary tea.Cmd in a unit test would fire real side effects.
func isQuitCmd(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	return reflect.ValueOf(cmd).Pointer() == reflect.ValueOf(tea.Cmd(tea.Quit)).Pointer()
}

func ctrlCCaseName(s ctrlCState) string {
	var b strings.Builder
	for _, part := range []struct {
		on   bool
		name string
	}{
		{s.secretPrompt, "secret"}, {s.modalVisible, "modal"},
		{s.decisionPending, "decision"}, {s.running, "running"},
		{s.busy, "busy"}, {s.searchActive, "search"}, {s.composer != "", "text"},
	} {
		if part.on {
			if b.Len() > 0 {
				b.WriteByte('+')
			}
			b.WriteString(part.name)
		}
	}
	if b.Len() == 0 {
		return "idle"
	}
	return b.String()
}
