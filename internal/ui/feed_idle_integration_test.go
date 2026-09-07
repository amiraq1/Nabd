package ui

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"nabd/internal/agent"
)

// idleTestRunner simulates an agent loop runner connected to a Batcher.
// When Run is called, it simulates the real agent loop by adding UserMsg,
// TextDelta streaming events, and TurnEnd to the batcher.
type idleTestRunner struct {
	mu           sync.Mutex
	batcher      *Batcher
	seq          int
	runs         []string
	customReply  map[string]string
	defaultReply string
	onRunCalled  chan string
}

func newIdleTestRunner(batcher *Batcher) *idleTestRunner {
	return &idleTestRunner{
		batcher:      batcher,
		customReply:  make(map[string]string),
		defaultReply: "acknowledged",
		onRunCalled:  make(chan string, 10),
	}
}

func (r *idleTestRunner) nextSeq() int {
	r.seq++
	return r.seq
}

func (r *idleTestRunner) Run(ctx context.Context, text string) error {
	r.mu.Lock()
	r.runs = append(r.runs, text)
	reply, ok := r.customReply[text]
	if !ok {
		reply = r.defaultReply
	}
	r.mu.Unlock()

	select {
	case r.onRunCalled <- text:
	default:
	}

	// 1. Emit UserMsg (non-sensitive)
	r.batcher.Add(agent.Event{
		Seq:  r.nextSeq(),
		Type: agent.UserMsg,
		Text: text,
	})

	// 2. Emit TextDelta (non-sensitive)
	r.batcher.Add(agent.Event{
		Seq:  r.nextSeq(),
		Type: agent.TextDelta,
		Text: reply,
	})

	// 3. Emit TurnEnd (non-sensitive)
	r.batcher.Add(agent.Event{
		Seq:  r.nextSeq(),
		Type: agent.TurnEnd,
	})

	return nil
}

// TestFeedLiveDeliveryAfterStartupIdle tests that in a live running terminal UI,
// after an idle period where the batcher timer fires on an empty event queue,
// subsequent user messages and assistant replies are properly delivered and rendered.
func TestFeedLiveDeliveryAfterStartupIdle(t *testing.T) {
	sess := StartPTYSession(t, 80, 24)

	emptyFlushed := make(chan struct{}, 20)
	interval := 20 * time.Millisecond

	batcher := NewBatcher(interval, 128, func(batch []agent.Event) {
		sess.Feed.SendBatch(batch)
	})
	batcher.emptyFlushHook = func() {
		select {
		case emptyFlushed <- struct{}{}:
		default:
		}
	}

	batcher.Start()
	t.Cleanup(func() {
		batcher.Stop()
	})

	runner := newIdleTestRunner(batcher)
	runner.customReply["hello world"] = "hello from assistant"
	sess.Feed.SetRunner(runner)

	// 1. Wait for batcher to experience an idle expiration on an empty queue.
	select {
	case <-emptyFlushed:
	case <-time.After(2 * time.Second):
		t.Fatal("batcher never fired idle tick on empty queue")
	}

	// 2. Type user message into composer via PTY.
	sess.WriteString("hello world")
	if err := sess.WaitForText("hello world", 2*time.Second); err != nil {
		t.Fatalf("typed user message did not appear in composer: %v", err)
	}

	// 3. Send Enter to submit the message.
	sess.SendKey([]byte("\r"))

	// 4. Verify runner received the run.
	select {
	case text := <-runner.onRunCalled:
		if text != "hello world" {
			t.Fatalf("runner received unexpected text: %q", text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runner was not invoked upon pressing Enter")
	}

	// 5. Assert the assistant reply becomes visible in the terminal snapshot.
	// On the buggy baseline, the batcher timer was dead after idle, so the non-sensitive
	// UserMsg, TextDelta, and TurnEnd events remained stranded in b.events and never appeared.
	if err := sess.WaitForText("hello from assistant", 3*time.Second); err != nil {
		snap := sess.Snapshot()
		t.Fatalf("assistant reply was never delivered or rendered after startup idle: %v\nScreen:\n%s", err, snap.PlainText())
	}
}

// TestFeedLiveDeliveryAfterIdleArabicMultiTurn tests the exact scenario reported by the user:
// an initial idle period or turn, followed by Arabic input ("قل أهلاً فقط، بدون استخدام أدوات.")
// and the assistant reply ("أهلاً"), ensuring correct delivery and rendering after idle periods.
func TestFeedLiveDeliveryAfterIdleArabicMultiTurn(t *testing.T) {
	sess := StartPTYSession(t, 80, 24)

	emptyFlushed := make(chan struct{}, 50)
	interval := 20 * time.Millisecond

	batcher := NewBatcher(interval, 128, func(batch []agent.Event) {
		sess.Feed.SendBatch(batch)
	})
	batcher.emptyFlushHook = func() {
		select {
		case emptyFlushed <- struct{}{}:
		default:
		}
	}

	batcher.Start()
	t.Cleanup(func() {
		batcher.Stop()
	})

	runner := newIdleTestRunner(batcher)
	arabicPrompt := "قل أهلاً فقط، بدون استخدام أدوات."
	arabicReply := "أهلاً"
	runner.customReply[arabicPrompt] = arabicReply
	runner.customReply["turn 1"] = "reply 1"
	sess.Feed.SetRunner(runner)

	// Turn 1: Initial turn
	sess.WriteString("turn 1\r")
	if err := sess.WaitForText("reply 1", 3*time.Second); err != nil {
		snap := sess.Snapshot()
		t.Fatalf("turn 1 reply failed to appear: %v\nScreen:\n%s", err, snap.PlainText())
	}
	select {
	case <-runner.onRunCalled:
	default:
	}

	// Drain any past empty flushed signals
	for len(emptyFlushed) > 0 {
		<-emptyFlushed
	}

	// Wait for an idle period to elapse after turn 1 (queue empty, timer fires)
	select {
	case <-emptyFlushed:
	case <-time.After(2 * time.Second):
		t.Fatal("batcher never fired idle tick between turns")
	}

	// Turn 2: Send Arabic message
	// First type the full text
	sess.WriteString(arabicPrompt)
	if err := sess.WaitForText("بدون استخدام أدوات.", 2*time.Second); err != nil {
		t.Fatalf("arabic prompt did not appear in composer: %v", err)
	}

	// Press Enter to submit
	sess.SendKey([]byte("\r"))

	// Verify runner received turn 2 with exact prompt
	select {
	case text := <-runner.onRunCalled:
		if text != arabicPrompt {
			t.Fatalf("runner received unexpected text: %q", text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runner was not invoked for turn 2")
	}

	// Assert assistant reply "أهلاً" appears as its own rendered line in the feed
	if err := sess.WaitForCondition("arabic reply in feed", 3*time.Second, func(snap ScreenSnapshot) bool {
		for _, r := range snap.PlainRows() {
			if strings.TrimSpace(r) == arabicReply {
				return true
			}
		}
		return false
	}); err != nil {
		snap := sess.Snapshot()
		t.Fatalf("arabic reply was never delivered or rendered in feed after idle period: %v\nScreen:\n%s", err, snap.PlainText())
	}
}
