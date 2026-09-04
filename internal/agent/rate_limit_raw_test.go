package agent_test

import (
	"encoding/json"
	"math"
	"path/filepath"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/store"
)

// TestEventRateLimitRawFieldRoundTrip proves the six raw 429 fields survive
// the real serialization path (ForStore → store.Append → store.Read) with
// precision intact. Precision loss here is the acceptance gate for any later
// rate-limit measurement: a 0.1s rounding is ~±13 tokens, a full-second
// rounding ~±67.
func TestEventRateLimitRawFieldRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	j, err := store.NewJSONL(path)
	if err != nil {
		t.Fatal(err)
	}

	src := agent.Event{
		Seq:        1,
		Type:       agent.EventRateLimit,
		Code:       429,
		Limit:      8000,
		Used:       1859,
		Requested:  6778,
		WaitSec:    4.7775,
		Attempt:    1,
		RetryAfter: 4.7775,
		RawMessage: `{"error":{"message":"Rate limit reached ... Limit 8000, Used 1859, Requested 6778. Please try again in 4.7775s."}}`,
		Err:        "http 429: rate limit",
	}
	if err := j.Append(src); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := store.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 event after round trip, got %d", len(got))
	}
	e := got[0]
	if e.Type != agent.EventRateLimit {
		t.Fatalf("expected EventRateLimit, got %s", e.Type)
	}
	const want = 4.7775
	if e.Limit != 8000 || e.Used != 1859 || e.Requested != 6778 || e.Attempt != 1 {
		t.Errorf("raw counts not preserved: %+v", e)
	}
	if math.Abs(e.RetryAfter-want) > 1e-9 {
		t.Errorf("retry_after precision lost: got %.10f want %.10f", e.RetryAfter, want)
	}
	if math.Abs(e.WaitSec-want) > 1e-9 {
		t.Errorf("wait_s precision lost: got %.10f want %.10f", e.WaitSec, want)
	}
	if !json.Valid([]byte(e.RawMessage)) || e.RawMessage == "" {
		t.Errorf("raw_message not preserved verbatim: %q", e.RawMessage)
	}
}

// TestEventRateLimitDoesNotShiftSerializedPromptContent: a list that contains
// an EventRateLimit must serialize byte-identically to the same list without
// it, through Messages() and json.Marshal — the encode path used for the
// request. Rate-limit events are operator-visible only and must not leak.
func TestEventRateLimitDoesNotShiftSerializedPromptContent(t *testing.T) {
	base := []agent.Event{
		{Seq: 1, Type: agent.UserMsg, Text: "start"},
		{Seq: 2, Parent: 1, Type: agent.ToolStart, Call: &agent.ToolCall{ID: "t1", Name: "read_file", Args: json.RawMessage(`{"path":"main.go"}`)}},
		{Seq: 3, Parent: 2, Type: agent.ToolEnd, Call: &agent.ToolCall{ID: "t1", Name: "read_file", Output: "content", OK: true}},
		{Seq: 4, Parent: 3, Type: agent.TurnEnd},
	}
	withRL := append(append([]agent.Event{}, base...), agent.Event{
		Seq:        5,
		Type:       agent.EventRateLimit,
		Code:       429,
		Limit:      8000,
		Used:       1859,
		Requested:  6778,
		WaitSec:    4.7775,
		Attempt:    1,
		RetryAfter: 4.7775,
	})

	bBase, err := json.Marshal(agent.Messages(base))
	if err != nil {
		t.Fatal(err)
	}
	bRL, err := json.Marshal(agent.Messages(withRL))
	if err != nil {
		t.Fatal(err)
	}
	if string(bBase) != string(bRL) {
		t.Fatalf("EventRateLimit shifted serialized prompt content:\nbase: %s\nwith RL: %s", bBase, bRL)
	}
}
