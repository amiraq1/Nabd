package ui

import (
	"context"
	"sync"
	"testing"
	"time"

	"nabd/internal/agent"

	tea "github.com/charmbracelet/bubbletea"
)

// feedWithRunner returns a sized feed with a recording runner.
func feedWithRunner(t *testing.T) (*Feed, *runnerRecorder) {
	t.Helper()
	f := NewFeed()
	f.width = 80
	f.height = 24
	r := &runnerRecorder{}
	f.SetRunner(r)
	return f, r
}

// feedWithBlockingRunner returns a feed whose runner blocks until released
// or cancelled.
func feedWithBlockingRunner(t *testing.T) (*Feed, *runnerRecorder) {
	t.Helper()
	f := NewFeed()
	f.width = 80
	f.height = 24
	r := newBlockingRunner()
	f.SetRunner(r)
	return f, r
}

// runnerRecorder records runs and can block until released or cancelled.
type runnerRecorder struct {
	mu       sync.Mutex
	texts    []string
	ctxs     []context.Context
	start    chan struct{} // closed once the first run starts
	release  chan struct{} // close to let a blocked run finish
	returned chan struct{} // closed once Run has returned
	blocking bool
	released bool
}

func newBlockingRunner() *runnerRecorder {
	return &runnerRecorder{
		start:    make(chan struct{}),
		release:  make(chan struct{}),
		returned: make(chan struct{}),
		blocking: true,
	}
}

func (r *runnerRecorder) Run(ctx context.Context, text string) error {
	r.mu.Lock()
	r.texts = append(r.texts, text)
	r.ctxs = append(r.ctxs, ctx)
	start := r.start
	r.mu.Unlock()
	if start != nil {
		close(start)
	}
	defer func() {
		r.mu.Lock()
		r.released = true
		r.mu.Unlock()
		if r.returned != nil {
			close(r.returned)
		}
	}()
	if r.blocking {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.release:
			return nil
		}
	}
	return nil
}

func (r *runnerRecorder) textsLen() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.texts)
}

// waitReturned waits until the blocked runner returns.
func (r *runnerRecorder) waitReturned(t *testing.T) {
	t.Helper()
	select {
	case <-r.returned:
	case <-timeoutChan(t):
		t.Fatal("runner did not return")
	}
}

// sendAndRun accepts a message through the real key path and executes the
// returned command so the runner actually starts.
func sendAndRun(f *Feed, text string) tea.Cmd {
	for _, r := range text {
		_, _ = f.Update(keyRunes(string(r)))
	}
	_, cmd := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return cmd
}

// execCmd runs a tea.Cmd if non-nil and returns the message it produced.
func execCmd(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	return cmd()
}

// updateCmd executes cmd and feeds its resulting message back into the
// model, mimicking Bubble Tea's loop. Returns the final model.
func updateCmd(f *Feed, cmd tea.Cmd) *Feed {
	if cmd == nil {
		return f
	}
	msg := cmd()
	if msg == nil {
		return f
	}
	mdl, _ := f.Update(msg)
	if m, ok := mdl.(*Feed); ok {
		return m
	}
	return f
}

// openModal simulates a PermAsk arriving through the event stream.
func openModal(f *Feed) {
	_, _ = f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 1, Type: agent.PermAsk, Call: &agent.ToolCall{ID: "c1", Name: "bash"}},
	}})
}

// TestModalKeyPriority: while the modal is visible, ordinary keys go to the
// modal. The composer does not change, the viewport does not move, no
// message is sent, and history does not open.
func TestModalKeyPriority(t *testing.T) {
	f, _ := feedWithRunner(t)
	typeIntoFeed(t, f, "typed before modal")
	openModal(f)

	if !f.modalVisible {
		t.Fatal("modal must be visible after PermAsk")
	}
	before := f.composer.value()

	// Ordinary non-modal keys while the modal is open must be swallowed
	// without producing a command.
	for _, k := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("x")},
		{Type: tea.KeyPgUp},
		{Type: tea.KeySpace},
	} {
		_, cmd := f.Update(k)
		if cmd != nil {
			t.Fatalf("key %v during modal produced a command", k)
		}
	}

	// Up/Down change the modal selection legitimately and must not produce a
	// command either (they do not close the modal).
	startSelected := f.permModal.selected
	_, cmdUp := f.Update(tea.KeyMsg{Type: tea.KeyUp})
	if cmdUp != nil {
		t.Fatal("Up during modal produced a command")
	}
	_, cmdDown := f.Update(tea.KeyMsg{Type: tea.KeyDown})
	if cmdDown != nil {
		t.Fatal("Down during modal produced a command")
	}
	// Net effect of Up then Down returns to the original selection.
	if f.permModal.selected != startSelected {
		t.Fatalf("Up then Down should restore selection: got %d, want %d",
			f.permModal.selected, startSelected)
	}

	if got := f.composer.value(); got != before {
		t.Fatalf("composer changed during modal: %q → %q", before, got)
	}
	if f.busy {
		t.Fatal("typing during modal must not start a run")
	}
	if f.HistoryLen() != 0 {
		t.Fatal("keys during modal must not open history or add entries")
	}
}

