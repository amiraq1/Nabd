package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nabd/internal/perm"
	"nabd/internal/provider"
)

// NBD-420: tool-call repair, table-driven.
//
// Every rule has a positive case (the malformed shape is corrected) and a
// negative one (a correct call is left exactly alone). The negatives carry half
// the value: an eager repair layer is worse than none, because it changes what
// runs.

// fixtureReg builds a registry over a temp root holding a small readable file.
func fixtureReg(t *testing.T) (*Registry, string) {
	t.Helper()
	reg, dir := newReg(t)
	body := "1|package fixture\n2|// line two\n3|// line three\n"
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte("1|package b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return reg, dir
}

// args renders a payload as the raw JSON a model would send.
func args(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRepairRules(t *testing.T) {
	reg, _ := fixtureReg(t)
	specs := reg.Specs()

	// Wrapper shapes are described, never written: the tests compose them from
	// neutral pieces so no literal envelope appears anywhere in the repository.
	preamble := "here is the call"
	trailer := "end of call"

	tests := []struct {
		name      string
		tool      string
		raw       json.RawMessage
		wantTool  string
		wantField string // a key that must be present in the corrected payload
		wantRule  string // "" means no fix at all
	}{
		// Rule: tool-name alias.
		{name: "alias read", tool: "read", raw: args(t, map[string]any{"path": "a.go"}), wantTool: "read_file", wantField: "path", wantRule: RuleToolAlias},
		{name: "alias list_files", tool: "list_files", raw: args(t, map[string]any{"pattern": "*.go"}), wantTool: "glob", wantField: "pattern", wantRule: RuleToolAlias},
		{name: "declared name untouched", tool: "read_file", raw: args(t, map[string]any{"path": "a.go"}), wantTool: "read_file", wantField: "path"},
		{name: "unknown name untouched", tool: "frobnicate", raw: args(t, map[string]any{"path": "a.go"}), wantTool: "frobnicate"},
		{name: "near miss is not inferred", tool: "read_files", raw: args(t, map[string]any{"path": "a.go"}), wantTool: "read_files"},

		// Rule: string-wrapped JSON.
		{name: "json in a string", tool: "read_file", raw: json.RawMessage(`"{\"path\":\"a.go\"}"`), wantTool: "read_file", wantField: "path", wantRule: RuleUnwrapString},
		{name: "json in a string in a string", tool: "read_file", raw: json.RawMessage(`"\"{\\\"path\\\":\\\"a.go\\\"}\""`), wantTool: "read_file", wantField: "path", wantRule: RuleUnwrapString},
		{name: "object untouched", tool: "read_file", raw: args(t, map[string]any{"path": "a.go"}), wantTool: "read_file", wantField: "path"},

		// Rule: wrapper text.
		{name: "prose around the object", tool: "read_file", raw: json.RawMessage(preamble + ` {"path":"a.go"} ` + trailer), wantTool: "read_file", wantField: "path", wantRule: RuleUnwrapText},

		// Rule: field alias.
		{name: "filename becomes path", tool: "read_file", raw: args(t, map[string]any{"filename": "a.go"}), wantTool: "read_file", wantField: "path", wantRule: RuleFieldAlias},
		{name: "filepath becomes path", tool: "read_file", raw: args(t, map[string]any{"filepath": "a.go"}), wantTool: "read_file", wantField: "path", wantRule: RuleFieldAlias},
		{name: "command becomes cmd", tool: "bash", raw: args(t, map[string]any{"command": "true"}), wantTool: "bash", wantField: "cmd", wantRule: RuleFieldAlias},
		{name: "declared field untouched", tool: "read_file", raw: args(t, map[string]any{"path": "a.go"}), wantTool: "read_file", wantField: "path"},
		{name: "conflicting spellings are refused", tool: "read_file", raw: args(t, map[string]any{"path": "a.go", "filename": "b.go"}), wantTool: "read_file", wantField: "path"},
		{name: "alias for an undeclared field is refused", tool: "read_file", raw: args(t, map[string]any{"body": "x"}), wantTool: "read_file"},

		// Rule: integer sent as text.
		{name: "offset as text", tool: "read_file", raw: args(t, map[string]any{"path": "a.go", "offset": "2"}), wantTool: "read_file", wantField: "offset", wantRule: RuleIntegerText},
		{name: "non-numeric text is refused", tool: "read_file", raw: args(t, map[string]any{"path": "a.go", "offset": "soon"}), wantTool: "read_file", wantField: "offset"},
		{name: "integer untouched", tool: "read_file", raw: args(t, map[string]any{"path": "a.go", "offset": 2}), wantTool: "read_file", wantField: "offset"},

		// Rule: Markdown link path.
		{name: "markdown link path", tool: "read_file", raw: args(t, map[string]any{"path": "[label](a.go)"}), wantTool: "read_file", wantField: "path", wantRule: RuleMarkdownPath},
		{name: "plain path untouched", tool: "read_file", raw: args(t, map[string]any{"path": "a.go"}), wantTool: "read_file", wantField: "path"},
		{name: "link in a non-path field is refused", tool: "write_file", raw: args(t, map[string]any{"path": "a.go", "content": "[label](target)"}), wantTool: "write_file", wantField: "content"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			gotTool, gotRaw, fixes := Repair(tc.tool, tc.raw, specs)

			if gotTool != tc.wantTool {
				t.Fatalf("tool = %q, want %q", gotTool, tc.wantTool)
			}
			if tc.wantRule == "" {
				if len(fixes) != 0 {
					t.Fatalf("expected no fix, got %+v", fixes)
				}
				if string(gotRaw) != string(tc.raw) {
					t.Fatalf("payload changed without a fix:\n got=%s\nwant=%s", gotRaw, tc.raw)
				}
				return
			}
			if len(fixes) == 0 {
				t.Fatalf("expected a %s fix, got none (payload %s)", tc.wantRule, gotRaw)
			}
			if !hasRule(fixes, tc.wantRule) {
				t.Fatalf("expected rule %s, got %+v", tc.wantRule, fixes)
			}
			if tc.wantField != "" {
				var obj map[string]any
				if err := json.Unmarshal(gotRaw, &obj); err != nil {
					t.Fatalf("corrected payload is not an object: %s", gotRaw)
				}
				if _, ok := obj[tc.wantField]; !ok {
					t.Fatalf("corrected payload lacks field %q: %s", tc.wantField, gotRaw)
				}
			}
		})
	}
}

func hasRule(fixes []Fix, rule string) bool {
	for _, f := range fixes {
		if f.Rule == rule {
			return true
		}
	}
	return false
}

// TestRepairIsDeterministic pins the purity contract: the same inputs give the
// same outputs, including the fix order, so a journal line cannot vary between
// runs.
func TestRepairIsDeterministic(t *testing.T) {
	reg, _ := fixtureReg(t)
	specs := reg.Specs()
	raw := args(t, map[string]any{"filename": "a.go", "offset": "2"})

	firstTool, firstRaw, firstFixes := Repair("read", raw, specs)
	for i := 0; i < 5; i++ {
		gotTool, gotRaw, gotFixes := Repair("read", raw, specs)
		if gotTool != firstTool || string(gotRaw) != string(firstRaw) || len(gotFixes) != len(firstFixes) {
			t.Fatalf("run %d differs: (%s,%s,%v) vs (%s,%s,%v)", i, gotTool, gotRaw, gotFixes, firstTool, firstRaw, firstFixes)
		}
		for j := range firstFixes {
			if gotFixes[j] != firstFixes[j] {
				t.Fatalf("run %d fix %d differs: %+v vs %+v", i, j, gotFixes[j], firstFixes[j])
			}
		}
	}
}

// TestRepairInferenceIsReadOnlyOnly is the guard for contract 3. It asks the
// registry for each target's class rather than reading a second written list,
// so the map and the policy cannot drift apart.
func TestRepairInferenceIsReadOnlyOnly(t *testing.T) {
	reg, _ := fixtureReg(t)

	// Every target the map can infer to must be a declared tool whose class is
	// ReadOnly.
	for from, to := range toolAliases {
		class, declared := reg.Class(to)
		if !declared {
			t.Errorf("alias %q → %q: target is not a declared tool", from, to)
			continue
		}
		if class != perm.ReadOnly {
			t.Errorf("alias %q → %q: target class is %v, want ReadOnly — inference must never reach a mutating or executing tool", from, to, class)
		}
	}

	// The runtime allowlist must equal the registry's ReadOnly set, so a new
	// ReadOnly tool cannot be inferred to while readOnlyTools says otherwise,
	// and a tool that changes class is caught here.
	declaredReadOnly := map[string]bool{}
	for _, spec := range reg.Specs() {
		if class, ok := reg.Class(spec.Name); ok && class == perm.ReadOnly {
			declaredReadOnly[spec.Name] = true
		}
	}
	for name := range declaredReadOnly {
		if !readOnlyTools[name] {
			t.Errorf("%q is ReadOnly in the registry but missing from readOnlyTools", name)
		}
	}
	for name := range readOnlyTools {
		if !declaredReadOnly[name] {
			t.Errorf("readOnlyTools lists %q, which the registry does not declare ReadOnly", name)
		}
	}
}

// TestRepairNameMapTargetsAreDeclared is the guard for contract 4: no map
// entry may point at a name the registry does not declare.
func TestRepairNameMapTargetsAreDeclared(t *testing.T) {
	reg, _ := fixtureReg(t)
	declared := map[string]bool{}
	for _, spec := range reg.Specs() {
		declared[spec.Name] = true
	}
	for from, to := range toolAliases {
		if !declared[to] {
			t.Errorf("alias %q → %q: target is not declared", from, to)
		}
		if from == to {
			t.Errorf("alias %q maps to itself", from)
		}
	}
}

// TestRepairCapReturnsUnchanged is the guard for contract 6: a call needing
// more than three corrections is a different call, not a malformed one, and is
// returned exactly as written.
func TestRepairCapReturnsUnchanged(t *testing.T) {
	reg, _ := fixtureReg(t)
	specs := reg.Specs()

	// Four corrections: wrapper text, a field rename, and two integers sent as
	// text.
	raw := json.RawMessage("see here {\"filename\":\"a.go\",\"offset\":\"2\",\"limit\":\"3\"} done")
	gotTool, gotRaw, fixes := Repair("read_file", raw, specs)

	if gotTool != "read_file" {
		t.Fatalf("tool = %q, want the input unchanged", gotTool)
	}
	if string(gotRaw) != string(raw) {
		t.Fatalf("a call over the cap was rewritten:\n got=%s\nwant=%s", gotRaw, raw)
	}
	if len(fixes) != 0 {
		t.Fatalf("a call over the cap reported fixes: %+v", fixes)
	}
}

// TestRepairPathResolutionIsUntouched is the guard for contract 5: the layer
// normalises shape and never path semantics. Root.Resolve alone decides what is
// inside the root, so nothing here may expand, absolutise or relativise.
func TestRepairPathResolutionIsUntouched(t *testing.T) {
	reg, dir := fixtureReg(t)
	specs := reg.Specs()

	for _, p := range []string{
		"../escape.go",
		"..",
		"/etc/passwd",
		"~/secret",
		"./a.go",
		"sub/../a.go",
	} {
		raw := args(t, map[string]any{"filename": p})
		_, gotRaw, fixes := Repair("read_file", raw, specs)
		if !hasRule(fixes, RuleFieldAlias) {
			t.Fatalf("path %q was not renamed; the test would not exercise the rule: %+v", p, fixes)
		}
		var obj map[string]any
		if err := json.Unmarshal(gotRaw, &obj); err != nil {
			t.Fatal(err)
		}
		if got := obj["path"]; got != p {
			t.Fatalf("path %q became %v; the repair layer must not rewrite path semantics", p, got)
		}
	}

	// And the guard itself still refuses what it always refused: nothing the
	// layer does makes an escaping path resolvable.
	if _, _, err := reg.Run(context.Background(), provider.ToolCall{
		Name: "read_file", Input: args(t, map[string]any{"filename": "../escape.go"}),
	}); err == nil {
		t.Fatalf("a traversal path resolved after repair; Root.Resolve must remain the only authority in %s", dir)
	}
}

// TestRepairAppliesOnBothEntryPoints is the guard for the single-chokepoint
// requirement: Registry.Run and Registry.RunDetailed are two paths, and a rule
// that silently skips one of them would look like a rule that saves no round.
func TestRepairAppliesOnBothEntryPoints(t *testing.T) {
	reg, _ := fixtureReg(t)
	malformed := provider.ToolCall{Name: "read", Input: args(t, map[string]any{"path": "a.go"})}

	if _, ok, err := reg.Run(context.Background(), malformed); err != nil || !ok {
		t.Fatalf("Run: ok=%v err=%v — the plain path did not repair", ok, err)
	}
	if _, err := reg.RunDetailed(context.Background(), malformed.Name, malformed.Input); err != nil {
		t.Fatalf("RunDetailed: %v — the rich path did not repair", err)
	}
}

// TestRepairAnnouncesEveryFix checks the announcement half of contract 1: the
// registry reports each fix it applies, before the tool runs.
func TestRepairAnnouncesEveryFix(t *testing.T) {
	reg, _ := fixtureReg(t)
	var got []Fix
	reg.OnRepair = func(f Fix) { got = append(got, f) }

	_, fixes := reg.RepairCallWithFixes(provider.ToolCall{
		Name:  "read",
		Input: args(t, map[string]any{"filename": "a.go", "offset": "2"}),
	})

	if len(fixes) != len(got) {
		t.Fatalf("announced %d fixes, applied %d", len(got), len(fixes))
	}
	if len(fixes) < 3 {
		t.Fatalf("expected the fixture to exercise three rules, got %+v", fixes)
	}
	for _, f := range got {
		if f.Rule == "" || f.Field == "" {
			t.Errorf("announcement lacks rule or field: %+v", f)
		}
		if !strings.Contains(f.Notice(), f.Rule) {
			t.Errorf("notice %q does not name its rule %q", f.Notice(), f.Rule)
		}
	}
}

// TestRepairNoticeDoesNotCopyWholeValues proves the journal line cannot carry a
// file body: values are shortened and control characters are removed.
func TestRepairNoticeDoesNotCopyWholeValues(t *testing.T) {
	body := strings.Repeat("secret", 100) + "\n\twith a newline"
	f := Fix{Field: "content", Was: body, Now: body, Rule: RuleFieldAlias}
	notice := f.Notice()

	if len(notice) > noticeValueCap*4 {
		t.Fatalf("notice is %d chars; a value leaked at full length: %q", len(notice), notice)
	}
	if strings.ContainsAny(notice, "\n\r\t") {
		t.Fatalf("notice carries control characters: %q", notice)
	}
}
