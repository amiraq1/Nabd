package journal

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	utcTime := time.Date(2026, 10, 27, 20, 0, 0, 0, time.UTC)
	rawPayload := json.RawMessage(`{"path":"hello.txt","size":1234567890123456789,"special":"<tag>&'\""}`)
	original := Event{
		Seq:    10,
		Parent: 9,
		Time:   utcTime,
		Kind:   ToolCall,
		Data:   rawPayload,
	}

	encoded, err := EncodeEvent(original)
	if err != nil {
		t.Fatalf("EncodeEvent failed: %v", err)
	}

	// Verify no HTML escaping
	if strings.Contains(string(encoded), `\u003c`) || strings.Contains(string(encoded), `\u0026`) {
		t.Fatalf("HTML characters were escaped in encoded output: %s", string(encoded))
	}

	// Verify no trailing newlines
	if strings.Contains(string(encoded), "\n") || strings.Contains(string(encoded), "\r") {
		t.Fatalf("encoded line contains newline characters: %q", string(encoded))
	}

	decoded, err := DecodeEvent(encoded)
	if err != nil {
		t.Fatalf("DecodeEvent failed: %v", err)
	}

	if decoded.Seq != original.Seq || decoded.Parent != original.Parent || decoded.Kind != original.Kind {
		t.Fatalf("decoded mismatch: got %+v, want %+v", decoded, original)
	}
	if !decoded.Time.Equal(original.Time) {
		t.Fatalf("time mismatch: got %v, want %v", decoded.Time, original.Time)
	}
}

func TestEncodeValidatesNoticeStatus(t *testing.T) {
	utcTime := time.Date(2026, 10, 27, 20, 0, 0, 0, time.UTC)

	// Valid turn.end notice
	validData, _ := json.Marshal(NoticeData{Code: "turn.end", TurnID: "turn-1", Status: StatusCompleted})
	validEvent := Event{Seq: 2, Parent: 1, Time: utcTime, Kind: Notice, Data: validData}
	if _, err := EncodeEvent(validEvent); err != nil {
		t.Fatalf("expected valid notice to encode: %v", err)
	}

	// Invalid turn.end notice (empty status)
	invalidData, _ := json.Marshal(NoticeData{Code: "turn.end", TurnID: "turn-1", Status: ""})
	invalidEvent := Event{Seq: 2, Parent: 1, Time: utcTime, Kind: Notice, Data: invalidData}
	if _, err := EncodeEvent(invalidEvent); err == nil {
		t.Fatal("expected error encoding turn.end with empty status")
	}

	// Invalid other notice (non-empty status on tool.execution_started)
	badOtherData, _ := json.Marshal(NoticeData{Code: "tool.execution_started", Status: StatusCompleted})
	badOtherEvent := Event{Seq: 2, Parent: 1, Time: utcTime, Kind: Notice, Data: badOtherData}
	if _, err := EncodeEvent(badOtherEvent); err == nil {
		t.Fatal("expected error encoding tool.execution_started with non-empty status")
	}
}

func TestCanonicalizeJSONKeyOrderingAndUnicode(t *testing.T) {
	input1 := []byte(`{"z":1,"a":{"b":2,"a":1},"m":"العربية"}`)
	input2 := []byte(`{"m":"العربية","a":{"a":1,"b":2},"z":1}`)

	c1, err := CanonicalizeJSON(input1)
	if err != nil {
		t.Fatalf("CanonicalizeJSON(input1) failed: %v", err)
	}
	c2, err := CanonicalizeJSON(input2)
	if err != nil {
		t.Fatalf("CanonicalizeJSON(input2) failed: %v", err)
	}

	if string(c1) != string(c2) {
		t.Fatalf("canonical outputs differ:\nc1: %s\nc2: %s", c1, c2)
	}

	expected := `{"a":{"a":1,"b":2},"m":"العربية","z":1}`
	if string(c1) != expected {
		t.Fatalf("unexpected canonical output: got %s, want %s", string(c1), expected)
	}
}

func TestComputeRequestHash(t *testing.T) {
	args1 := json.RawMessage(`{"path":"test.txt","mode":"w","flag":true}`)
	args2 := json.RawMessage(`{"flag":true,"mode":"w","path":"test.txt"}`)

	h1, err := ComputeRequestHash("edit_file", args1, "/workspace/repo")
	if err != nil {
		t.Fatalf("ComputeRequestHash failed: %v", err)
	}
	h2, err := ComputeRequestHash("edit_file", args2, "/workspace/repo")
	if err != nil {
		t.Fatalf("ComputeRequestHash failed: %v", err)
	}

	if h1 != h2 {
		t.Fatalf("hashes should match despite args key order: %s != %s", h1, h2)
	}

	// Verify validRequestHash oracle accepts it
	if !validRequestHash(h1) {
		t.Fatalf("ComputeRequestHash produced invalid digest shape: %s", h1)
	}
}
