package journal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// referenceLive is a test oracle, not the future persistence implementation.
func referenceLive(es []Event) []Event {
	byID := map[int64]Event{}
	for _, e := range es {
		byID[e.Seq] = e
	}
	var out []Event
	if len(es) == 0 {
		return out
	}
	for id := es[len(es)-1].Seq; id != 0; {
		e, ok := byID[id]
		if !ok {
			panic("invalid fixture parent")
		}
		out = append(out, e)
		if e.Kind == Compact || e.Kind == SessionStart {
			break
		}
		id = e.Parent
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}
func TestRewindGolden(t *testing.T) {
	es := fixture(t, "rewind")
	var all, live strings.Builder
	var rewinds int
	for i, e := range es {
		if e.Kind == Text {
			all.WriteString(decode[TextData](t, e.Data).Text)
		}
		if e.Kind == Rewind {
			d := decode[RewindData](t, e.Data)
			if i == 0 || d.PreviousHead != es[i-1].Seq || e.Parent >= d.PreviousHead {
				t.Fatal("invalid rewind branch")
			}
			rewinds++
		}
	}
	for _, e := range referenceLive(es) {
		if e.Kind == Text {
			live.WriteString(decode[TextData](t, e.Data).Text)
		}
	}
	if rewinds != 1 || !strings.Contains(all.String(), "Abandoned answer.") || strings.Contains(live.String(), "Abandoned answer.") {
		t.Fatal("rewind did not change branch")
	}
	if live.String() != "First answer.Replacement answer." {
		t.Fatalf("unexpected live text: %q", live.String())
	}
}
func TestCompactContextBoundary(t *testing.T) {
	es := fixture(t, "rewind")
	last := es[len(es)-1]
	summary, _ := json.Marshal(CompactData{Summary: "kept summary"})
	es = append(es, Event{Seq: last.Seq + 1, Parent: last.Seq, Kind: Compact, Data: summary}, Event{Seq: last.Seq + 2, Parent: last.Seq + 1, Kind: User, Data: json.RawMessage(`{"text":"new"}`)})
	live := referenceLive(es)
	if len(live) != 2 || live[0].Kind != Compact {
		t.Fatal("old context leaked past compact")
	}
}
func TestUnknownEventPayloadRoundTrip(t *testing.T) {
	payload := json.RawMessage(`{"future":{"n":9007199254740993,"keep":[1,"x"]}}`)
	e := Event{Seq: 2, Parent: 1, Time: time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC), Kind: Kind("future.kind"), Data: payload}
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	got := decode[Event](t, raw)
	if got.Kind != e.Kind || !bytes.Equal(got.Data, payload) {
		t.Fatal("unknown payload changed")
	}
	es := fixture(t, "read_denied")[:1]
	es = append(es, got)
	if len(referenceLive(es)) != 2 {
		t.Fatal("unknown event broke ancestry")
	}
}
func ExampleEvent() {
	fmt.Println(SchemaVersion, SessionStart, Plan, Execute)
	// Output: 1 session_start plan execute
}
