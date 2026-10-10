package mcp

import (
	"encoding/json"
	"testing"
)

func TestCanonicalJSONVectors(t *testing.T) {
	// RFC 8785 §3 examples (whitespace-free, lexicographic key order).
	cases := []struct{ in, want string }{
		{`{"b":1,"a":2}`, `{"a":2,"b":1}`},
		{`{"a":{"d":4,"c":3},"b":[2,1]}`, `{"a":{"c":3,"d":4},"b":[2,1]}`},
		{`[3,2,1]`, `[3,2,1]`},
		{`{"s":"x","n":null,"b":true}`, `{"b":true,"n":null,"s":"x"}`},
		{`{"e":"line\nbreak"}`, `{"e":"line\nbreak"}`},
	}
	for _, tc := range cases {
		got, err := CanonicalJSON(json.RawMessage(tc.in))
		if err != nil {
			t.Errorf("CanonicalJSON(%s): %v", tc.in, err)
			continue
		}
		if string(got) != tc.want {
			t.Errorf("CanonicalJSON(%s) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestCanonicalJSONBigNumbers(t *testing.T) {
	// Precision must survive: no float64 rounding.
	got, err := CanonicalJSON(json.RawMessage(`{"n":12345678901234567890123}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"n":12345678901234567890123}` {
		t.Fatalf("big number not preserved: %s", got)
	}
}

func TestFingerprintToolDeterministic(t *testing.T) {
	tool := json.RawMessage(`{"name":"t","description":"d","inputSchema":{"type":"object"}}`)
	a, err := FingerprintTool("srv", tool)
	if err != nil {
		t.Fatal(err)
	}
	b, err := FingerprintTool("srv", tool)
	if err != nil {
		t.Fatal(err)
	}
	if a != b || len(a) != 64 {
		t.Fatalf("not deterministic or not sha256 hex: %q", a)
	}
	// Field order must not matter.
	shuffled := json.RawMessage(`{"inputSchema":{"type":"object"},"description":"d","name":"t"}`)
	c, err := FingerprintTool("srv", shuffled)
	if err != nil {
		t.Fatal(err)
	}
	if c != a {
		t.Fatal("fingerprint depends on field order")
	}
}

func TestFingerprintToolChangeDetection(t *testing.T) {
	base := json.RawMessage(`{"name":"t","description":"d","inputSchema":{"type":"object"}}`)
	a, err := FingerprintTool("srv", base)
	if err != nil {
		t.Fatal(err)
	}
	changed := []json.RawMessage{
		json.RawMessage(`{"name":"t","description":"CHANGED","inputSchema":{"type":"object"}}`),
		json.RawMessage(`{"name":"t","description":"d","inputSchema":{"type":"object"},"readOnlyHint":true}`),
		json.RawMessage(`{"name":"t","description":"d","inputSchema":{"type":"object","properties":{"x":{}}}}`),
	}
	for _, c := range changed {
		got, err := FingerprintTool("srv", c)
		if err != nil {
			t.Fatal(err)
		}
		if got == a {
			t.Fatalf("change not detected for %s", c)
		}
	}
	// Same tool on another server binds differently.
	other, err := FingerprintTool("other", base)
	if err != nil {
		t.Fatal(err)
	}
	if other == a {
		t.Fatal("server id not bound into fingerprint")
	}
}

func TestFingerprintToolRejectsNonObject(t *testing.T) {
	if _, err := FingerprintTool("srv", json.RawMessage(`[1,2]`)); err == nil {
		t.Fatal("expected error for non-object tool JSON")
	}
	if _, err := FingerprintTool("srv", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("empty object should fingerprint: %v", err)
	}
	if _, err := FingerprintTool("srv", json.RawMessage(`{bad`)); err == nil {
		t.Fatal("expected parse error for malformed JSON")
	}
}