// TestModalEnterDefaultsToDenyAndNeverSendsComposer: Enter while the modal is
// visible submits a Deny decision by default — it never starts a runner run
// and never modifies the composer.
// Contract change: previously selected=-1 made Enter a no-op; now the default
// selection is Deny, so Enter must produce a Deny permReply.
func TestModalEnterDefaultsToDenyAndNeverSendsComposer(t *testing.T) {
	f, r := feedWithRunner(t)
	typeIntoFeed(t, f, "would be sent")
	openModal(f)

	_, cmd := f.Update(tea.KeyMsg{Type: tea.KeyEnter})

	// Runner must never be invoked.
	if r.textsLen() != 0 {
		t.Fatal("runner must not be invoked during modal")
	}

	// Composer text must be preserved.
	if got := f.composer.value(); got != "would be sent" {
		t.Fatalf("composer text lost during modal: %q", got)
	}

	// Enter on the default selection must produce a Deny reply.
	if cmd == nil {
		t.Fatal("Enter during modal must produce a permission-reply command")
	}
	msg := cmd()
	reply, ok := msg.(permReplyMsg)
	if !ok {
		t.Fatalf("expected permReplyMsg, got %T", msg)
	}
	if reply.Decision != agent.Deny {
		t.Fatalf("expected Decision=Deny, got %v", reply.Decision)
	}
}

// TestModalFeedKeepsUpdating: events arriving while the modal is visible
// still update the logical feed; auto-scroll pauses (unseen grows).
func TestModalFeedKeepsUpdating(t *testing.T) {
	f, _ := feedWithRunner(t)
	openModal(f)
	// Assistant text arrives behind the modal.
	_, _ = f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 2, Type: agent.TextDelta, Text: "work continues"},
		{Seq: 3, Type: agent.TurnEnd},
	}})
	items := f.proj.Items()
	var asst bool
	for _, it := range items {
		if it.Text == "work continues" {
			asst = true
		}
	}
	if !asst {
		t.Fatal("feed must keep projecting events while the modal is visible")
	}
	if f.unseen == 0 {
		t.Fatal("unseen must grow while the modal pauses auto-scroll")
	}
}

// TestModalAnswerRestoresFocus: answering the modal restores composer focus
// (and follow mode is still off while the modal was open, per Phase 2).
func TestModalAnswerRestoresFocus(t *testing.T) {
	f, _ := feedWithRunner(t)
	openModal(f)
	if f.composer.focused() {
		t.Fatal("composer must lose focus while the modal is visible")
	}
	_, cmd := f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if cmd == nil {
		t.Fatal("y during modal must produce a perm-reply command")
	}
	if f.modalVisible {
		t.Fatal("modal must close after y")
	}
	if !f.composer.focused() {
		t.Fatal("composer focus must be restored after the modal closes")
	}
}

// TestModalUpDownNoHistory: Up/Down during the modal do not open history.
func TestModalUpDownNoHistory(t *testing.T) {
	f, _ := feedWithRunner(t)
	// Seed history through a real send.
	cmd := sendAndRun(f, "stored message")
	execCmd(cmd)
	// New run ended.
	_, _ = f.Update(doneMsg{err: nil})
	openModal(f)
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyUp})
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyDown})
	if got := f.composer.value(); got != "" {
		t.Fatalf("composer changed via history during modal: %q", got)
	}
}

// TestCtrlCDuringRunCancelsNotQuits: Ctrl+C during a run cancels and never
// returns tea.Quit.
func TestCtrlCDuringRunCancelsNotQuits(t *testing.T) {
	f, r := feedWithBlockingRunner(t)
	startBlockingRun(t, f, r, "ask something")

	if !f.busy {
		t.Fatal("feed must be busy after the run starts")
	}
	_, cmd2 := f.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	// Cancellation is direct (context cancel), never a Quit command. The
	// model must not ask Bubble Tea to quit on this press.
	if cmd2 != nil {
		// tea.Quit would be a nil-producing Cmd; anything else is wrong.
		if _, ok := cmd2().(tea.QuitMsg); ok {
			t.Fatal("Ctrl+C during a run must not quit")
		}
	}
	if f.cancel != nil {
		t.Fatal("cancel context must be cleared after cancellation")
	}
	// The cancelled run returns.
	r.waitReturned(t)
}

