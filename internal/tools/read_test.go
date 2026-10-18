package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadFileStrictArgs verifies the three required argument handling cases for read_file:
// 1. Extra unknown key is dropped by the repair layer (RuleDropUnknown) and reading succeeds.
// 2. Duplicate key is rejected by decodeStrict.
// 3. Valid fields (path, offset, limit) work as expected.
func TestReadFileStrictArgs(t *testing.T) {
	r, dir := newReg(t)
	samplePath := filepath.Join(dir, "sample.txt")
	sampleContent := "line 1: apple\nline 2: banana\nline 3: cherry\nline 4: date\n"
	if err := os.WriteFile(samplePath, []byte(sampleContent), 0o644); err != nil {
		t.Fatalf("failed to write sample file: %v", err)
	}

	t.Run("extra key dropped and read succeeds", func(t *testing.T) {
		var fixes []Fix
		r.OnRepair = func(f Fix) {
			fixes = append(fixes, f)
		}
		defer func() { r.OnRepair = nil }()

		raw := json.RawMessage(`{"path":"sample.txt","bogus":123}`)
		out, ok, err := r.Run(context.Background(), providerToolCall("read_file", raw))
		if err != nil || !ok {
			t.Fatalf("read_file failed with dropped key: ok=%v err=%v", ok, err)
		}
		if !strings.Contains(out, "1|line 1: apple") {
			t.Fatalf("expected file contents, got: %q", out)
		}
		dropped := DroppedKeys(fixes)
		if len(dropped) != 1 || dropped[0] != "bogus" {
			t.Fatalf("expected bogus to be in dropped keys, got fixes: %+v, dropped: %v", fixes, dropped)
		}
	})

	t.Run("duplicate key rejected", func(t *testing.T) {
		raw := json.RawMessage(`{"path":"sample.txt","path":"sample.txt"}`)
		out, ok, err := r.Run(context.Background(), providerToolCall("read_file", raw))
		if err == nil || ok {
			t.Fatalf("expected duplicate key to be rejected, got ok=%v, err=%v, out=%q", ok, err, out)
		}
		if !strings.Contains(err.Error(), "duplicate key") {
			t.Fatalf("expected error mentioning 'duplicate key', got: %v", err)
		}
	})

	t.Run("trailing data rejected", func(t *testing.T) {
		raw := json.RawMessage(`{"path":"sample.txt"}{"path":"other.txt"}`)
		out, ok, err := r.Run(context.Background(), providerToolCall("read_file", raw))
		if err == nil || ok {
			t.Fatalf("expected trailing data to be rejected, got ok=%v, err=%v, out=%q", ok, err, out)
		}
		if !strings.Contains(err.Error(), "unexpected trailing data") {
			t.Fatalf("expected error mentioning trailing data, got: %v", err)
		}
	})

	t.Run("case-varied key is refused, not guessed", func(t *testing.T) {
		// Two spellings that reach the same Go field are an ambiguity, and the
		// repair layer does not guess between them — the same rule it already
		// applies to a field alias when both spellings are present. The strict
		// decoder refuses the call, so neither spelling silently wins.
		raw := json.RawMessage(`{"path":"sample.txt","Path":"other.txt"}`)
		out, ok, err := r.Run(context.Background(), providerToolCall("read_file", raw))
		if err == nil || ok {
			t.Fatalf("a case-varied duplicate was accepted: ok=%v err=%v out=%q", ok, err, out)
		}
		if !strings.Contains(err.Error(), "duplicate key") {
			t.Fatalf("expected a duplicate-key refusal, got: %v", err)
		}
	})

	t.Run("valid fields work as expected", func(t *testing.T) {
		raw := json.RawMessage(`{"path":"sample.txt","offset":2,"limit":2}`)
		out, ok, err := r.Run(context.Background(), providerToolCall("read_file", raw))
		if err != nil || !ok {
			t.Fatalf("read_file failed with valid args: ok=%v err=%v", ok, err)
		}
		if !strings.Contains(out, "2|line 2: banana") || !strings.Contains(out, "3|line 3: cherry") {
			t.Fatalf("expected lines 2 and 3, got: %q", out)
		}
		if strings.Contains(out, "1|line 1: apple") || strings.Contains(out, "4|line 4: date") {
			t.Fatalf("did not expect lines 1 or 4, got: %q", out)
		}
	})
}

// TestReadFileDecoderBoundaryWithRepairOff pins the decoder itself, with repair
// disabled: that is the net behind RepairCall, so a bypass the repair layer
// happens to drop as an undeclared key must still be refused on its own.
func TestReadFileDecoderBoundaryWithRepairOff(t *testing.T) {
	r, dir := newReg(t)
	r.repairOff = true
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), []byte("line 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		raw  string
	}{
		{"mixed-case duplicate key", `{"path":"sample.txt","Path":"other.txt"}`},
		{"nested duplicate key", `{"path":"sample.txt","opts":{"a":1,"a":2}}`},
		{"leading whitespace duplicate key", "  {\"path\":\"sample.txt\",\"path\":\"other.txt\"}"},
		{"trailing object", `{"path":"sample.txt"}{"path":"other.txt"}`},
		{"trailing primitive", `{"path":"sample.txt"} 5`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, ok, err := r.Run(context.Background(), providerToolCall("read_file", json.RawMessage(tc.raw)))
			if err == nil || ok {
				t.Fatalf("the decoder accepted %s: ok=%v err=%v out=%q", tc.name, ok, err, out)
			}
		})
	}
}
