package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func oversizedCall(extra int) Event {
	return Event{
		Type: ToolEnd,
		Call: &ToolCall{
			ID:     "call_1",
			Name:   "bash",
			Output: strings.Repeat("a", MaxPersistedOutput+extra),
			OK:     true,
		},
	}
}

// TestForStoreRecordsTruncatedBytes is the point of the change: the size of
// the loss is data, not prose buried in the payload.
func TestForStoreRecordsTruncatedBytes(t *testing.T) {
	stored := oversizedCall(500).ForStore()
	if stored.Call.TruncatedBytes != 500 {
		t.Fatalf("TruncatedBytes = %d, want 500", stored.Call.TruncatedBytes)
	}
	if !strings.Contains(stored.Call.Output, "[truncated 500 bytes]") {
		t.Errorf("human marker lost from output: %q", tailOf(stored.Call.Output))
	}
}

// TestForStoreLeavesSmallOutputAlone keeps the field honest: a zero must mean
// "nothing was cut", which is also what older journals decode to.
func TestForStoreLeavesSmallOutputAlone(t *testing.T) {
	e := Event{Type: ToolEnd, Call: &ToolCall{ID: "c", Name: "glob", Output: "three files"}}
	stored := e.ForStore()
	if stored.Call.TruncatedBytes != 0 {
		t.Fatalf("TruncatedBytes = %d, want 0", stored.Call.TruncatedBytes)
	}
	if stored.Call.Output != "three files" {
		t.Fatalf("output was modified: %q", stored.Call.Output)
	}
}

// TestForStoreDoesNotMutateLiveEvent protects the existing copy-on-write
// property: the in-memory event the UI holds must not gain the marker.
func TestForStoreDoesNotMutateLiveEvent(t *testing.T) {
	live := oversizedCall(64)
	before := len(live.Call.Output)
	_ = live.ForStore()
	if len(live.Call.Output) != before || live.Call.TruncatedBytes != 0 {
		t.Fatalf("live event mutated: len=%d truncated=%d", len(live.Call.Output), live.Call.TruncatedBytes)
	}
}

// TestTruncatedBytesIsAdditive proves the wire contract: the key is absent
// when nothing was cut, so old readers and old journals are unaffected.
func TestTruncatedBytesIsAdditive(t *testing.T) {
	clean, err := json.Marshal(Event{Type: ToolEnd, Call: &ToolCall{ID: "c", Name: "glob"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(clean), "truncated_bytes") {
		t.Fatalf("field should be omitted when zero: %s", clean)
	}

	var legacy Event
	if err := json.Unmarshal([]byte(`{"type":"tool_end","call":{"id":"c","name":"bash","out":"x"}}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.Call.TruncatedBytes != 0 {
		t.Fatalf("legacy record decoded to %d, want 0", legacy.Call.TruncatedBytes)
	}
}

func tailOf(s string) string {
	if len(s) <= 60 {
		return s
	}
	return s[len(s)-60:]
}