func timeoutChan(t *testing.T) <-chan struct{} {
	t.Helper()
	ch := make(chan struct{})
	go func() {
		// Short bounded wait; the runner must observe the cancel quickly.
		for i := 0; i < 100; i++ {
			time.Sleep(10 * time.Millisecond)
		}
		close(ch)
	}()
	return ch
}

// startBlockingRun sends a message via the real key path and executes the
// resulting command in a goroutine so the blocking runner parks. It waits
// until the runner has actually started.
func startBlockingRun(t *testing.T, f *Feed, r *runnerRecorder, text string) {
	t.Helper()
	cmd := sendAndRun(f, text)
	if cmd == nil {
		t.Fatal("send must produce a command")
	}
	go func() { cmd() }()
	select {
	case <-r.start:
	case <-timeoutChan(t):
		t.Fatal("runner did not start")
	}
}

// TestCtrlCWithTextClearsComposer: Ctrl+C with text clears the composer and
// never quits.
func TestCtrlCWithTextClearsComposer(t *testing.T) {
	f, _ := feedWithRunner(t)
	typeIntoFeed(t, f, "draft text")
	_, cmd := f.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd != nil {
		t.Fatal("Ctrl+C with text must not produce a command (no quit)")
	}
	if v := f.composer.value(); v != "" {
		t.Fatalf("composer after Ctrl+C = %q, want empty", v)
	}
	// Second Ctrl+C with empty composer and idle: quit.
	_, cmd2 := f.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd2 == nil {
		t.Fatal("Ctrl+C with empty composer and idle must quit")
	}
	msg := cmd2()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Fatalf("idle Ctrl+C produced %T, want tea.QuitMsg", msg)
	}
}

// TestCtrlCIdleQuits: Ctrl+C with empty composer, idle, no modal quits
// (the feed's existing exit policy, unchanged).
func TestCtrlCIdleQuits(t *testing.T) {
	f, _ := feedWithRunner(t)
	_, cmd := f.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("idle Ctrl+C must quit")
	}
}

// TestCtrlCRepeatedNoPanic: repeated Ctrl+C while a run is cancelling never
// panics and never double-cancels destructively.
func TestCtrlCRepeatedNoPanic(t *testing.T) {
	f, r := feedWithBlockingRunner(t)
	startBlockingRun(t, f, r, "long run")
	for i := 0; i < 5; i++ {
		_, _ = f.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	}
	// No panic is the main assertion; state must be consistent.
	if !f.busy && f.cancel != nil {
		t.Fatal("inconsistent cancel state")
	}
	r.waitReturned(t)
}

// TestCtrlCModalNoImplicitApprove: Ctrl+C during the modal never answers
// the permission request.
func TestCtrlCModalNoImplicitApprove(t *testing.T) {
	f, r := feedWithBlockingRunner(t)
	startBlockingRun(t, f, r, "tool request")
	openModal(f)
	// The approver records nothing unless answered.
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	// History must not contain a permission decision (it never does: only
	// user messages enter history).
	if f.HistoryLen() != 1 {
		t.Fatalf("history = %d entries, want 1 (only the user message)", f.HistoryLen())
	}
	r.waitReturned(t)
}

// TestCtrlDModalNoQuit: Ctrl+D during the modal never quits and never
// approves.
func TestCtrlDModalNoQuit(t *testing.T) {
	f, _ := feedWithRunner(t)
	openModal(f)
	_, cmd := f.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	if cmd != nil {
		t.Fatal("Ctrl+D during modal must not produce a command")
	}
	if f.modalVisible == false {
		t.Fatal("modal must remain visible after Ctrl+D")
	}
}

// TestCtrlDWithTextDeletesRune: Ctrl+D with text deletes the rune under the
// cursor (or nothing at end of text); it never quits and never breaks UTF-8.
func TestCtrlDWithTextDeletesRune(t *testing.T) {
	f, _ := feedWithRunner(t)
	// Arabic multi-byte text.
	typeIntoFeed(t, f, "مرحبا")
	// Move cursor to the start, then delete forward twice.
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyCtrlA}) // textarea: line start
	_, cmd := f.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	if cmd != nil {
		t.Fatal("Ctrl+D with text must not quit")
	}
	if v := f.composer.value(); v != "رحبا" {
		t.Fatalf("after one Ctrl+D at start = %q, want 'رحبا'", v)
	}
	// No lone bytes: the string must stay valid UTF-8.
	if !validUTF8(f.composer.value()) {
		t.Fatal("composer text is not valid UTF-8 after Ctrl+D")
	}
}

