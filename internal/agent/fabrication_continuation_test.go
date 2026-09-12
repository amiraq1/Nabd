package agent_test

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/provider"
)

// This test documents, with a reproducible transcript, the case from session
// 20260910-211830.829.jsonl: after a truncated read_file whose Result tail said
// "continue with offset=170", the model did NOT call read_file again. It emitted
// assistant text that mimicked the tool's own "<n>|<content>" output for lines
// 170-173 (content that contradicts README.md) and stopped. The journal shows a
// read_record with next_offset=170 (seq 165), then a text_delta-only turn
// (seq 166-306), and no fourth tool call anywhere.
//
// Finding (option (b) of the task): this is OUT OF SCOPE for the journal layer.
//   - The loop appends each TextDelta as it streams; it cannot retract one.
//   - The journal is defined as a faithful record ("if it is not an Event, it
//     did not happen"). An assistant turn that lies about tool output is still
//     an event that happened.
//   - Messages() is the only journal->wire translation. Filtering the text here
//     would make the model see a session that never happened.
//
// A runtime "flag" would require a new event type, which is a session.jsonl
// schema change and needs explicit sign-off before it can be implemented, so no
// such event is added here.
//
// The layer that CAN catch it is the UI render guard (or an equivalent post-hoc
// journal linter). The structural condition it must use is pinned below by
// uncorroboratedReadBlocks: assistant text presented in read_file's numbered
// output format is only trustworthy when the exact line was returned by a
// read_file tool_result in the same user round. The condition keys on the event
// sequence, never on the wording of the surrounding prose.
func TestFabricatedReadContinuationIsJournaledNotJudged(t *testing.T) {
	readOut := readBlock(132, 169) // what the third read actually returned
	fabricated := "متابعة من السطر 170:\n\n" + readBlock(170, 173)

	evs := eventChain(
		agent.Event{Type: agent.RunStart, Text: "nabd test"},
		agent.Event{Type: agent.UserMsg, Text: "تابع من السطر 130"},
		agent.Event{Type: agent.TurnStart},
		agent.Event{Type: agent.TextDelta, Text: "سأقرأ الجزء التالي."},
		agent.Event{Type: agent.TurnEnd},
		agent.Event{Type: agent.ToolStart, Call: &agent.ToolCall{
			ID: "c3", Name: "read_file",
			Args: []byte(`{"path":"README.md","offset":132,"limit":200}`),
		}},
		agent.Event{Type: agent.ToolEnd, Call: &agent.ToolCall{
			ID: "c3", Name: "read_file", Output: readOut, OK: true,
		}},
		agent.Event{Type: agent.EventRead, Read: &agent.ReadRecord{
			Path: "README.md", Truncated: true, NextOffset: 170,
		}},
		agent.Event{Type: agent.TurnStart},
		agent.Event{Type: agent.TextDelta, Text: fabricated},
		agent.Event{Type: agent.TurnEnd},
		agent.Event{Type: agent.RunEnd},
	)

	// 1. Out of scope for the journal layer: the fabricated block reaches the
	//    wire verbatim. There is no suppression and no synthetic event.
	wire := assistantText(agent.Messages(agent.Live(evs)))
	if !strings.Contains(wire, "170|content of line 170") {
		t.Fatalf("journal layer must relay the fabricated block verbatim; wire=%q", wire)
	}

	// 2. No read_file call exists after the read_record: the content 170-173 was
	//    never actually read. (This is the fact the fabrication denies.)
	var readRecordSeq int
	var readsAfter int
	for _, e := range evs {
		if e.Type == agent.EventRead {
			readRecordSeq = e.Seq
		}
	}
	for _, e := range evs {
		if e.Seq <= readRecordSeq {
			continue
		}
		if (e.Type == agent.ToolStart || e.Type == agent.ToolEnd) &&
			e.Call != nil && e.Call.Name == "read_file" {
			readsAfter++
		}
	}
	if readsAfter != 0 {
		t.Fatalf("reproduction must contain no read_file call after the read_record, got %d", readsAfter)
	}

	// 3. The structural predicate the catching layer needs: every gutter line in
	//    the assistant text is uncorroborated by an actual read_file result in
	//    the same round.
	got := uncorroboratedReadBlocks(evs)
	want := []string{
		"170|content of line 170",
		"171|content of line 171",
		"172|content of line 172",
		"173|content of line 173",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("uncorroborated blocks = %q, want %q", got, want)
	}

	t.Logf("journal layer: fabricated block relayed verbatim (%d bytes), read_file calls after read_record = %d",
		len(wire), readsAfter)
	t.Logf("structural predicate: UNVERIFIED_CONTINUATION_BLOCKS=%d (catching layer: UI render guard)", len(got))
}

