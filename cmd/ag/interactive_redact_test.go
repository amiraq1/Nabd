package main

import (
	"strings"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/redact"
	"nabd/internal/ui"
)

// feedUpdateSink drives a ui.Feed through its real Update method — the same
// intake the batcher uses in production (feedSink -> Batcher -> SendBatch ->
// Update). It stands in for the batcher so the test is deterministic.
type feedUpdateSink struct{ feed *ui.Feed }

func (s feedUpdateSink) Emit(e agent.Event) error {
	s.feed.Update(ui.AgentEventBatch([]agent.Event{e}))
	return nil
}

// TestInteractiveFeedRedactsStreamedSecret pins the interactive half of the
// disclosure contract: assistant text reaches the feed through
// newStreamRedactSink, which main.go wires around the whole Fanout, so a
// credential streamed across several text_delta events is redacted before the
// feed ever renders it. Plain headless stdout is the one surface that stays
// verbatim (TestHeadlessPlainStdoutNotRedactedByDesign).
func TestInteractiveFeedRedactsStreamedSecret(t *testing.T) {
	const secret = "sk-proj-abcdefghijklmnopqrstuvwxyz0123456789"
	// Split across three deltas so a per-chunk redactor would let it through;
	// the stream redactor joins the chunks and redacts the joined value.
	chunks := []string{"the key is ", secret[:15], secret[15:]}
	run := make([]agent.Event, 0, len(chunks)+1)
	for i, chunk := range chunks {
		run = append(run, agent.Event{Seq: i + 1, Type: agent.TextDelta, Text: chunk})
	}
	// A non-delta event ends the text run and flushes the held bytes, redacted.
	run = append(run, agent.Event{Seq: len(chunks) + 1, Type: agent.TurnEnd})

	// The exact production decorator, wrapped around the feed's Update intake.
	feed := ui.NewFeed()
	sink := newStreamRedactSink(feedUpdateSink{feed: feed})
	for _, e := range run {
		if err := sink.Emit(e); err != nil {
			t.Fatal(err)
		}
	}

	view := feed.View()
	if strings.Contains(view, secret) {
		t.Fatalf("interactive feed leaked the streamed secret:\n%s", view)
	}
	if !strings.Contains(view, redact.Token) {
		t.Fatalf("interactive feed did not render the redaction token:\n%s", view)
	}

	// Non-vacuity: the same run delivered without the decorator renders the
	// credential verbatim, so the assertions above prove redaction and not an
	// empty projection.
	control := ui.NewFeed()
	control.Update(ui.AgentEventBatch(run))
	if !strings.Contains(control.View(), "sk-proj-") {
		t.Fatalf("control feed should render the raw credential:\n%s", control.View())
	}
}