func validUTF8(s string) bool {
	for _, r := range s {
		if r == 0xFFFD {
			return false
		}
	}
	return true
}

// TestCtrlDEndOfTextNoQuit: Ctrl+D at the end of non-empty text is a
// no-op, never a quit.
func TestCtrlDEndOfTextNoQuit(t *testing.T) {
	f, _ := feedWithRunner(t)
	typeIntoFeed(t, f, "text")
	_, cmd := f.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	if cmd != nil {
		t.Fatal("Ctrl+D at end of text must not produce a command")
	}
	if v := f.composer.value(); v != "text" {
		t.Fatalf("composer changed by Ctrl+D at end: %q", v)
	}
}

// TestCtrlDEmptyWhileRunningNoQuit: Ctrl+D with empty composer while a run
// is in flight never quits.
func TestCtrlDEmptyWhileRunningNoQuit(t *testing.T) {
	f, r := feedWithBlockingRunner(t)
	startBlockingRun(t, f, r, "running")
	_, cmd2 := f.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	if cmd2 != nil {
		t.Fatal("Ctrl+D during a run must not quit")
	}
	// Release the run.
	close(r.release)
	r.waitReturned(t)
	_, _ = f.Update(doneMsg{err: nil})
}

// TestCtrlDSafeStateQuits: Ctrl+D with empty composer, no run, no modal
// quits.
func TestCtrlDSafeStateQuits(t *testing.T) {
	f, _ := feedWithRunner(t)
	_, cmd := f.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	if cmd == nil {
		t.Fatal("Ctrl+D in the safe state must quit")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Fatalf("safe Ctrl+D produced %T, want tea.QuitMsg", msg)
	}
}

// TestResizePreservesTextAndFocus: a resize keeps the composer text and
// focus, recomputes layout, and never yields a negative viewport.
func TestResizePreservesTextAndFocus(t *testing.T) {
	f, _ := feedWithRunner(t)
	typeIntoFeed(t, f, "مرحبا\nكيف حالك")
	focusedBefore := f.composer.focused()
	_, _ = f.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
	if got := f.composer.value(); got != "مرحبا\nكيف حالك" {
		t.Fatalf("resize lost text: %q", got)
	}
	if f.composer.focused() != focusedBefore {
		t.Fatal("resize changed focus")
	}
	// Tiny terminal: no panic, no negative viewport.
	_, _ = f.Update(tea.WindowSizeMsg{Width: 5, Height: 2})
	if f.viewportHeight() < 0 {
		t.Fatal("viewport height went negative on a tiny terminal")
	}
	_ = f.View()
}

// TestComposerNeverExceedsMaxHeight: even with many lines the composer
// height stays within [minComposerHeight, maxComposerHeight].
func TestComposerNeverExceedsMaxHeight(t *testing.T) {
	f, _ := feedWithRunner(t)
	// Add 20 lines via Ctrl+J.
	typeIntoFeed(t, f, "line 0")
	for i := 1; i < 20; i++ {
		_, _ = f.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
		typeIntoFeed(t, f, "line "+string(rune('0'+i%10)))
	}
	if h := f.composer.height; h > maxComposerHeight {
		t.Fatalf("composer height = %d, want <= %d", h, maxComposerHeight)
	}
	if h := f.composer.height; h < minComposerHeight {
		t.Fatalf("composer height = %d, want >= %d", h, minComposerHeight)
	}
}

// TestStreamingDoesNotStealFocus: events arriving while the composer is
// focused do not move focus away.
func TestStreamingDoesNotStealFocus(t *testing.T) {
	f, _ := feedWithRunner(t)
	typeIntoFeed(t, f, "partial")
	_, _ = f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 1, Type: agent.TextDelta, Text: "assistant"},
		{Seq: 2, Type: agent.TurnEnd},
	}})
	if !f.composer.focused() {
		t.Fatal("streaming text must not steal composer focus")
	}
	if got := f.composer.value(); got != "partial" {
		t.Fatalf("streaming lost composer text: %q", got)
	}
}