// TestReadContinuationGuardControls pins the other side of the predicate: a
// continuation that a read_file call actually returned is corroborated, and an
// assistant turn that does not impersonate tool output has nothing to flag.
// Without these the predicate above would be a phrase detector rather than a
// structural check.
func TestReadContinuationGuardControls(t *testing.T) {
	t.Run("corroborated continuation is not flagged", func(t *testing.T) {
		readOut := readBlock(132, 173)
		legit := "متابعة من السطر 170:\n\n" + readBlock(170, 173)
		evs := eventChain(
			agent.Event{Type: agent.UserMsg, Text: "تابع"},
			agent.Event{Type: agent.TurnStart},
			agent.Event{Type: agent.ToolStart, Call: &agent.ToolCall{
				ID: "c4", Name: "read_file",
				Args: []byte(`{"path":"README.md","offset":170,"limit":200}`),
			}},
			agent.Event{Type: agent.ToolEnd, Call: &agent.ToolCall{
				ID: "c4", Name: "read_file", Output: readOut, OK: true,
			}},
			agent.Event{Type: agent.TurnEnd},
			agent.Event{Type: agent.TurnStart},
			agent.Event{Type: agent.TextDelta, Text: legit},
			agent.Event{Type: agent.TurnEnd},
		)
		if got := uncorroboratedReadBlocks(evs); len(got) != 0 {
			t.Fatalf("corroborated continuation must not be flagged, got %q", got)
		}
	})

	t.Run("prose without a gutter block is not flagged", func(t *testing.T) {
		evs := eventChain(
			agent.Event{Type: agent.UserMsg, Text: "لخّص"},
			agent.Event{Type: agent.TurnStart},
			agent.Event{Type: agent.TextDelta, Text: "README يشرح موجّه المزودين. لا مزيد."},
			agent.Event{Type: agent.TurnEnd},
		)
		if got := uncorroboratedReadBlocks(evs); len(got) != 0 {
			t.Fatalf("plain prose must not be flagged, got %q", got)
		}
	})
}

// readGutterLineRE matches read_file's numbered output format: "<n>|<content>".
var readGutterLineRE = regexp.MustCompile(`^\s*\d+\|`)

// uncorroboratedReadBlocks returns every assistant line that uses read_file's
// output format but that no read_file tool_result in the same user round
// actually returned. A round is the span between two user turns. The predicate
// is structural: it compares the assistant's claimed lines against the events'
// real tool output, so it cannot be fooled by prose and does not need a phrase
// blocklist.
func uncorroboratedReadBlocks(evs []agent.Event) []string {
	var (
		out       []string
		toolOut   []string
		assistant strings.Builder
	)
	flush := func() {
		for _, line := range strings.Split(assistant.String(), "\n") {
			if !readGutterLineRE.MatchString(line) {
				continue
			}
			corroborated := false
			for _, o := range toolOut {
				if strings.Contains(o, line) {
					corroborated = true
					break
				}
			}
			if !corroborated {
				out = append(out, line)
			}
		}
		toolOut = nil
		assistant.Reset()
	}
	for _, e := range agent.Live(evs) {
		switch e.Type {
		case agent.UserMsg:
			flush()
		case agent.TextDelta:
			assistant.WriteString(e.Text)
		case agent.ToolEnd:
			if e.Call != nil && e.Call.Name == "read_file" {
				toolOut = append(toolOut, e.Call.Output)
			}
		}
	}
	flush()
	return out
}

// assistantText concatenates every assistant message's text in order.
func assistantText(ms []provider.Message) string {
	var b strings.Builder
	for _, m := range ms {
		if m.Role == provider.Assistant {
			b.WriteString(m.Text)
		}
	}
	return b.String()
}

// readBlock fabricates a read_file-style numbered block for lines start..end.
func readBlock(start, end int) string {
	var b strings.Builder
	for n := start; n <= end; n++ {
		fmt.Fprintf(&b, "%d|content of line %d\n", n, n)
	}
	return strings.TrimRight(b.String(), "\n")
}

// eventChain stamps Seq and Parent the way Loop.emit does, so Live() can walk
// the branch.
func eventChain(evs ...agent.Event) []agent.Event {
	for i := range evs {
		evs[i].Seq = i + 1
		if i > 0 {
			evs[i].Parent = i
		}
	}
	return evs
}
