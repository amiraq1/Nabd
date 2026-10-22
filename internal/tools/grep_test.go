package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestGrepStrictArgumentBoundary pins the decoder behind grep with repair
// disabled: grep is the other ReadOnly tool whose arguments were decoded with
// json.Unmarshal, which drops an undeclared key silently and keeps the last
// value of a duplicate.
func TestGrepStrictArgumentBoundary(t *testing.T) {
	r, _ := newReg(t)
	r.repairOff = true

	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"exact duplicate key", `{"pattern":"a","pattern":"b"}`, "duplicate key"},
		{"case-varied duplicate key", `{"pattern":"a","Pattern":"b"}`, "duplicate key"},
		{"duplicate across two declared fields", `{"pattern":"a","glob":"*.go","Glob":"*.md"}`, "duplicate key"},
		{"undeclared field", `{"pattern":"a","bogus":1}`, "unknown field"},
		{"nesting past the ceiling", `{"pattern":"a","deep":` + nestedObject(maxJSONDepth+1) + `}`, "nest deeper than"},
		{"trailing object", `{"pattern":"a"}{"pattern":"b"}`, "unexpected trailing data"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, ok, err := r.Run(context.Background(), providerToolCall("grep", json.RawMessage(tc.raw)))
			if err == nil || ok {
				t.Fatalf("grep accepted %s: ok=%v err=%v out=%q", tc.name, ok, err, out)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("grep %s: error %q does not contain %q", tc.name, err, tc.want)
			}
		})
	}
}

// TestGrepArgumentsOnTheProductionPath pins the layering for grep: a declared
// key repeated is refused by the decoder, while an undeclared (or case-varied)
// key is dropped by the repair layer and reported rather than silently
// interpreted.
func TestGrepArgumentsOnTheProductionPath(t *testing.T) {
	t.Run("duplicate key rejected", func(t *testing.T) {
		r, _ := newReg(t)
		out, ok, err := r.Run(context.Background(), providerToolCall("grep", json.RawMessage(`{"pattern":"a","pattern":"b"}`)))
		if err == nil || ok {
			t.Fatalf("grep accepted a duplicate key: ok=%v err=%v out=%q", ok, err, out)
		}
		if !strings.Contains(err.Error(), "duplicate key") {
			t.Fatalf("error %q does not contain %q", err, "duplicate key")
		}
	})

	t.Run("undeclared field dropped and reported", func(t *testing.T) {
		r, _ := newReg(t)
		var fixes []Fix
		r.OnRepair = func(f Fix) { fixes = append(fixes, f) }
		defer func() { r.OnRepair = nil }()

		_, ok, err := r.Run(context.Background(), providerToolCall("grep", json.RawMessage(`{"pattern":"a","bogus":1}`)))
		if err != nil || !ok {
			t.Fatalf("grep failed: ok=%v err=%v", ok, err)
		}
		if got := DroppedKeys(fixes); len(got) != 1 || got[0] != "bogus" {
			t.Fatalf("the dropped key must be reported, got fixes=%+v dropped=%v", fixes, got)
		}
	})

	t.Run("duplicate plus an undeclared key is refused, not repaired", func(t *testing.T) {
		r, _ := newReg(t)
		var fixes []Fix
		r.OnRepair = func(f Fix) { fixes = append(fixes, f) }
		defer func() { r.OnRepair = nil }()

		out, ok, err := r.Run(context.Background(), providerToolCall("grep", json.RawMessage(`{"pattern":"a","pattern":"b","bogus":1}`)))
		if err == nil || ok {
			t.Fatalf("repair erased a duplicate key instead of leaving the refusal to the decoder: ok=%v err=%v out=%q fixes=%+v", ok, err, out, fixes)
		}
		if !strings.Contains(err.Error(), "duplicate key") {
			t.Fatalf("error %q does not contain %q", err, "duplicate key")
		}
	})
}