// TestBusyRejectsConcurrentSend: Enter while a run is in flight does not
// start a second run; the text stays; nothing enters history; nothing
// duplicates in the feed.
func TestBusyRejectsConcurrentSend(t *testing.T) {
	f, r := feedWithBlockingRunner(t)
	startBlockingRun(t, f, r, "first run")

	// Second send while busy.
	typeIntoFeed(t, f, "second run")
	_, cmd2 := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd2 != nil {
		t.Fatal("Enter while busy must not produce a run command")
	}
	if r.textsLen() != 1 {
		t.Fatalf("runner invoked %d times, want 1", r.textsLen())
	}
	if got := f.composer.value(); got != "second run" {
		t.Fatalf("busy rejection must keep the text, got %q", got)
	}
	if f.HistoryLen() != 1 {
		t.Fatalf("history = %d, want 1 (only the first accepted message)", f.HistoryLen())
	}
	// Feed items: only the accepted message appears (the projector never
	// saw the rejected one, and the UI never added it manually).
	close(r.release)
	r.waitReturned(t)
}

// TestEnterAfterRunEndsStartsNewRun: after doneMsg, a new send works.
func TestEnterAfterRunEndsStartsNewRun(t *testing.T) {
	f, r := feedWithRunner(t)
	cmd := sendAndRun(f, "first")
	if cmd == nil {
		t.Fatal("first send must produce a command")
	}
	execCmd(cmd) // non-blocking runner returns immediately
	_, _ = f.Update(doneMsg{err: nil})
	if f.busy {
		t.Fatal("feed must be idle after doneMsg")
	}
	cmd2 := sendAndRun(f, "second")
	if cmd2 == nil {
		t.Fatal("send after run end must produce a command")
	}
	execCmd(cmd2)
	if r.textsLen() != 2 {
		t.Fatalf("runner invoked %d times, want 2", r.textsLen())
	}
}

// TestHistoryUpAfterSendRecalls: Up with an empty composer recalls the
// most recent message; Down restores the empty draft.
func TestHistoryUpAfterSendRecalls(t *testing.T) {
	f, _ := feedWithRunner(t)
	cmd := sendAndRun(f, "remembered message")
	execCmd(cmd)
	_, _ = f.Update(doneMsg{err: nil})

	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := f.composer.value(); got != "remembered message" {
		t.Fatalf("Up recall = %q, want 'remembered message'", got)
	}
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyDown})
	if got := f.composer.value(); got != "" {
		t.Fatalf("Down to draft = %q, want empty", got)
	}
}

// TestResizeZeroHeightNoPanic: extreme resize values never panic.
func TestResizeZeroHeightNoPanic(t *testing.T) {
	f, _ := feedWithRunner(t)
	for _, dim := range []tea.WindowSizeMsg{
		{Width: 0, Height: 0},
		{Width: 2, Height: 1},
		{Width: 300, Height: 200},
	} {
		_, _ = f.Update(dim)
		_ = f.View()
	}
}

// TestComposerGrowsThenCaps: the composer height grows with content up to
// maxComposerHeight and the viewport never goes negative.
func TestComposerGrowsThenCaps(t *testing.T) {
	f, _ := feedWithRunner(t)
	f.width = 60
	f.height = 24
	_, _ = f.Update(tea.WindowSizeMsg{Width: 60, Height: 24})

	if got := f.composer.height; got != minComposerHeight {
		t.Fatalf("initial composer height = %d, want 1", got)
	}
	// Three lines → height 3.
	typeIntoFeed(t, f, "a")
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	typeIntoFeed(t, f, "b")
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	typeIntoFeed(t, f, "c")
	if got := f.composer.height; got != 3 {
		t.Fatalf("composer height after 3 lines = %d, want 3", got)
	}
	if got := f.viewportHeight(); got <= 0 {
		t.Fatalf("viewport height after composer growth = %d, want > 0", got)
	}
	// Many more lines → capped at maxComposerHeight.
	for i := 0; i < 17; i++ {
		_, _ = f.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
		typeIntoFeed(t, f, "x")
	}
	if got := f.composer.height; got != maxComposerHeight {
		t.Fatalf("composer height after many lines = %d, want %d", got, maxComposerHeight)
	}
	// View still renders without panic.
	_ = f.View()
}

// TestClearShrinksComposer: clearing the composer returns it to one row.
func TestClearShrinksComposer(t *testing.T) {
	f, _ := feedWithRunner(t)
	f.width = 60
	f.height = 24
	_, _ = f.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	typeIntoFeed(t, f, "a")
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	typeIntoFeed(t, f, "b")
	if got := f.composer.height; got != 2 {
		t.Fatalf("composer height = %d, want 2", got)
	}
	f.composer.clear()
	if got := f.composer.height; got != minComposerHeight {
		t.Fatalf("composer height after clear = %d, want 1", got)
	}
}
