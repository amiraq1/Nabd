package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// decodeTarget mirrors the production targets: named pointer fields (absent and
// null stay distinct from a value) plus one opaque RawMessage, the shape
// mcp_tool's args has. The opaque field is what lets a case prove the boundary
// refuses something the typed decode would otherwise accept unexamined.
type decodeTarget struct {
	Cmd  *string         `json:"cmd"`
	Path *string         `json:"path"`
	Args json.RawMessage `json:"args"`
}

func decodeStrictString(raw string) error {
	var dst decodeTarget
	return decodeStrict(json.RawMessage(raw), &dst)
}

// nestedObject builds {"a":{"a":…1…}} with exactly n object levels.
func nestedObject(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(`{"a":`)
	}
	b.WriteString("1")
	b.WriteString(strings.Repeat("}", n))
	return b.String()
}

// TestDecodeStrictRejectsTrailingData pins the single-value contract V14 asked
// for: the decoder used to perform one Decode with no EOF check, so a second
// value after the first was ignored while the first still ran.
func TestDecodeStrictRejectsTrailingData(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string // substring of the error; empty means the call must succeed
	}{
		{"adjacent objects", `{"cmd":"true"}{"cmd":"false"}`, "unexpected trailing data"},
		{"trailing primitive", `{"cmd":"true"} 5`, "unexpected trailing data"},
		{"trailing null", `{"cmd":"true"}null`, "unexpected trailing data"},
		{"trailing array", `{"cmd":"true"}[]`, "unexpected trailing data"},
		{"trailing newline accepted", "{\"cmd\":\"true\"}\n", ""},
		{"trailing spaces accepted", `{"cmd":"true"}   `, ""},
		{"plain object accepted", `{"cmd":"true"}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := decodeStrictString(tc.raw)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("decodeStrict(%q) = %v, want success", tc.raw, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("decodeStrict(%q) accepted trailing data", tc.raw)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("decodeStrict(%q) = %q, want it to contain %q", tc.raw, err, tc.want)
			}
		})
	}
}

// TestDecodeStrictRejectsDuplicateKeysAtAnyDepthOrSpelling pins the three
// bypasses V14 listed: leading whitespace skipped the old check entirely,
// case-varied keys both match one Go struct field, and only the top level was
// ever inspected.
func TestDecodeStrictRejectsDuplicateKeysAtAnyDepthOrSpelling(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string // substring of the error; empty means the call must succeed
	}{
		{"exact duplicate", `{"cmd":"a","cmd":"b"}`, "duplicate key"},
		{"escaped spelling of the same key", `{"cmd":"a","\u0063md":"b"}`, "duplicate key"},
		{"mixed-case spelling", `{"cmd":"a","Cmd":"b"}`, "duplicate key"},
		{"leading whitespace no longer skips the check", " \n " + `{"cmd":"a","cmd":"b"}`, "duplicate key"},
		{"nested object", `{"args":{"x":1,"x":2}}`, "duplicate key"},
		{"inside an array element", `{"args":[{"x":1,"x":2}]}`, "duplicate key"},
		{"two levels down", `{"args":{"outer":{"inner":1,"inner":2}}}`, "duplicate key"},
		{"distinct keys accepted", `{"cmd":"a","path":"b"}`, ""},
		{"same key in sibling objects accepted", `{"args":[{"x":1},{"x":2}]}`, ""},
		{"same key at two levels accepted", `{"cmd":"a","args":{"cmd":1}}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := decodeStrictString(tc.raw)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("decodeStrict(%q) = %v, want success", tc.raw, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("decodeStrict(%q) accepted a duplicate key", tc.raw)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("decodeStrict(%q) = %q, want it to contain %q", tc.raw, err, tc.want)
			}
		})
	}
}

// TestDecodeStrictNamesTheDuplicateKey: a refusal must name what was repeated,
// not just that something was.
func TestDecodeStrictNamesTheDuplicateKey(t *testing.T) {
	err := decodeStrictString(`{"cmd":"a","cmd":"b"}`)
	if err == nil {
		t.Fatal("a duplicate key was accepted")
	}
	if !strings.Contains(err.Error(), `"cmd"`) {
		t.Fatalf("the refusal must name the repeated key, got %q", err)
	}
}

// TestDecodeStrictRefusesPathologicalNesting: the depth guard is only real if a
// deep document fails with its own message. A syntax or type error would mean
// the guard never fired.
func TestDecodeStrictRefusesPathologicalNesting(t *testing.T) {
	t.Run("at the ceiling accepted", func(t *testing.T) {
		var dst any
		if err := decodeStrict(json.RawMessage(nestedObject(maxJSONDepth)), &dst); err != nil {
			t.Fatalf("nesting at the ceiling must decode: %v", err)
		}
	})
	t.Run("one level past the ceiling refused", func(t *testing.T) {
		var dst any
		err := decodeStrict(json.RawMessage(nestedObject(maxJSONDepth+1)), &dst)
		if err == nil {
			t.Fatal("nesting past the ceiling was accepted")
		}
		if !strings.Contains(err.Error(), "nest deeper than") {
			t.Fatalf("want the depth guard's message, got %q", err)
		}
	})
	t.Run("an opaque field is not a bypass", func(t *testing.T) {
		raw := `{"args":` + nestedObject(maxJSONDepth+1) + `}`
		var dst decodeTarget
		err := decodeStrict(json.RawMessage(raw), &dst)
		if err == nil || !strings.Contains(err.Error(), "nest deeper than") {
			t.Fatalf("a deeply nested opaque field must be refused, got %v", err)
		}
	})
}

// TestDecodeStrictLeavesSyntaxErrorsToTheTypedDecoder: malformed input keeps
// the decoder's own message, so a caller reading the error is not told a
// document was inspected and found duplicated or too deep when it was not.
func TestDecodeStrictLeavesSyntaxErrorsToTheTypedDecoder(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"truncated object", `{"cmd":`, "unexpected EOF"},
		{"not JSON", `not json`, "invalid character"},
		{"top-level scalar", `42`, "cannot unmarshal"},
		// json.Decoder.Decode reports io.EOF for empty input; the
		// "unexpected end of JSON input" wording belongs to json.Unmarshal.
		{"empty input", ``, "EOF"},
		{"unclosed array", `[{"a":1}`, "unexpected EOF"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := decodeStrictString(tc.raw)
			if err == nil {
				t.Fatalf("decodeStrict(%q) accepted malformed input", tc.raw)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("decodeStrict(%q) = %q, want it to contain %q", tc.raw, err, tc.want)
			}
			if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "nest deeper than") {
				t.Fatalf("a syntax error must not be reported as %q", err)
			}
		})
	}
}

// TestDecodeStrictAcceptsManyUniqueKeys pins that the duplicate check itself is
// linear: one map per object and one pass, never a per-key scan of a slice.
func TestDecodeStrictAcceptsManyUniqueKeys(t *testing.T) {
	const n = 5000
	var b strings.Builder
	b.WriteByte('{')
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `"k%d":%d`, i, i)
	}
	b.WriteByte('}')
	var dst map[string]int
	if err := decodeStrict(json.RawMessage(b.String()), &dst); err != nil {
		t.Fatalf("an object with %d unique keys was refused: %v", n, err)
	}
	if len(dst) != n {
		t.Fatalf("decoded %d keys, want %d", len(dst), n)
	}
}
